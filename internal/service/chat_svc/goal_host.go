package chat_svc

import (
	"context"
	"strings"

	"github.com/cago-frame/agents/agent/blocks"
	"github.com/cago-frame/cago/pkg/i18n"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/agent_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/chat_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/llm_provider_entity"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"

	"github.com/agentre-hub/agentre/internal/pkg/code"
	"github.com/agentre-hub/agentre/internal/service/chat_svc/goal"
)

// 目标(codex goal)链路已迁到 chat_svc/goal;这里只留 DTO 边界与惰性装配。
func (s *chatSvc) goals() *goal.Controller {
	s.goalsOnce.Do(func() { s.goalsImpl = goal.New(chatGoalHost{s: s}) })
	return s.goalsImpl
}

func (s *chatSvc) GetGoal(ctx context.Context, req *GoalRequest) (*GoalResponse, error) {
	if req == nil || req.SessionID <= 0 {
		return nil, i18n.NewError(ctx, code.InvalidParameter)
	}
	g, err := s.goals().Get(ctx, req.SessionID)
	if err != nil {
		return nil, err
	}
	return &GoalResponse{Goal: chatGoalFromRuntime(g)}, nil
}

func (s *chatSvc) SetGoal(ctx context.Context, req *SetGoalRequest) (*GoalResponse, error) {
	if req == nil || req.SessionID <= 0 {
		return nil, i18n.NewError(ctx, code.InvalidParameter)
	}
	if req.Objective == nil && req.Status == nil && req.TokenBudget == nil {
		return nil, i18n.NewError(ctx, code.InvalidParameter)
	}
	g, err := s.goals().Set(ctx, req.SessionID, goal.Patch{
		Objective:   req.Objective,
		Status:      req.Status,
		TokenBudget: req.TokenBudget,
	})
	if err != nil {
		return nil, err
	}
	return &GoalResponse{Goal: chatGoalFromRuntime(g)}, nil
}

func (s *chatSvc) StartGoal(ctx context.Context, req *StartGoalRequest) (*StartGoalResponse, error) {
	if req == nil || req.AgentID <= 0 {
		return nil, i18n.NewError(ctx, code.InvalidParameter)
	}
	if req.Objective == nil || strings.TrimSpace(*req.Objective) == "" {
		return nil, i18n.NewError(ctx, code.InvalidParameter)
	}
	sessionID, g, err := s.goals().Start(ctx, goal.StartInput{
		AgentID:        req.AgentID,
		ProjectID:      req.ProjectID,
		PermissionMode: req.PermissionMode,
		Objective:      *req.Objective,
		Patch:          goal.Patch{Status: req.Status, TokenBudget: req.TokenBudget},
	})
	if err != nil {
		return nil, err
	}
	return &StartGoalResponse{SessionID: sessionID, Goal: chatGoalFromRuntime(g)}, nil
}

func (s *chatSvc) ClearGoal(ctx context.Context, req *ClearGoalRequest) (*ClearGoalResponse, error) {
	if req == nil || req.SessionID <= 0 {
		return nil, i18n.NewError(ctx, code.InvalidParameter)
	}
	cleared, err := s.goals().Clear(ctx, req.SessionID)
	if err != nil {
		return nil, err
	}
	return &ClearGoalResponse{Cleared: cleared}, nil
}

func (h chatGoalHost) ResolveAgentBackend(
	ctx context.Context, sess *chat_entity.Session, agentID, projectID int64,
) (*agent_entity.Agent, *agent_backend_entity.AgentBackend, *llm_provider_entity.LLMProvider, error) {
	return h.s.resolveAgentBackend(ctx, sess, agentID, projectID)
}

func (h chatGoalHost) ResolveSessionProvider(
	ctx context.Context, sess *chat_entity.Session,
	be *agent_backend_entity.AgentBackend, prov *llm_provider_entity.LLMProvider,
) (*llm_provider_entity.LLMProvider, *blocks.NoticeBlock, error) {
	return h.s.resolveSessionProvider(ctx, sess, be, prov)
}

func (h chatGoalHost) SelectRunner(
	ctx context.Context, be *agent_backend_entity.AgentBackend, sessionID int64,
) (agentruntime.Runtime, error) {
	return h.s.selectRunner(ctx, be, sessionID)
}

func (h chatGoalHost) EffectiveLLM(
	ctx context.Context, sess *chat_entity.Session,
	be *agent_backend_entity.AgentBackend, prov *llm_provider_entity.LLMProvider,
) (*agentruntime.EffectiveLLMConfig, error) {
	return h.s.effectiveLLMForNonRemoteTurn(ctx, sess, be, prov)
}

func (h chatGoalHost) ResolveSessionCwd(
	ctx context.Context, sess *chat_entity.Session, be *agent_backend_entity.AgentBackend,
) (string, error) {
	return resolveSessionCwd(ctx, sess, be)
}

func (h chatGoalHost) RemoteLeaseFor(ctx context.Context, be *agent_backend_entity.AgentBackend) (int64, bool) {
	if !beTargetsRemote(be) {
		return 0, false
	}
	return localPairedDeviceID(ctx, be.DeviceFingerprint)
}

func (h chatGoalHost) ReleaseRemoteRuntime(deviceID, sessionID int64) {
	h.s.releaseRemoteRuntime(deviceID, sessionID)
}

func (h chatGoalHost) ResolveProjectContext(ctx context.Context, projectID, agentID int64) (int64, error) {
	return h.s.resolveProjectContext(ctx, projectID, agentID)
}

func (h chatGoalHost) PinExecTargetIfUnset(
	ctx context.Context, sess *chat_entity.Session, be *agent_backend_entity.AgentBackend,
) {
	h.s.pinExecTargetIfUnset(ctx, sess, be)
}

func (h chatGoalHost) SessionTitle(text string) string { return sessionTitleFromFirstMessage(text) }

func (h chatGoalHost) Fail(ctx context.Context, cause error) error {
	return operationFailedWithCause(ctx, cause)
}

func chatGoalFromRuntime(g *agentruntime.Goal) *ChatGoal {
	if g == nil {
		return nil
	}
	return &ChatGoal{
		ThreadID:        g.ThreadID,
		Objective:       g.Objective,
		Status:          g.Status,
		TokenBudget:     g.TokenBudget,
		TokensUsed:      g.TokensUsed,
		TimeUsedSeconds: g.TimeUsedSeconds,
		CreatedAt:       g.CreatedAt,
		UpdatedAt:       g.UpdatedAt,
	}
}
