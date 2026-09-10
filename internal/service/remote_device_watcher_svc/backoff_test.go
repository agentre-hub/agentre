package remote_device_watcher_svc_test

import (
	"math/rand"
	"testing"
	"time"

	. "github.com/smartystreets/goconvey/convey"

	"github.com/agentre-hub/agentre/internal/service/remote_device_watcher_svc"
)

func TestBackoff(t *testing.T) {
	Convey("First Next() returns initial±jitter", t, func() {
		b := remote_device_watcher_svc.NewBackoff(remote_device_watcher_svc.BackoffConfig{
			Initial: time.Second, Max: 30 * time.Second,
			Multiplier: 2.0, Jitter: 0.0, // jitter=0 让断言稳定
		}, rand.New(rand.NewSource(1)))
		So(b.Next(), ShouldEqual, time.Second)
	})

	Convey("Backoff doubles, capped at Max", t, func() {
		b := remote_device_watcher_svc.NewBackoff(remote_device_watcher_svc.BackoffConfig{
			Initial: time.Second, Max: 8 * time.Second,
			Multiplier: 2.0, Jitter: 0.0,
		}, rand.New(rand.NewSource(1)))
		got := []time.Duration{b.Next(), b.Next(), b.Next(), b.Next(), b.Next()}
		So(got, ShouldResemble, []time.Duration{
			1 * time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second, 8 * time.Second,
		})
	})

	Convey("Reset returns to initial", t, func() {
		b := remote_device_watcher_svc.NewBackoff(remote_device_watcher_svc.BackoffConfig{
			Initial: time.Second, Max: 30 * time.Second,
			Multiplier: 2.0, Jitter: 0.0,
		}, rand.New(rand.NewSource(1)))
		_, _, _ = b.Next(), b.Next(), b.Next()
		b.Reset()
		So(b.Next(), ShouldEqual, time.Second)
	})

	Convey("Jitter keeps result within ±jitter band", t, func() {
		b := remote_device_watcher_svc.NewBackoff(remote_device_watcher_svc.BackoffConfig{
			Initial: 10 * time.Second, Max: 30 * time.Second,
			Multiplier: 1.0, Jitter: 0.2,
		}, rand.New(rand.NewSource(42)))
		for i := 0; i < 50; i++ {
			d := b.Next()
			So(d, ShouldBeGreaterThanOrEqualTo, 8*time.Second)
			So(d, ShouldBeLessThanOrEqualTo, 12*time.Second)
		}
	})
}
