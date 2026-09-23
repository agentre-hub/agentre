package chat_svc

import (
	"context"
	"slices"
	"strings"

	"github.com/cago-frame/cago/pkg/i18n"

	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
	"github.com/agentre-hub/agentre/internal/pkg/code"
	"github.com/agentre-hub/agentre/internal/pkg/transcript/blocks"
	"github.com/agentre-hub/agentre/internal/repository/agent_backend_repo"
	"github.com/agentre-hub/agentre/internal/repository/agent_repo"
	"github.com/agentre-hub/agentre/internal/repository/chat_repo"
)

type ResolveExecApprovalRequest struct {
	SessionID  int64  `json:"sessionId"`
	ApprovalID string `json:"approvalId"`
	Decision   string `json:"decision"`
}

type ResolveExecApprovalResponse struct {
	Status   string `json:"status"`
	Decision string `json:"decision,omitempty"`
}

func (s *chatSvc) ResolveExecApproval(ctx context.Context, req *ResolveExecApprovalRequest) (*ResolveExecApprovalResponse, error) {
	if req == nil || req.SessionID <= 0 || strings.TrimSpace(req.ApprovalID) == "" || !agentruntime.IsApprovalDecision(req.Decision) {
		return nil, i18n.NewError(ctx, code.InvalidParameter)
	}
	session, err := chat_repo.Session().Find(ctx, req.SessionID)
	if err != nil || session == nil {
		return nil, i18n.NewError(ctx, code.ChatSessionNotFound)
	}
	agent, err := agent_repo.Agent().Find(ctx, session.AgentID)
	if err != nil || agent == nil {
		return nil, i18n.NewError(ctx, code.AgentNotFound)
	}
	backend, err := agent_backend_repo.AgentBackend().Find(ctx, agent.AgentBackendID)
	if err != nil || backend == nil {
		return nil, i18n.NewError(ctx, code.AgentBackendNotFound)
	}
	runner, err := s.selectRunner(ctx, backend, session.ID)
	if err != nil {
		return nil, i18n.NewError(ctx, code.AgentBackendTypeUnsupported)
	}
	sink, ok := runner.(agentruntime.ExecApprovalSink)
	if !ok {
		return nil, i18n.NewError(ctx, code.AgentBackendTypeUnsupported)
	}
	resolution, err := sink.ResolveExecApproval(ctx, session.ID, strings.TrimSpace(req.ApprovalID), req.Decision)
	if err != nil {
		return nil, err
	}
	return &ResolveExecApprovalResponse{Status: resolution.Status, Decision: resolution.Decision}, nil
}

func execApprovalBlockToDTO(block blocks.ExecApprovalBlock) *ChatBlockExecApproval {
	return &ChatBlockExecApproval{
		ID: block.ID, Kind: block.Kind, CommandText: block.CommandText, CommandPreview: block.CommandPreview,
		Description: block.Description, ToolName: block.ToolName, PluginName: block.PluginName,
		Warnings: slices.Clone(block.Warnings), ActionCategory: block.ActionCategory,
		MessageTargets: slices.Clone(block.MessageTargets), RecipientCount: block.RecipientCount,
		PaymentAmount: block.PaymentAmount, PaymentPayee: block.PaymentPayee,
		PublishTarget: block.PublishTarget, PublishVisibility: block.PublishVisibility,
		AutomationName:   block.AutomationName,
		AllowedDecisions: append([]string(nil), block.AllowedDecisions...),
		Host:             block.Host, NodeID: block.NodeID, AgentID: block.AgentID,
		Status: block.Status, Decision: block.Decision, ResolvedBy: block.ResolvedBy,
		CreatedAtMs: block.CreatedAtMs, ExpiresAtMs: block.ExpiresAtMs, ResolvedAtMs: block.ResolvedAtMs,
	}
}

func execApprovalBlockToChatBlock(block blocks.ExecApprovalBlock) ChatBlock {
	return ChatBlock{Type: ChatBlockTypeExecApproval, ExecApproval: execApprovalBlockToDTO(block)}
}
