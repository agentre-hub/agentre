package remote_device_svc_test

import (
	"testing"

	. "github.com/smartystreets/goconvey/convey"

	"github.com/agentre-hub/agentre/internal/service/remote_device_svc"
)

// TargetsAnotherMachine 是「这一档要不要派到别的机器上跑」的全仓单一判据。它取代了
// 散在 20+ 处的 `be.IsRemote() && !IsSelfDevice(be.DeviceID)` 手写组合 —— R13 认领后
// 本机 backend 的 DeviceID 就是本机指纹，只问 IsRemote() 会把本机判成远端，漏一处就
// 出一个 bug（已经踩过两次：文件面板打不开、本机会话显示离线/未配对）。
func TestTargetsAnotherMachine(t *testing.T) {
	Convey("Given this installation has a device fingerprint", t, func() {
		_, _, kc, _, svc := setupSvc(t)
		kc.EXPECT().Get("agentre-device-fingerprint").Return("sha256:self", nil).AnyTimes()
		prev := remote_device_svc.Default()
		remote_device_svc.SetDefault(svc)
		t.Cleanup(func() { remote_device_svc.SetDefault(prev) })

		Convey("空 DeviceID（从未认领的本机档）不是别的机器", func() {
			So(remote_device_svc.TargetsAnotherMachine(""), ShouldBeFalse)
			So(remote_device_svc.ExternalDeviceID(""), ShouldEqual, "")
		})
		Convey("本机指纹（R13 认领后的本机档）不是别的机器", func() {
			So(remote_device_svc.TargetsAnotherMachine("sha256:self"), ShouldBeFalse)
			So(remote_device_svc.ExternalDeviceID("sha256:self"), ShouldEqual, "")
		})
		Convey("别台机器的指纹是别的机器——即便本机没配对过它", func() {
			So(remote_device_svc.TargetsAnotherMachine("sha256:other"), ShouldBeTrue)
			So(remote_device_svc.ExternalDeviceID("sha256:other"), ShouldEqual, "sha256:other")
		})
		Convey("历史数值配对行 ID 仍是别的机器（pre-R13 producer 还在发）", func() {
			So(remote_device_svc.TargetsAnotherMachine("7"), ShouldBeTrue)
			So(remote_device_svc.ExternalDeviceID("7"), ShouldEqual, "7")
		})
	})

	// bootstrap 之前 / 浏览器语境里没有「自己」：此时具名指纹一律按别的机器处理，
	// 不能因为认不出自己就把远端档误判成本机（那会把活派错地方）。
	Convey("Given the service is not wired up yet", t, func() {
		prev := remote_device_svc.Default()
		remote_device_svc.SetDefault(nil)
		t.Cleanup(func() { remote_device_svc.SetDefault(prev) })

		So(remote_device_svc.TargetsAnotherMachine(""), ShouldBeFalse)
		So(remote_device_svc.TargetsAnotherMachine("sha256:anything"), ShouldBeTrue)
	})
}
