package httpgateway

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestSteerInbox_PushDrainBasic(t *testing.T) {
	ib := NewSteerInbox()
	ib.Push("sid-1", "id-a", "hello")
	ib.Push("sid-1", "id-b", "world")
	ib.Push("sid-2", "id-c", "other")

	got := ib.Drain("sid-1")
	if len(got) != 2 || got[0].Text != "hello" || got[1].Text != "world" {
		t.Fatalf("sid-1 drain: %v", got)
	}
	if got[0].ID != "id-a" || got[1].ID != "id-b" {
		t.Fatalf("sid-1 drain ids: %v", got)
	}
	if got := ib.Drain("sid-1"); got != nil {
		t.Fatalf("sid-1 second drain not empty: %v", got)
	}
	if got := ib.Drain("sid-2"); len(got) != 1 || got[0].Text != "other" || got[0].ID != "id-c" {
		t.Fatalf("sid-2 drain: %v", got)
	}
}

func TestSteerInbox_SubscribeDrainReceivesConsumedItems(t *testing.T) {
	ib := NewSteerInbox()
	ch, cancel := ib.SubscribeDrain("sid-1")
	defer cancel()

	ib.Push("sid-1", "id-a", "hello")
	ib.Push("sid-1", "id-b", "world")
	got := ib.Drain("sid-1")
	if len(got) != 2 {
		t.Fatalf("Drain returned %v", got)
	}

	select {
	case batch := <-ch:
		if len(batch) != 2 || batch[0].ID != "id-a" || batch[1].Text != "world" {
			t.Fatalf("subscriber batch = %v", batch)
		}
	case <-time.After(time.Second):
		t.Fatalf("subscriber did not receive drain batch")
	}
}

func TestSteerInbox_SubscribeDrainIgnoresEmptyDrain(t *testing.T) {
	ib := NewSteerInbox()
	ch, cancel := ib.SubscribeDrain("sid-1")
	defer cancel()

	got := ib.Drain("sid-1")
	if got != nil {
		t.Fatalf("empty Drain returned %v", got)
	}
	select {
	case batch := <-ch:
		t.Fatalf("empty drain should not notify subscriber: %v", batch)
	case <-time.After(20 * time.Millisecond):
	}
}

func TestSteerInbox_PushEmptySIDIgnored(t *testing.T) {
	ib := NewSteerInbox()
	ib.Push("", "id", "ignored")
	if got := ib.Drain(""); got != nil {
		t.Fatalf("empty sid should not store: %v", got)
	}
}

func TestSteerInbox_PushEmptyIDIgnored(t *testing.T) {
	// Empty queuedID is a programmer bug; ignore to avoid creating
	// uncancellable entries with id="" that would collide on Remove.
	ib := NewSteerInbox()
	ib.Push("sid", "", "no id")
	if got := ib.Drain("sid"); got != nil {
		t.Fatalf("empty id should not store: %v", got)
	}
}

func TestSteerInbox_ForgetClears(t *testing.T) {
	ib := NewSteerInbox()
	ib.Push("sid", "id-a", "a")
	ib.Forget("sid")
	if got := ib.Drain("sid"); got != nil {
		t.Fatalf("Forget did not clear: %v", got)
	}
}

func TestSteerInbox_RemoveHit(t *testing.T) {
	ib := NewSteerInbox()
	ib.Push("sid", "id-a", "a")
	ib.Push("sid", "id-b", "b")
	ib.Push("sid", "id-c", "c")

	if !ib.Remove("sid", "id-b") {
		t.Fatalf("Remove(id-b) should hit")
	}
	got := ib.Drain("sid")
	if len(got) != 2 || got[0].ID != "id-a" || got[1].ID != "id-c" {
		t.Fatalf("Drain after Remove: %v", got)
	}
}

func TestSteerInbox_RemoveMiss(t *testing.T) {
	ib := NewSteerInbox()
	ib.Push("sid", "id-a", "a")
	if ib.Remove("sid", "nope") {
		t.Fatalf("Remove(unknown) should return false")
	}
	if ib.Remove("other-sid", "id-a") {
		t.Fatalf("Remove on unknown sid should return false")
	}
	got := ib.Drain("sid")
	if len(got) != 1 {
		t.Fatalf("Remove miss should preserve queue: %v", got)
	}
}

func TestSteerInbox_RemoveLastEntryClearsKey(t *testing.T) {
	ib := NewSteerInbox()
	ib.Push("sid", "id-only", "x")
	if !ib.Remove("sid", "id-only") {
		t.Fatalf("Remove(only) should hit")
	}
	// Subsequent Push to same sid must still work — internal map key gone.
	ib.Push("sid", "id-new", "y")
	got := ib.Drain("sid")
	if len(got) != 1 || got[0].ID != "id-new" {
		t.Fatalf("post-clear Drain: %v", got)
	}
}

func TestSteerInbox_ConcurrencyMessagesConserved(t *testing.T) {
	ib := NewSteerInbox()
	const writers = 10
	const perWriter = 200

	var pushWG sync.WaitGroup
	for i := 0; i < writers; i++ {
		pushWG.Add(1)
		go func(w int) {
			defer pushWG.Done()
			for j := 0; j < perWriter; j++ {
				ib.Push("sid", idForTest(w, j), "x")
			}
		}(i)
	}

	stop := make(chan struct{})
	var drainWG sync.WaitGroup
	var drained atomic.Int64
	for i := 0; i < 4; i++ {
		drainWG.Add(1)
		go func() {
			defer drainWG.Done()
			for {
				select {
				case <-stop:
					return
				default:
					drained.Add(int64(len(ib.Drain("sid"))))
				}
			}
		}()
	}
	pushWG.Wait()
	close(stop)
	drainWG.Wait()
	// Final sweep in case any messages landed after the last drainer woke.
	drained.Add(int64(len(ib.Drain("sid"))))
	want := int64(writers * perWriter)
	if drained.Load() != want {
		t.Fatalf("drained %d, want %d (lost messages?)", drained.Load(), want)
	}
}

func idForTest(writer, idx int) string {
	return string(rune('A'+writer)) + "-" + itoa(idx)
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [16]byte
	pos := len(buf)
	for n > 0 {
		pos--
		buf[pos] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[pos:])
}

// 用户清空排队是撤回，不是消费：取走的条目不能广播给 drain 订阅方，否则会被当成
// 「AI 已取走」落进转录。
func TestSteerInbox_WithdrawAllDoesNotNotifySubscribers(t *testing.T) {
	ib := NewSteerInbox()
	ch, cancel := ib.SubscribeDrain("sid-1")
	defer cancel()

	ib.Push("sid-1", "id-a", "hello")
	ib.Push("sid-1", "id-b", "world")
	got := ib.WithdrawAll("sid-1")
	if len(got) != 2 || got[0].ID != "id-a" || got[1].ID != "id-b" {
		t.Fatalf("WithdrawAll returned %v", got)
	}
	if rest := ib.Drain("sid-1"); len(rest) != 0 {
		t.Fatalf("queue not cleared: %v", rest)
	}
	select {
	case batch := <-ch:
		t.Fatalf("subscriber notified of a withdrawal: %v", batch)
	case <-time.After(50 * time.Millisecond):
	}
}
