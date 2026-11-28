package chat_svc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"

	"github.com/cago-frame/cago/pkg/consts"
	"github.com/cago-frame/cago/pkg/i18n"
	"github.com/cago-frame/cago/pkg/logger"
	"github.com/cago-frame/cago/pkg/utils/httputils"
	"go.uber.org/zap"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/chat_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/llm_provider_entity"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/runtimes/remote/wire"
	"github.com/agentre-hub/agentre/internal/pkg/code"
	"github.com/agentre-hub/agentre/internal/pkg/conversationid"
	"github.com/agentre-hub/agentre/internal/pkg/transcript/blocks"
	"github.com/agentre-hub/agentre/internal/repository/agent_backend_repo"
	"github.com/agentre-hub/agentre/internal/repository/agent_repo"
	"github.com/agentre-hub/agentre/internal/repository/chat_repo"
	"github.com/agentre-hub/agentre/internal/repository/llm_provider_repo"
	"github.com/agentre-hub/agentre/internal/repository/transcript_repo"
	"github.com/agentre-hub/agentre/internal/service/chat_svc/ipc"
	"github.com/agentre-hub/agentre/internal/service/chat_svc/sessionengine"
	"github.com/agentre-hub/agentre/internal/service/dispatch_svc"
	"github.com/agentre-hub/agentre/internal/service/exec_target_svc"
	"github.com/agentre-hub/agentre/internal/service/remote_device_svc"
	"github.com/agentre-hub/agentre/pkg/wire/devicefp"
)

// CtlConversationStatus 是 agrctl 看到的会话运行态(spec「等待」「跟随输出」)。
type CtlConversationStatus string

const (
	CtlStatusIdle    CtlConversationStatus = "idle"
	CtlStatusRunning CtlConversationStatus = "running"
	CtlStatusWaiting CtlConversationStatus = "waiting"
	CtlStatusFailed  CtlConversationStatus = "failed"
)

// ErrCtlConversationInvalidID 是 agrctl 给的会话引用不是一个完整的 conversation UUID。
var ErrCtlConversationInvalidID = errors.New("invalid session id: want a full session UUID")

// ErrCtlCursorInvalid 是 tail 的 --from 指向一个这条会话没有发过的位置。
var ErrCtlCursorInvalid = errors.New("cursor is no longer valid")

// CtlConversation 是控制 API(agrctl)眼里的一条会话:按 conversation_id 定位,带它此刻的运行态。
type CtlConversation struct {
	SessionID      int64
	ConversationID string
	AgentID        int64
	AgentName      string
	// Device 是执行这条会话的配对设备名;空 = 本机桌面。
	Device string
	Status CtlConversationStatus
	// LastAssistantMessageID / LastError 是最近一条 assistant 消息与它落库的错误文本(没有 = 0 / "")。
	LastAssistantMessageID int64
	LastError              string
	// Title / ProjectID(0 = 自由会话) / CreatedAt / LastMessageAt(unix ms,0 = 没有过消息)
	// 是会话行本身就有的展示字段,agrctl list / get 直接用。
	Title         string
	ProjectID     int64
	CreatedAt     int64
	LastMessageAt int64
}

// CtlOverrides 是 agrctl send 对本会话设置的覆盖(spec「发起与继续」);空串 = 不覆盖。
type CtlOverrides struct {
	// Model 是 <provider>/<model>:提供方名或 key、斜杠后是模型 ID 或 key。
	Model          string
	CLIModel       string
	Effort         string
	PermissionMode string
}

// Empty 报告是否一项覆盖都没给。
func (o CtlOverrides) Empty() bool {
	return o.Model == "" && o.CLIModel == "" && o.Effort == "" && o.PermissionMode == ""
}

// CtlOverrideError 是一项覆盖值不被目标后端支持:Flag 是 agrctl 的 flag 名(不带 --),
// Options 是这一档后端可选的值(没有可选值时为空,Reason 说明原因)。
type CtlOverrideError struct {
	Flag    string
	Value   string
	Options []string
	Reason  string
}

