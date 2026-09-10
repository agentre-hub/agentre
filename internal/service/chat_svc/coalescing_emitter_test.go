package chat_svc

import (
	"context"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	. "github.com/smartystreets/goconvey/convey"
)

type coalesceRecord struct {
	stream  string
	payload any
}

type coalesceRecorder struct {
	mu   sync.Mutex
	rows []coalesceRecord
}

func (r *coalesceRecorder) Emit(_ context.Context, name string, payload any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.rows = append(r.rows, coalesceRecord{stream: name, payload: payload})
}

func (r *coalesceRecorder) events() []ChatStreamEvent {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]ChatStreamEvent, 0, len(r.rows))
	for _, row := range r.rows {
		ev, ok := row.payload.(ChatStreamEvent)
		if !ok {
			continue
		}
		out = append(out, ev)
	}
	return out
}

// fixedClock 让 flush 的时间判定在测试里完全确定,不依赖真实时钟。
type fixedClock struct{ t time.Time }

func (c *fixedClock) now() time.Time      { return c.t }
func (c *fixedClock) add(d time.Duration) { c.t = c.t.Add(d) }

func newTestCoalescer(inner Emitter) (*coalescingEmitter, *fixedClock) {
	clk := &fixedClock{t: time.Unix(0, 0)}
	ce := NewCoalescingEmitter(inner).(*coalescingEmitter)
	ce.now = clk.now
	return ce, clk
}

func TestCoalescingEmitter_MergesConsecutiveDeltasOnTheSameStream(t *testing.T) {
	Convey("同一 stream 上间隔内的连续 chunk 合成一条", t, func() {
		rec := &coalesceRecorder{}
		ce, clk := newTestCoalescer(rec)
		ctx := context.Background()

		ce.Emit(ctx, "s1", ChatStreamEvent{Kind: StreamChunk, Delta: "he"})
		clk.add(time.Millisecond)
		ce.Emit(ctx, "s1", ChatStreamEvent{Kind: StreamChunk, Delta: "ll"})
		clk.add(time.Millisecond)
		ce.Emit(ctx, "s1", ChatStreamEvent{Kind: StreamChunk, Delta: "o"})

		// 还没有任何终止事件 → 一条都不该发出去。
		So(len(rec.events()), ShouldEqual, 0)

		ce.Emit(ctx, "s1", ChatStreamEvent{Kind: StreamDone})

		evs := rec.events()
		So(len(evs), ShouldEqual, 2)
		So(evs[0].Kind, ShouldEqual, StreamChunk)
		So(evs[0].Delta, ShouldEqual, "hello")
		So(evs[1].Kind, ShouldEqual, StreamDone)
	})
}

// TestCoalescingEmitter_FlushesBeforeAnyNonTextEvent 是这一改动最关键的不变式:
// 攒着的文本必须在任何**别的**事件之前发出去,否则前端会先看到 tool_use 卡片、
// 再看到本该在它前面的正文,转录顺序就错了。
func TestCoalescingEmitter_FlushesBeforeAnyNonTextEvent(t *testing.T) {
	Convey("非文本事件到达时先把攒着的文本发出去,顺序不变", t, func() {
		rec := &coalesceRecorder{}
		ce, _ := newTestCoalescer(rec)
		ctx := context.Background()

		ce.Emit(ctx, "s1", ChatStreamEvent{Kind: StreamChunk, Delta: "let me check"})
		ce.Emit(ctx, "s1", ChatStreamEvent{Kind: StreamToolUse, ToolCallID: "t1", ToolName: "Read"})
		ce.Emit(ctx, "s1", ChatStreamEvent{Kind: StreamChunk, Delta: "found it"})
		ce.Emit(ctx, "s1", ChatStreamEvent{Kind: StreamDone})

		evs := rec.events()
		So(len(evs), ShouldEqual, 4)
		So(evs[0].Kind, ShouldEqual, StreamChunk)
		So(evs[0].Delta, ShouldEqual, "let me check")
		So(evs[1].Kind, ShouldEqual, StreamToolUse)
		So(evs[1].ToolCallID, ShouldEqual, "t1")
		So(evs[2].Kind, ShouldEqual, StreamChunk)
		So(evs[2].Delta, ShouldEqual, "found it")
		So(evs[3].Kind, ShouldEqual, StreamDone)
	})
}

