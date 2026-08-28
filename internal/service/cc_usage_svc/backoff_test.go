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

func TestProbe_Backoff429(t *testing.T) {
	Convey("Probe 在收到 429 后,下一次 Probe 在 backoff 窗口内被跳过(不调 fetcher)", t, func() {
		now := time.Unix(1_000_000, 0)
		mgr := cc_usage_svc.NewManager(cc_usage_svc.ManagerOpts{
			Now:                 func() time.Time { return now },
			RateLimitBackoffMin: 60 * time.Second, // base 60s
		})

		var calls int32
		mgr.SetFetcherResolver(func(_ cc_usage_svc.DeviceKey) (cc_usage_svc.Fetcher, error) {
			return func(_ context.Context) (*ccoauth.RateLimits, error) {
				atomic.AddInt32(&calls, 1)
				return nil, ccoauth.ErrRateLimited
			}, nil
		})

		mgr.Probe(context.Background(), "local")
		So(atomic.LoadInt32(&calls), ShouldEqual, 1)

		// 时间未推进,再 Probe 应当被 backoff 跳过(fetcher 调用计数不增加)
		mgr.Probe(context.Background(), "local")
		So(atomic.LoadInt32(&calls), ShouldEqual, 1)

		// 推进时间超过 backoff 窗口,Probe 应该再次执行
		now = now.Add(61 * time.Second)
		mgr.Probe(context.Background(), "local")
		So(atomic.LoadInt32(&calls), ShouldEqual, 2)
	})

	Convey("Probe 在成功后清空 429 backoff,下次 Probe 立即执行", t, func() {
		now := time.Unix(1_000_000, 0)
		mgr := cc_usage_svc.NewManager(cc_usage_svc.ManagerOpts{
			Now:                 func() time.Time { return now },
			RateLimitBackoffMin: 60 * time.Second,
		})

		var calls int32
		mgr.SetFetcherResolver(func(_ cc_usage_svc.DeviceKey) (cc_usage_svc.Fetcher, error) {
			return func(_ context.Context) (*ccoauth.RateLimits, error) {
				c := atomic.AddInt32(&calls, 1)
				if c == 1 {
					return nil, ccoauth.ErrRateLimited // 第 1 次 429
				}
				return &ccoauth.RateLimits{FiveHourPercent: 1}, nil // 之后都成功
			}, nil
		})

		mgr.Probe(context.Background(), "local") // 429
		now = now.Add(61 * time.Second)
		mgr.Probe(context.Background(), "local") // 成功,清 backoff
		mgr.Probe(context.Background(), "local") // 即刻再次成功(无 backoff 拦截)

		So(atomic.LoadInt32(&calls), ShouldEqual, 3)
	})
}
