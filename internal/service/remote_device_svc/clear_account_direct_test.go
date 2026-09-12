package remote_device_svc_test

import (
	"context"
	"errors"
	"testing"

	. "github.com/smartystreets/goconvey/convey"
	"go.uber.org/mock/gomock"

	"github.com/agentre-hub/agentre/internal/model/entity/paired_agentred_entity"
)

// D10：桌面端登出时,「来自账号的直连」行的地址/证书/钥匙串凭据清掉,行本身留着,
// 此后按今天的收编行（IsRelayOnly）处理；手动配对行与纯中转收编行都不受影响。
func TestClearAccountDirect(t *testing.T) {
	Convey("clears account-direct rows back to relay-only shape and deletes their keychain credential", t, func() {
		repo, _, kc, w, svc := setupSvc(t)
		repo.EXPECT().List(gomock.Any()).Return([]*paired_agentred_entity.PairedAgentred{
			{ID: 1, Origin: "account", URL: "wss://a:7456/rpc"},
			{ID: 2, Origin: "manual", URL: "ws://manual/rpc"}, // 手动配对行：不动
			{ID: 3, URL: ""}, // 纯中转收编行：不是账号直连行，不动
		}, nil)
		repo.EXPECT().ClearAccountDirect(gomock.Any(), int64(1)).Return(nil)
		kc.EXPECT().Delete("agentre-daemon-token-1").Return(nil)
		w.EXPECT().Restart(gomock.Any(), int64(1)).Return(nil)
		// 没有针对 id 2 / 3 的 ClearAccountDirect / keychain.Delete EXPECT。

		n, err := svc.ClearAccountDirect(context.Background())
		So(err, ShouldBeNil)
		So(n, ShouldEqual, 1)
	})

	Convey("no account-direct rows: no-op, zero cleared", t, func() {
		repo, _, _, _, svc := setupSvc(t)
		repo.EXPECT().List(gomock.Any()).Return([]*paired_agentred_entity.PairedAgentred{
			{ID: 2, Origin: "manual", URL: "ws://manual/rpc"},
		}, nil)
		n, err := svc.ClearAccountDirect(context.Background())
		So(err, ShouldBeNil)
		So(n, ShouldEqual, 0)
	})

	Convey("keychain delete failure does not error (logged, matches Remove's precedent)", t, func() {
		repo, _, kc, w, svc := setupSvc(t)
		repo.EXPECT().List(gomock.Any()).Return([]*paired_agentred_entity.PairedAgentred{
			{ID: 1, Origin: "account", URL: "wss://a:7456/rpc"},
		}, nil)
		repo.EXPECT().ClearAccountDirect(gomock.Any(), int64(1)).Return(nil)
		kc.EXPECT().Delete("agentre-daemon-token-1").Return(errors.New("kc down"))
		w.EXPECT().Restart(gomock.Any(), int64(1)).Return(nil)

		n, err := svc.ClearAccountDirect(context.Background())
		So(err, ShouldBeNil)
		So(n, ShouldEqual, 1)
	})

	Convey("repo.List failure surfaces", t, func() {
		repo, _, _, _, svc := setupSvc(t)
		repo.EXPECT().List(gomock.Any()).Return(nil, errors.New("db down"))
		_, err := svc.ClearAccountDirect(context.Background())
		So(err, ShouldNotBeNil)
	})
}
