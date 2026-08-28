package chat_svc

import (
	"context"
	"strings"
	"sync"
	"time"
)

// 流式文本合帧。
//
// 每一条 TextDelta / ThinkingDelta 此前都是**一次独立的 Wails 事件**:一次
// json.Marshal + 一次主线程 WKWebView evaluateJavaScript(wails v2 的 EventsEmit
// 就是拼一段 `window.wails.EventsNotify(...)` 交给 webview 求值)。一条长回复是
// 上千个 token,一个会话还可以同时有用户轮 / 自主续轮 / 后台 subagent 活动轮三路流。
//
// 这里把「间隔内连续到达的同类文本」并成一条再发。合帧只在**高频**时才真的起作用:
// 判定是「距本批第一条是否已过 deltaFlushInterval」,所以慢速流的每一条都会立刻发出去
// (延迟不变),只有快到人眼分辨不出的流才会被并起来 —— 自调节,不需要给不同 backend
// 调参。
//
// 同样的做法在 PTY 输出上已经用了很久(internal/service/terminal_svc/service.go 的
// 10ms / 32KB flush),这里是同一套思路搬到 chat 流上;区别是终端那条是独立 goroutine
// 配 ticker,而 chat 的事件是在 runTurn 的单 goroutine 里顺序 emit 的,所以这里用
// 「按到达时刻判定」而不是定时器 —— 不引入新 goroutine,也就不引入新的顺序不确定性。
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
	since  time.Time
	ctx    context.Context
	stream string
}

// coalescingEmitter 包在真正的 Emitter 外面合并流式文本帧。
//
// 顺序保证(这是整个类型的核心契约):任何**非文本**事件到达某路 stream 时,先把该
// stream 攒着的文本发出去,再发这个事件。所以前端看到的相对顺序与不合帧时逐字一致 ——
// 正文永远在触发它的 tool_use / done 之前。跨 stream 不互相 flush:三路流本就是各自
// 独立的 goroutine,它们之间从来就没有全局顺序。
type coalescingEmitter struct {
	inner Emitter
	// now 可注入,让 flush 的时间判定在单测里确定。
	now func() time.Time

	mu sync.Mutex
	// pending 只装「此刻有待发文本」的 stream,flush 后立刻删,避免变成只增不减的表。
	pending map[string]*pendingDelta
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
		inner:   inner,
		now:     time.Now,
		pending: map[string]*pendingDelta{},
	}
}

func (c *coalescingEmitter) Emit(ctx context.Context, stream string, payload any) {
	ev, ok := payload.(ChatStreamEvent)
	if !ok || !isTextDeltaKind(ev.Kind) {
		// 非文本(或非 ChatStreamEvent)一律:先把攒的发掉,再原样透传。
		c.mu.Lock()
		out := c.takeLocked(stream)
		c.mu.Unlock()
		c.emitPending(out)
		c.inner.Emit(ctx, stream, payload)
		return
	}
	if ev.Delta == "" {
		// 空 delta 既不该造出一条空事件,也不该被当成「别的事件」去打断合帧。
		return
	}

	c.mu.Lock()
	p := c.pending[stream]
	var flush *pendingDelta
	if p != nil && p.kind != ev.Kind {
		// chunk 与 thinking 是两路文本,不能并进同一条。
		flush = c.takeLocked(stream)
		p = nil
	}
	if p == nil {
		p = &pendingDelta{kind: ev.Kind, since: c.now(), ctx: ctx}
		c.pending[stream] = p
	}
	p.buf.WriteString(ev.Delta)

	var ready *pendingDelta
	if p.buf.Len() >= deltaFlushBytes || c.now().Sub(p.since) >= deltaFlushInterval {
		ready = c.takeLocked(stream)
	}
	c.mu.Unlock()

	// emit 在锁外:inner 最终会走到 Wails 的 webview 求值,不能占着锁做。
	c.emitPending(flush)
	c.emitPending(ready)
}

// takeLocked 摘走某路 stream 的待发文本(调用方持锁)。
func (c *coalescingEmitter) takeLocked(stream string) *pendingDelta {
	p := c.pending[stream]
	if p == nil {
		return nil
	}
	delete(c.pending, stream)
	p.stream = stream
	return p
}

func (c *coalescingEmitter) emitPending(p *pendingDelta) {
	if p == nil || p.buf.Len() == 0 {
		return
	}
	c.inner.Emit(p.ctx, p.stream, ChatStreamEvent{Kind: p.kind, Delta: p.buf.String()})
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
