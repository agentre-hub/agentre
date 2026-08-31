package app

import (
	"encoding/json"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/agentre-hub/agentre/internal/service/peer_svc"
)

// fakeFlushClock 把「到点触发」变成测试显式调用,让批次边界完全确定。
type fakeFlushClock struct {
	mu      sync.Mutex
	pending []func()
	windows []time.Duration
}

func (c *fakeFlushClock) schedule(d time.Duration, f func()) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.windows = append(c.windows, d)
	c.pending = append(c.pending, f)
}

// fire 触发所有已排期的 flush(通常只有一个)。
func (c *fakeFlushClock) fire() {
	c.mu.Lock()
	fns := c.pending
	c.pending = nil
	c.mu.Unlock()
	for _, f := range fns {
		f()
	}
}

func (c *fakeFlushClock) scheduledCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.pending)
}

func peerFrame(seq int64, text string) peer_svc.PeerEvent {
	return peer_svc.PeerEvent{
		Fingerprint:    "sha256:peer",
		ConversationID: convID(7),
		Seq:            seq,
		Event:          json.RawMessage(`{"kind":"text_delta","text":"` + text + `"}`),
	}
}

// TestPeerEventBatcher_ManyFramesBecomeOneBroadcast 是这一改动的目的:一次 Wails
// 广播承载一批帧,而不是一帧一次。
//
// 每次 EventsEmit 都是一次 json.Marshal + 一次主线程 webview 求值,而 Peer Tab 流式
// 时是一个 token 一帧。
func TestPeerEventBatcher_ManyFramesBecomeOneBroadcast(t *testing.T) {
	clock := &fakeFlushClock{}
	var batches [][]peer_svc.PeerEvent
	b := newPeerEventBatcher(peerEventFlushWindow, clock.schedule, func(batch []peer_svc.PeerEvent) {
		batches = append(batches, batch)
	})

	b.Emit(peerFrame(1, "he"))
	b.Emit(peerFrame(2, "ll"))
	b.Emit(peerFrame(3, "o"))
	assert.Empty(t, batches, "窗口没到之前不该广播")
	assert.Equal(t, 1, clock.scheduledCount(), "一批只排一次 flush,不是每帧一次")

	clock.fire()

	require.Len(t, batches, 1, "三帧应当合成一次广播")
	require.Len(t, batches[0], 3, "但帧本身不合并:三帧还是三帧")
}

// TestPeerEventBatcher_PreservesPerFrameSeqAndOrder 是安全性的核心:批只是装帧的
// 信封,帧本身**逐字不动**。
//
// 为什么不能像 chat 那样把文本并成一条:每帧带自己的 seq,前端在 attaching 期间靠
// `seq <= highWater` 丢掉已被 pull 覆盖的帧。把 seq 10 和 seq 13 并成一条盖上 13,
// 就会把 pull 已经渲染过的那半段再渲染一遍。
func TestPeerEventBatcher_PreservesPerFrameSeqAndOrder(t *testing.T) {
	clock := &fakeFlushClock{}
	var got []peer_svc.PeerEvent
	b := newPeerEventBatcher(peerEventFlushWindow, clock.schedule, func(batch []peer_svc.PeerEvent) {
		got = append(got, batch...)
	})

	for seq := int64(1); seq <= 5; seq++ {
		b.Emit(peerFrame(seq, "x"))
	}
	clock.fire()

	require.Len(t, got, 5)
	for i, ev := range got {
		assert.Equal(t, int64(i+1), ev.Seq, "seq 必须逐帧原样保留且保序")
		assert.JSONEq(t, `{"kind":"text_delta","text":"x"}`, string(ev.Event))
	}
}

