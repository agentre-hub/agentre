package chat_svc

import (
	"context"
	"strings"
	"sync"
	"time"
)

// 流式文本合帧。
//
// 不合帧的话,每一条 TextDelta / ThinkingDelta 都是**一次独立的 Wails 事件**:一次
// json.Marshal + 一次主线程 WKWebView evaluateJavaScript(wails v2 的 EventsEmit
// 就是拼一段 `window.wails.EventsNotify(...)` 交给 webview 求值)。一条长回复是
// 上千个 token,一个会话还可以同时有用户轮 / 自主续轮 / 后台 subagent 活动轮三路流。
//
// 这里把「间隔内连续到达的同类文本」并成一条再发。合帧只在**高频**时才真的起作用:
// 判定是「距本流上次发送是否已过 deltaFlushInterval」,所以慢速流的每一条都会立刻发出去
// (延迟不变),只有快到人眼分辨不出的流才会被并起来,尾巴由定时器兜底 —— 自调节,不需要给不同 backend
// 调参。
//
// 同样的做法在 PTY 输出上已经用了很久(internal/service/terminal_svc/service.go 的
// 10ms / 32KB flush),这里是同一套思路搬到 chat 流上;区别是这里按 stream 挂一次性定时器,
// 且同一 stream 的所有发送由 emitMu 串行,定时器的 flush 不会被后到的事件越过。
const (
	// deltaFlushInterval 一批文本最多攒这么久。16ms ≈ 一帧,肉眼无感。
	deltaFlushInterval = 16 * time.Millisecond
	// deltaFlushBytes 单批字节上限,防止一次超大 delta 把批撑得过大。
	deltaFlushBytes = 16 << 10
)

// pendingDelta 是某一路 stream 上攒着还没发的同类文本。
type pendingDelta struct {
	kind   ChatStreamEventKind
	buf    strings.Builder
	ctx    context.Context
	stream string
}

// timerStopper 是 afterFunc 返回的句柄(time.Timer 满足它),单测里换成假的。
type timerStopper interface{ Stop() bool }

// streamState 是某一路 stream 的合帧状态。
//
// lastSent 跨 flush 保留:「距上次发送是否已 ≥ 一个间隔」才是立即发 / 挂定时器的判据。
// 只有 timer 非空时才有尾巴定时器在飞;gen 每次摘走待发文本就加一,让已经排队的
// 过期定时器回调认出自己过期而什么都不发(终态之后绝不再有文字冒出来)。
type streamState struct {
	// emitMu 串行化该 stream 上的所有 inner.Emit(文本 flush、定时器 flush、透传事件),
	// 保证定时器的 flush 不会被后到的事件越过。获取顺序:emitMu → coalescingEmitter.mu。
	emitMu   sync.Mutex
	lastSent time.Time
	pending  *pendingDelta
	timer    timerStopper
	gen      uint64
}

// coalescingEmitter 包在真正的 Emitter 外面合并流式文本帧。
//
// 时序:一条 delta 距本流上次发送已 ≥ deltaFlushInterval(含从未发过)就立即发出;
// 否则攒起来,并挂一个到「上次发送 + 间隔」的定时器发尾巴 —— 没有后续事件时最后一段
// 文字也不会一直不可见。
//
// 顺序保证(这是整个类型的核心契约):任何**非文本**事件到达某路 stream 时,先把该
// stream 攒着的文本发出去,再发这个事件。所以前端看到的相对顺序与不合帧时逐字一致 ——
// 正文永远在触发它的 tool_use / done 之前。跨 stream 不互相 flush:三路流本就是各自
// 独立的 goroutine,它们之间从来就没有全局顺序。终态事件(done / error / aborted)
// 之后该 stream 的状态与定时器一并释放。
type coalescingEmitter struct {
	inner Emitter
	// now 可注入,让 flush 的时间判定在单测里确定。
	now func() time.Time
	// afterFunc 可注入,让尾巴定时器在单测里确定。
	afterFunc func(d time.Duration, f func()) timerStopper

	mu sync.Mutex
	// streams 只装有状态的 stream;终态事件时删除,避免变成只增不减的表。
	streams map[string]*streamState
}

// NewCoalescingEmitter 把 inner 包成合帧 emitter。
//
// 装配点在 internal/app(真正接 Wails 的那一处),而不是 NewChat 里面:合帧是
// **传输代价**的处理,不是 chat 的语义;放在装配点,注入假 emitter 的单测仍然逐条
// 看到原始事件,不必为了合帧改写一堆断言。
func NewCoalescingEmitter(inner Emitter) Emitter {
	if inner == nil {
		return NoopEmitter{}
	}
	return &coalescingEmitter{
		inner: inner,
		now:   time.Now,
		afterFunc: func(d time.Duration, f func()) timerStopper {
			return time.AfterFunc(d, f)
		},
		streams: map[string]*streamState{},
	}
}

