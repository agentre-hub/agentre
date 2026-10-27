package chat_svc

import (
	"testing"

	"github.com/cago-frame/agents/agent/blocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/agentre-hub/agentre/internal/model/entity/chat_entity"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
	chatblocks "github.com/agentre-hub/agentre/internal/pkg/transcript/blocks"
)

func agrctlCardRuns() []agentruntime.SubagentRun {
	return []agentruntime.SubagentRun{{
		ID: "run-0", Agent: "reviewer", Task: "审查 diff", Model: "claude-haiku-4-5", Status: "completed",
	}}
}

func TestToChatMessage_AgrctlShellCallReloadsAsAgentSpawn(t *testing.T) {
	for _, tool := range []string{"Bash", "command_execution", "bash"} {
		t.Run(tool, func(t *testing.T) {
			m := &chat_entity.Message{ID: 1, SessionID: 9, Role: "assistant"}
			require.NoError(t, m.SetBlocks([]blocks.ContentBlock{
				blocks.ToolUseBlock{ID: "sh", Name: tool, Input: map[string]any{"command": "agrctl send --wait reviewer 审查 diff"}},
				chatblocks.SubagentStateBlock{
					ParentToolCallID: "sh", Kind: agentruntime.SubagentKindAgrctl, Origin: agentruntime.SubagentOriginAgrctl,
					ChildSessionID: 42, Isolated: true, FinalText: "LGTM", Status: "completed",
					Runs: agrctlCardRuns(), ToolUses: 3, TotalTokens: 900, DurationMs: 1200, Model: "claude-haiku-4-5",
				},
			}))

			cm, err := toChatMessage(m)
			require.NoError(t, err)
			require.Len(t, cm.Blocks, 1)
			outer := cm.Blocks[0]
			require.NotNil(t, outer.Canonical)
			assert.Equal(t, "agent.spawn", string(outer.Canonical.Kind))
			require.NotNil(t, outer.Canonical.AgentSpawn)
			spawn := outer.Canonical.AgentSpawn
			assert.Equal(t, "审查 diff", spawn.TaskDescription)
			assert.Equal(t, "claude-haiku-4-5", spawn.Model)
			require.Len(t, spawn.Runs, 1)
			assert.Equal(t, "reviewer", spawn.Runs[0].Agent)
			assert.Equal(t, 3, spawn.ToolUses)
			assert.Equal(t, "completed", spawn.Status)
			require.NotNil(t, outer.Subagent)
			assert.Equal(t, agentruntime.SubagentOriginAgrctl, outer.Subagent.Origin)
			assert.Equal(t, int64(42), outer.Subagent.ChildSessionID)
			assert.True(t, outer.Subagent.Isolated)
			assert.Equal(t, "LGTM", outer.Subagent.FinalText)
		})
	}
}

func TestToChatMessage_ShellCallWithoutAgrctlOriginStaysCommand(t *testing.T) {
	m := &chat_entity.Message{ID: 1, SessionID: 9, Role: "assistant"}
	require.NoError(t, m.SetBlocks([]blocks.ContentBlock{
		blocks.ToolUseBlock{ID: "sh", Name: "Bash", Input: map[string]any{"command": "sleep 1", "run_in_background": true}},
		chatblocks.SubagentStateBlock{ParentToolCallID: "sh", Kind: agentruntime.SubagentKindLocalBash, Status: "running", Runs: agrctlCardRuns()},
	}))

	cm, err := toChatMessage(m)
	require.NoError(t, err)
	require.Len(t, cm.Blocks, 1)
	if cm.Blocks[0].Canonical != nil {
		assert.NotEqual(t, "agent.spawn", string(cm.Blocks[0].Canonical.Kind))
	}
	assert.Empty(t, cm.Blocks[0].Subagent.Origin)
}
