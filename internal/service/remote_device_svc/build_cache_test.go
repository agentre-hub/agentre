package remote_device_svc_test

import (
	"context"
	"testing"

	. "github.com/smartystreets/goconvey/convey"
	"go.uber.org/mock/gomock"

	"github.com/agentre-hub/agentre/internal/model/entity/paired_agentred_entity"
)

// 设备行要显示远端 agentred 的真实版本,而版本号与短 commit 是两件事(决策 4):
// 版本号要能比较,短 commit 为空才是「非发布构建」的判据(决策 5)。合并成一个展示串
// 会把展示格式变成契约,所以两者各自成字段、各自投影。
//
// 与能力位同类:这两个值描述的是当前那个 daemon 进程,进程内缓存、不落库 ——
// 桌面端重启后重新探,机器换了版本就在下一次 health.ping 覆盖。
func TestRemoteDeviceSvc_BuildCache_ProjectsDaemonVersionAndCommit(t *testing.T) {
	Convey("daemon 构建标识的进程内缓存", t, func() {
		repo, _, _, _, svc := setupSvc(t)
		rows := []*paired_agentred_entity.PairedAgentred{
			{ID: 7, Name: "lab", URL: "ws://lab/rpc", TLSMode: "default"},
			{ID: 8, Name: "other", URL: "ws://other/rpc", TLSMode: "default"},
		}

		Convey("还没探过:两个字段都是空,界面据此什么都不说", func() {
			repo.EXPECT().List(gomock.Any()).Return(rows, nil)

			got, err := svc.List(context.Background())

			So(err, ShouldBeNil)
			So(got[0].DaemonVersion, ShouldEqual, "")
			So(got[0].DaemonCommit, ShouldEqual, "")
		})

		Convey("记下之后:同一张视图投影出这一对值,别的设备不受影响", func() {
			repo.EXPECT().List(gomock.Any()).Return(rows, nil)
			svc.RecordDeviceBuild(7, "0.5.2", "a1b2c3d")

			got, err := svc.List(context.Background())

			So(err, ShouldBeNil)
			So(got[0].DaemonVersion, ShouldEqual, "0.5.2")
			So(got[0].DaemonCommit, ShouldEqual, "a1b2c3d")
			So(got[1].DaemonVersion, ShouldEqual, "")
		})

		Convey("机器升过级:后一次心跳的值覆盖前一次", func() {
			repo.EXPECT().List(gomock.Any()).Return(rows, nil)
			svc.RecordDeviceBuild(7, "0.5.2", "a1b2c3d")
			svc.RecordDeviceBuild(7, "0.6.0", "9f8e7d6")

			got, err := svc.List(context.Background())

			So(err, ShouldBeNil)
			So(got[0].DaemonVersion, ShouldEqual, "0.6.0")
			So(got[0].DaemonCommit, ShouldEqual, "9f8e7d6")
		})

		Convey("远端是本地构建:短 commit 记成空串,而不是丢掉版本号", func() {
			repo.EXPECT().List(gomock.Any()).Return(rows, nil)
			svc.RecordDeviceBuild(7, "1.0.0", "")

			got, err := svc.List(context.Background())

			So(err, ShouldBeNil)
			So(got[0].DaemonVersion, ShouldEqual, "1.0.0")
			So(got[0].DaemonCommit, ShouldEqual, "")
		})
	})
}
