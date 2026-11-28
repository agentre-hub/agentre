package openclaw

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
)

func runTool(t *testing.T, frames ...string) []agentruntime.Event {
	t.Helper()
	a := &activeTurn{ctx: context.Background(), out: make(chan agentruntime.Event, 8)}
	for _, f := range frames {
		a.handleTool(json.RawMessage(f))
	}
	close(a.out)
	var got []agentruntime.Event
	for ev := range a.out {
		got = append(got, ev)
	}
	return got
}

func TestHandleTool_ExecWorkdir(t *testing.T) {
	t.Run("exec call carries workdir and its result the same location", func(t *testing.T) {
		got := runTool(t,
			`{"phase":"start","name":"exec","toolCallId":"e1","args":{"command":"ls","workdir":"/repo/wt"}}`,
			`{"phase":"result","name":"exec","toolCallId":"e1","result":"ok"}`)
		require.Len(t, got, 2)
		assert.Equal(t, "/repo/wt", got[0].(agentruntime.ToolCall).Cwd)
		assert.Equal(t, "/repo/wt", got[1].(agentruntime.ToolResult).Cwd)
	})
	t.Run("missing or relative workdir is unknown", func(t *testing.T) {
		for _, args := range []string{`{"command":"ls"}`, `{"command":"ls","workdir":"sub"}`} {
			got := runTool(t,
				`{"phase":"start","name":"exec","toolCallId":"e1","args":`+args+`}`,
				`{"phase":"result","name":"exec","toolCallId":"e1","result":"ok"}`)
			require.Len(t, got, 2)
			assert.Empty(t, got[0].(agentruntime.ToolCall).Cwd, args)
			assert.Empty(t, got[1].(agentruntime.ToolResult).Cwd, args)
		}
	})
	t.Run("other tools do not claim a workdir", func(t *testing.T) {
		got := runTool(t, `{"phase":"start","name":"read","toolCallId":"r1","args":{"workdir":"/x"}}`)
		require.Len(t, got, 1)
		assert.Empty(t, got[0].(agentruntime.ToolCall).Cwd)
	})
}
