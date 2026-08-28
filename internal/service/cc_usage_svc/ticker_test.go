package cc_usage_svc_test

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	. "github.com/smartystreets/goconvey/convey"

	"github.com/agentre-hub/agentre/internal/pkg/ccoauth"
	"github.com/agentre-hub/agentre/internal/service/cc_usage_svc"
)

func TestStartTicker_FiresUntilCtxCancel(t *testing.T) {
	Convey("StartTicker 在每个 tick 调用 Probe,ctx 取消时退出", t, func() {
		mgr := cc_usage_svc.NewManager(cc_usage_svc.ManagerOpts{})
		var calls int32
		mgr.SetFetcherResolver(func(_ cc_usage_svc.DeviceKey) (cc_usage_svc.Fetcher, error) {
			return func(_ context.Context) (*ccoauth.RateLimits, error) {
				atomic.AddInt32(&calls, 1)
				return &ccoauth.RateLimits{}, nil
			}, nil
		})

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		// 20ms 间隔,跑 100ms,预期至少 3-5 次(允许调度抖动)
		mgr.StartTicker(ctx, "local", 20*time.Millisecond)

		time.Sleep(120 * time.Millisecond)
		cancel()
		// 给 goroutine 一点时间退出
		time.Sleep(50 * time.Millisecond)

		c := atomic.LoadInt32(&calls)
		So(c, ShouldBeGreaterThanOrEqualTo, 3)
		// 取消后不再增长(短暂窗口允许 1 次最后的 tick 漏入)
		later := atomic.LoadInt32(&calls)
		time.Sleep(60 * time.Millisecond)
		So(atomic.LoadInt32(&calls), ShouldBeLessThanOrEqualTo, later+1)
	})
}

func TestStopTicker(t *testing.T) {
	Convey("StopTicker 立即停止指定 device 的 ticker", t, func() {
		mgr := cc_usage_svc.NewManager(cc_usage_svc.ManagerOpts{})
		var calls int32
		mgr.SetFetcherResolver(func(_ cc_usage_svc.DeviceKey) (cc_usage_svc.Fetcher, error) {
			return func(_ context.Context) (*ccoauth.RateLimits, error) {
				atomic.AddInt32(&calls, 1)
				return &ccoauth.RateLimits{}, nil
			}, nil
		})

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		mgr.StartTicker(ctx, "local", 15*time.Millisecond)

		time.Sleep(60 * time.Millisecond)
		mgr.StopTicker("local")
		time.Sleep(30 * time.Millisecond)
		stopAt := atomic.LoadInt32(&calls)

		time.Sleep(80 * time.Millisecond)
		So(atomic.LoadInt32(&calls), ShouldEqual, stopAt)
	})
}

func TestStartTicker_ReplacesExisting(t *testing.T) {
	Convey("StartTicker 重复调用同 key 替换旧 ticker, 不泄漏 goroutine", t, func() {
		mgr := cc_usage_svc.NewManager(cc_usage_svc.ManagerOpts{})
		mgr.SetFetcherResolver(func(_ cc_usage_svc.DeviceKey) (cc_usage_svc.Fetcher, error) {
			return func(_ context.Context) (*ccoauth.RateLimits, error) {
				return &ccoauth.RateLimits{}, nil
			}, nil
		})

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		mgr.StartTicker(ctx, "local", 50*time.Millisecond)
		mgr.StartTicker(ctx, "local", 50*time.Millisecond) // 不应 panic / 不应 leak

		mgr.StopTicker("local")
	})
}
