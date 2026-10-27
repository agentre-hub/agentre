package chat_svc

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"

	"github.com/cago-frame/agents/agent/blocks"

	"github.com/cago-frame/cago/pkg/i18n"
	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/chat_entity"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
	piagentrt "github.com/agentre-hub/agentre/internal/pkg/agentruntime/runtimes/piagent"
	"github.com/agentre-hub/agentre/internal/pkg/code"
	"github.com/agentre-hub/agentre/internal/repository/chat_repo"
	"github.com/agentre-hub/agentre/internal/service/chat_svc/view"
	"github.com/agentre-hub/agentre/internal/service/exec_target_svc"
)

// validateDraftCLIModel 在建行前拒绝矛盾或不支持的草稿选择。
func validateDraftCLIModel(ctx context.Context, req *SendRequest, be *agent_backend_entity.AgentBackend) error {
	if !chat_entity.ValidCLIModelChoice(req.CLIModelMode, req.CLIModel) {
		return i18n.NewError(ctx, code.InvalidParameter)
	}
	if req.CLIModelMode == "" {
		return nil
	}
	if req.ProviderKey != "" || req.ModelKey != "" || be.LLMProviderKey != "" {
		return i18n.NewError(ctx, code.InvalidParameter)
	}
	if _, ok := agentruntime.RuntimeFor(agent_backend_entity.BackendType(be.Type)).(agentruntime.CLIModelSetter); !ok {
		return agentruntime.ErrUnsupported
	}
	return nil
}

func applySessionCLIModel(req *agentruntime.RunRequest, sess *chat_entity.Session) {
	req.CLIModelMode, req.CLIModel = sess.CLIModelMode, sess.CLIModel
}

// refreshCLIModelChoice 自动续轮不能复用上一轮开始时的选择快照。
func refreshCLIModelChoice(ctx context.Context, sess *chat_entity.Session) error {
	if sess.ID <= 0 {
		return nil
	}
	latest, err := chat_repo.Session().Find(ctx, sess.ID)
	if err != nil {
		return err
	}
	if latest == nil {
		return i18n.NewError(ctx, code.ChatSessionNotFound)
	}
	sess.CLIModelMode, sess.CLIModel = latest.CLIModelMode, latest.CLIModel
	sess.ProviderKey, sess.ModelKey = latest.ProviderKey, latest.ModelKey
	return nil
}

// cliModelDeviation 按 CLI 目录解析别名后再比较，不能把 sonnet 与完整 ID 的正常映射报成偏离。
// CLI 报的正是所选值时不算偏离（codex 的 id 与 model 可以不同，线程模型记的是下发的 id）。
// 目录读不到（catalogKnown=false）时无从解析别名，不猜，不追加提示。
func cliModelDeviation(req agentruntime.RunRequest, actual string, models []agentruntime.CLIModel, catalogKnown bool) *blocks.NoticeBlock {
	if req.CLIModelMode != agentruntime.CLIModelModeFixed || actual == "" || actual == req.CLIModel || !catalogKnown {
		return nil
	}
	expected := req.CLIModel
	for _, model := range models {
		if model.Value == req.CLIModel && model.ModelID != "" {
			expected = model.ModelID
			break
		}
	}
	if expected == actual {
		return nil
	}
	text, _ := json.Marshal(view.ProviderNotice{Kind: view.NoticeKindCLIModelDeviation, CLIModel: req.CLIModel, ActualModel: actual})
	return &blocks.NoticeBlock{Level: "warning", Text: string(text)}
}

// cliModelStateReader 取这条会话 CLI 模型账的读侧。远端只看本机已开着的那条连接
// （pairedDeviceID 是调用方已解析好的配对行，0 = 本机没配对那台设备），不为打开会话
// 拨号、占池引用或改执行归属；与 remotePendingSessionWaiters 不同，轮间 lease 已还给池
// 也照样读那条连接上的 runtime（peekRemoteRuntime）—— 账在轮末记下，用户正是在轮与轮
// 之间看 pill。没有连接时交 nil，pill 不猜实际模型。
func (s *chatSvc) cliModelStateReader(ctx context.Context, be *agent_backend_entity.AgentBackend, sessionID, pairedDeviceID int64) agentruntime.CLIModelStateReader {
	var runner agentruntime.Runtime
	if exec_target_svc.BackendTargetsRemote(be) {
		if pairedDeviceID <= 0 {
			return nil
		}
		if rt := s.peekRemoteRuntime(pairedDeviceID); rt != nil {
			runner = rt
		}
	} else if r, err := s.selectRunner(ctx, be, sessionID); err == nil {
		runner = r
	}
	reader, _ := runner.(agentruntime.CLIModelStateReader)
	return reader
}

// cliModelPending 判这条会话的 CLI 模型选择是否还挂着没落到进程上（pill 的「挂起」
// 与排队消息是否留给下一轮同用这一处）。
func cliModelPending(state agentruntime.CLIModelState, sess *chat_entity.Session, be *agent_backend_entity.AgentBackend) bool {
	wanted := agentruntime.CLIModelChoiceValue(sess.CLIModelMode, sess.CLIModel, be.DefaultModel)
	return state.Active && state.AppliedValue != wanted && sess.ProviderKey == "" && be.LLMProviderKey == ""
}