func (e *CtlOverrideError) Error() string {
	msg := fmt.Sprintf("--%s %q is not supported by the target backend", e.Flag, e.Value)
	if e.Reason != "" {
		msg += ": " + e.Reason
	}
	if len(e.Options) > 0 {
		msg += " (one of: " + strings.Join(e.Options, ", ") + ")"
	}
	return msg
}

// CtlTailNotification 是 tail 交给控制 API 的一条通知:对端通知的原样 (method, params),
// 外加它的持久帧号(0 = 预览帧或轮次边界帧,不推进游标)。
type CtlTailNotification struct {
	Seq    int64
	Method string
	Params any
}

// CtlTail 是一次 tail 订阅:先按序吐 Replay(--from 之后错过的持久帧),再从 Live 读实时
// 通知;Live 关闭即订阅结束(调用方 ctx 结束,或订阅方落后太多被摘掉 —— Lagged 为 true,
// 调用方应从最后一个游标续接)。LatestSeq 是订阅那一刻的高水位。
type CtlTail struct {
	Replay    []CtlTailNotification
	Live      <-chan CtlTailNotification
	Lagged    func() bool
	LatestSeq int64
}

// CtlEngine 是 agrctl --engine 点名的一份引擎配置。
type CtlEngine struct {
	ID   int64
	Name string
}

// CtlEngineNotFoundError 是 --engine 点名的引擎配置不存在（或已删除）；Options 是可选的引擎
// 配置名，命令行据此给出用法错误。
type CtlEngineNotFoundError struct {
	Ref     string
	Options []string
}

func (e *CtlEngineNotFoundError) Error() string {
	return fmt.Sprintf("--engine %q is not an engine profile (one of: %s)", e.Ref, strings.Join(e.Options, ", "))
}

// CtlDevice 是共享派发函数对一台设备的结果，加上 --device 能选中它的全部写法。
type CtlDevice struct {
	// Name 是展示名（本机没有账号名时为 "this desktop"）。
	Name string
	// Refs 是 --device 能选中它的写法：名字、指纹；本机另有「本机」「local」与主机名。
	Refs        []string
	Fingerprint devicefp.Carrier
	Eligible    bool
	Chosen      bool
	// Reason 是不通过时第一条不满足的检查；通过时为 nil。
	Reason *dispatch_svc.ReasonView
}

// CtlDispatch 是一次新会话的派发方案：用的引擎配置、选中的设备，与每台设备的结果。
type CtlDispatch struct {
	// Found 为 false = 没有任何设备通过（无处可跑），Devices 带着逐台的原因。
	Found           bool
	EngineProfileID int64
	// Chosen 是自动选中的设备（Found 时有值）。
	Chosen  devicefp.Carrier
	Devices []CtlDevice
}

// CtlPendingApproval 是一张还挂着的审批卡,形状与 agentred 的 PendingApproval 相同:Kind 是卡的
// 工具族(ctl / org),Title 是工具名,Summary 是卡上变更清单的 JSON(命令已脱敏、密钥只有标记)。
type CtlPendingApproval struct {
	RequestID  string          `json:"requestId"`
	Kind       string          `json:"kind"`
	Title      string          `json:"title"`
	Summary    json.RawMessage `json:"summary,omitempty"`
	Createtime int64           `json:"createtime"`
}

