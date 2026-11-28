package acp

import (
	"encoding/json"
	"testing"

	acpsdk "github.com/coder/acp-go-sdk"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/canonical"
)

// persistedRecompute 模拟落库再读回:只保留 ToolCall 的 Name + Input,走重算入口。
func persistedRecompute(t *testing.T, c *acpsdk.SessionUpdateToolCall) (canonical.CanonicalTool, bool) {
	t.Helper()
	ev, _ := translateToolCall(c)
	var input map[string]any
	require.NoError(t, json.Unmarshal(ev.Input, &input))
	return canonical.FromToolUse(ev.Name, input)
}

func TestPersistedACPToolCall_RecomputeMatchesLive(t *testing.T) {
	t.Run("edit 带 locations:落库后重算得到同一路径", func(t *testing.T) {
		c, ok := persistedRecompute(t, &acpsdk.SessionUpdateToolCall{
			ToolCallId: "e1", Title: "Editing main.go", Kind: acpsdk.ToolKindEdit,
			Locations: []acpsdk.ToolCallLocation{{Path: "/wt/main.go"}},
		})
		require.True(t, ok)
		fe, isEdit := c.(canonical.FileEdit)
		require.True(t, isEdit)
		assert.Equal(t, "/wt/main.go", fe.Files[0].Path)
	})
	t.Run("edit 带完整 content:重算为 FileWrite", func(t *testing.T) {
		c, ok := persistedRecompute(t, &acpsdk.SessionUpdateToolCall{
			ToolCallId: "e2", Title: "Write a.txt", Kind: acpsdk.ToolKindEdit,
			Locations: []acpsdk.ToolCallLocation{{Path: "/wt/a.txt"}},
			RawInput:  map[string]any{"content": "hi\n"},
		})
		require.True(t, ok)
		assert.Equal(t, "/wt/a.txt", c.(canonical.FileWrite).Path)
	})
	t.Run("多个 locations 全部保留", func(t *testing.T) {
		c, ok := persistedRecompute(t, &acpsdk.SessionUpdateToolCall{
			ToolCallId: "e3", Title: "Edit two", Kind: acpsdk.ToolKindEdit,
			Locations: []acpsdk.ToolCallLocation{{Path: "/wt/x.go"}, {Path: "/wt/y.go"}},
		})
		require.True(t, ok)
		fe := c.(canonical.FileEdit)
		require.Len(t, fe.Files, 2)
		assert.Equal(t, "/wt/y.go", fe.Files[1].Path)
	})
	t.Run("delete 重算为 deleted", func(t *testing.T) {
		c, ok := persistedRecompute(t, &acpsdk.SessionUpdateToolCall{
			ToolCallId: "d1", Title: "Delete", Kind: acpsdk.ToolKindDelete,
			Locations: []acpsdk.ToolCallLocation{{Path: "/wt/old.go"}},
		})
		require.True(t, ok)
		assert.Equal(t, canonical.ChangeDeleted, c.(canonical.FileEdit).Files[0].Kind)
	})
	t.Run("read 与 execute 重算不识别", func(t *testing.T) {
		for _, k := range []acpsdk.ToolKind{acpsdk.ToolKindRead, acpsdk.ToolKindExecute} {
			_, ok := persistedRecompute(t, &acpsdk.SessionUpdateToolCall{
				ToolCallId: "r", Title: "Reading", Kind: k,
				Locations: []acpsdk.ToolCallLocation{{Path: "/wt/r.go"}},
			})
			assert.False(t, ok, string(k))
		}
	})
}
