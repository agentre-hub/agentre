package piagent

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Pi 的 compact turn 不发 agent_end —— 完成信号是 response{command:"compact"}。
// Stream 必须把它当终止帧（emit Done 后结束），否则 drain 会一直 block 在 stdin
// 之后的 scan 上（生产里就是卡死）。
func TestCompactStreamTerminatesOnCompactResponse(t *testing.T) {
	script := strings.Join([]string{
		`{"type":"compaction_start","reason":"manual"}`,
		`{"type":"compaction_end","reason":"manual","result":{"summary":"done"}}`,
		`{"type":"response","command":"compact","success":true,"data":{"summary":"done"}}`,
		`{"type":"message_update","assistantMessageEvent":{"type":"text_delta","delta":"SHOULD NOT REACH"}}`,
		"",
	}, "\n")
	client, _ := newCaptureClient(script)
	session, err := client.OpenSession(context.Background())
	require.NoError(t, err)
	t.Cleanup(func() { _ = session.Close(context.Background()) })

	s, err := session.Compact(context.Background())
	require.NoError(t, err)

	var kinds []EventKind
	for s.Next() {
		kinds = append(kinds, s.Event().Kind)
	}

	assert.Contains(t, kinds, EventCompactBoundary)
	require.NotEmpty(t, kinds)
	assert.Equal(t, EventDone, kinds[len(kinds)-1], "compact response is terminal → Done")
	assert.NotContains(t, kinds, EventError, "successful compact must not surface a process-dead error")
	assert.NotContains(t, kinds, EventTextDelta, "must stop at compact response, not read past it")
}
