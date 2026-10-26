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
	"github.com/agentre-hub/agentre/internal/pkg/code"
	"github.com/agentre-hub/agentre/internal/repository/chat_repo"
	"github.com/agentre-hub/agentre/internal/service/chat_svc/view"
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
	if sess.CLIModelMode == "" && sess.ID <= 0 {
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
func cliModelDeviation(req agentruntime.RunRequest, actual string, models []agentruntime.CLIModel) *blocks.NoticeBlock {
	if req.CLIModelMode != "fixed" || actual == "" {
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
	text, _ := json.Marshal(view.ProviderNotice{Kind: "cli_model_deviation", CLIModel: req.CLIModel, ActualModel: actual})
	return &blocks.NoticeBlock{Level: "warning", Text: string(text)}
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
	next := value
	if mode == "" {
		next = be.DefaultModel
	}
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
			previous := sess.CLIModel
			if sess.CLIModelMode == "" {
				previous = be.DefaultModel
			}
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
