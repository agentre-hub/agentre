package handlers_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
	agentrewire "github.com/agentre-hub/agentre/pkg/wire/agentrewire"
)

// Given 对端起一轮并报了 conversation_id，When agentred 组 RunRequest，
// Then runner 收到的请求携带这条对话身份（而不是按对端隔离后的 rid）。
func TestRuntime_Run_GivenConversationID_WhenExecuting_ThenRunRequestCarriesItForSessionMarker(t *testing.T) {
	rt := &fullRT{}
	ctx, notif, h := setupRuntimeTestWithCLIOverlay(t, rt, func(string) (string, bool) { return "", false })
	be := agent_backend_entity.AgentBackend{Type: string(agent_backend_entity.TypeClaudeCode)}

	_, err := h.Run(ctx, &agentrewire.RuntimeRunRequest{Backend: backendProto(t, be), ConversationId: convID(91)})
	require.NoError(t, err)
	_ = notif.waitFrames(t, 1)
	rt.mu.Lock()
	runReqs := append([]runCall(nil), rt.runReqs...)
	rt.mu.Unlock()
	require.Len(t, runReqs, 1)
	assert.Equal(t, convID(91), runReqs[0].req.ConversationID)
}

// Given 对端发来带 conversation_id 的 goal 请求，When agentred 组 GoalRequest，
// Then runtime 收到的请求携带这条对话身份（codex goal 路径据此给 CLI 注入会话标记）。
func TestRuntime_SetGoal_GivenConversationID_WhenExecuting_ThenGoalRequestCarriesIt(t *testing.T) {
	rt := &fullRT{}
	ctx, _, _, _, h := setupRuntimeTest(t, rt)
	objective := "ship"

	_, err := h.SetGoal(ctx, &agentrewire.RuntimeGoalRequest{
		ConversationId: convID(92),
		AgentId:        7,
		Backend:        backendProto(t, agent_backend_entity.AgentBackend{Type: string(agent_backend_entity.TypeCodex)}),
		Objective:      &objective,
	})
	require.NoError(t, err)
	require.Len(t, rt.setGoalCalls, 1)
	assert.Equal(t, convID(92), rt.setGoalCalls[0].req.ConversationID)
}
