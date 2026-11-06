package peerstream

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/agentre-hub/agentre/internal/model/entity/chat_entity"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/runtimes/remote/wire"
)

// manualClock 让合帧的时间判定与尾巴定时器在单测里确定。
type manualClock struct {
	mu     sync.Mutex
	t      time.Time
	timers []*manualTimer
}

type manualTimer struct {
	f       func()
	stopped bool
}

func (m *manualTimer) Stop() bool { was := !m.stopped; m.stopped = true; return was }

func (c *manualClock) now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.t }

func (c *manualClock) afterFunc(_ time.Duration, f func()) previewTimer {
	c.mu.Lock()
	defer c.mu.Unlock()
	timer := &manualTimer{f: f}
	c.timers = append(c.timers, timer)
	return timer
}

// advanceAndFire 推进时间并触发所有未被停掉的尾巴定时器。
func (c *manualClock) advanceAndFire(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	timers := c.timers
	c.timers = nil
	c.mu.Unlock()
	for _, timer := range timers {
		if !timer.stopped {
			timer.stopped = true
			timer.f()
		}
	}
}

func attachReadyCoalescingPeer(t *testing.T, clock *manualClock) (*peerSessionTestDeps, *peerRecordingSubscriber) {
	t.Helper()
	deps := setupPeerSessionTest(t)
	deps.svc.now, deps.svc.afterFunc = clock.now, clock.afterFunc
	ctx := context.Background()
	deps.session.EXPECT().Find(ctx, int64(41)).Return(&chat_entity.Session{ID: 41, AgentID: 7, AgentStatus: "idle"}, nil).AnyTimes()
	deps.agent.EXPECT().Find(ctx, int64(7)).Return(agentForPeerSession(), nil).AnyTimes()
	deps.backend.EXPECT().Find(ctx, int64(11)).Return(nil, nil).AnyTimes()
	deps.message.EXPECT().List(ctx, int64(41)).Return(nil, nil).AnyTimes()
	sub := newRecordingPeerSubscriber()
	_, err := deps.svc.AttachPeerSession(ctx, wire.SessionAttachParams{ConversationID: convID(41)}, sub)
	require.NoError(t, err)
	_, err = deps.svc.PullPeerSession(ctx, wire.SessionPullParams{ConversationID: convID(41)}, sub)
	require.NoError(t, err)
	return deps, sub
}

// previewSummary 把收到的帧压成 "kind:text" 便于断言顺序。
func previewSummary(sub *peerRecordingSubscriber) []string {
	var out []string
	for _, n := range sub.notifications() {
		frame := n.params.(wire.EventFrame)
		switch ev := frame.Event.(type) {
		case agentruntime.TextDelta:
			out = append(out, "text:"+ev.Text)
		case agentruntime.ThinkingDelta:
			out = append(out, "thinking:"+ev.Text)
		case agentruntime.ToolCall:
			out = append(out, "tool")
		default:
			out = append(out, "other")
		}
	}
	return out
}

func TestPublishEvent_GivenBurstOfTextDeltas_ThenCoalescedIntoAtMostTwoFramesWithSameText(t *testing.T) {
	clock := &manualClock{t: time.Unix(1000, 0)}
	deps, sub := attachReadyCoalescingPeer(t, clock)

	var want strings.Builder
	for i := range 40 {
		text := string(rune('a' + i%26))
		want.WriteString(text)
		deps.svc.PublishEvent(41, agentruntime.TextDelta{Text: text})
	}
	clock.advanceAndFire(deltaFlushInterval)

	require.Eventually(t, func() bool {
		var got strings.Builder
		for _, s := range previewSummary(sub) {
			got.WriteString(strings.TrimPrefix(s, "text:"))
		}
		return got.String() == want.String()
	}, 2*time.Second, time.Millisecond, "合帧后拼接文本必须等于输入,尾巴由定时器兜底")
	assert.LessOrEqual(t, len(sub.notifications()), 2)
}

func TestPublishEvent_GivenToolFrameBetweenDeltas_ThenPendingTextFlushedBeforeItInOrder(t *testing.T) {
	clock := &manualClock{t: time.Unix(1000, 0)}
	deps, sub := attachReadyCoalescingPeer(t, clock)

	for _, s := range []string{"a", "b", "c", "d"} {
		deps.svc.PublishEvent(41, agentruntime.TextDelta{Text: s})
	}
	deps.svc.PublishEvent(41, agentruntime.ToolCall{})
	deps.svc.PublishEvent(41, agentruntime.TextDelta{Text: "e"})
	deps.svc.PublishEvent(41, agentruntime.TextDelta{Text: "f"})
	clock.advanceAndFire(deltaFlushInterval)

	require.Eventually(t, func() bool { return len(sub.notifications()) >= 4 }, 2*time.Second, time.Millisecond)
	assert.Equal(t, []string{"text:a", "text:bcd", "tool", "text:ef"}, previewSummary(sub))
}

func TestPublishEvent_GivenAlternatingKinds_ThenNeverMergesTextWithThinking(t *testing.T) {
	clock := &manualClock{t: time.Unix(1000, 0)}
	deps, sub := attachReadyCoalescingPeer(t, clock)

	deps.svc.PublishEvent(41, agentruntime.TextDelta{Text: "a"})
	deps.svc.PublishEvent(41, agentruntime.ThinkingDelta{Text: "x"})
	deps.svc.PublishEvent(41, agentruntime.ThinkingDelta{Text: "y"})
	deps.svc.PublishEvent(41, agentruntime.TextDelta{Text: "b"})
	clock.advanceAndFire(deltaFlushInterval)

	require.Eventually(t, func() bool { return len(sub.notifications()) >= 3 }, 2*time.Second, time.Millisecond)
	assert.Equal(t, []string{"text:a", "thinking:xy", "text:b"}, previewSummary(sub))
}

