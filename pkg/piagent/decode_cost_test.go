package piagent

import (
	"context"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// growingMessageUpdate mimics Pi's message_update: the cumulative message and
// the assistantMessageEvent partial both carry the whole text so far.
func growingMessageUpdate(totalBytes int) []byte {
	text := strings.Repeat("a", totalBytes/2)
	msg := `{"role":"assistant","content":[{"type":"text","text":"` + text + `"}]}`
	return []byte(`{"type":"message_update","message":` + msg +
		`,"assistantMessageEvent":{"type":"text_delta","contentIndex":0,"delta":"x","partial":` + msg + `}}`)
}

func newDrainStream(proc *rpcProcess) *Stream {
	s := newStream(proc, time.Second)
	s.events = make(chan Event, 1024)
	return s
}

func allocPerLine(t *testing.T, s *Stream, line []byte, emitDiagnostic bool) float64 {
	t.Helper()
	const rounds = 50
	drain := func() {
		for len(s.events) > 0 {
			<-s.events
		}
	}
	require.True(t, s.drainLine(context.Background(), line, emitDiagnostic)) // warm
	drain()
	// TotalAlloc is process-wide: goroutines leaked by earlier tests can add
	// noise, so report the quietest of several batches.
	best := -1.0
	for trial := 0; trial < 7; trial++ {
		var before, after runtime.MemStats
		runtime.GC()
		runtime.ReadMemStats(&before)
		for i := 0; i < rounds; i++ {
			s.drainLine(context.Background(), line, emitDiagnostic)
			drain()
		}
		runtime.ReadMemStats(&after)
		per := float64(after.TotalAlloc-before.TotalAlloc) / rounds
		if best < 0 || per < best {
			best = per
		}
	}
	return best
}

func TestDrainLineMessageUpdateAllocatesLessThanOneAndHalfLineLengths(t *testing.T) {
	// Given a 64 KiB growing message_update frame,
	// When the stream drains it with diagnostics disabled,
	// Then the per-line allocation stays within ~1.5x the line length.
	line := growingMessageUpdate(64 * 1024)
	s := newDrainStream(&rpcProcess{})

	per := allocPerLine(t, s, line, true)

	assert.LessOrEqual(t, per, 1.5*float64(len(line)), "bytes allocated per line")
}

func TestEmitRawFrameSanitizesOnlyWhenSinkEnabled(t *testing.T) {
	// Given a raw sink whose enabled gate is closed,
	// When frames are emitted, Then nothing is sanitized or delivered;
	// and once the gate opens the sanitized frame reaches the sink.
	enabled := false
	var got [][]byte
	proc := &rpcProcess{
		rawSink:        func(b []byte) { got = append(got, append([]byte(nil), b...)) },
		rawSinkEnabled: func() bool { return enabled },
	}
	line := []byte(`{"type":"agent_settled"}`)

	emitRawFrame(proc, line)
	assert.Empty(t, got)

	enabled = true
	emitRawFrame(proc, line)
	require.Len(t, got, 1)
	assert.JSONEq(t, `{"type":"agent_settled"}`, string(got[0]))
}

func TestEmitRawFrameWithoutGateStillDelivers(t *testing.T) {
	var got int
	proc := &rpcProcess{rawSink: func([]byte) { got++ }}
	emitRawFrame(proc, []byte(`{"type":"agent_settled"}`))
	assert.Equal(t, 1, got)
}

func TestDrainLineSkipsProgressDecodeForOrdinaryTools(t *testing.T) {
	// Given a progress predicate that only wants subagent tools,
	// When an ordinary tool streams a large tool_execution_update,
	// Then no event is emitted and the frame is not copied; subagent updates still carry PartialResult.
	big := strings.Repeat("o", 64*1024)
	ordinary := []byte(`{"type":"tool_execution_update","toolCallId":"c1","toolName":"bash","args":{},"partialResult":{"content":[{"type":"text","text":"` + big + `"}]}}`)
	sub := []byte(`{"type":"tool_execution_update","toolCallId":"c2","toolName":"subagent","partialResult":{"details":{}}}`)
	proc := &rpcProcess{progressTool: func(name string) bool { return strings.Contains(strings.ToLower(name), "subagent") }}
	s := newDrainStream(proc)

	per := allocPerLine(t, s, ordinary, false)
	assert.Less(t, per, float64(len(ordinary))/4)

	require.True(t, s.drainLine(context.Background(), ordinary, false))
	assert.Empty(t, s.events)
	require.True(t, s.drainLine(context.Background(), sub, false))
	require.Len(t, s.events, 1)
	ev := <-s.events
	assert.Equal(t, EventToolUseUpdate, ev.Kind)
	assert.Equal(t, "c2", ev.Tool.ID)
	assert.JSONEq(t, `{"details":{}}`, string(ev.Tool.PartialResult))
}

func TestDrainLineWithoutProgressPredicateKeepsEveryToolUpdate(t *testing.T) {
	line := []byte(`{"type":"tool_execution_update","toolCallId":"c1","toolName":"bash","partialResult":{"x":1}}`)
	s := newDrainStream(&rpcProcess{})
	require.True(t, s.drainLine(context.Background(), line, false))
	require.Len(t, s.events, 1)
	assert.Equal(t, `{"x":1}`, string((<-s.events).Tool.PartialResult))
}

func TestDrainLineMessageUpdateStillEmitsDelta(t *testing.T) {
	s := newDrainStream(&rpcProcess{})
	require.True(t, s.drainLine(context.Background(), growingMessageUpdate(256), false))
	require.Len(t, s.events, 1)
	ev := <-s.events
	assert.Equal(t, EventTextDelta, ev.Kind)
	assert.Equal(t, "x", ev.Text)
}

func TestDrainLineMalformedAndBlankLinesAreIgnored(t *testing.T) {
	s := newDrainStream(&rpcProcess{})
	for _, l := range []string{"", "   ", "not json", `{"type":"message_update","assistantMessageEvent":`} {
		assert.True(t, s.drainLine(context.Background(), []byte(l), false), l)
	}
	assert.Empty(t, s.events)
}

func BenchmarkDrainLineMessageUpdate64K(b *testing.B) {
	line := growingMessageUpdate(64 * 1024)
	s := newDrainStream(&rpcProcess{})
	b.SetBytes(int64(len(line)))
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		s.drainLine(context.Background(), line, true)
		for len(s.events) > 0 {
			<-s.events
		}
	}
}
