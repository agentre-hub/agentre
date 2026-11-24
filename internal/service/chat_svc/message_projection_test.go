package chat_svc

import (
	"context"
	"testing"

	"github.com/cago-frame/agents/agent/blocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/agentre-hub/agentre/internal/model/entity/chat_entity"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
	chatblocks "github.com/agentre-hub/agentre/internal/pkg/transcript/blocks"
	"github.com/agentre-hub/agentre/internal/pkg/transcript/handlers"
	"github.com/agentre-hub/agentre/internal/pkg/transcript/turn"
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

// Given 一轮里执行侧报来的工具事件带着「AI 当时所在目录」(外层调用与结果、子代理内层
// 调用与结果各一组,外加一条没带目录的调用);When 经落库处理器累积、按落库形态编码
// (SetBlocks)、再按重新加载的路径投影(toChatMessage);Then 每张工具卡的 Cwd 都是
// 当时报来的那个,没报的为空(前端据此回落到会话工作目录),记录本身不成独立的卡。
func TestToChatMessage_ToolCwdSurvivesSaveAndReload(t *testing.T) {
	ctx := context.Background()
	acc := turn.New()
	for _, ev := range []agentruntime.Event{
		agentruntime.ToolCall{ID: "t1", Name: "Bash", Cwd: "/repo"},
		agentruntime.ToolResult{ToolCallID: "t1", Content: "ok", Cwd: "/repo/wt"},
		agentruntime.ToolCall{ID: "t2", Name: "Task"},
		agentruntime.ToolCall{ID: "n1", Name: "Read", ParentToolCallID: "t2", Cwd: "/repo/sub"},
		agentruntime.ToolResult{ToolCallID: "n1", Content: "ok", ParentToolCallID: "t2", Cwd: "/repo/sub2"},
	} {
		var err error
		switch ev.(type) {
		case agentruntime.ToolCall:
			err = handlers.ToolCallHandler{}.Apply(ctx, ev, acc, nil, nil)
		case agentruntime.ToolResult:
			err = handlers.ToolResultHandler{}.Apply(ctx, ev, acc, nil, nil)
		}
		require.NoError(t, err)
	}
	saved := &chat_entity.Message{ID: 1, SessionID: 9, Role: "assistant"}
	require.NoError(t, saved.SetBlocks(acc.Finalize()))
	reloaded := &chat_entity.Message{ID: 1, SessionID: 9, Role: "assistant", BlocksJSON: saved.BlocksJSON}

	cm, err := toChatMessage(reloaded)
	require.NoError(t, err)

	type card struct{ Type, ID, Cwd string }
	got := make([]card, 0, len(cm.Blocks))
	for _, b := range cm.Blocks {
		got = append(got, card{b.Type, b.ToolCallID, b.Cwd})
	}
	assert.Equal(t, []card{
		{ChatBlockTypeToolUse, "t1", "/repo"},
		{ChatBlockTypeToolResult, "t1", "/repo/wt"},
		{ChatBlockTypeToolUse, "t2", ""},
		{ChatBlockTypeToolUse, "n1", "/repo/sub"},
		{ChatBlockTypeToolResult, "n1", "/repo/sub2"},
	}, got)
}

// Given 本轮之前落库、没有目录记录的旧消息;When 重新加载;Then 工具卡的 Cwd 为空,
// 投影与今天逐卡相同。
func TestToChatMessage_OldMessageWithoutToolCwdHasEmptyCwd(t *testing.T) {
	m := &chat_entity.Message{ID: 1, SessionID: 9, Role: "assistant", BlocksJSON: `[` +
		`{"type":"tool_use","data":{"id":"t1","name":"Bash","input":{"command":"ls"}}},` +
		`{"type":"tool_result","data":{"tool_use_id":"t1","content":[{"type":"text","data":{"text":"ok"}}]}}]`}

	cm, err := toChatMessage(m)
	require.NoError(t, err)
	require.Len(t, cm.Blocks, 2)
	assert.Empty(t, cm.Blocks[0].Cwd)
	assert.Empty(t, cm.Blocks[1].Cwd)
}
