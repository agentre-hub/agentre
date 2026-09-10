package handlers

import (
	"context"

	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
	"github.com/agentre-hub/agentre/internal/service/chat_svc/blocks"
	"github.com/agentre-hub/agentre/internal/service/chat_svc/turn"
)

type ExecApprovalRequestedHandler struct{}

func (ExecApprovalRequestedHandler) Apply(ctx context.Context, ev agentruntime.Event, acc *turn.Accumulator, emit turn.Emitter, _ turn.View, tc *turn.TurnContext) error {
	request := ev.(agentruntime.ExecApprovalRequested)
	block := &blocks.ExecApprovalBlock{
		ID: request.ID, CommandText: request.CommandText, CommandPreview: request.CommandPreview,
		AllowedDecisions: append([]string(nil), request.AllowedDecisions...),
		Host:             request.Host, NodeID: request.NodeID, AgentID: request.AgentID,
		Status: "pending", CreatedAtMs: request.CreatedAtMs, ExpiresAtMs: request.ExpiresAtMs,
	}
	acc.AddBlock(block, "exec_approval:"+request.ID)
	if emit != nil {
		emit.Emit(ctx, streamOf(tc), map[string]any{"kind": "exec_approval", "execApproval": block})
	}
	if tc != nil && tc.SessionTransitioner != nil && tc.Session != nil {
		tc.SessionTransitioner.MarkWaiting(ctx, tc.Session, tc.Stream)
	}
	return nil
}

type ExecApprovalResolvedHandler struct{}

func (ExecApprovalResolvedHandler) Apply(ctx context.Context, ev agentruntime.Event, acc *turn.Accumulator, emit turn.Emitter, _ turn.View, tc *turn.TurnContext) error {
	resolved := ev.(agentruntime.ExecApprovalResolved)
	var captured *blocks.ExecApprovalBlock
	if !turn.Mutate[blocks.ExecApprovalBlock](acc, "exec_approval:"+resolved.ID, func(block *blocks.ExecApprovalBlock) {
		block.Status = resolved.Status
		block.Decision = resolved.Decision
		block.ResolvedBy = resolved.ResolvedBy
		block.ResolvedAtMs = resolved.ResolvedAtMs
		captured = block
	}) {
		return nil
	}
	if emit != nil {
		emit.Emit(ctx, streamOf(tc), map[string]any{"kind": "exec_approval", "execApproval": captured})
	}
	if tc != nil && tc.SessionTransitioner != nil && tc.Session != nil && !hasPendingExecApproval(acc) {
		tc.SessionTransitioner.MarkRunning(ctx, tc.Session, tc.Stream)
	}
	return nil
}

func hasPendingExecApproval(acc *turn.Accumulator) bool {
	for _, content := range acc.Snapshot() {
		switch block := content.(type) {
		case *blocks.ExecApprovalBlock:
			if block != nil && block.Status == "pending" {
				return true
			}
		case blocks.ExecApprovalBlock:
			if block.Status == "pending" {
				return true
			}
		}
	}
	return false
}