// TestPeerEventBatcher_TailFlushesWithoutAFollowingFrame 尾帧不能等下一帧来推。
//
// chat 那条合帧可以靠「下一个非文本事件」把尾巴带出去,peer 这条流全是同类事件、
// 没有天然的收尾信号 —— 所以必须由定时器兜底,否则一轮回复的最后几个 token 会一直
// 悬在缓冲里,直到对端下一次说话。
func TestPeerEventBatcher_TailFlushesWithoutAFollowingFrame(t *testing.T) {
	clock := &fakeFlushClock{}
	var batches [][]peer_svc.PeerEvent
	b := newPeerEventBatcher(peerEventFlushWindow, clock.schedule, func(batch []peer_svc.PeerEvent) {
		batches = append(batches, batch)
	})

	b.Emit(peerFrame(1, "tail"))
	require.Equal(t, 1, clock.scheduledCount(), "单独一帧也必须排上 flush")
	clock.fire()

	require.Len(t, batches, 1)
	require.Len(t, batches[0], 1)
	assert.Equal(t, int64(1), batches[0][0].Seq)
}

// TestPeerEventBatcher_IdleDoesNotBroadcast 空闲时定时器到点不该发空广播。
func TestPeerEventBatcher_IdleDoesNotBroadcast(t *testing.T) {
	clock := &fakeFlushClock{}
	calls := 0
	b := newPeerEventBatcher(peerEventFlushWindow, clock.schedule, func([]peer_svc.PeerEvent) { calls++ })

	b.Emit(peerFrame(1, "a"))
	clock.fire()
	require.Equal(t, 1, calls)

	// 没有新帧,再触发一次(模拟残留定时器)不该再广播。
	clock.fire()
	assert.Equal(t, 1, calls, "缓冲空时不得广播")
}

// TestPeerEventBatcher_NewFrameAfterFlushStartsANewWindow flush 之后要重新排期,
// 否则第二批会永远没人来发。
func TestPeerEventBatcher_NewFrameAfterFlushStartsANewWindow(t *testing.T) {
	clock := &fakeFlushClock{}
	var batches [][]peer_svc.PeerEvent
	b := newPeerEventBatcher(peerEventFlushWindow, clock.schedule, func(batch []peer_svc.PeerEvent) {
		batches = append(batches, batch)
	})

	b.Emit(peerFrame(1, "a"))
	clock.fire()
	b.Emit(peerFrame(2, "b"))
	require.Equal(t, 1, clock.scheduledCount(), "第二批必须重新排期")
	clock.fire()

	require.Len(t, batches, 2)
	assert.Equal(t, int64(1), batches[0][0].Seq)
	assert.Equal(t, int64(2), batches[1][0].Seq)
}

// TestPeerEventBatcher_ConcurrentEmitIsSafe 多台对端各自一条连接的读循环会并发
// 打进同一个 batcher。
func TestPeerEventBatcher_ConcurrentEmitIsSafe(t *testing.T) {
	clock := &fakeFlushClock{}
	var mu sync.Mutex
	total := 0
	b := newPeerEventBatcher(peerEventFlushWindow, clock.schedule, func(batch []peer_svc.PeerEvent) {
		mu.Lock()
		total += len(batch)
		mu.Unlock()
	})

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(seq int64) {
			defer wg.Done()
			b.Emit(peerFrame(seq, "x"))
		}(int64(i + 1))
	}
	wg.Wait()
	// 并发期间可能已经排了多次期;把它们全部触发。
	for clock.scheduledCount() > 0 {
		clock.fire()
	}

	mu.Lock()
	defer mu.Unlock()
	assert.Equal(t, 50, total, "并发发进来的帧一条都不能丢")
}

// convID 把一个短会话号折成一条**格式合法**的 conversation_id,只在测试里用:
// 线上身份是 uuid,而这些用例真正要断言的是"同一个值原样往返"与"两条不同的对话
// 互不并轨",一个可读、可复现的映射比随机 uuid 更好读。
func convID(n int64) string {
	return fmt.Sprintf("00000000-0000-7000-8000-%012d", n)
}
