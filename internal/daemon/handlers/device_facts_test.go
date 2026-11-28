package handlers_test

import (
	"context"
	"errors"
	"testing"

	. "github.com/smartystreets/goconvey/convey"

	"github.com/agentre-hub/agentre/internal/daemon/handlers"
	"github.com/agentre-hub/agentre/pkg/syncwire"
	"github.com/agentre-hub/agentre/pkg/wire/agentrewire"
)

type fakeFactsSource struct {
	current, refreshed syncwire.DeviceFactsReport
	err                error
	refreshes          int
}

func (f *fakeFactsSource) Current(context.Context) (syncwire.DeviceFactsReport, error) {
	return f.current, f.err
}

func (f *fakeFactsSource) Refresh(context.Context) (syncwire.DeviceFactsReport, error) {
	f.refreshes++
	return f.refreshed, f.err
}

func TestDeviceFacts(t *testing.T) {
	Convey("device.facts 交出这台 agentred 的情况", t, func() {
		ctx := context.Background()
		src := &fakeFactsSource{
			current:   syncwire.DeviceFactsReport{CollectedAt: 1, Runtimes: []syncwire.RuntimeFact{{Runtime: "codex", Found: true, Version: "0.40.0"}}},
			refreshed: syncwire.DeviceFactsReport{CollectedAt: 2, Runtimes: []syncwire.RuntimeFact{{Runtime: "codex", Found: true, Version: "0.46.0"}}},
		}
		h := handlers.NewDeviceFactsHandlers(src)

		Convey("Given 不要求刷新, Then 交回最近一次的情况,不重新探测", func() {
			resp, err := h.Facts(ctx, &agentrewire.DeviceFactsRequest{})
			So(err, ShouldBeNil)
			So(resp.CollectedAtMs, ShouldEqual, 1)
			So(resp.Runtimes[0].Version, ShouldEqual, "0.40.0")
			So(src.refreshes, ShouldEqual, 0)
		})
		Convey("Given 用户点了刷新, Then 立即重探并交回新情况", func() {
			resp, err := h.Facts(ctx, &agentrewire.DeviceFactsRequest{Refresh: true})
			So(err, ShouldBeNil)
			So(resp.CollectedAtMs, ShouldEqual, 2)
			So(resp.Runtimes[0].Version, ShouldEqual, "0.46.0")
			So(src.refreshes, ShouldEqual, 1)
		})
		Convey("Given 探测失败, Then 报错而不是交回一份空情况", func() {
			src.err = errors.New("collect failed")
			_, err := h.Facts(ctx, &agentrewire.DeviceFactsRequest{})
			So(err, ShouldNotBeNil)
		})
	})
}
