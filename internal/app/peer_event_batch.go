package app

import (
	"sync"
	"time"

	"github.com/agentre-hub/agentre/internal/service/peer_svc"
)

// peerEventFlushWindow 一批 peer 帧最多攒这么久。16ms ≈ 一帧,肉眼无感。
//
// 与 chat_svc 的合帧窗口取同一个量级,但两者机制不同,见 peerEventBatcher 的注释。
const peerEventFlushWindow = 16 * time.Millisecond

// peerEventBatcher 把「一帧一次 Wails 广播」改成「一批一次」。
//
// 为什么需要它:每次 EventsEmit 都是一次 json.Marshal 加一次主线程 WKWebView
// evaluateJavaScript,而 Peer Tab 流式时是一个 token 一帧;前端那侧还要为每帧做一次
// store 写入并重渲染全部订阅者。这正是 chat 那条流刚刚消除掉的开销。
//
// 为什么**不**像 chat 那样把文本并成一条:peer 帧每帧带自己的 seq,前端在 attaching
// 期间靠 `seq <= highWater` 丢掉已被 pull 覆盖的帧(见 peer-session-store.ts)。把
// seq 10 和 seq 13 并成一条盖上 13,就会把 pull 已经渲染过的那半段再渲染一遍。所以
// 这里只做「装信封」:批是运输单位,帧逐字不动、seq 与顺序原样保留,前端的去重口径
// 因此完全不用改。
//
// 为什么用定时器而不是像 chat 那样「靠下一个非文本事件把尾巴带出去」:peer 这条流
// 全是同类事件,没有天然的收尾信号,尾帧只能由定时器兜底,否则一轮回复的最后几个
// token 会悬到对端下一次说话。
type peerEventBatcher struct {
	window   time.Duration
	schedule func(time.Duration, func())
	flush    func([]peer_svc.PeerEvent)

	mu      sync.Mutex
	pending []peer_svc.PeerEvent
	armed   bool
}

// newPeerEventBatcher 构造一个批量广播器。schedule 可注入,让单测里的批次边界确定
// (生产传 time.AfterFunc)。
func newPeerEventBatcher(window time.Duration, schedule func(time.Duration, func()), flush func([]peer_svc.PeerEvent)) *peerEventBatcher {
	return &peerEventBatcher{window: window, schedule: schedule, flush: flush}
}

// Emit 收下一帧。第一帧负责排期,窗口内的后续帧只是追加。
func (b *peerEventBatcher) Emit(e peer_svc.PeerEvent) {
	b.mu.Lock()
	b.pending = append(b.pending, e)
	arm := !b.armed
	if arm {
		b.armed = true
	}
	b.mu.Unlock()

	if arm {
		b.schedule(b.window, b.flushPending)
	}
}

// flushPending 把攒着的一批发出去。缓冲空时不发空广播。
func (b *peerEventBatcher) flushPending() {
	b.mu.Lock()
	batch := b.pending
	b.pending = nil
	// 解除排期:下一帧会重新排,否则第二批永远没人来发。
	b.armed = false
	b.mu.Unlock()

	if len(batch) == 0 {
		return
	}
	// flush 在锁外:它最终会走到 Wails 的 webview 求值,不能占着锁做。
	b.flush(batch)
}
