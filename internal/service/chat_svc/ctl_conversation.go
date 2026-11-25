package chat_svc

import (
	"context"
	"errors"
	"fmt"
	"slices"
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
	"github.com/agentre-hub/agentre/internal/repository/agent_repo"
	"github.com/agentre-hub/agentre/internal/repository/chat_repo"
	"github.com/agentre-hub/agentre/internal/repository/llm_provider_repo"
	"github.com/agentre-hub/agentre/internal/repository/transcript_repo"
	"github.com/agentre-hub/agentre/internal/service/chat_svc/ipc"
	"github.com/agentre-hub/agentre/internal/service/exec_target_svc"
	"github.com/agentre-hub/agentre/internal/service/remote_device_svc"
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

// CtlConversationGateway 是控制 API(ctl_svc)对「按 conversation_id 定位并操作一条会话」
// 的窄依赖。与 HeadlessRunnerGateway 分开:那个端口 subagent_svc 也在用,拓宽它会让另一个
// 消费方平白背上这些能力。
type CtlConversationGateway interface {
	// LookupCtlConversation 按 conversation_id 定位本机会话;合法但本机没有 → (nil, nil),
	// 畸形 → ErrCtlConversationInvalidID。
	LookupCtlConversation(ctx context.Context, conversationID string) (*CtlConversation, error)
	// DescribeCtlConversation 同上,按本机主键。
	DescribeCtlConversation(ctx context.Context, sessionID int64) (*CtlConversation, error)
	// CheckCtlOverrides 只校验不写:sessionID>0 按该会话下一轮的后端,否则按 agentID 的默认后端。
	CheckCtlOverrides(ctx context.Context, agentID, sessionID int64, o CtlOverrides) error
	// ApplyCtlOverrides 把覆盖写到会话上(自下一轮生效),与桌面端会话设置同一条路径。
	ApplyCtlOverrides(ctx context.Context, sessionID int64, o CtlOverrides) error
	// SteerCtlConversation 把一条插话排进正在跑的这一轮(与桌面端插话同一个队列),交回排队 id。
	SteerCtlConversation(ctx context.Context, sessionID int64, text string) (string, error)
	// DiscardCtlSteers 撤回所有尚未送达的插话,交回撤回的条数。
	DiscardCtlSteers(ctx context.Context, sessionID int64) (int, error)
	// PendingCtlWaiters 是这条会话此刻卡着的待处理项(工具审批与提问)。
	PendingCtlWaiters(ctx context.Context, sessionID int64) (wire.SessionPendingWaitersResult, error)
	// TailCtlConversation 订阅这条会话的通知;resume 时从 from 之后续接,否则从当前开始。
	TailCtlConversation(ctx context.Context, sessionID int64, from int64, resume bool) (*CtlTail, error)
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

func (s *chatSvc) CheckCtlOverrides(ctx context.Context, agentID, sessionID int64, o CtlOverrides) error {
	if o.Empty() {
		return nil
	}
	sess := &chat_entity.Session{AgentID: agentID}
	if sessionID > 0 {
		found, err := chat_repo.Session().Find(ctx, sessionID)
		if err != nil {
			return operationFailedWithCause(ctx, err, zap.Int64("sessionId", sessionID))
		}
		if found == nil {
			return i18n.NewError(ctx, code.ChatSessionNotFound)
		}
		sess = found
	}
	be, err := sessionProviderBackend(ctx, sess)
	if err != nil {
		return err
	}
	_, err = s.resolveCtlOverrides(ctx, be, o)
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