// CtlConversationGateway 是控制 API(ctl_svc)对「按 conversation_id 定位并操作一条会话」
// 的窄依赖。与 HeadlessRunnerGateway 分开:那个端口 subagent_svc 也在用,拓宽它会让另一个
// 消费方平白背上这些能力。
type CtlConversationGateway interface {
	// LookupCtlConversation 按 conversation_id 定位本机会话;合法但本机没有 → (nil, nil),
	// 畸形 → ErrCtlConversationInvalidID。
	LookupCtlConversation(ctx context.Context, conversationID string) (*CtlConversation, error)
	// DescribeCtlConversation 同上,按本机主键。
	DescribeCtlConversation(ctx context.Context, sessionID int64) (*CtlConversation, error)
	// CheckCtlOverrides 只校验不写:sessionID>0 按该会话下一轮用的引擎配置;否则按新会话将用的
	// 那份 —— engineProfileID>0 是 --engine 点名的,0 是成员生效的那份。
	CheckCtlOverrides(ctx context.Context, agentID, engineProfileID, sessionID int64, o CtlOverrides) error
	// ResolveCtlEngine 把 --engine(名字或 id)解析成一份活的引擎配置;找不到报 *CtlEngineNotFoundError。
	ResolveCtlEngine(ctx context.Context, ref string) (*CtlEngine, error)
	// CtlDispatchPlan 按共享派发函数给新会话出方案(与控制台、新会话界面同一份判定):
	// engineProfileID>0 是本会话换用的引擎配置,0 是成员生效的那份。
	CtlDispatchPlan(ctx context.Context, agentID, projectID, engineProfileID int64) (*CtlDispatch, error)
	// PinCtlDispatch 在新会话首轮之前把方案选中的引擎配置与设备钉到会话上(会话不能换设备)。
	PinCtlDispatch(ctx context.Context, sessionID, engineProfileID int64, device devicefp.Carrier) error
	// ApplyCtlOverrides 把覆盖写到会话上(自下一轮生效),与桌面端会话设置同一条路径。
	ApplyCtlOverrides(ctx context.Context, sessionID int64, o CtlOverrides) error
	// SteerCtlConversation 把一条插话排进正在跑的这一轮(与桌面端插话同一个队列),交回排队 id。
	SteerCtlConversation(ctx context.Context, sessionID int64, text string) (string, error)
	// DiscardCtlSteers 撤回所有尚未送达的插话,交回撤回的条数。
	DiscardCtlSteers(ctx context.Context, sessionID int64) (int, error)
	// PendingCtlWaiters 是这条会话此刻卡着的待处理项(工具审批与提问)。
	PendingCtlWaiters(ctx context.Context, sessionID int64) (wire.SessionPendingWaitersResult, error)
	// PendingCtlApprovals 是这条会话在跑那一轮上仍挂着的审批卡(会话内 agent 调 agrctl 写资源
	// 或组织操作时出的卡),先出的在前。
	PendingCtlApprovals(ctx context.Context, sessionID int64) []CtlPendingApproval
	// AnswerToolApproval 批准或拒绝这条会话上挂着的一张审批卡(与桌面端审批卡同一个入口)。
	AnswerToolApproval(ctx context.Context, sessionID int64, requestID string, allow bool) error
	// TailCtlConversation 订阅这条会话的通知;resume 时从 from 之后续接,否则从当前开始。
	TailCtlConversation(ctx context.Context, sessionID int64, from int64, resume bool) (*CtlTail, error)
	// ListCtlConversations 按最近活动顺序列出本机桌面的会话(agent / 项目收窄下推给仓储,
	// 状态与设备在这里筛),最多 f.Limit 条。
	ListCtlConversations(ctx context.Context, f CtlListFilter) ([]*CtlConversation, error)
	// DetailCtlConversation 是 agrctl get session 要的详情;没有 / 已删 → (nil, nil)。
	DetailCtlConversation(ctx context.Context, sessionID int64) (*CtlConversationDetail, error)
	// ListCtlMessages 按轮分页读转录(默认只含文本,q.Full 带工具调用与结果)。
	ListCtlMessages(ctx context.Context, sessionID int64, q CtlMessagesQuery) (*CtlMessagesPage, error)
	// AnswerCtlQuestion 把对一道提问卡的回答投回正在等它的目标 agent(与桌面端同一条路径)。
	AnswerCtlQuestion(ctx context.Context, sessionID int64, requestID string, answers []blocks.AskAnswerDTO) error
}

// CtlConversationSvcGateway 生产用端口(供 ctl_svc 按请求现取,须在 RegisterChat 之后)。
func CtlConversationSvcGateway() CtlConversationGateway {
	gw, _ := Chat().(CtlConversationGateway)
	return gw
}

var _ CtlConversationGateway = (*chatSvc)(nil)

// ctlEffortLevels 是 --effort 可选的档位(entity 那张六档表去掉「跟随后端」的空串)。
var ctlEffortLevels = []string{"low", "medium", "high", "xhigh", "max"}

