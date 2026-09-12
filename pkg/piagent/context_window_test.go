package piagent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Given a fresh Pi session whose authoritative context window is available before
// the first prompt, when the first assistant usage arrives, then Agentre has
// already surfaced the denominator needed by the live context meter.
func TestStreamEmitsInitialContextWindowBeforeFirstTurnUsage(t *testing.T) {
	reader := newStreamingRPCReader()
	client, proc := newStreamingCaptureClient(reader)
	t.Cleanup(reader.Close)

	s, err := client.Stream(context.Background(), "inspect the project")
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close(context.Background()) })

	reader.Push(
		`{"type":"response","command":"prompt","success":true}`,
		`{"type":"message_update","assistantMessageEvent":{"type":"text_delta","delta":"working"}}`,
	)
	first := nextContextWindowTestEvent(t, s)
	assert.Equal(t, EventTextDelta, first.Kind,
		"initial stats must be held until usage so it cannot race ahead of the frontend stream subscription")

	reader.Push(
		`{"type":"message_end","message":{"role":"assistant","content":[{"type":"text","text":"working"}],"model":"gpt-5.6-sol","usage":{"input":1200,"output":20,"cacheRead":2400,"cacheWrite":0},"stopReason":"toolUse"}}`,
		`{"type":"message_end","message":{"role":"assistant","content":[{"type":"text","text":"finished"}],"model":"gpt-5.6-sol","usage":{"input":1400,"output":30,"cacheRead":3600,"cacheWrite":0},"stopReason":"stop"}}`,
		`{"id":"initial-session-stats","type":"response","command":"get_session_stats","success":true,"data":{"contextUsage":{"tokens":0,"contextWindow":258000,"percent":0}}}`,
		`{"type":"agent_end","messages":[],"willRetry":false}`,
		`{"type":"agent_settled"}`,
		`{"type":"response","command":"get_session_stats","success":true,"data":{}}`,
	)
	remaining := collectUntilTerminal(t, s)
	kinds := eventKinds(remaining)
	require.Contains(t, kinds, EventContextWindow,
		"the stream surfaces an explicit context-window update")
	var usages []Event
	for _, ev := range remaining {
		if ev.Kind == EventUsage {
			usages = append(usages, ev)
		}
	}
	require.Len(t, usages, 2)
	assert.Equal(t, 258000, usages[0].ContextWindow,
		"get_state model metadata must cover usage even when get_session_stats is delayed")
	assert.Equal(t, 258000, usages[1].ContextWindow,
		"a later usage must recover the denominator when an earlier Wails event was missed")

	frames := stdinFrames(t, proc.stdin.String())
	require.Len(t, frames, 4)
	assert.Equal(t, "get_state", frames[0]["type"])
	assert.Equal(t, "get_session_stats", frames[1]["type"])
	assert.Equal(t, "prompt", frames[2]["type"])
	assert.Equal(t, "get_session_stats", frames[3]["type"])
}

// Given usage already consumed the get_state window, when the pre-prompt stats
// response later corrects that value, then the correction is surfaced even if
// there is no subsequent usage and the final refresh is empty.
func TestStreamSurfacesLateInitialContextCorrectionAfterLastUsage(t *testing.T) {
	reader := newStreamingRPCReader()
	client, _ := newStreamingCaptureClient(reader)
	t.Cleanup(reader.Close)

	s, err := client.Stream(context.Background(), "correct the context window")
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close(context.Background()) })

	reader.Push(
		`{"type":"response","command":"prompt","success":true}`,
		`{"type":"message_end","message":{"role":"assistant","content":[{"type":"text","text":"done"}],"model":"gpt-5.6-sol","usage":{"input":1200,"output":20,"cacheRead":2400,"cacheWrite":0},"stopReason":"stop"}}`,
		`{"id":"initial-session-stats","type":"response","command":"get_session_stats","success":true,"data":{"contextUsage":{"tokens":3600,"contextWindow":300000,"percent":1.2}}}`,
		`{"type":"agent_end","messages":[],"willRetry":false}`,
		`{"type":"agent_settled"}`,
		`{"id":"final-session-stats","type":"response","command":"get_session_stats","success":true,"data":{}}`,
	)
	events := collectUntilTerminal(t, s)
	assert.Equal(t, []int{258000, 300000}, contextWindows(events))
}

