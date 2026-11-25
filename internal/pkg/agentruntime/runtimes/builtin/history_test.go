package builtin

import (
	"testing"

	"github.com/cago-frame/agents/agent/blocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
	"github.com/agentre-hub/agentre/internal/pkg/shellloc"
)

// 落库的 bash 调用入参带着 shellloc.WorktreeAddKey(认领用的标记,模型从没发过它)。
// 历史喂回模型时要去掉它,别的入参与块原样保留,落库的块也不被改。
func TestHistoryMessages_DropsTheWorktreeAddMarkerFromReplayedToolInput(t *testing.T) {
	stored := &blocks.ToolUseBlock{ID: "a", Name: "bash", Input: map[string]any{
		"command": "git worktree add ../wt", shellloc.WorktreeAddKey: "/w/wt",
	}}
	plain := blocks.ToolUseBlock{ID: "b", Name: "read", Input: map[string]any{"path": "x.go"}}
	text := blocks.TextBlock{Text: "hi"}

	got := historyMessages([]agentruntime.HistoryMessage{
		{Role: "assistant", Blocks: []blocks.ContentBlock{text, stored, plain}},
	})

	require.Len(t, got, 1)
	require.Len(t, got[0].Content, 3)
	assert.Equal(t, text, got[0].Content[0])
	replayed, ok := got[0].Content[1].(*blocks.ToolUseBlock)
	require.True(t, ok)
	assert.Equal(t, map[string]any{"command": "git worktree add ../wt"}, replayed.Input)
	assert.Equal(t, plain, got[0].Content[2])
	assert.Contains(t, stored.Input, shellloc.WorktreeAddKey, "the persisted block is not mutated")
}
