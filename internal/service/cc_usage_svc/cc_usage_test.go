package cc_usage_svc_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	. "github.com/smartystreets/goconvey/convey"

	"github.com/agentre-hub/agentre/internal/pkg/ccoauth"
	"github.com/agentre-hub/agentre/internal/service/cc_usage_svc"
)

// recordEmitter 收集所有 emit 事件供断言。
type recordEmitter struct {
	mu       sync.Mutex
	payloads []cc_usage_svc.EmitPayload
}

func (e *recordEmitter) emit(p cc_usage_svc.EmitPayload) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.payloads = append(e.payloads, p)
}
func (e *recordEmitter) all() []cc_usage_svc.EmitPayload {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]cc_usage_svc.EmitPayload(nil), e.payloads...)
}

func TestProbe_Success(t *testing.T) {
	Convey("Probe 在 fetcher 返回 RateLimits 时缓存并 emit reason=ok", t, func() {
		mgr := cc_usage_svc.NewManager(cc_usage_svc.ManagerOpts{
			Now: func() time.Time { return time.Unix(100, 0) },
		})
		rec := &recordEmitter{}
		mgr.SetEmitter(rec.emit)
		mgr.SetFetcherResolver(func(_ cc_usage_svc.DeviceKey) (cc_usage_svc.Fetcher, error) {
			return func(_ context.Context) (*ccoauth.RateLimits, error) {
				return &ccoauth.RateLimits{FiveHourPercent: 25, WeeklyPercent: 7}, nil
			}, nil
		})

		mgr.Probe(context.Background(), "local")

		state, ok := mgr.Get("local")
		So(ok, ShouldBeTrue)
		So(state.Reason, ShouldEqual, "ok")
		So(state.Data, ShouldNotBeNil)
		So(state.Data.FiveHourPercent, ShouldEqual, 25)
		So(state.Stale, ShouldBeFalse)

		emits := rec.all()
		So(emits, ShouldHaveLength, 1)
		So(emits[0].DeviceKey, ShouldEqual, cc_usage_svc.DeviceKey("local"))
		So(emits[0].State.Reason, ShouldEqual, "ok")
	})
}

func TestProbe_ErrorMapping(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want string
	}{
		{"no_credentials", ccoauth.ErrNoCredentials, "no_credentials"},
		{"auth_expired", ccoauth.ErrAuthExpired, "auth_expired"},
		{"rate_limited", ccoauth.ErrRateLimited, "rate_limited"},
		{"network", errors.New("dial nope"), "network"},
	}
	for _, tc := range cases {
		Convey("Probe 把 fetcher 错误映射到 reason="+tc.want, t, func() {
			mgr := cc_usage_svc.NewManager(cc_usage_svc.ManagerOpts{})
			rec := &recordEmitter{}
			mgr.SetEmitter(rec.emit)
			mgr.SetFetcherResolver(func(_ cc_usage_svc.DeviceKey) (cc_usage_svc.Fetcher, error) {
				return func(_ context.Context) (*ccoauth.RateLimits, error) {
					return nil, tc.err
				}, nil
			})

			mgr.Probe(context.Background(), "local")

			state, _ := mgr.Get("local")
			So(state.Reason, ShouldEqual, tc.want)
			So(state.Data, ShouldBeNil)
			So(rec.all()[0].State.Reason, ShouldEqual, tc.want)
		})
	}
}

func TestProbe_StaleDataOnTransientError(t *testing.T) {
	Convey("Probe 在拿到成功数据后,再次遇到 network/rate_limited 错误时保留 stale Data", t, func() {
		var callCount int
		mgr := cc_usage_svc.NewManager(cc_usage_svc.ManagerOpts{})
		mgr.SetFetcherResolver(func(_ cc_usage_svc.DeviceKey) (cc_usage_svc.Fetcher, error) {
			return func(_ context.Context) (*ccoauth.RateLimits, error) {
				callCount++
				if callCount == 1 {
					return &ccoauth.RateLimits{FiveHourPercent: 50}, nil
				}
				return nil, ccoauth.ErrRateLimited
			}, nil
		})

		mgr.Probe(context.Background(), "local") // 成功
		mgr.Probe(context.Background(), "local") // 429

		state, _ := mgr.Get("local")
		So(state.Reason, ShouldEqual, "rate_limited")
		So(state.Stale, ShouldBeTrue)
		So(state.Data, ShouldNotBeNil)
		So(state.Data.FiveHourPercent, ShouldEqual, 50)
	})

	Convey("Probe 在 auth_expired 时清空 Data(凭证失效则旧数据无意义)", t, func() {
		var callCount int
		mgr := cc_usage_svc.NewManager(cc_usage_svc.ManagerOpts{})
		mgr.SetFetcherResolver(func(_ cc_usage_svc.DeviceKey) (cc_usage_svc.Fetcher, error) {
			return func(_ context.Context) (*ccoauth.RateLimits, error) {
				callCount++
				if callCount == 1 {
					return &ccoauth.RateLimits{FiveHourPercent: 80}, nil
				}
				return nil, ccoauth.ErrAuthExpired
			}, nil
		})

		mgr.Probe(context.Background(), "local")
		mgr.Probe(context.Background(), "local")

		state, _ := mgr.Get("local")
		So(state.Reason, ShouldEqual, "auth_expired")
		So(state.Data, ShouldBeNil)
		So(state.Stale, ShouldBeFalse)
	})
}

func TestProbe_ResolverError(t *testing.T) {
	Convey("Probe 在 resolver 返回错误时 emit reason=device_offline", t, func() {
		mgr := cc_usage_svc.NewManager(cc_usage_svc.ManagerOpts{})
		rec := &recordEmitter{}
		mgr.SetEmitter(rec.emit)
		mgr.SetFetcherResolver(func(_ cc_usage_svc.DeviceKey) (cc_usage_svc.Fetcher, error) {
			return nil, errors.New("device 42 not online")
		})

		mgr.Probe(context.Background(), "remote:42")

		state, _ := mgr.Get("remote:42")
		So(state.Reason, ShouldEqual, "device_offline")
		So(state.Data, ShouldBeNil)
	})
}

func TestProbe_NoResolver(t *testing.T) {
	Convey("Probe 在 resolver 未设置时不调 fetcher, 静默 no-op", t, func() {
		mgr := cc_usage_svc.NewManager(cc_usage_svc.ManagerOpts{})
		rec := &recordEmitter{}
		mgr.SetEmitter(rec.emit)
		// 不 SetFetcherResolver
		mgr.Probe(context.Background(), "local")
		So(rec.all(), ShouldBeEmpty)
		_, ok := mgr.Get("local")
		So(ok, ShouldBeFalse)
	})
}

func TestGet_UnknownDeviceKey(t *testing.T) {
	Convey("Get 未知 deviceKey 返回 (zero, false)", t, func() {
		mgr := cc_usage_svc.NewManager(cc_usage_svc.ManagerOpts{})
		_, ok := mgr.Get("nope")
		So(ok, ShouldBeFalse)
	})
}
