package hermes

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
)

func translateOne(t *testing.T, kind EventKind, payload string) agentruntime.Event {
	t.Helper()
	events, _, _ := translate(Event{Kind: kind, Payload: json.RawMessage(payload)})
	require.Len(t, events, 1)
	return events[0]
}

func TestTranslate_TerminalCwd(t *testing.T) {
	t.Run("terminal call carries its workdir", func(t *testing.T) {
		call := translateOne(t, EventToolStart, `{"tool_id":"t1","name":"terminal","args":{"command":"ls","workdir":"/repo/wt"}}`).(agentruntime.ToolCall)
		assert.Equal(t, "/repo/wt", call.Cwd)
	})
	t.Run("terminal call without workdir or with a relative one is unknown", func(t *testing.T) {
		for _, args := range []string{`{"command":"ls"}`, `{"command":"ls","workdir":"sub"}`, `{"command":"ls","workdir":""}`} {
			call := translateOne(t, EventToolStart, `{"tool_id":"t1","name":"terminal","args":`+args+`}`).(agentruntime.ToolCall)
			assert.Empty(t, call.Cwd, args)
		}
	})
	t.Run("other tools do not claim a workdir", func(t *testing.T) {
		call := translateOne(t, EventToolStart, `{"tool_id":"t1","name":"read_file","args":{"workdir":"/x"}}`).(agentruntime.ToolCall)
		assert.Empty(t, call.Cwd)
	})
	t.Run("result reports its cwd", func(t *testing.T) {
		res := translateOne(t, EventToolComplete, `{"tool_id":"t1","name":"terminal","args":{"workdir":"/old"},"result":{"output":"ok","cwd":"/repo/wt"}}`).(agentruntime.ToolResult)
		assert.Equal(t, "/repo/wt", res.Cwd)
	})
	t.Run("result without cwd falls back to the call workdir", func(t *testing.T) {
		res := translateOne(t, EventToolComplete, `{"tool_id":"t1","name":"terminal","args":{"workdir":"/repo/wt"},"result":{"output":"ok"}}`).(agentruntime.ToolResult)
		assert.Equal(t, "/repo/wt", res.Cwd)
	})
	t.Run("string result with no workdir is unknown", func(t *testing.T) {
		res := translateOne(t, EventToolComplete, `{"tool_id":"t1","name":"terminal","result":"pong"}`).(agentruntime.ToolResult)
		assert.Empty(t, res.Cwd)
	})
}