func (s *chatSvc) LookupCtlConversation(ctx context.Context, conversationID string) (*CtlConversation, error) {
	if err := conversationid.Validate(conversationID); err != nil {
		return nil, fmt.Errorf("%w: %q", ErrCtlConversationInvalidID, conversationID)
	}
	sess, err := chat_repo.Session().FindByConversationID(ctx, conversationID)
	if err != nil {
		return nil, operationFailedWithCause(ctx, err)
	}
	if sess == nil || sess.Status != consts.ACTIVE {
		return nil, nil
	}
	return s.describeCtlConversation(ctx, sess)
}

func (s *chatSvc) DescribeCtlConversation(ctx context.Context, sessionID int64) (*CtlConversation, error) {
	sess, err := chat_repo.Session().Find(ctx, sessionID)
	if err != nil {
		return nil, operationFailedWithCause(ctx, err, zap.Int64("sessionId", sessionID))
	}
	if sess == nil || sess.Status != consts.ACTIVE {
		return nil, nil
	}
	return s.describeCtlConversation(ctx, sess)
}

func (s *chatSvc) describeCtlConversation(ctx context.Context, sess *chat_entity.Session) (*CtlConversation, error) {
	out := &CtlConversation{
		SessionID:      sess.ID,
		ConversationID: sess.ConversationID,
		AgentID:        sess.AgentID,
		Status:         s.ctlStatus(sess),
		Title:          sess.Title,
		ProjectID:      sess.ProjectID,
		CreatedAt:      sess.Createtime,
		LastMessageAt:  sess.LastMessageAt,
	}
	a, err := agent_repo.Agent().Find(ctx, sess.AgentID)
	if err != nil {
		return nil, operationFailedWithCause(ctx, err, zap.Int64("agentId", sess.AgentID))
	}
	if a != nil {
		out.AgentName = a.Name
	}
	last, err := transcript_repo.Message().LatestAssistant(ctx, sess.ID)
	if err != nil {
		return nil, operationFailedWithCause(ctx, err, zap.Int64("sessionId", sess.ID))
	}
	if last != nil {
		out.LastAssistantMessageID, out.LastError = last.ID, last.ErrorText
	}
	if sess.RanOnDaemon() {
		out.Device = ctlDeviceName(ctx, sess.ExecDeviceID)
	}
	return out, nil
}

// ctlStatus 把会话行的 agent_status 折算成 agrctl 的运行态。内存里还挂着在飞那一轮
// 时一律算在跑:行上的状态以落库为准,可能慢半拍。
func (s *chatSvc) ctlStatus(sess *chat_entity.Session) CtlConversationStatus {
	switch sess.AgentStatus {
	case "waiting":
		return CtlStatusWaiting
	case "running":
		return CtlStatusRunning
	}
	if s.activeCancels != nil {
		if _, ok := s.activeCancels.Load(sess.ID); ok {
			return CtlStatusRunning
		}
	}
	if sess.AgentStatus == "error" {
		return CtlStatusFailed
	}
	return CtlStatusIdle
}

// ctlDeviceName 是执行这条会话的配对设备的展示名;查不到时退回它的编号。
func ctlDeviceName(ctx context.Context, deviceID int64) string {
	fallback := fmt.Sprintf("device #%d", deviceID)
	rds := remote_device_svc.Default()
	if rds == nil {
		return fallback
	}
	views, err := rds.List(ctx)
	if err != nil {
		logger.Ctx(ctx).Warn("chat_svc.ctlDeviceName: list paired devices failed",
			zap.Int64("deviceId", deviceID), zap.Error(err))
		return fallback
	}
	for _, v := range views {
		if v != nil && v.ID == deviceID && strings.TrimSpace(v.Name) != "" {
			return v.Name
		}
	}
	return fallback
}

