package claudecode

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// collectWithin 把 p 的出口 drain 到 close 为止;超时说明出口没被关。
func collectWithin[T any](t *testing.T, p *pipe[T], d time.Duration) []T {
	t.Helper()
	got := make([]T, 0, 8)
	deadline := time.After(d)
	for {
		select {
		case v, ok := <-p.out():
			if !ok {
				return got
			}
			got = append(got, v)
		case <-deadline:
			t.Fatalf("pipe 出口在 %s 内没有 close,已收到 %d 个", d, len(got))
			return got
		}
	}
}

// TestPipe_PushNeverBlocksAndCloseFlushesInOrder:
// Given 没有任何消费方在读,When 连续 push 远超任何合理缓冲的元素再 close,
// Then push 全部立刻返回,随后消费方仍能按原序拿到全部元素、并看到出口 close。
//
// 会被本用例拒绝的错误实现:出口是有界 channel、push 直接阻塞发送 —— 生产方在
// 没有消费方时就卡住了,这正是 sess-3110 死锁的那一半。
func TestPipe_PushNeverBlocksAndCloseFlushesInOrder(t *testing.T) {
	p := newPipe[int]()

	pushed := make(chan struct{})
	go func() {
		defer close(pushed)
		for i := range 1000 {
			p.push(i)
		}
		p.close()
	}()

	select {
	case <-pushed:
	case <-time.After(5 * time.Second):
		t.Fatal("push 在无消费方时阻塞了")
	}

	got := collectWithin(t, p, 5*time.Second)
	require.Len(t, got, 1000)
	for i, v := range got {
		require.Equal(t, i, v, "第 %d 个元素乱序", i)
	}
}

// TestPipe_PushAfterCloseIsDropped:
// Given 已经 close 的 pipe,When 继续 push,Then 新元素被丢弃且不会 panic
// (readLoop 与收尾方是两条路径,收尾后仍可能有一帧在途)。
func TestPipe_PushAfterCloseIsDropped(t *testing.T) {
	p := newPipe[string]()
	p.push("before")
	p.close()
	assert.NotPanics(t, func() { p.push("after") })

	assert.Equal(t, []string{"before"}, collectWithin(t, p, 5*time.Second))
}

// TestPipe_AbandonDropsBufferedAndKeepsCloseWorking:
// Given 消费方已放弃(Turn 的 ctx 取消)且缓冲里还压着元素,When abandon 之后
// 继续 push 再 close,Then 缓冲里剩下的和后续的元素都不再投递(出口里最多只剩
// abandon 之前就已经交出去的那一小撮),出口仍照常 close —— 放弃的一轮不能把
// pump 永久留在原地。
func TestPipe_AbandonDropsBufferedAndKeepsCloseWorking(t *testing.T) {
	const pushed, afterAbandon = 100, -1
	p := newPipe[int]()
	for i := range pushed {
		p.push(i)
	}
	p.abandon()
	p.push(afterAbandon)
	p.close()

	got := collectWithin(t, p, 5*time.Second)
	// abandon 收不回**已经**交到出口 channel 里的元素(消费方本就不再读它们),
	// 但缓冲里剩下的必须全部丢掉 —— 否则「放弃」只是延后而已。
	assert.LessOrEqual(t, len(got), pipeChanBuffer)
	assert.NotContains(t, got, afterAbandon, "abandon 之后 push 的元素不得被投递")
}

// TestPipe_AbandonUnblocksPumpMidDelivery:
// Given 消费方读走一个元素后就再也不读,pump 因此卡在投递下一个上,When abandon,
// Then 出口在随后的 close 上照常关闭 —— 否则 pump goroutine 永久泄漏。
func TestPipe_AbandonUnblocksPumpMidDelivery(t *testing.T) {
	p := newPipe[int]()
	for i := range 200 {
		p.push(i)
	}
	select {
	case <-p.out():
	case <-time.After(5 * time.Second):
		t.Fatal("pipe 没有投出任何元素")
	}

	p.abandon()
	p.close()

	deadline := time.After(5 * time.Second)
	for {
		select {
		case _, ok := <-p.out():
			if !ok {
				return
			}
		case <-deadline:
			t.Fatal("abandon 之后出口没有 close,pump 卡在投递上")
		}
	}
}

// TestPipe_CloseIsIdempotent:Given 已 close 的 pipe,When 再次 close,Then 不 panic。
// 收尾路径有两条(finishActiveTurn 与 shutdownReader),同一轮可能被走两遍。
func TestPipe_CloseIsIdempotent(t *testing.T) {
	p := newPipe[int]()
	p.close()
	assert.NotPanics(t, p.close)
	assert.Empty(t, collectWithin(t, p, 5*time.Second))
}
