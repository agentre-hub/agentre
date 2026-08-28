package handlers

import (
	"context"
	"testing"

	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
	"github.com/agentre-hub/agentre/internal/service/chat_svc/turn"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type waitingTransitions struct {
	waiting int
	running int
}

func (f *waitingTransitions) MarkWaiting(context.Context, any, string) { f.waiting++ }
func (f *waitingTransitions) MarkRunning(context.Context, any, string) { f.running++ }

func TestWaitingProjection_MultipleRequestsRequireAllResolutions(t *testing.T) {
	// Given one turn has an unanswered user question and a tool approval.
	transitions := &waitingTransitions{}
	tc := &turn.TurnContext{
		Session:             struct{}{},
		Stream:              "chat:event:1:2",
		SessionTransitioner: transitions,
		Waits:               turn.NewWaitTracker(),
	}
	acc := turn.New()
	require.NoError(t, UserAskRequestHandler{}.Apply(context.Background(),
		agentruntime.UserAskRequest{RequestID: "ask-a"}, acc, nil, nil, tc))
	require.NoError(t, ToolPermissionRequestHandler{}.Apply(context.Background(),
		agentruntime.ToolPermissionRequest{RequestID: "approval-b", ToolName: "Bash"}, acc, nil, nil, tc))

	// When only one request resolves, then the session remains waiting.
	require.NoError(t, UserAskResolvedHandler{}.Apply(context.Background(),
		agentruntime.UserAskResolved{RequestID: "ask-a", Skipped: true}, acc, nil, nil, tc))
	assert.Equal(t, 1, transitions.waiting, "only the first unique waiter enters waiting")
	assert.Zero(t, transitions.running, "one unresolved waiter still owns waiting")

	// When the final request resolves, then and only then the projection returns to running.
	require.NoError(t, ToolPermissionResolvedHandler{}.Apply(context.Background(),
		agentruntime.ToolPermissionResolved{RequestID: "approval-b", Allowed: false}, acc, nil, nil, tc))
	assert.Equal(t, 1, transitions.running)

	// Duplicate resolution is idempotent and cannot flip status again.
	require.NoError(t, ToolPermissionResolvedHandler{}.Apply(context.Background(),
		agentruntime.ToolPermissionResolved{RequestID: "approval-b", Allowed: false}, acc, nil, nil, tc))
	assert.Equal(t, 1, transitions.running)
}
