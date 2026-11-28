package dispatch_svc_test

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	. "github.com/smartystreets/goconvey/convey"

	"github.com/agentre-hub/agentre/internal/service/device_facts_svc"
	"github.com/agentre-hub/agentre/internal/service/dispatch_svc"
	"github.com/agentre-hub/agentre/pkg/syncwire"
	"github.com/agentre-hub/agentre/pkg/syncwire/dispatch"
	"github.com/agentre-hub/agentre/pkg/wire/devicefp"
)

type countingSelf struct{}

func (countingSelf) Current(context.Context) (syncwire.DeviceFactsReport, error) {
	return *runtimeFacts("claudecode", true, syncwire.NativeLoginLoggedIn), nil
}
func (countingSelf) Refresh(ctx context.Context) (syncwire.DeviceFactsReport, error) {
	return countingSelf{}.Current(ctx)
}
func (countingSelf) Connected(context.Context) error    { return nil }
func (countingSelf) Run(context.Context, time.Duration) {}

type liveRPCDevices struct{ reads atomic.Int32 }

func (*liveRPCDevices) Self() (devicefp.Carrier, error) { return selfFP, nil }
func (*liveRPCDevices) Paired(context.Context) ([]device_facts_svc.PairedDevice, error) {
	return []device_facts_svc.PairedDevice{{ID: 7, Fingerprint: boxFP, Name: "机房"}}, nil
}
func (d *liveRPCDevices) ReadFacts(context.Context, int64, bool) (syncwire.DeviceFactsReport, error) {
	d.reads.Add(1)
	return *runtimeFacts("claudecode", true, syncwire.NativeLoginLoggedIn), nil
}

func TestPlan_GivenFactsCache_ThenPlanNeverCallsTheLiveFactsRPC(t *testing.T) {
	Convey("Plan 读设备情况缓存,不为它同步 RPC 每台 agentred", t, func() {
		ctx := context.Background()
		live := &liveRPCDevices{}
		facts := device_facts_svc.New(device_facts_svc.WithSelf(countingSelf{}), device_facts_svc.WithDevices(live))
		w := newWorld()
		w.factsPort = dispatch_svc.NewCachedFacts(facts)

		Convey("巡检前:没读过的 agentred 没有情况(运行时缺失),但 Plan 不触发任何 RPC", func() {
			p, err := w.svc(t).Plan(ctx, &dispatch_svc.PlanRequest{AgentID: 1})
			So(err, ShouldBeNil)
			So(live.reads.Load(), ShouldEqual, 0)
			So(p.Chosen, ShouldEqual, selfFP)
			So(deviceOf(p, boxFP).Eligible, ShouldBeFalse)
			So(deviceOf(p, boxFP).Reason.Code, ShouldEqual, string(dispatch.ReasonRuntimeMissing))
		})

		Convey("巡检一次后:反复 Plan 都用缓存,RPC 次数不再增加", func() {
			facts.Sync(ctx)
			after := live.reads.Load()
			So(after, ShouldEqual, 1)
			for range 3 {
				p, err := w.svc(t).Plan(ctx, &dispatch_svc.PlanRequest{AgentID: 1})
				So(err, ShouldBeNil)
				So(deviceOf(p, boxFP).Eligible, ShouldBeTrue)
			}
			So(live.reads.Load(), ShouldEqual, after)
		})
	})
}