// nextTurnSteerer 是能把一条排队消息扣下、留到这一轮结束后作为下一轮 prompt 的
// runtime（轮末经 SteerDrainer 交回）。CLI 自己会在本轮里消费 steer 的后端（pi）
// 实现它：轮中切了模型之后排队的消息必须先应用挂起的模型，不能跑在旧模型那一轮里。
type nextTurnSteerer interface {
	SteerNextTurn(ctx context.Context, sessionID int64, queuedID, text string) error
}

// 签名对不上时类型断言会静默落空、排队消息又回到旧模型那一轮，这里在编译期钉住。
var _ nextTurnSteerer = (*piagentrt.Runtime)(nil)

// queuedSteerFunc 选这条排队消息的去向：CLI 模型切换还挂着时留给下一轮（与手动发送
// 同一保证：开轮前先应用挂起的模型），否则照常插进当前这一轮。
func queuedSteerFunc(ctx context.Context, runner agentruntime.Runtime, steerer agentruntime.Steerer, sess *chat_entity.Session, be *agent_backend_entity.AgentBackend) func(context.Context, int64, string, string) error {
	deferrer, ok := runner.(nextTurnSteerer)
	if !ok {
		return steerer.Steer
	}
	reader, ok := runner.(agentruntime.CLIModelStateReader)
	if !ok || !cliModelPending(reader.CLIModelState(ctx, sess.ID), sess, be) {
		return steerer.Steer
	}
	return deferrer.SteerNextTurn
}

func (s *chatSvc) modelChoiceLock(id int64) *sync.Mutex {
	v, _ := s.modelChoiceLocks.LoadOrStore(id, &sync.Mutex{})
	return v.(*sync.Mutex)
}

// SetChatSessionCLIModel 空闲先经 CLI 确认再落库，轮中只保存下一轮的选择。
func (s *chatSvc) SetChatSessionCLIModel(ctx context.Context, req *SetChatSessionCLIModelRequest) (*SetChatSessionCLIModelResponse, error) {
	if req == nil || req.SessionID <= 0 {
		return nil, i18n.NewError(ctx, code.InvalidParameter)
	}
	mode, value := strings.TrimSpace(req.Mode), strings.TrimSpace(req.Value)
	if !chat_entity.ValidCLIModelChoice(mode, value) {
		return nil, i18n.NewError(ctx, code.InvalidParameter)
	}
	lk := s.modelChoiceLock(req.SessionID)
	lk.Lock()
	defer lk.Unlock()
	sess, err := chat_repo.Session().Find(ctx, req.SessionID)
	if err != nil {
		return nil, operationFailedWithCause(ctx, err)
	}
	if sess == nil {
		return nil, i18n.NewError(ctx, code.ChatSessionNotFound)
	}
	be, err := sessionProviderBackend(ctx, sess)
	if err != nil {
		return nil, err
	}
	// 已绑供应商的后端不显示 CLI 段，也不能经直接调用绕过。
	if be.LLMProviderKey != "" {
		return nil, agentruntime.ErrUnsupported
	}
	runner, release, err := s.selectTurnRunner(ctx, sess, be)
	if err != nil {
		return nil, err
	}
	defer release()
	setter, ok := runner.(agentruntime.CLIModelSetter)
	if !ok {
		return nil, agentruntime.ErrUnsupported
	}
	if sess.CLIModelMode == mode && sess.CLIModel == value && sess.ProviderKey == "" && sess.ModelKey == "" {
		return &SetChatSessionCLIModelResponse{Mode: mode, Value: value}, nil
	}
	next := agentruntime.CLIModelChoiceValue(mode, value, be.DefaultModel)
	applied := false
	// 从供应商目标切回 CLI 须走下一轮的启动身份边界，不能在旧网关进程上换模型。
	if sess.AgentStatus != "running" && sess.AgentStatus != "waiting" && sess.ProviderKey == "" {
		applyErr := setter.SetCLIModel(ctx, sess.ID, next)
		applied = applyErr == nil
		if err := applyErr; err != nil && !errors.Is(err, agentruntime.ErrNoActiveTurn) {
			logger.Ctx(ctx).Warn("chat_svc.SetChatSessionCLIModel: CLI rejected model", zap.Int64("sessionId", sess.ID), zap.Error(err))
			return nil, err
		}
	}
	if err := chat_repo.Session().UpdateCLIModel(ctx, sess.ID, mode, value); err != nil {
		if applied {
			previous := agentruntime.CLIModelChoiceValue(sess.CLIModelMode, sess.CLIModel, be.DefaultModel)
			if rollbackErr := setter.SetCLIModel(context.WithoutCancel(ctx), sess.ID, previous); rollbackErr != nil {
				logger.Ctx(ctx).Error("chat_svc.SetChatSessionCLIModel: rollback failed", zap.Int64("sessionId", sess.ID), zap.Error(rollbackErr))
			}
		}
		return nil, operationFailedWithCause(ctx, err)
	}
	sess.CLIModelMode, sess.CLIModel = mode, value
	sess.ProviderKey, sess.ModelKey = "", ""
	logger.Ctx(ctx).Info("chat_svc.SetChatSessionCLIModel: session model switched", zap.Int64("sessionId", sess.ID), zap.String("mode", mode), zap.String("model", value))
	appendSessionNotice(ctx, sess, be, "chat_svc.SetChatSessionCLIModel", view.EncodeCLIModelSwitch(mode, value))
	return &SetChatSessionCLIModelResponse{Mode: mode, Value: value}, nil
}