func (s *chatSvc) CheckCtlOverrides(ctx context.Context, agentID, engineProfileID, sessionID int64, o CtlOverrides) error {
	if o.Empty() {
		return nil
	}
	var be *agent_backend_entity.AgentBackend
	if sessionID > 0 {
		found, err := chat_repo.Session().Find(ctx, sessionID)
		if err != nil {
			return operationFailedWithCause(ctx, err, zap.Int64("sessionId", sessionID))
		}
		if found == nil {
			return i18n.NewError(ctx, code.ChatSessionNotFound)
		}
		if be, err = sessionProviderBackend(ctx, found); err != nil {
			return err
		}
	} else {
		a, err := agent_repo.Agent().Find(ctx, agentID)
		if err != nil {
			return operationFailedWithCause(ctx, err, zap.Int64("agentId", agentID))
		}
		if a == nil {
			return i18n.NewError(ctx, code.AgentNotFound)
		}
		profile, err := s.turnEngineProfile(ctx, a, engineProfileID)
		if err != nil {
			return err
		}
		be = sessionengine.View(profile, "")
	}
	_, err := s.resolveCtlOverrides(ctx, be, o)
	return err
}

func (s *chatSvc) ApplyCtlOverrides(ctx context.Context, sessionID int64, o CtlOverrides) error {
	if o.Empty() {
		return nil
	}
	sess, err := chat_repo.Session().Find(ctx, sessionID)
	if err != nil {
		return operationFailedWithCause(ctx, err, zap.Int64("sessionId", sessionID))
	}
	if sess == nil {
		return i18n.NewError(ctx, code.ChatSessionNotFound)
	}
	be, err := sessionProviderBackend(ctx, sess)
	if err != nil {
		return err
	}
	target, err := s.resolveCtlOverrides(ctx, be, o)
	if err != nil {
		return err
	}
	// 每一项都走桌面端会话设置的同一个入口:落库、转录留痕、自下一轮生效都由它们负责。
	if o.Model != "" {
		if _, err := s.SetChatSessionModelTarget(ctx, &SetChatSessionModelTargetRequest{
			SessionID: sessionID, ProviderKey: target.providerKey, ModelKey: target.modelKey,
		}); err != nil {
			return err
		}
	}
	if o.CLIModel != "" {
		if _, err := s.SetChatSessionCLIModel(ctx, &SetChatSessionCLIModelRequest{
			SessionID: sessionID, Mode: "fixed", Value: o.CLIModel,
		}); err != nil {
			return err
		}
	}
	if o.Effort != "" {
		if _, err := s.SetChatSessionReasoningEffort(ctx, &SetChatSessionReasoningEffortRequest{
			SessionID: sessionID, ReasoningEffort: o.Effort,
		}); err != nil {
			return err
		}
	}
	if o.PermissionMode != "" {
		if _, err := s.SetPermissionMode(ctx, &SetPermissionModeRequest{SessionID: sessionID, Mode: o.PermissionMode}); err != nil {
			return err
		}
	}
	logger.Ctx(ctx).Info("chat_svc.ApplyCtlOverrides: session overrides applied",
		zap.Int64("sessionId", sessionID),
		zap.Bool("model", o.Model != ""), zap.Bool("cliModel", o.CLIModel != ""),
		zap.String("effort", o.Effort), zap.String("permissionMode", o.PermissionMode))
	return nil
}

// ctlModelTarget 是 --model 解析出的 ModelTarget。
type ctlModelTarget struct {
	providerKey, modelKey string
}