func TestPublishEvent_GivenPendingTextAndDurableFrame_ThenTextPrecedesDurableFrame(t *testing.T) {
	clock := &manualClock{t: time.Unix(1000, 0)}
	deps, sub := attachReadyCoalescingPeer(t, clock)

	deps.svc.PublishEvent(41, agentruntime.TextDelta{Text: "a"})
	deps.svc.PublishEvent(41, agentruntime.TextDelta{Text: "b"})
	deps.svc.PublishEvent(41, agentruntime.TextDelta{Text: "c"})
	publishPeerDurableFrame(t, deps.svc, 41, 93, "durable")

	require.Eventually(t, func() bool { return len(sub.notifications()) >= 3 }, 2*time.Second, time.Millisecond)
	assert.Equal(t, []string{"text:a", "text:bc", "other"}, previewSummary(sub))
}

func TestDetach_GivenLastSubscriberLeaves_ThenPublicationReleasedAndFlushLoopExits(t *testing.T) {
	clock := &manualClock{t: time.Unix(1000, 0)}
	deps, sub := attachReadyCoalescingPeer(t, clock)
	publishPeerDurableFrame(t, deps.svc, 41, 93, "history")
	value, ok := deps.svc.peerPublications.Load(int64(41))
	require.True(t, ok)
	publication := value.(*peerSessionPublication)

	close(sub.done)

	select {
	case <-publication.loopDone:
	case <-time.After(2 * time.Second):
		t.Fatal("flush goroutine still running after the last subscriber left")
	}
	_, still := deps.svc.peerPublications.Load(int64(41))
	assert.False(t, still, "最后一个订阅者离开后 publication 必须从表里摘除")
	publication.mu.Lock()
	assert.Empty(t, publication.history, "历史必须释放")
	publication.mu.Unlock()

	// 摘除后的 publish / pull 不得把它再建出来。
	deps.svc.PublishEvent(41, agentruntime.TextDelta{Text: "late"})
	_, err := deps.svc.PullPeerSession(context.Background(), wire.SessionPullParams{ConversationID: convID(41)}, sub)
	assert.Error(t, err)
	_, recreated := deps.svc.peerPublications.Load(int64(41))
	assert.False(t, recreated)
}

func TestDetach_GivenOneOfTwoSubscribersLeaves_ThenPublicationKept(t *testing.T) {
	clock := &manualClock{t: time.Unix(1000, 0)}
	deps, first := attachReadyCoalescingPeer(t, clock)
	second := newRecordingPeerSubscriber()
	_, err := deps.svc.AttachPeerSession(context.Background(), wire.SessionAttachParams{ConversationID: convID(41)}, second)
	require.NoError(t, err)

	close(first.done)

	require.Eventually(t, func() bool { return deps.svc.SubscriberCount(41) == 1 }, 2*time.Second, time.Millisecond)
	_, ok := deps.svc.peerPublications.Load(int64(41))
	assert.True(t, ok)
}

func TestAttach_GivenReattachAfterRelease_ThenRebuildsFromStoreAndDeliversLiveFrames(t *testing.T) {
	clock := &manualClock{t: time.Unix(1000, 0)}
	deps, first := attachReadyCoalescingPeer(t, clock)
	value, _ := deps.svc.peerPublications.Load(int64(41))
	old := value.(*peerSessionPublication)
	close(first.done)
	select {
	case <-old.loopDone:
	case <-time.After(2 * time.Second):
		t.Fatal("old flush goroutine did not exit")
	}

	second := newRecordingPeerSubscriber()
	_, err := deps.svc.AttachPeerSession(context.Background(), wire.SessionAttachParams{ConversationID: convID(41)}, second)
	require.NoError(t, err)
	_, err = deps.svc.PullPeerSession(context.Background(), wire.SessionPullParams{ConversationID: convID(41)}, second)
	require.NoError(t, err)
	deps.svc.PublishEvent(41, agentruntime.TextDelta{Text: "again"})

	require.Eventually(t, func() bool { return len(second.notifications()) == 1 }, 2*time.Second, time.Millisecond)
	assert.Equal(t, []string{"text:again"}, previewSummary(second))
	fresh, _ := deps.svc.peerPublications.Load(int64(41))
	assert.NotSame(t, old, fresh)
}

// Given 一次持久帧发布正停在取号(publishMu 已持有、号还没交回)
// When  最后一个订阅者此刻离开
// Then  这份宇宙要等那次发布做完才释放 —— 先释放的话,紧接着的重新 attach 会在另一份
//
//	宇宙(另一把 publishMu)里给同一批位置惰性编号,与在途那次取到的号各记一份。
func TestDetach_GivenPublishInFlight_ThenReleaseWaitsForIt(t *testing.T) {
	clock := &manualClock{t: time.Unix(1000, 0)}
	deps, sub := attachReadyCoalescingPeer(t, clock)
	value, ok := deps.svc.peerPublications.Load(int64(41))
	require.True(t, ok)
	publication := value.(*peerSessionPublication)

	entered, release := deps.ledger.holdNextAllocate()
	published := make(chan struct{})
	go func() {
		defer close(published)
		publishPeerDurableFrame(t, deps.svc, 41, 93, "in flight")
	}()
	<-entered

	close(sub.done)
	assert.Never(t, func() bool {
		_, present := deps.svc.peerPublications.Load(int64(41))
		return !present
	}, 200*time.Millisecond, 5*time.Millisecond, "发布还在取号,宇宙不能先被释放")

	release()
	<-published
	select {
	case <-publication.loopDone:
	case <-time.After(2 * time.Second):
		t.Fatal("publication not released after the in-flight publish finished")
	}
}
