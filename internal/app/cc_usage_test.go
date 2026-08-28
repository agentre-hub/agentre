package app

import (
	"context"
	"testing"
	"time"

	. "github.com/smartystreets/goconvey/convey"

	"github.com/agentre-hub/agentre/internal/pkg/ccoauth"
	"github.com/agentre-hub/agentre/internal/service/cc_usage_svc"
	"github.com/agentre-hub/agentre/pkg/wire/agentrewire"
)

func TestProtobufRateLimitsPreservesOptionalPresence(t *testing.T) {
	reset := int64(1_700_000_000_000)
	zero := 0.0
	got := protobufRateLimits(&agentrewire.ClaudeCodeRateLimits{FiveHourPercent: 12, FiveHourResetsAtMs: &reset, SonnetWeeklyPercent: &zero})
	if got.FiveHourResetsAt == nil || !got.FiveHourResetsAt.Equal(time.UnixMilli(reset)) {
		t.Fatalf("five-hour reset = %v", got.FiveHourResetsAt)
	}
	if got.WeeklyResetsAt != nil {
		t.Fatalf("missing weekly reset became %v", got.WeeklyResetsAt)
	}
	if got.SonnetWeeklyPercent == nil || *got.SonnetWeeklyPercent != 0 {
		t.Fatalf("optional zero lost: %v", got.SonnetWeeklyPercent)
	}
	if got.OpusWeeklyPercent != nil {
		t.Fatalf("missing opus percent became %v", got.OpusWeeklyPercent)
	}
}

// TestBuildCCUsageResolver_LocalVsRemoteIsolation 锁住:resolver 必须把 LocalKey
// 走本地 fetcher、把 "remote:<id>" 走远端 RPC 分支,二者不能串。这是 cc_usage_svc
// 数据按 deviceKey 分桶的前提 —— 一旦 resolver 把 remote 落到 local 路径,前端两
// 个 device 看到的就是同一份数字。
func TestBuildCCUsageResolver_LocalVsRemoteIsolation(t *testing.T) {
	Convey("buildCCUsageResolver 把 local / remote 分流到不同 fetcher", t, func() {
		a := &App{}
		resolver := a.buildCCUsageResolver()

		Convey("local key 返回 fetcher 且不报错(走桌面本地 ccoauth)", func() {
			fetcher, err := resolver(cc_usage_svc.LocalKey)
			So(err, ShouldBeNil)
			So(fetcher, ShouldNotBeNil)
		})

		Convey("remote:<id> 在 remote_device_svc 未注入时 → ErrDeviceOffline", func() {
			// remote_device_svc.Default() == nil:这是生产 wire-up 之外的隔离测试,
			// 保证 resolver 没把请求"降级"成本地 fetcher 而是显式标记 offline。
			fetcher, err := resolver(cc_usage_svc.DeviceKey("remote:42"))
			So(fetcher, ShouldBeNil)
			So(err, ShouldEqual, cc_usage_svc.ErrDeviceOffline)
		})

		Convey("未识别的 key 也 → ErrDeviceOffline,不会 fallback 到 local fetcher", func() {
			fetcher, err := resolver(cc_usage_svc.DeviceKey("garbage"))
			So(fetcher, ShouldBeNil)
			So(err, ShouldEqual, cc_usage_svc.ErrDeviceOffline)
		})
	})
}

// TestManager_ProbeKeepsKeysSeparate 锁住:同一个 Manager 对 local / remote 探后,
// 两份 state 在 m.states 里独立,前端按 key 取不会拿到对方的数字。这是 chat-panel
// "本地 session 显示本机配额、远端 session 显示远端配额"的语义前提。
func TestManager_ProbeKeepsKeysSeparate(t *testing.T) {
	Convey("Probe(local) 和 Probe(remote:1) 各写各的 bucket,不串", t, func() {
		mgr := cc_usage_svc.NewManager(cc_usage_svc.ManagerOpts{})

		// resolver 按 key 返回带"标签"的 fetcher,让我们能反推哪个 bucket 拿到了哪份数据。
		mgr.SetFetcherResolver(func(key cc_usage_svc.DeviceKey) (cc_usage_svc.Fetcher, error) {
			marker := float64(0)
			switch key {
			case cc_usage_svc.LocalKey:
				marker = 11
			case cc_usage_svc.DeviceKey("remote:1"):
				marker = 77
			default:
				return nil, cc_usage_svc.ErrDeviceOffline
			}
			return func(_ context.Context) (*ccoauth.RateLimits, error) {
				return &ccoauth.RateLimits{FiveHourPercent: marker, WeeklyPercent: marker}, nil
			}, nil
		})

		mgr.Probe(context.Background(), cc_usage_svc.LocalKey)
		mgr.Probe(context.Background(), cc_usage_svc.DeviceKey("remote:1"))

		local, ok := mgr.Get(cc_usage_svc.LocalKey)
		So(ok, ShouldBeTrue)
		So(local.Data, ShouldNotBeNil)
		So(local.Data.FiveHourPercent, ShouldEqual, 11)

		remote, ok := mgr.Get(cc_usage_svc.DeviceKey("remote:1"))
		So(ok, ShouldBeTrue)
		So(remote.Data, ShouldNotBeNil)
		So(remote.Data.FiveHourPercent, ShouldEqual, 77)
	})
}