// resolveCtlOverrides 按这一档后端校验每一项覆盖,并把 --model 解析成 ModelTarget。
func (s *chatSvc) resolveCtlOverrides(ctx context.Context, be *agent_backend_entity.AgentBackend, o CtlOverrides) (ctlModelTarget, error) {
	if o.Effort != "" && !agent_backend_entity.IsValidReasoningEffort(o.Effort) {
		return ctlModelTarget{}, &CtlOverrideError{Flag: "effort", Value: o.Effort, Options: ctlEffortLevels}
	}
	if o.PermissionMode != "" {
		meta, ok := ipc.PermissionModeMetaFor(agent_backend_entity.BackendType(be.Type))
		if !ok {
			return ctlModelTarget{}, &CtlOverrideError{Flag: "permission-mode", Value: o.PermissionMode,
				Reason: "this backend has no permission modes"}
		}
		if !slices.Contains(meta.AllowedModes, o.PermissionMode) {
			return ctlModelTarget{}, &CtlOverrideError{Flag: "permission-mode", Value: o.PermissionMode, Options: meta.AllowedModes}
		}
	}
	if o.CLIModel != "" {
		if be.LLMProviderKey != "" {
			return ctlModelTarget{}, &CtlOverrideError{Flag: "cli-model", Value: o.CLIModel,
				Reason: "this backend runs on a provider; use --model <provider>/<model>"}
		}
		if !exec_target_svc.BackendTargetsRemote(be) {
			if _, ok := agentruntime.RuntimeFor(agent_backend_entity.BackendType(be.Type)).(agentruntime.CLIModelSetter); !ok {
				return ctlModelTarget{}, &CtlOverrideError{Flag: "cli-model", Value: o.CLIModel,
					Reason: "this backend has no CLI model choice"}
			}
		}
	}
	if o.Model == "" {
		return ctlModelTarget{}, nil
	}
	return s.resolveCtlModel(ctx, be, o.Model)
}

// resolveCtlModel 把 <provider>/<model> 解析成与这一档后端兼容、启用着的 ModelTarget;
// 只给提供方时是 provider-default。解析不出来时列出全部可选的 <provider>/<model>。
func (s *chatSvc) resolveCtlModel(ctx context.Context, be *agent_backend_entity.AgentBackend, ref string) (ctlModelTarget, error) {
	kind := be.Kind()
	if kind == nil {
		return ctlModelTarget{}, i18n.NewError(ctx, code.AgentBackendInvalidType)
	}
	providers, err := llm_provider_repo.LLMProvider().List(ctx)
	if err != nil {
		return ctlModelTarget{}, operationFailedWithCause(ctx, err)
	}
	wantProvider, wantModel, _ := strings.Cut(strings.TrimSpace(ref), "/")
	var options []string
	for _, p := range providers {
		if !p.IsActive() || !p.IsEnabled() || !kind.ProviderTypeMatch(llm_provider_entity.ProviderType(p.Type)) {
			continue
		}
		models, err := llm_provider_repo.LLMProvider().ListModels(ctx, p.ID)
		if err != nil {
			return ctlModelTarget{}, operationFailedWithCause(ctx, err, zap.Int64("providerId", p.ID))
		}
		providerHit := wantProvider == p.Name || wantProvider == p.ProviderKey
		if providerHit && wantModel == "" {
			return ctlModelTarget{providerKey: p.ProviderKey}, nil
		}
		for _, m := range models {
			if !m.IsEnabled() {
				continue
			}
			if providerHit && (wantModel == m.ModelID || wantModel == m.ModelKey) {
				return ctlModelTarget{providerKey: p.ProviderKey, modelKey: m.ModelKey}, nil
			}
			options = append(options, p.Name+"/"+m.ModelID)
		}
	}
	reason := ""
	if len(options) == 0 {
		reason = "no enabled provider model is compatible with this backend"
	}
	return ctlModelTarget{}, &CtlOverrideError{Flag: "model", Value: ref, Options: options, Reason: reason}
}

func (s *chatSvc) SteerCtlConversation(ctx context.Context, sessionID int64, text string) (string, error) {
	resp, err := s.enqueue(ctx, &EnqueueRequest{SessionID: sessionID, Text: text})
	if err != nil {
		return "", err
	}
	return resp.QueuedID, nil
}

// DiscardCtlSteers 撤回整条插话队列。后端不支持撤回(插话一发即送达)或这一轮已收口时,
// 没有尚未送达的插话,按 0 条处理。
func (s *chatSvc) DiscardCtlSteers(ctx context.Context, sessionID int64) (int, error) {
	resp, err := s.CancelQueued(ctx, &CancelQueuedRequest{SessionID: sessionID})
	if err != nil {
		if hasErrCode(err, code.ChatCancelUnsupported) || hasErrCode(err, code.ChatSteerNoActive) {
			return 0, nil
		}
		return 0, err
	}
	return len(resp.Removed), nil
}