func TestCoalescingEmitter_DoesNotMixChunkAndThinking(t *testing.T) {
	Convey("chunk 与 thinking 是两路文本,切换时先 flush 再攒", t, func() {
		rec := &coalesceRecorder{}
		ce, _ := newTestCoalescer(rec)
		ctx := context.Background()

		ce.Emit(ctx, "s1", ChatStreamEvent{Kind: StreamThinking, Delta: "hmm"})
		ce.Emit(ctx, "s1", ChatStreamEvent{Kind: StreamChunk, Delta: "answer"})
		ce.Emit(ctx, "s1", ChatStreamEvent{Kind: StreamDone})

		evs := rec.events()
		So(len(evs), ShouldEqual, 3)
		So(evs[0].Kind, ShouldEqual, StreamThinking)
		So(evs[0].Delta, ShouldEqual, "hmm")
		So(evs[1].Kind, ShouldEqual, StreamChunk)
		So(evs[1].Delta, ShouldEqual, "answer")
	})
}

// TestCoalescingEmitter_KeepsStreamsIndependent 一个会话可以同时有用户轮、自主续轮、
// 后台 subagent 活动轮三路流。攒在 A 上的文本不能被 B 的事件冲掉,也不能串到 B 上。
func TestCoalescingEmitter_KeepsStreamsIndependent(t *testing.T) {
	Convey("不同 stream 的缓冲互不干扰", t, func() {
		rec := &coalesceRecorder{}
		ce, _ := newTestCoalescer(rec)
		ctx := context.Background()

		ce.Emit(ctx, "s1", ChatStreamEvent{Kind: StreamChunk, Delta: "A1"})
		ce.Emit(ctx, "s2", ChatStreamEvent{Kind: StreamChunk, Delta: "B1"})
		ce.Emit(ctx, "s1", ChatStreamEvent{Kind: StreamChunk, Delta: "A2"})
		// s2 上来个非文本事件:只该 flush s2,不该动 s1。
		ce.Emit(ctx, "s2", ChatStreamEvent{Kind: StreamDone})

		So(len(rec.rows), ShouldEqual, 2)
		So(rec.rows[0].stream, ShouldEqual, "s2")
		So(rec.rows[0].payload.(ChatStreamEvent).Delta, ShouldEqual, "B1")
		So(rec.rows[1].stream, ShouldEqual, "s2")
		So(rec.rows[1].payload.(ChatStreamEvent).Kind, ShouldEqual, StreamDone)

		// s1 的缓冲还在,收到自己的终止事件才出来。
		ce.Emit(ctx, "s1", ChatStreamEvent{Kind: StreamDone})
		So(rec.rows[2].stream, ShouldEqual, "s1")
		So(rec.rows[2].payload.(ChatStreamEvent).Delta, ShouldEqual, "A1A2")
	})
}

func TestCoalescingEmitter_FlushesOnceIntervalElapsed(t *testing.T) {
	Convey("攒够一个间隔就发,单条流不会被无限期攒住", t, func() {
		rec := &coalesceRecorder{}
		ce, clk := newTestCoalescer(rec)
		ctx := context.Background()

		ce.Emit(ctx, "s1", ChatStreamEvent{Kind: StreamChunk, Delta: "a"})
		clk.add(deltaFlushInterval / 2)
		ce.Emit(ctx, "s1", ChatStreamEvent{Kind: StreamChunk, Delta: "b"})
		So(len(rec.events()), ShouldEqual, 0)

		// 越过间隔的这一条把自己也一并带出去。
		clk.add(deltaFlushInterval)
		ce.Emit(ctx, "s1", ChatStreamEvent{Kind: StreamChunk, Delta: "c"})

		evs := rec.events()
		So(len(evs), ShouldEqual, 1)
		So(evs[0].Delta, ShouldEqual, "abc")

		// flush 之后重新起算,下一条不该立刻又发。
		ce.Emit(ctx, "s1", ChatStreamEvent{Kind: StreamChunk, Delta: "d"})
		So(len(rec.events()), ShouldEqual, 1)
	})
}

func TestCoalescingEmitter_FlushesOnByteThreshold(t *testing.T) {
	Convey("单批攒到字节上限立刻发,不等间隔", t, func() {
		rec := &coalesceRecorder{}
		ce, _ := newTestCoalescer(rec)
		ctx := context.Background()

		big := strings.Repeat("x", deltaFlushBytes+1)
		ce.Emit(ctx, "s1", ChatStreamEvent{Kind: StreamChunk, Delta: big})

		evs := rec.events()
		So(len(evs), ShouldEqual, 1)
		So(evs[0].Delta, ShouldEqual, big)
	})
}

