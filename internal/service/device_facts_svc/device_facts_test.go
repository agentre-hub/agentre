package device_facts_svc_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/cago-frame/cago/pkg/utils/httputils"
	. "github.com/smartystreets/goconvey/convey"

	"github.com/agentre-hub/agentre/internal/pkg/code"
	"github.com/agentre-hub/agentre/internal/service/device_facts_svc"
	"github.com/agentre-hub/agentre/pkg/syncwire"
	"github.com/agentre-hub/agentre/pkg/wire/devicefp"
)

const (
	self devicefp.Carrier = "sha256:self"
	boxA devicefp.Carrier = "sha256:boxa"
	boxB devicefp.Carrier = "sha256:boxb"
)

func factsWith(version string, at int64) syncwire.DeviceFactsReport {
	return syncwire.DeviceFactsReport{CollectedAt: at, Runtimes: []syncwire.RuntimeFact{{Runtime: "claudecode", Found: true, Version: version}}}
}

type fakeSelf struct {
	mu        sync.Mutex
	current   syncwire.DeviceFactsReport
	err       error
	refreshes int
	connects  int
}

func (f *fakeSelf) Current(context.Context) (syncwire.DeviceFactsReport, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.current, f.err
}

func (f *fakeSelf) Refresh(context.Context) (syncwire.DeviceFactsReport, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.refreshes++
	f.current = factsWith("9.9.9", 900)
	return f.current, f.err
}

func (f *fakeSelf) Connected(context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.connects++
	return nil
}

func (f *fakeSelf) Run(context.Context, time.Duration) {}

type read struct {
	id      int64
	refresh bool
}

type fakeDevices struct {
	mu      sync.Mutex
	paired  []device_facts_svc.PairedDevice
	facts   map[int64]syncwire.DeviceFactsReport
	failing map[int64]bool
	reads   []read
}

func (f *fakeDevices) Self() (devicefp.Carrier, error) { return self, nil }

func (f *fakeDevices) Paired(context.Context) ([]device_facts_svc.PairedDevice, error) {
	return f.paired, nil
}

func (f *fakeDevices) ReadFacts(_ context.Context, id int64, refresh bool) (syncwire.DeviceFactsReport, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reads = append(f.reads, read{id: id, refresh: refresh})
	if f.failing[id] {
		return syncwire.DeviceFactsReport{}, errors.New("dial failed")
	}
	return f.facts[id], nil
}

func errCode(err error) int {
	var httpErr *httputils.Error
	if errors.As(err, &httpErr) {
		return httpErr.Code
	}
	return 0
}

func TestDeviceFactsSvc(t *testing.T) {
	Convey("设备情况读取", t, func() {
		ctx := context.Background()
		selfFacts := &fakeSelf{current: factsWith("2.1.0", 100)}
		devices := &fakeDevices{
			paired: []device_facts_svc.PairedDevice{
				{ID: 1, Fingerprint: boxA, Name: "Box A"},
				{ID: 2, Fingerprint: boxB, Name: "Box B"},
				{ID: 3, Fingerprint: self, Name: "loopback"}, // 本机自己出现在配对表里也只算一次
			},
			facts:   map[int64]syncwire.DeviceFactsReport{1: factsWith("0.46.0", 200), 2: factsWith("0.40.0", 300)},
			failing: map[int64]bool{},
		}
		svc := device_facts_svc.New(device_facts_svc.WithSelf(selfFacts), device_facts_svc.WithDevices(devices))

		Convey("Given 本机与两台已配对 agentred, When List, Then 本机在前,每台带上自己报的情况与上报时间", func() {
			views, err := svc.List(ctx)
			So(err, ShouldBeNil)
			So(views, ShouldHaveLength, 3)
			So(views[0].Fingerprint, ShouldEqual, self)
			So(views[0].IsSelf, ShouldBeTrue)
			So(views[0].Kind, ShouldEqual, "desktop")
			So(views[0].Facts.CollectedAt, ShouldEqual, 100)
			So(views[1].Fingerprint, ShouldEqual, boxA)
			So(views[1].Name, ShouldEqual, "Box A")
			So(views[1].Kind, ShouldEqual, "agentred")
			So(views[1].Reachable, ShouldBeTrue)
			So(views[1].Facts.Runtimes[0].Version, ShouldEqual, "0.46.0")
			So(views[2].Facts.CollectedAt, ShouldEqual, 300)
			for _, r := range devices.reads {
				So(r.refresh, ShouldBeFalse) // 列表读的是对方记下的那份,不触发重探
			}
		})

		Convey("Given 一台 agentred 读过一次之后离线, When 再 List, Then 仍显示最后一次的情况与时间,并标为够不着", func() {
			_, err := svc.List(ctx)
			So(err, ShouldBeNil)
			devices.failing[1] = true
			views, err := svc.List(ctx)
			So(err, ShouldBeNil)
			So(views[1].Reachable, ShouldBeFalse)
			So(views[1].Facts, ShouldNotBeNil)
			So(views[1].Facts.CollectedAt, ShouldEqual, 200)
		})

		Convey("Given 一台 agentred 从没读到过, When 读不到, Then 情况为空、标为够不着,其余设备照常", func() {
			devices.failing[2] = true
			views, err := svc.List(ctx)
			So(err, ShouldBeNil)
			So(views[2].Facts, ShouldBeNil)
			So(views[2].Reachable, ShouldBeFalse)
			So(views[1].Reachable, ShouldBeTrue)
		})

		Convey("Given 用户在设备页点本机的刷新, Then 本机立即重探(并由它上报),交回新情况", func() {
			view, err := svc.Refresh(ctx, self)
			So(err, ShouldBeNil)
			So(selfFacts.refreshes, ShouldEqual, 1)
			So(view.IsSelf, ShouldBeTrue)
			So(view.Facts.Runtimes[0].Version, ShouldEqual, "9.9.9")
		})

		Convey("Given 用户点一台 agentred 的刷新, Then 让那台立即重探", func() {
			view, err := svc.Refresh(ctx, boxB)
			So(err, ShouldBeNil)
			So(devices.reads, ShouldResemble, []read{{id: 2, refresh: true}})
			So(view.Fingerprint, ShouldEqual, boxB)
			So(view.Reachable, ShouldBeTrue)
		})

		Convey("Given 要刷新的 agentred 够不着, Then 报「设备够不着」", func() {
			devices.failing[2] = true
			_, err := svc.Refresh(ctx, boxB)
			So(errCode(err), ShouldEqual, code.DeviceFactsDeviceUnreachable)
		})

		Convey("Given 不认识的设备, Then 报「设备不存在」", func() {
			_, err := svc.Refresh(ctx, "sha256:nobody")
			So(errCode(err), ShouldEqual, code.DeviceFactsDeviceNotFound)
		})

		Convey("Given 本机情况探不出来, When List, Then 本机那一行情况为空,不拖垮整张列表", func() {
			selfFacts.err = errors.New("db down")
			views, err := svc.List(ctx)
			So(err, ShouldBeNil)
			So(views[0].Facts, ShouldBeNil)
			So(views[1].Facts, ShouldNotBeNil)
		})

		Convey("Given 连上账号中继, When AccountChannelConnected, Then 本机整份上报一次", func() {
			svc.AccountChannelConnected(ctx)
			So(selfFacts.connects, ShouldEqual, 1)
		})
	})
}
