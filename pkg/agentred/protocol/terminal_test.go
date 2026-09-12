package protocol_test

import (
	"encoding/json"
	"testing"

	"github.com/agentre-hub/agentre/pkg/agentred/protocol"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTerminalOpenParams_GivenSuppliedIDWhenRoundtrippedThenPreservesCallerIdentity(t *testing.T) {
	in := protocol.TerminalOpenParams{
		TerminalID: "desktop-terminal-1",
		SessionID:  42,
		Cwd:        "/home/me",
		Shell:      "/bin/zsh",
		Env:        []string{"TERM=xterm-256color"},
		Cols:       120,
		Rows:       30,
	}
	b, err := json.Marshal(in)
	require.NoError(t, err)
	var out protocol.TerminalOpenParams
	require.NoError(t, json.Unmarshal(b, &out))
	assert.Equal(t, in, out)

	resultWire, err := json.Marshal(protocol.TerminalOpenResult{TerminalID: out.TerminalID})
	require.NoError(t, err)
	var result protocol.TerminalOpenResult
	require.NoError(t, json.Unmarshal(resultWire, &result))
	assert.Equal(t, in.TerminalID, result.TerminalID)
}

func TestTerminalParams_GivenLegacyOpenAndOrdinaryCloseWhenMarshaledThenNewFieldsAreOmitted(t *testing.T) {
	openWire, err := json.Marshal(protocol.TerminalOpenParams{Cols: 80, Rows: 24})
	require.NoError(t, err)
	assert.NotContains(t, string(openWire), "terminalId")

	closeWire, err := json.Marshal(protocol.TerminalCloseParams{TerminalID: "terminal-1"})
	require.NoError(t, err)
	assert.NotContains(t, string(closeWire), "cancelPendingOpen")
}

func TestTerminalCloseParams_GivenPendingCancellationIntentWhenRoundtrippedThenPreservesIntent(t *testing.T) {
	in := protocol.TerminalCloseParams{TerminalID: "terminal-1", CancelPendingOpen: true}
	b, err := json.Marshal(in)
	require.NoError(t, err)
	assert.Contains(t, string(b), `"cancelPendingOpen":true`)

	var out protocol.TerminalCloseParams
	require.NoError(t, json.Unmarshal(b, &out))
	assert.Equal(t, in, out)
}

func TestTerminalExitEvent_ReasonString(t *testing.T) {
	ev := protocol.TerminalExitEvent{TerminalID: "abc", Code: 137, Reason: "killed", Msg: "sighup"}
	b, err := json.Marshal(ev)
	require.NoError(t, err)
	assert.Contains(t, string(b), `"reason":"killed"`)
}