func nextContextWindowTestEvent(t *testing.T, s *Stream) Event {
	t.Helper()
	timer := time.NewTimer(2 * time.Second)
	defer timer.Stop()
	select {
	case ev, ok := <-s.events:
		if !ok {
			t.Fatal("stream closed before the first event")
		}
		return ev
	case <-timer.C:
		t.Fatal("timed out waiting for the first event")
	}
	return Event{}
}

// Given the initial stats response is delayed until after settlement, when Pi
// returns the final stats response, then the stale pre-prompt value is ignored.
func TestStreamFinalStatsIgnoresDelayedInitialResponse(t *testing.T) {
	tests := []struct {
		name      string
		responses []string
	}{
		{
			name: "correlated responses",
			responses: []string{
				`{"id":"initial-session-stats","type":"response","command":"get_session_stats","success":true,"data":{"contextUsage":{"tokens":10,"contextWindow":111111,"percent":0.01}}}`,
				`{"id":"final-session-stats","type":"response","command":"get_session_stats","success":true,"data":{"contextUsage":{"tokens":20,"contextWindow":222222,"percent":0.02}}}`,
			},
		},
		{
			name: "missing initial response with no-id final fallback",
			responses: []string{
				`{"type":"response","command":"get_session_stats","success":true,"data":{"contextUsage":{"tokens":20,"contextWindow":222222,"percent":0.02}}}`,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reader := newStreamingRPCReader()
			client, proc := newStreamingCaptureClient(reader)
			t.Cleanup(reader.Close)

			s, err := client.Stream(context.Background(), "finish with authoritative stats")
			require.NoError(t, err)
			t.Cleanup(func() { _ = s.Close(context.Background()) })

			reader.Push(
				`{"type":"response","command":"prompt","success":true}`,
				`{"type":"agent_end","messages":[],"willRetry":false}`,
				`{"type":"agent_settled"}`,
			)
			frames := waitForContextWindowTestFrames(t, proc, 4)
			assert.Equal(t, "initial-session-stats", frames[1]["id"])
			assert.Equal(t, "final-session-stats", frames[3]["id"])

			reader.Push(tt.responses...)
			events := collectUntilTerminal(t, s)
			assert.Equal(t, []int{222222}, contextWindows(events))
		})
	}
}

