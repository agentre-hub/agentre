package remote

import (
	"testing"

	"github.com/cago-frame/agents/agent/blocks"
	"github.com/stretchr/testify/require"

	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
	transcriptblocks "github.com/agentre-hub/agentre/internal/pkg/transcript/blocks"
)

// Given 一段落库历史,外层工具调用旁记着「AI 当时所在目录」(tool_cwd,只给界面看);
// When 编成发往 agentred 的历史;Then tool_cwd 不在其中,其余块原样保序。
//
// 在这条改动之前构建的 agentred 不认识 tool_cwd 这个块类型,blocks.DecodeAll 遇到它
// 会整轮拒跑 —— 决策 18 要求新加的目录对旧端无害,所以它不能越过这条边界。
func TestEncodeHistory_GivenToolCwdBlock_ThenItNeverReachesTheExecutingSide(t *testing.T) {
	history := []agentruntime.HistoryMessage{{
		Role: "assistant",
		Blocks: []blocks.ContentBlock{
			&blocks.ToolUseBlock{ID: "t1", Name: "Bash"},
			&transcriptblocks.ToolCwdBlock{ToolCallID: "t1", Cwd: "/repo/wt"},
			&blocks.ToolResultBlock{ToolUseID: "t1", Content: []blocks.ContentBlock{blocks.TextBlock{Text: "ok"}}},
			transcriptblocks.ToolCwdBlock{ToolCallID: "t1", ResultCwd: "/repo/wt"},
		},
	}}

	encoded, err := encodeHistory(history)
	require.NoError(t, err)
	require.Len(t, encoded, 1)
	types := make([]string, 0, len(encoded[0].Blocks))
	for _, sb := range encoded[0].Blocks {
		types = append(types, sb.Type)
	}
	require.Equal(t, []string{"tool_use", "tool_result"}, types)
}