func (s *chatSvc) PendingCtlWaiters(ctx context.Context, sessionID int64) (wire.SessionPendingWaitersResult, error) {
	session, err := chat_repo.Session().Find(ctx, sessionID)
	if err != nil {
		return wire.SessionPendingWaitersResult{}, operationFailedWithCause(ctx, err)
	}
	if session == nil {
		return wire.SessionPendingWaitersResult{}, i18n.NewError(ctx, code.ChatSessionNotFound)
	}
	return s.pendingSessionWaiters(ctx, session)
}

func (s *chatSvc) PendingCtlApprovals(_ context.Context, sessionID int64) []CtlPendingApproval {
	t := s.activeTurn(sessionID)
	if t == nil {
		return nil
	}
	return t.pendingApprovals()
}

// hasErrCode 报告 err 是不是带指定业务码的 i18n 错误。
func hasErrCode(err error, want int) bool {
	var he *httputils.Error
	return errors.As(err, &he) && he.Code == want
}

// TailCtlConversation 把 agrctl tail 挂到这条会话的对端通知宇宙上(与账号对端同一份
// 持久帧编号):先按游标补齐错过的持久帧,再交实时通知。游标因此跨进程重启仍然有效。
func (s *chatSvc) TailCtlConversation(ctx context.Context, sessionID int64, from int64, resume bool) (*CtlTail, error) {
	sess, err := chat_repo.Session().Find(ctx, sessionID)
	if err != nil {
		return nil, operationFailedWithCause(ctx, err, zap.Int64("sessionId", sessionID))
	}
	if sess == nil {
		return nil, i18n.NewError(ctx, code.ChatSessionNotFound)
	}
	sub := &ctlTailSubscriber{ch: make(chan CtlTailNotification, ctlTailBuffer), done: ctx.Done()}
	attached, err := s.peerStream().AttachPeerSession(ctx, wire.SessionAttachParams{ConversationID: sess.ConversationID}, sub)
	if err != nil {
		return nil, err
	}
	cursor := attached.LatestSeq
	if resume {
		if from < 0 || from > attached.LatestSeq {
			return nil, ErrCtlCursorInvalid
		}
		cursor = from
	}
	var replay []CtlTailNotification
	for {
		page, err := s.peerStream().PullPeerSession(ctx, wire.SessionPullParams{
			ConversationID: sess.ConversationID, Cursor: cursor, Limit: wire.MaxSessionPullLimit,
		}, sub)
		if err != nil {
			return nil, err
		}
		for _, n := range page.Notifications {
			replay = append(replay, CtlTailNotification{Seq: n.Seq, Method: n.Method, Params: n.Params})
		}
		if !page.HasMore || page.Cursor <= cursor {
			break
		}
		cursor = page.Cursor
	}
	return &CtlTail{Replay: replay, Live: sub.ch, Lagged: sub.lagged.Load, LatestSeq: attached.LatestSeq}, nil
}

// ctlTailBuffer 是一条 tail 订阅的实时缓冲;读得比这更慢的订阅被摘掉,由客户端从游标续接。
const ctlTailBuffer = 1024

var errCtlTailLagged = errors.New("agrctl tail fell behind")

// ctlTailSubscriber 是一条 agrctl tail 在对端通知宇宙上的订阅者。Notify 只由发布方唯一
// 的投递 goroutine 串行调用,一旦返回错误就被摘掉、不再调用,所以 close 只会发生一次。
type ctlTailSubscriber struct {
	ch     chan CtlTailNotification
	done   <-chan struct{}
	lagged atomic.Bool
}

func (t *ctlTailSubscriber) Notify(method string, params any) error {
	n := CtlTailNotification{Seq: ctlNotificationSeq(params), Method: method, Params: params}
	select {
	case <-t.done:
		return context.Canceled
	case t.ch <- n:
		return nil
	default:
		t.lagged.Store(true)
		close(t.ch)
		return errCtlTailLagged
	}
}

func (t *ctlTailSubscriber) Done() <-chan struct{} { return t.done }

// ctlNotificationSeq 取一条实时通知的持久帧号;预览帧与轮次边界帧没有号。
func ctlNotificationSeq(params any) int64 {
	switch p := params.(type) {
	case wire.EventFrame:
		return p.Seq
	case *wire.EventFrame:
		return p.Seq
	}
	return 0
}

