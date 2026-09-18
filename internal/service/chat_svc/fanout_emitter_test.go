package chat_svc

import (
	"context"
	"testing"
	"time"
)

// 本文件覆盖会话级流事件扇出(NewFanoutEmitter):多订阅者各收各的、慢订阅者不
// 阻塞生产者、取消后不再投递且通道关闭、按 sessionID 隔离。

func TestSessionIDFromStreamName(t *testing.T) {
	cases := []struct {
		stream string
		want   int64
		ok     bool
	}{
		{StreamName(42, 7), 42, true},
		{AutonomousStreamName(42), 0, false},
		{"chat:event:notanumber:7", 0, false},
		{"chat:event:42", 0, false},
		{"chat:event:", 0, false},
		{"", 0, false},
	}
	for _, tc := range cases {
		got, ok := SessionIDFromStreamName(tc.stream)
		if got != tc.want || ok != tc.ok {
			t.Fatalf("SessionIDFromStreamName(%q) = (%d,%v), want (%d,%v)", tc.stream, got, ok, tc.want, tc.ok)
		}
	}
}

func TestFanoutEmitter_MultipleSubscribersReceive(t *testing.T) {
	fe := NewFanoutEmitter(NoopEmitter{})
	a, cancelA := fe.SubscribeSessionEvents(1)
	defer cancelA()
	b, cancelB := fe.SubscribeSessionEvents(1)
	defer cancelB()

	fe.Emit(context.Background(), StreamName(1, 9), ChatStreamEvent{Kind: StreamChunk, Delta: "hello"})

	for i, ch := range []<-chan ChatStreamEvent{a, b} {
		select {
		case ev := <-ch:
			if ev.Kind != StreamChunk || ev.Delta != "hello" {
				t.Fatalf("subscriber %d got %+v", i, ev)
			}
		case <-time.After(time.Second):
			t.Fatalf("subscriber %d received nothing", i)
		}
	}
}

func TestFanoutEmitter_SlowSubscriberDoesNotBlockProducer(t *testing.T) {
	fe := NewFanoutEmitter(NoopEmitter{})
	ch, cancel := fe.SubscribeSessionEvents(1)
	defer cancel()

	done := make(chan struct{})
	go func() {
		for i := 0; i < sessionEventQueueDepth*4; i++ {
			fe.Emit(context.Background(), StreamName(1, 100), ChatStreamEvent{Kind: StreamChunk, Delta: "x"})
		}
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("producer blocked on a slow subscriber")
	}
	// 缓冲被填满,多出来的被丢弃而不是阻塞生产者。
	if got := len(ch); got != sessionEventQueueDepth {
		t.Fatalf("queued events = %d, want %d", got, sessionEventQueueDepth)
	}
}

func TestFanoutEmitter_CancelClosesChannelAndIsIdempotent(t *testing.T) {
	fe := NewFanoutEmitter(NoopEmitter{})
	ch, cancel := fe.SubscribeSessionEvents(1)
	cancel()
	cancel() // 重复调用不得 panic

	if _, ok := <-ch; ok {
		t.Fatal("channel should be closed after cancel")
	}
	// 取消之后再 emit 不得 panic(不得向已关闭通道投递)。
	fe.Emit(context.Background(), StreamName(1, 1), ChatStreamEvent{Kind: StreamChunk, Delta: "late"})
}

func TestFanoutEmitter_SessionIsolation(t *testing.T) {
	fe := NewFanoutEmitter(NoopEmitter{})
	ch1, cancel1 := fe.SubscribeSessionEvents(1)
	defer cancel1()
	ch2, cancel2 := fe.SubscribeSessionEvents(2)
	defer cancel2()

	fe.Emit(context.Background(), StreamName(1, 10), ChatStreamEvent{Kind: StreamChunk, Delta: "one"})

	select {
	case ev := <-ch1:
		if ev.Delta != "one" {
			t.Fatalf("session 1 got %+v", ev)
		}
	case <-time.After(time.Second):
		t.Fatal("session 1 received nothing")
	}
	select {
	case ev := <-ch2:
		t.Fatalf("session 2 must not receive session 1 event: %+v", ev)
	case <-time.After(50 * time.Millisecond):
	}
}

func TestChatSvc_SubscribeSessionEventsRoutesToFanout(t *testing.T) {
	svc := NewChat(NewFanoutEmitter(NoopEmitter{})).(*chatSvc)
	ch, cancel := svc.SubscribeSessionEvents(7)
	defer cancel()

	svc.emitter.Emit(context.Background(), StreamName(7, 1), ChatStreamEvent{Kind: StreamChunk, Delta: "z"})
	select {
	case ev := <-ch:
		if ev.Delta != "z" {
			t.Fatalf("got %+v", ev)
		}
	case <-time.After(time.Second):
		t.Fatal("chatSvc subscription received nothing")
	}
}

func TestChatSvc_SubscribeSessionEventsWithoutFanoutReturnsClosed(t *testing.T) {
	svc := NewChat(NoopEmitter{}).(*chatSvc)
	ch, cancel := svc.SubscribeSessionEvents(1)
	defer cancel()
	if _, ok := <-ch; ok {
		t.Fatal("channel must be closed when no fanout emitter is wired")
	}
}

func TestFanoutEmitter_ForwardsToInner(t *testing.T) {
	var seen []string
	inner := EmitterFunc(func(_ context.Context, name string, _ any) { seen = append(seen, name) })
	fe := NewFanoutEmitter(inner)
	fe.Emit(context.Background(), StreamName(1, 1), ChatStreamEvent{Kind: StreamChunk, Delta: "x"})
	if len(seen) != 1 || seen[0] != StreamName(1, 1) {
		t.Fatalf("inner emitter saw %v", seen)
	}
}