func waitForContextWindowTestFrames(t *testing.T, proc *captureProc, count int) []map[string]any {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		frames := stdinFrames(t, proc.stdin.String())
		if len(frames) >= count {
			return frames
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %d RPC frames", count)
	return nil
}

// Given an older or degraded Pi RPC that rejects the optional initial stats
// request, when a prompt starts, then the turn continues instead of failing.
func TestStreamContinuesWhenInitialSessionStatsUnavailable(t *testing.T) {
	script := strings.Join([]string{
		`{"type":"response","command":"get_session_stats","success":false,"error":"unsupported command"}`,
		`{"type":"response","command":"prompt","success":true}`,
		`{"type":"message_update","assistantMessageEvent":{"type":"text_delta","delta":"still running"}}`,
		`{"type":"agent_end","messages":[],"willRetry":false}`,
		`{"type":"agent_settled"}`,
		`{"type":"response","command":"get_session_stats","success":true,"data":{}}`,
		"",
	}, "\n")
	client, proc := newCaptureClient(script)

	s, err := client.Stream(context.Background(), "continue without stats")
	require.NoError(t, err)

	var text string
	var done bool
	for s.Next() {
		ev := s.Event()
		switch ev.Kind {
		case EventTextDelta:
			text += ev.Text
		case EventDone:
			done = true
		}
	}

	assert.Equal(t, "still running", text)
	assert.True(t, done)
	assert.NoError(t, s.Err())

	frames := stdinFrames(t, proc.stdin.String())
	require.Len(t, frames, 4)
	assert.Equal(t, "get_state", frames[0]["type"])
	assert.Equal(t, "get_session_stats", frames[1]["type"])
	assert.Equal(t, "prompt", frames[2]["type"])
	assert.Equal(t, "get_session_stats", frames[3]["type"])
}

func TestStreamEmitsContextWindowFromSessionStats(t *testing.T) {
	script := strings.Join([]string{
		`{"type":"response","command":"prompt","success":true}`,
		`{"type":"agent_end","messages":[],"willRetry":false}`,
		`{"type":"agent_settled"}`,
		`{"id":"final-session-stats","type":"response","command":"get_session_stats","success":true,"data":{"contextUsage":{"tokens":1234,"contextWindow":1050000,"percent":0.12}}}`,
		"",
	}, "\n")
	client, proc := newCaptureClient(script)

	s, err := client.Stream(context.Background(), "hi")
	require.NoError(t, err)

	var kinds []EventKind
	var windows []int
	for s.Next() {
		ev := s.Event()
		kinds = append(kinds, ev.Kind)
		if ev.Kind == EventContextWindow {
			windows = append(windows, ev.ContextWindow)
		}
	}

	require.NotEmpty(t, kinds)
	assert.Equal(t, EventDone, kinds[len(kinds)-1])
	assert.Equal(t, []int{1_050_000}, windows)

	frames := stdinFrames(t, proc.stdin.String())
	require.Len(t, frames, 4)
	assert.Equal(t, "get_state", frames[0]["type"])
	assert.Equal(t, "get_session_stats", frames[1]["type"])
	assert.Equal(t, "prompt", frames[2]["type"])
	assert.Equal(t, "get_session_stats", frames[3]["type"])
}

func TestCompactStreamEmitsContextWindowFromSessionStats(t *testing.T) {
	script := strings.Join([]string{
		`{"type":"compaction_start","reason":"manual"}`,
		`{"type":"compaction_end","reason":"manual","result":{"summary":"done"}}`,
		`{"type":"response","command":"compact","success":true,"data":{"summary":"done"}}`,
		`{"type":"response","command":"get_session_stats","success":true,"data":{"contextUsage":{"tokens":null,"contextWindow":200000,"percent":null}}}`,
		"",
	}, "\n")
	client, proc := newCaptureClient(script)

	s, err := client.Compact(context.Background(), "/data/pi-sessions/agentre-7.jsonl")
	require.NoError(t, err)

	var kinds []EventKind
	var windows []int
	for s.Next() {
		ev := s.Event()
		kinds = append(kinds, ev.Kind)
		if ev.Kind == EventContextWindow {
			windows = append(windows, ev.ContextWindow)
		}
	}

	assert.Contains(t, kinds, EventCompactBoundary)
	require.NotEmpty(t, kinds)
	assert.Equal(t, EventDone, kinds[len(kinds)-1])
	assert.Equal(t, []int{200_000}, windows)

	frames := stdinFrames(t, proc.stdin.String())
	require.Len(t, frames, 3)
	assert.Equal(t, "get_state", frames[0]["type"])
	assert.Equal(t, "compact", frames[1]["type"])
	assert.Equal(t, "get_session_stats", frames[2]["type"])
}

func stdinFrames(t *testing.T, raw string) []map[string]any {
	t.Helper()
	lines := strings.Split(strings.TrimSpace(raw), "\n")
	frames := make([]map[string]any, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var frame map[string]any
		require.NoError(t, json.Unmarshal([]byte(line), &frame))
		frames = append(frames, frame)
	}
	return frames
}