// ctlLocalDevice 是本机桌面在 --device 里的展示名。
const ctlLocalDevice = "this desktop"

// ResolveCtlEngine 见 CtlConversationGateway。
func (s *chatSvc) ResolveCtlEngine(ctx context.Context, ref string) (*CtlEngine, error) {
	ref = strings.TrimSpace(ref)
	profiles, err := agent_backend_repo.EngineProfile().List(ctx)
	if err != nil {
		return nil, operationFailedWithCause(ctx, err)
	}
	var options []string
	for _, p := range profiles {
		if !p.IsActive() {
			continue
		}
		options = append(options, p.Name)
		if ref != "" && (p.Name == ref || strconv.FormatInt(p.ID, 10) == ref) {
			return &CtlEngine{ID: p.ID, Name: p.Name}, nil
		}
	}
	return nil, &CtlEngineNotFoundError{Ref: ref, Options: options}
}

// CtlDispatchPlan 见 CtlConversationGateway。
func (s *chatSvc) CtlDispatchPlan(ctx context.Context, agentID, projectID, engineProfileID int64) (*CtlDispatch, error) {
	dispatcher := dispatch_svc.Dispatch()
	if dispatcher == nil {
		return nil, operationFailedWithCause(ctx, errors.New("dispatch service unavailable"), zap.Int64("agentId", agentID))
	}
	plan, err := dispatcher.Plan(ctx, &dispatch_svc.PlanRequest{AgentID: agentID, ProjectID: projectID, EngineProfileID: engineProfileID})
	if err != nil {
		return nil, err
	}
	out := &CtlDispatch{Found: plan.Found, EngineProfileID: plan.Engine.ID, Chosen: plan.Chosen}
	for _, d := range plan.Devices {
		if d == nil {
			continue
		}
		name, refs := ctlDeviceNames(d)
		out.Devices = append(out.Devices, CtlDevice{
			Name: name, Refs: refs, Fingerprint: d.Fingerprint, Eligible: d.Eligible, Chosen: d.Chosen, Reason: d.Reason,
		})
	}
	return out, nil
}

// ctlDeviceNames 是一台参与派发的设备的展示名，以及 --device 能选中它的全部写法。
func ctlDeviceNames(d *dispatch_svc.DeviceResult) (string, []string) {
	name := strings.TrimSpace(d.Name)
	var refs []string
	if name != "" {
		refs = append(refs, name)
	}
	if d.IsSelf {
		if name == "" {
			name = ctlLocalDevice
		}
		refs = append(refs, ctlLocalDevice, "本机", "local")
		if host, err := os.Hostname(); err == nil && host != "" {
			refs = append(refs, host)
		}
	}
	if name == "" {
		name = string(d.Fingerprint)
	}
	if d.Fingerprint != "" {
		refs = append(refs, string(d.Fingerprint))
	}
	return name, slices.Compact(refs)
}

// PinCtlDispatch 见 CtlConversationGateway。借 pinTurn 写（先设备后引擎配置、本机不写设备
// 列），它写库失败只记日志，所以这里回读实体确认真的钉上了。
func (s *chatSvc) PinCtlDispatch(ctx context.Context, sessionID, engineProfileID int64, device devicefp.Carrier) error {
	sess, err := chat_repo.Session().Find(ctx, sessionID)
	if err != nil {
		return operationFailedWithCause(ctx, err, zap.Int64("sessionId", sessionID))
	}
	if sess == nil {
		return i18n.NewError(ctx, code.ChatSessionNotFound)
	}
	if !remote_device_svc.TargetsAnotherMachine(device) {
		device = ""
	}
	s.pinTurn(ctx, sess, &turnPin{engineProfileID: engineProfileID, device: device})
	if sess.EngineProfileID != engineProfileID {
		return operationFailedWithCause(ctx, errors.New("pin the dispatched engine profile failed"),
			zap.Int64("sessionId", sessionID), zap.Int64("engineProfileId", engineProfileID))
	}
	return nil
}