// TestCoalescingEmitter_PassesThroughForeignPayloads 非 ChatStreamEvent 的负载
// (别的 emit 路径)必须原样透传,并且同样先 flush —— 否则顺序照样会错。
func TestCoalescingEmitter_PassesThroughForeignPayloads(t *testing.T) {
	Convey("非 ChatStreamEvent 负载先 flush 再原样透传", t, func() {
		rec := &coalesceRecorder{}
		ce, _ := newTestCoalescer(rec)
		ctx := context.Background()

		ce.Emit(ctx, "s1", ChatStreamEvent{Kind: StreamChunk, Delta: "text"})
		ce.Emit(ctx, "s1", map[string]any{"kind": "something_else"})

		So(len(rec.rows), ShouldEqual, 2)
		So(rec.rows[0].payload.(ChatStreamEvent).Delta, ShouldEqual, "text")
		So(rec.rows[1].payload, ShouldHaveSameTypeAs, map[string]any{})
	})
}

// TestCoalescingEmitter_EmptyDeltaIsNotSwallowed 空 delta 不该凭空造出一条空事件,
// 但也不能把它当成"非文本事件"去 flush —— 直接忽略。
func TestCoalescingEmitter_EmptyDeltaDoesNotEmitEmptyEvent(t *testing.T) {
	Convey("空 delta 不产生事件", t, func() {
		rec := &coalesceRecorder{}
		ce, _ := newTestCoalescer(rec)
		ctx := context.Background()

		ce.Emit(ctx, "s1", ChatStreamEvent{Kind: StreamChunk, Delta: ""})
		ce.Emit(ctx, "s1", ChatStreamEvent{Kind: StreamDone})

		evs := rec.events()
		So(len(evs), ShouldEqual, 1)
		So(evs[0].Kind, ShouldEqual, StreamDone)
	})
}

// TestCoalescingEmitter_ReleasesStreamStateAfterFlush 缓冲表只能装"此刻有待发文本"
// 的流。会话开开关关是常态,flush 之后不删条目就是一张只增不减的表。
func TestCoalescingEmitter_ReleasesStreamStateAfterFlush(t *testing.T) {
	Convey("flush 之后不留下 per-stream 条目", t, func() {
		rec := &coalesceRecorder{}
		ce, _ := newTestCoalescer(rec)
		ctx := context.Background()

		for i := 0; i < 50; i++ {
			stream := "s" + strings.Repeat("x", i)
			ce.Emit(ctx, stream, ChatStreamEvent{Kind: StreamChunk, Delta: "a"})
			ce.Emit(ctx, stream, ChatStreamEvent{Kind: StreamDone})
		}

		ce.mu.Lock()
		defer ce.mu.Unlock()
		So(len(ce.pending), ShouldEqual, 0)
	})
}

// TestChunkEventsCarryOnlyKindAndDelta 是 coalescingEmitter 合并逻辑的守卫测试。
//
// 合并时新事件是 ChatStreamEvent{Kind, Delta} 重新拼的 —— 也就是说 chunk / thinking
// 事件上**除这两个字段外的任何字段都会在合并中丢失**。dispatcher_emitter 当前对这两
// 种 kind 只填 Kind + Delta,这条测试把该前提钉死:哪天有人给 chunk 加了字段,这里
// 先红,而不是让那个字段在生产上被静默丢掉。
func TestChunkEventsCarryOnlyKindAndDelta(t *testing.T) {
	Convey("dispatcherEmitter 对 chunk/thinking 只填 Kind + Delta", t, func() {
		rec := &coalesceRecorder{}
		d := &dispatcherEmitter{svc: &chatSvc{emitter: rec}}
		ctx := context.Background()

		d.Emit(ctx, "s1", map[string]any{"kind": string(StreamChunk), "delta": "a"})
		d.Emit(ctx, "s1", map[string]any{"kind": string(StreamThinking), "delta": "b"})

		evs := rec.events()
		So(len(evs), ShouldEqual, 2)
		for _, ev := range evs {
			v := reflect.ValueOf(ev)
			typ := v.Type()
			for i := 0; i < typ.NumField(); i++ {
				name := typ.Field(i).Name
				if name == "Kind" || name == "Delta" {
					continue
				}
				So(v.Field(i).IsZero(), ShouldBeTrue)
			}
		}
	})
}