func (c *coalescingEmitter) Emit(ctx context.Context, stream string, payload any) {
	ev, ok := payload.(ChatStreamEvent)
	if !ok || !isTextDeltaKind(ev.Kind) {
		c.emitNonText(ctx, stream, ev, ok, payload)
		return
	}
	if ev.Delta == "" {
		// 空 delta 既不该造出一条空事件,也不该被当成「别的事件」去打断合帧。
		return
	}

	st := c.state(stream, true)
	st.emitMu.Lock()
	defer st.emitMu.Unlock()

	c.mu.Lock()
	var flush *pendingDelta
	if p := st.pending; p != nil && p.kind != ev.Kind {
		// chunk 与 thinking 是两路文本,不能并进同一条。
		flush = c.takeLocked(stream, st)
	}
	if st.pending == nil {
		st.pending = &pendingDelta{kind: ev.Kind, ctx: ctx}
	}
	st.pending.buf.WriteString(ev.Delta)

	var ready *pendingDelta
	elapsed := c.now().Sub(st.lastSent)
	if st.pending.buf.Len() >= deltaFlushBytes || st.lastSent.IsZero() || elapsed >= deltaFlushInterval {
		ready = c.takeLocked(stream, st)
	} else if st.timer == nil {
		gen := st.gen
		st.timer = c.afterFunc(deltaFlushInterval-elapsed, func() { c.fire(stream, st, gen) })
	}
	c.mu.Unlock()

	// emit 在 mu 之外:inner 最终会走到 Wails 的 webview 求值,不能占着全局锁做。
	c.emitPending(flush)
	c.emitPending(ready)
}

// emitNonText 先发掉该 stream 攒着的文本,再原样透传;终态事件额外释放 stream 状态。
func (c *coalescingEmitter) emitNonText(ctx context.Context, stream string, ev ChatStreamEvent, isEvent bool, payload any) {
	terminal := isEvent && isTerminalKind(ev.Kind)
	st := c.state(stream, false)
	if st == nil {
		c.inner.Emit(ctx, stream, payload)
		return
	}
	st.emitMu.Lock()
	defer st.emitMu.Unlock()

	c.mu.Lock()
	out := c.takeLocked(stream, st)
	if terminal {
		// 摘走后 gen 已变,在飞的过期回调不会再发;状态随之释放。
		if c.streams[stream] == st {
			delete(c.streams, stream)
		}
	}
	c.mu.Unlock()

	c.emitPending(out)
	c.inner.Emit(ctx, stream, payload)
}

// fire 是尾巴定时器的回调:仍属于同一代才发,否则说明已被别的 flush / 终态接管。
func (c *coalescingEmitter) fire(stream string, st *streamState, gen uint64) {
	st.emitMu.Lock()
	defer st.emitMu.Unlock()

	c.mu.Lock()
	if st.gen != gen || st.pending == nil {
		c.mu.Unlock()
		return
	}
	out := c.takeLocked(stream, st)
	c.mu.Unlock()

	c.emitPending(out)
}

// state 取某路 stream 的状态;create=false 时不存在返回 nil。
func (c *coalescingEmitter) state(stream string, create bool) *streamState {
	c.mu.Lock()
	defer c.mu.Unlock()
	st := c.streams[stream]
	if st == nil && create {
		st = &streamState{}
		c.streams[stream] = st
	}
	return st
}

// takeLocked 摘走某路 stream 的待发文本并停掉尾巴定时器(调用方持 mu)。
// 摘走了文本就把它记作一次发送:lastSent 跨 flush 保留。
func (c *coalescingEmitter) takeLocked(stream string, st *streamState) *pendingDelta {
	if st.timer != nil {
		st.timer.Stop()
		st.timer = nil
	}
	st.gen++
	p := st.pending
	if p == nil {
		return nil
	}
	st.pending = nil
	st.lastSent = c.now()
	p.stream = stream
	return p
}

func (c *coalescingEmitter) emitPending(p *pendingDelta) {
	if p == nil || p.buf.Len() == 0 {
		return
	}
	c.inner.Emit(p.ctx, p.stream, ChatStreamEvent{Kind: p.kind, Delta: p.buf.String()})
}

// isTerminalKind 是一路 stream 的终态事件:之后不会再有该 stream 的文字。
func isTerminalKind(k ChatStreamEventKind) bool {
	return k == StreamDone || k == StreamError || k == StreamAborted
}

// isTextDeltaKind 只有这两种 kind 参与合帧。
//
// 合帧后的事件是 ChatStreamEvent{Kind, Delta} 重新拼的,也就是说这两种 kind 上除
// 这两个字段之外的任何字段都会在合并中丢失。dispatcher_emitter 目前对它们确实只填
// Kind + Delta,该前提由 TestChunkEventsCarryOnlyKindAndDelta 守住。
// 注意 plan_update 也带 Delta,但它同时带 Canonical,所以不在这里。
func isTextDeltaKind(k ChatStreamEventKind) bool {
	return k == StreamChunk || k == StreamThinking
}
