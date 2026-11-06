package claudecode

import (
	"bufio"
	"bytes"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/agentre-hub/agentre/internal/pkg/orderedpipe"
)

// newReadLoopSession builds a Session whose scanner reads the given stdout.
func newReadLoopSession(stdout []byte) *Session {
	sc := bufio.NewScanner(bytes.NewReader(stdout))
	sc.Buffer(make([]byte, 0, 64<<10), maxFrameBytes)
	return &Session{
		proc:               &process{},
		scanner:            sc,
		pendingTurns:       make(chan *activeTurn, 4),
		autoCh:             make(chan *AutoTurn, 8),
		subagentCh:         orderedpipe.New[*SubagentActivity](),
		readerDone:         make(chan struct{}),
		sideActivities:     make(map[string]*activeTurn),
		subagentToolUseIDs: make(map[string]struct{}),
		backgroundTaskIDs:  make(map[string]string),
	}
}

// bigIgnoredFrame is a frame the session parses but emits nothing for; its
// Message payload is the only thing a decode has to copy.
func bigIgnoredFrame(payloadBytes int) []byte {
	return []byte(`{"type":"system","subtype":"hook_started","message":"` +
		strings.Repeat("a", payloadBytes) + `"}`)
}

func readLoopBytesPerLine(lines int, frame []byte) float64 {
	stdout := bytes.Repeat(append(append([]byte(nil), frame...), '\n'), lines)
	s := newReadLoopSession(stdout)
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	s.readLoop()
	runtime.ReadMemStats(&after)
	return float64(after.TotalAlloc-before.TotalAlloc) / float64(lines)
}

func TestReadLoopDecodesEachStdoutLineOnce(t *testing.T) {
	// Given a 40 KiB frame whose Message payload is copied by every decode,
	// When readLoop parses and routes it,
	// Then it allocates ~one copy of the line, not one per decode.
	frame := bigIgnoredFrame(40 * 1024)
	best := -1.0
	for trial := 0; trial < 5; trial++ {
		if per := readLoopBytesPerLine(100, frame); best < 0 || per < best {
			best = per
		}
	}
	assert.LessOrEqual(t, best, 1.5*float64(len(frame)), "bytes allocated per line")
}

func BenchmarkReadLoopLargeFrame(b *testing.B) {
	frame := bigIgnoredFrame(40 * 1024)
	stdout := bytes.Repeat(append(append([]byte(nil), frame...), '\n'), b.N)
	s := newReadLoopSession(stdout)
	b.SetBytes(int64(len(frame)))
	b.ReportAllocs()
	b.ResetTimer()
	s.readLoop()
}
