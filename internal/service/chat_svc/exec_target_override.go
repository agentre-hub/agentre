package chat_svc

import (
	"context"

	"github.com/cago-frame/cago/pkg/i18n"
	"go.uber.org/zap"

	"github.com/agentre-hub/agentre/internal/pkg/code"
	"github.com/agentre-hub/agentre/internal/repository/agent_backend_repo"
	"github.com/agentre-hub/agentre/internal/repository/agent_repo"
)

// validateExecTargetOverride 校验 R15a 手动指定的执行目标：agentBackendID 必须在
// 这个 Agent 的执行目标列表里，且此刻按 R15 判定可用——拒绝指定一个不可用的档，
// 不静默回落到自动挑选。projectID<=0（自由会话）时不做项目路径判定，与
// PickExecTarget / evalExecTargetAvailability 的既有语义一致。
func (s *chatSvc) validateExecTargetOverride(ctx context.Context, agentID, projectID, agentBackendID int64) error {
	targets, err := agent_repo.AgentExecTarget().ListByAgent(ctx, agentID)
	if err != nil {
		return operationFailedWithCause(ctx, err, zap.Int64("agentId", agentID))
	}
	inList := false
	for _, t := range targets {
		if t.AgentBackendID == agentBackendID {
			inList = true
			break
		}
	}
	if !inList {
		return i18n.NewError(ctx, code.ChatExecTargetOverrideNotInList)
	}

	be, err := agent_backend_repo.AgentBackend().Find(ctx, agentBackendID)
	if err != nil {
		return operationFailedWithCause(ctx, err, zap.Int64("agentBackendId", agentBackendID))
	}
	reason, _, err := s.evalExecTargetAvailability(ctx, be, projectID)
	if err != nil {
		return err
	}
	if reason != "" {
		return i18n.NewError(ctx, code.ChatExecTargetOverrideUnavailable)
	}
	return nil
}
