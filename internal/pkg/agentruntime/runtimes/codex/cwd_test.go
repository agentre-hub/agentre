package codex

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
	"github.com/agentre-hub/agentre/pkg/codex"
)

func translateTool(t *testing.T, kind codex.EventKind, name, input string) agentruntime.Event {
	t.Helper()
	out, _, _ := translate(codex.Event{
		Kind: kind,
		Tool: &codex.ToolEvent{ID: "c1", Name: name, Input: json.RawMessage(input)},
	})
	require.Len(t, out, 1)
	return out[0]
}

func TestTranslate_CommandCwd(t *testing.T) {
	t.Run("command start carries the command cwd", func(t *testing.T) {
		call := translateTool(t, codex.EventPreToolUse, "command_execution", `{"command":"ls","cwd":"/repo/wt"}`).(agentruntime.ToolCall)
		assert.Equal(t, "/repo/wt", call.Cwd)
	})
	t.Run("command result carries the same cwd", func(t *testing.T) {
		res := translateTool(t, codex.EventPostToolUse, "command_execution", `{"command":"ls","cwd":"/repo/wt"}`).(agentruntime.ToolResult)
		assert.Equal(t, "/repo/wt", res.Cwd)
	})
	t.Run("missing, relative or malformed cwd stays unknown", func(t *testing.T) {
		for _, in := range []string{`{"command":"ls"}`, `{"command":"ls","cwd":""}`, `{"command":"ls","cwd":"rel/dir"}`, `{"command":"ls","cwd":5}`, `not json`} {
			call := translateTool(t, codex.EventPreToolUse, "command_execution", in).(agentruntime.ToolCall)
			assert.Empty(t, call.Cwd, in)
		}
	})
	t.Run("other tools do not claim a cwd", func(t *testing.T) {
		call := translateTool(t, codex.EventPreToolUse, "mcp_tool", `{"cwd":"/x"}`).(agentruntime.ToolCall)
		assert.Empty(t, call.Cwd)
	})
}
