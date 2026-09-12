package piagent

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Given Pi emits each blocking extension dialog plus a fire-and-forget method,
// when the stream consumes them, then each dialog receives one matching
// serialized cancellation and the fire-and-forget request receives none.
func TestStreamCancelsBlockingExtensionUIDialogsExactlyOnce(t *testing.T) {
	script := strings.Join([]string{
		`{"type":"response","command":"prompt","success":true}`,
		`{"type":"extension_ui_request","id":"confirm-1","method":"confirm","title":"Confirm","message":"approve?"}`,
		`{"type":"extension_ui_request","id":"select-1","method":"select","title":"Select","options":["yes","no"]}`,
		`{"type":"extension_ui_request","id":"input-1","method":"input","title":"Input","message":"value"}`,
		`{"type":"extension_ui_request","id":"editor-1","method":"editor","title":"Editor","message":"content"}`,
		`{"type":"extension_ui_request","id":"notify-1","method":"notify","message":"informational"}`,
		`{"type":"agent_settled"}`,
		`{"type":"response","command":"get_session_stats","success":true,"data":{}}`,
		"",
	}, "\n")
	client, proc := newCaptureClient(script)

	s, err := client.Stream(context.Background(), "use project agent")
	require.NoError(t, err)
	for s.Next() {
	}
	require.NoError(t, s.Err())

	frames := stdinFrames(t, proc.stdin.String())
	responses := make(map[string]int)
	for _, frame := range frames {
		if frame["type"] != "extension_ui_response" {
			continue
		}
		id, _ := frame["id"].(string)
		responses[id]++
		assert.Equal(t, true, frame["cancelled"]) //nolint:misspell // Pin the required Pi RPC wire key.
		assert.Len(t, frame, 3, "dialog response must contain only type, id, and the cancellation flag")
	}
	assert.Equal(t, map[string]int{
		"confirm-1": 1,
		"select-1":  1,
		"input-1":   1,
		"editor-1":  1,
	}, responses)
	assert.NotContains(t, responses, "notify-1")
}
