package remote_device_watcher_svc_test

import (
	"context"
	"testing"
	"time"

	. "github.com/smartystreets/goconvey/convey"

	"github.com/agentre-hub/agentre/internal/service/remote_device_watcher_svc"
)

func TestFakeClock(t *testing.T) {
	Convey("Advance 推动 Now 前进", t, func() {
		fc := remote_device_watcher_svc.NewFakeClock(time.Unix(100, 0))
		So(fc.NowMs(), ShouldEqual, int64(100_000))
		fc.Advance(2 * time.Second)
		So(fc.NowMs(), ShouldEqual, int64(102_000))
	})

	Convey("Sleep 阻塞到时间被 Advance 推过", t, func() {
		fc := remote_device_watcher_svc.NewFakeClock(time.Unix(0, 0))
		done := make(chan bool, 1)
		go func() {
			done <- fc.Sleep(context.Background(), 5*time.Second)
		}()
		time.Sleep(20 * time.Millisecond) // 让 goroutine 进入 Sleep
		select {
		case <-done:
			t.Fatal("Sleep returned before Advance")
		default:
		}
		fc.Advance(5 * time.Second)
		select {
		case ok := <-done:
			So(ok, ShouldBeTrue)
		case <-time.After(time.Second):
			t.Fatal("Sleep didn't return after Advance")
		}
	})

	Convey("Sleep 在 ctx cancel 时立刻返回 false", t, func() {
		fc := remote_device_watcher_svc.NewFakeClock(time.Unix(0, 0))
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan bool, 1)
		go func() { done <- fc.Sleep(ctx, time.Hour) }()
		time.Sleep(20 * time.Millisecond)
		cancel()
		select {
		case ok := <-done:
			So(ok, ShouldBeFalse)
		case <-time.After(time.Second):
			t.Fatal("Sleep didn't return after ctx cancel")
		}
	})
}
