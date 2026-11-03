package goal

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/agent_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/chat_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/llm_provider_entity"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/capability"
)

// markerHost 只实现 controllerForSession 用到的几件事，其余方法走内嵌的 nil 接口。
type markerHost struct {
	Host
	runner agentruntime.Runtime
}

func (h markerHost) SelectRunner(context.Context, *agent_backend_entity.AgentBackend, int64) (agentruntime.Runtime, error) {
	return h.runner, nil
}
func (markerHost) RemoteLeaseFor(context.Context, *agent_backend_entity.AgentBackend) (int64, bool) {
	return 0, false
}
func (markerHost) ResolveSessionCwd(context.Context, *chat_entity.Session, *agent_backend_entity.AgentBackend) (string, error) {
	return "/tmp/work", nil
}
func (markerHost) EffectiveLLM(context.Context, *chat_entity.Session, *agent_backend_entity.AgentBackend, *llm_provider_entity.LLMProvider) (*agentruntime.EffectiveLLMConfig, error) {
	return nil, nil
}

type markerRunner struct {
	agentruntime.Runtime
	agentruntime.GoalController
}

func (markerRunner) Capabilities() capability.Capabilities {
	return capability.Capabilities{Set: map[capability.Capability]bool{capability.CapGoal: true}}
}

// Given 会话带 conversation_id，When 组装 goal 请求，
// Then GoalRequest 携带它（codex goal 路径起的 CLI 据此带 AGENTRE_SESSION）。
func TestControllerForSession_GivenConversationID_ThenGoalRequestCarriesIt(t *testing.T) {
	c := New(markerHost{runner: markerRunner{}})
	sess := &chat_entity.Session{ID: 5, ConversationID: "conv-goal-1"}

	_, req, release, err := c.controllerForSession(context.Background(), sess,
		&agent_entity.Agent{ID: 7}, &agent_backend_entity.AgentBackend{Type: string(agent_backend_entity.TypeCodex)}, nil)
	require.NoError(t, err)
	defer release()
	assert.Equal(t, "conv-goal-1", req.ConversationID)
}
