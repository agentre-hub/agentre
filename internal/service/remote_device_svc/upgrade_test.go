package remote_device_svc_test

import (
	"context"
	"errors"
	"testing"
	"time"

	. "github.com/smartystreets/goconvey/convey"
	"go.uber.org/mock/gomock"

	remoterepomock "github.com/agentre-hub/agentre/internal/repository/remote_device_repo/mock_remote_device_repo"
	"github.com/agentre-hub/agentre/internal/service/remote_device_svc"
	svcmock "github.com/agentre-hub/agentre/internal/service/remote_device_svc/mock_remote_device_svc"
	"github.com/agentre-hub/agentre/pkg/wire/agentrewire"
	"github.com/agentre-hub/agentre/pkg/wire/protorpc"
)

func TestRemoteDeviceSvc_Upgrade(t *testing.T) {
	Convey("Upgrade", t, func() {
		ctrl := gomock.NewController(t)
		t.Cleanup(ctrl.Finish)

		// Upgrade 借连接全靠 pool.Borrow,不碰 repo(不像 Refresh/Get 那样要先读一遍
		// 落库的行);deviceRepo 因此只是满足构造函数签名,不设期望。
		deviceRepo := remoterepomock.NewMockPairedAgentredRepo(ctrl)
		dial := svcmock.NewMockDaemonDialPort(ctrl)
		kc := svcmock.NewMockKeychainPort(ctrl)
		pool := svcmock.NewMockConnPool(ctrl)
		svc := remote_device_svc.New(deviceRepo, dial, kc, pool)

		Convey("accepted: reports target version, no reject reason", func() {
			lease := svcmock.NewMockLease(ctrl)
			pool.EXPECT().Borrow(gomock.Any(), int64(42)).Return(lease, nil)
			lease.EXPECT().SelfUpdate(gomock.Any(), gomock.Any()).
				DoAndReturn(func(_ context.Context, req *agentrewire.AgentredSelfUpdateRequest) (*agentrewire.AgentredSelfUpdateResponse, error) {
					So(req.Channel, ShouldEqual, "")
					So(req.Force, ShouldBeFalse)
					return &agentrewire.AgentredSelfUpdateResponse{Accepted: true, TargetVersion: "0.6.0"}, nil
				})
			lease.EXPECT().Release()

			got, err := svc.Upgrade(context.Background(), 42, "", false)
			So(err, ShouldBeNil)
			So(got.Accepted, ShouldBeTrue)
			So(got.RejectReason, ShouldEqual, remote_device_svc.UpgradeRejectNone)
			So(got.TargetVersion, ShouldEqual, "0.6.0")
		})

		// daemon 的受理判定把解析发布、下载、校验、替换**全部**跑完才应答(见
		// handlers.SelfUpdateHandlers.Update 的顺序注释),所以这一次调用必须按
		// 「换一个几十 MB 的二进制」给预算。缺了它,protorpc.Conn 会按
		// DefaultCallTimeout(60 秒)兜底,每一次真能成功的升级都在本端超时 ——
		// 桌面端把它翻成 RemoteDeviceTimeout,而那台机器照样升完重启,界面从此
		// 停在一个假的失败上。
		Convey("the call carries a budget sized for a download, not the 60s fallback", func() {
			lease := svcmock.NewMockLease(ctrl)
			pool.EXPECT().Borrow(gomock.Any(), int64(42)).Return(lease, nil)
			var budget time.Duration
			lease.EXPECT().SelfUpdate(gomock.Any(), gomock.Any()).
				DoAndReturn(func(ctx context.Context, _ *agentrewire.AgentredSelfUpdateRequest) (*agentrewire.AgentredSelfUpdateResponse, error) {
					deadline, ok := ctx.Deadline()
					So(ok, ShouldBeTrue)
					budget = time.Until(deadline)
					return &agentrewire.AgentredSelfUpdateResponse{Accepted: true}, nil
				})
			lease.EXPECT().Release()

			_, err := svc.Upgrade(context.Background(), 42, "", false)
			So(err, ShouldBeNil)
			So(budget, ShouldBeGreaterThan, protorpc.DefaultCallTimeout)
			So(budget.Round(time.Second), ShouldEqual, remote_device_svc.UpgradeCallTimeout)
		})

		Convey("force flag is passed through to the wire request unchanged", func() {
			lease := svcmock.NewMockLease(ctrl)
			pool.EXPECT().Borrow(gomock.Any(), int64(42)).Return(lease, nil)
			lease.EXPECT().SelfUpdate(gomock.Any(), gomock.Any()).
				DoAndReturn(func(_ context.Context, req *agentrewire.AgentredSelfUpdateRequest) (*agentrewire.AgentredSelfUpdateResponse, error) {
					So(req.Force, ShouldBeTrue)
					return &agentrewire.AgentredSelfUpdateResponse{Accepted: true}, nil
				})
			lease.EXPECT().Release()

			_, err := svc.Upgrade(context.Background(), 42, "", true)
			So(err, ShouldBeNil)
		})

		Convey("active-turns rejection surfaces the daemon's own wording and count", func() {
			lease := svcmock.NewMockLease(ctrl)
			pool.EXPECT().Borrow(gomock.Any(), int64(42)).Return(lease, nil)
			lease.EXPECT().SelfUpdate(gomock.Any(), gomock.Any()).Return(&agentrewire.AgentredSelfUpdateResponse{
				RejectReason: agentrewire.AgentredSelfUpdateRejectReason_AGENTRED_SELF_UPDATE_REJECT_REASON_ACTIVE_TURNS,
				Message:      "this machine has 2 running conversation(s); upgrading would interrupt them",
				ActiveTurns:  2,
			}, nil)
			lease.EXPECT().Release()

			got, err := svc.Upgrade(context.Background(), 42, "", false)
			So(err, ShouldBeNil)
			So(got.Accepted, ShouldBeFalse)
			So(got.RejectReason, ShouldEqual, remote_device_svc.UpgradeRejectActiveTurns)
			So(got.Message, ShouldEqual, "this machine has 2 running conversation(s); upgrading would interrupt them")
			So(got.ActiveTurns, ShouldEqual, int32(2))
		})

		Convey("other reject reasons map one-to-one", func() {
			cases := []struct {
				wire agentrewire.AgentredSelfUpdateRejectReason
				want remote_device_svc.UpgradeRejectReason
			}{
				{agentrewire.AgentredSelfUpdateRejectReason_AGENTRED_SELF_UPDATE_REJECT_REASON_IN_PROGRESS, remote_device_svc.UpgradeRejectInProgress},
				{agentrewire.AgentredSelfUpdateRejectReason_AGENTRED_SELF_UPDATE_REJECT_REASON_NOT_WRITABLE, remote_device_svc.UpgradeRejectNotWritable},
				{agentrewire.AgentredSelfUpdateRejectReason_AGENTRED_SELF_UPDATE_REJECT_REASON_ALREADY_LATEST, remote_device_svc.UpgradeRejectAlreadyLatest},
				{agentrewire.AgentredSelfUpdateRejectReason_AGENTRED_SELF_UPDATE_REJECT_REASON_DOWNLOAD_FAILED, remote_device_svc.UpgradeRejectDownloadFailed},
			}
			for _, c := range cases {
				lease := svcmock.NewMockLease(ctrl)
				pool.EXPECT().Borrow(gomock.Any(), int64(42)).Return(lease, nil)
				lease.EXPECT().SelfUpdate(gomock.Any(), gomock.Any()).Return(&agentrewire.AgentredSelfUpdateResponse{
					RejectReason: c.wire,
				}, nil)
				lease.EXPECT().Release()

				got, err := svc.Upgrade(context.Background(), 42, "", false)
				So(err, ShouldBeNil)
				So(got.RejectReason, ShouldEqual, c.want)
			}
		})

		Convey("borrow failure is mapped, not passed through raw", func() {
			pool.EXPECT().Borrow(gomock.Any(), int64(42)).Return(nil, remote_device_svc.ErrDeviceNotFound)

			_, err := svc.Upgrade(context.Background(), 42, "", false)
			So(err, ShouldNotBeNil)
			So(errors.Is(err, remote_device_svc.ErrDeviceNotFound), ShouldBeFalse)
		})
	})
}
