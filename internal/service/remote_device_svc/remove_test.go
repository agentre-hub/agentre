// internal/service/remote_device_svc/remove_test.go
package remote_device_svc_test

import (
	"context"
	"errors"
	"testing"

	. "github.com/smartystreets/goconvey/convey"
	"go.uber.org/mock/gomock"

	"github.com/agentre-hub/agentre/internal/model/entity/paired_agentred_entity"
)

func TestRemove(t *testing.T) {
	manualRow := &paired_agentred_entity.PairedAgentred{ID: 1, Origin: "manual", URL: "ws://manual:7456/rpc"}

	Convey("repo.Delete failure surfaces", t, func() {
		repo, _, _, _, svc := setupSvc(t)
		repo.EXPECT().Get(gomock.Any(), int64(1)).Return(manualRow, nil).AnyTimes()
		repo.EXPECT().Delete(gomock.Any(), int64(1)).Return(errors.New("db"))
		err := svc.Remove(context.Background(), 1)
		So(err, ShouldNotBeNil)
	})
	Convey("happy path deletes row + token", t, func() {
		repo, _, kc, w, svc := setupSvc(t)
		repo.EXPECT().Get(gomock.Any(), int64(1)).Return(manualRow, nil).AnyTimes()
		repo.EXPECT().Delete(gomock.Any(), int64(1)).Return(nil)
		kc.EXPECT().Delete("agentre-daemon-token-1").Return(nil)
		w.EXPECT().Stop(int64(1))
		err := svc.Remove(context.Background(), 1)
		So(err, ShouldBeNil)
	})
	Convey("keychain delete failure does not error (logged)", t, func() {
		repo, _, kc, w, svc := setupSvc(t)
		repo.EXPECT().Get(gomock.Any(), int64(1)).Return(manualRow, nil).AnyTimes()
		repo.EXPECT().Delete(gomock.Any(), int64(1)).Return(nil)
		kc.EXPECT().Delete("agentre-daemon-token-1").Return(errors.New("kc down"))
		w.EXPECT().Stop(int64(1))
		err := svc.Remove(context.Background(), 1)
		So(err, ShouldBeNil)
	})

	// D15：用户在设备面板移除一台「来自账号的直连」设备时，本机的地址、证书与本地直连凭据
	// 一并删除；软删行（墓碑，按指纹挡住再次收编）照留，但不再带着地址列表与证书。
	Convey("account-direct row: its addresses and certificate are cleared before the tombstone is left", t, func() {
		repo, _, kc, w, svc := setupSvc(t)
		repo.EXPECT().Get(gomock.Any(), int64(3)).Return(&paired_agentred_entity.PairedAgentred{
			ID: 3, Origin: "account", URL: "wss://192.168.1.10:7456/rpc", DaemonFingerprint: "sha256:abc",
			TLSMode: "pin-cert", TLSCertPEM: "-----BEGIN CERTIFICATE-----",
			DirectURLsJSON: `["wss://192.168.1.10:7456/rpc","wss://10.0.0.2:7456/rpc"]`,
		}, nil).AnyTimes()
		gomock.InOrder(
			repo.EXPECT().ClearAccountDirect(gomock.Any(), int64(3)).Return(nil),
			repo.EXPECT().Delete(gomock.Any(), int64(3)).Return(nil),
		)
		kc.EXPECT().Delete("agentre-daemon-token-3").Return(nil)
		w.EXPECT().Stop(int64(3))

		err := svc.Remove(context.Background(), 3)
		So(err, ShouldBeNil)
	})

	Convey("account-direct row: a failure to clear its endpoint surfaces and leaves no tombstone behind", t, func() {
		repo, _, _, _, svc := setupSvc(t)
		repo.EXPECT().Get(gomock.Any(), int64(3)).Return(&paired_agentred_entity.PairedAgentred{
			ID: 3, Origin: "account", URL: "wss://192.168.1.10:7456/rpc", DaemonFingerprint: "sha256:abc",
		}, nil).AnyTimes()
		repo.EXPECT().ClearAccountDirect(gomock.Any(), int64(3)).Return(errors.New("db"))
		// 没有 Delete EXPECT：清不掉就不留一块带着地址与证书的墓碑。

		err := svc.Remove(context.Background(), 3)
		So(err, ShouldNotBeNil)
	})

	Convey("reading the row fails: the failure surfaces and nothing is deleted", t, func() {
		repo, _, _, _, svc := setupSvc(t)
		repo.EXPECT().Get(gomock.Any(), int64(3)).Return(nil, errors.New("db"))

		err := svc.Remove(context.Background(), 3)
		So(err, ShouldNotBeNil)
	})
}
