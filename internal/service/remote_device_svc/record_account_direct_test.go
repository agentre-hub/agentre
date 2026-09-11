package remote_device_svc_test

import (
	"context"
	"errors"
	"testing"

	. "github.com/smartystreets/goconvey/convey"
	"go.uber.org/mock/gomock"

	"github.com/agentre-hub/agentre/internal/model/entity/paired_agentred_entity"
	"github.com/agentre-hub/agentre/internal/service/remote_device_svc"
	"github.com/agentre-hub/agentre/pkg/wire/devicefp"
)

func validDelivery() remote_device_svc.AccountDirectDelivery {
	return remote_device_svc.AccountDirectDelivery{ //nolint:gosec // G101: credential-shaped test fixture, not a real secret.
		DaemonFingerprint: "sha256:abc",
		URLs:              []string{"wss://a:7456/rpc", "wss://b:7456/rpc"},
		CertPEM:           "PEM",
		Credential:        "opaque-cred",
	}
}

// D6：从没见过的一台机器,账号下发直连内容,新建一行「来自账号的直连」——地址位是
// 下发列表的第一个（没直连成功过时的默认），全部地址落进 DirectURLsJSON，
// TLS 是 pin-cert，凭据进 keychain,来源标记为 account。
func TestRecordAccountDirect_NewRow(t *testing.T) {
	Convey("delivers to a fingerprint this desktop has never seen: creates an account-direct row", t, func() {
		repo, _, kc, w, svc := setupSvc(t)
		repo.EXPECT().ListDeleted(gomock.Any()).Return(nil, nil)
		repo.EXPECT().FindByFingerprint(gomock.Any(), devicefp.Carrier("sha256:abc")).Return(nil, nil)
		var created *paired_agentred_entity.PairedAgentred
		repo.EXPECT().Create(gomock.Any(), gomock.Any()).DoAndReturn(
			func(_ context.Context, p *paired_agentred_entity.PairedAgentred) error {
				created = p
				p.ID = 11
				return nil
			})
		kc.EXPECT().Set("agentre-daemon-token-11", "opaque-cred").Return(nil)
		w.EXPECT().Start(gomock.Any(), int64(11)).Return(nil)

		err := svc.RecordAccountDirect(context.Background(), validDelivery())

		So(err, ShouldBeNil)
		So(created, ShouldNotBeNil)
		So(created.URL, ShouldEqual, "wss://a:7456/rpc")
		So(created.DirectURLs(), ShouldResemble, []string{"wss://a:7456/rpc", "wss://b:7456/rpc"})
		So(created.TLSMode, ShouldEqual, "pin-cert")
		So(created.TLSCertPEM, ShouldEqual, "PEM")
		So(created.IsAccountDirect(), ShouldBeTrue)
		So(created.DaemonFingerprint, ShouldEqual, devicefp.Carrier("sha256:abc"))
	})
}

// D6 再下发：已有一行账号直连（比如证书轮换、地址变化，见 D16），新内容整体覆盖
// 旧的地址列表/地址位/证书，行不重建。
func TestRecordAccountDirect_RedeliveryUpdatesExistingRow(t *testing.T) {
	Convey("a second delivery to the same fingerprint updates the existing row in place", t, func() {
		repo, _, kc, w, svc := setupSvc(t)
		repo.EXPECT().ListDeleted(gomock.Any()).Return(nil, nil)
		existing := &paired_agentred_entity.PairedAgentred{
			ID: 11, Name: "devbox", DaemonFingerprint: "sha256:abc",
			URL: "wss://old:7456/rpc", TLSMode: "pin-cert", TLSCertPEM: "OLD-PEM",
			Origin: "account", Status: 1,
		}
		existing.SetDirectURLs([]string{"wss://old:7456/rpc"})
		repo.EXPECT().FindByFingerprint(gomock.Any(), devicefp.Carrier("sha256:abc")).Return(existing, nil)
		repo.EXPECT().UpsertAccountDirect(gomock.Any(), int64(11),
			"wss://a:7456/rpc", `["wss://a:7456/rpc","wss://b:7456/rpc"]`, "PEM").Return(nil)
		kc.EXPECT().Set("agentre-daemon-token-11", "opaque-cred").Return(nil)
		w.EXPECT().Restart(gomock.Any(), int64(11)).Return(nil)
		// 没有 Create 的 EXPECT：再下发不建新行。

		err := svc.RecordAccountDirect(context.Background(), validDelivery())
		So(err, ShouldBeNil)
	})
}

// D6「地址位显示最近一次直连成功的那个地址」：这一行上次经直连连上的是第二个地址
// （RecordDirectSuccess 把地址位写成了它）。之后中转赢下竞速、账号握手再下发同一批
// 地址时，地址位不能被打回列表的第一个——它仍在下发列表里，就仍是最近一次成功的地址。
func TestRecordAccountDirect_RedeliveryKeepsLastDirectSuccessAddress(t *testing.T) {
	Convey("a re-delivery that still lists the last successful direct address keeps it in the address slot", t, func() {
		repo, _, kc, w, svc := setupSvc(t)
		repo.EXPECT().ListDeleted(gomock.Any()).Return(nil, nil)
		existing := &paired_agentred_entity.PairedAgentred{
			ID: 11, Name: "devbox", DaemonFingerprint: "sha256:abc",
			URL: "wss://b:7456/rpc", TLSMode: "pin-cert", TLSCertPEM: "PEM",
			Origin: "account", Status: 1,
		}
		existing.SetDirectURLs([]string{"wss://a:7456/rpc", "wss://b:7456/rpc"})
		repo.EXPECT().FindByFingerprint(gomock.Any(), devicefp.Carrier("sha256:abc")).Return(existing, nil)
		repo.EXPECT().UpsertAccountDirect(gomock.Any(), int64(11),
			"wss://b:7456/rpc", `["wss://a:7456/rpc","wss://b:7456/rpc"]`, "PEM").Return(nil)
		kc.EXPECT().Set("agentre-daemon-token-11", "opaque-cred").Return(nil)
		w.EXPECT().Restart(gomock.Any(), int64(11)).Return(nil)

		err := svc.RecordAccountDirect(context.Background(), validDelivery())
		So(err, ShouldBeNil)
	})
}

// D6：账号收编来的纯中转行（IsRelayOnly，没有 LAN 地址）第一次收到直连下发时同样是
// 「升级」这一行，而不是新建——否则同一台机器会在面板上变成两台。
func TestRecordAccountDirect_UpgradesRelayOnlyRow(t *testing.T) {
	Convey("a relay-only adopted row is upgraded in place, not duplicated", t, func() {
		repo, _, kc, w, svc := setupSvc(t)
		repo.EXPECT().ListDeleted(gomock.Any()).Return(nil, nil)
		adopted := &paired_agentred_entity.PairedAgentred{
			ID: 11, Name: "devbox", DaemonFingerprint: "sha256:abc", TLSMode: "default", Status: 1,
		}
		repo.EXPECT().FindByFingerprint(gomock.Any(), devicefp.Carrier("sha256:abc")).Return(adopted, nil)
		repo.EXPECT().UpsertAccountDirect(gomock.Any(), int64(11),
			"wss://a:7456/rpc", `["wss://a:7456/rpc","wss://b:7456/rpc"]`, "PEM").Return(nil)
		kc.EXPECT().Set("agentre-daemon-token-11", "opaque-cred").Return(nil)
		w.EXPECT().Restart(gomock.Any(), int64(11)).Return(nil)

		err := svc.RecordAccountDirect(context.Background(), validDelivery())
		So(err, ShouldBeNil)
	})
}

// D14 的另一半：这一行已经是本机自己 LAN 配对来的（手动，非 relay-only、非
// account-direct），账号下发的内容不该覆盖它——手动配对永远赢。
func TestRecordAccountDirect_ManuallyPairedRowWins(t *testing.T) {
	Convey("delivery for an already manually-paired fingerprint is a no-op", t, func() {
		repo, _, _, _, svc := setupSvc(t)
		repo.EXPECT().ListDeleted(gomock.Any()).Return(nil, nil)
		manual := &paired_agentred_entity.PairedAgentred{
			ID: 9, Name: "devbox", DaemonFingerprint: "sha256:abc",
			URL: "ws://192.168.1.9:7456/rpc", TLSMode: "default", Status: 1,
		}
		repo.EXPECT().FindByFingerprint(gomock.Any(), devicefp.Carrier("sha256:abc")).Return(manual, nil)
		// 没有 UpsertAccountDirect / Create / keychain.Set / watcher 的 EXPECT。

		err := svc.RecordAccountDirect(context.Background(), validDelivery())
		So(err, ShouldBeNil)
	})
}

// D15：用户在本机移除过这台机器（软删行留在 ListDeleted 里），后续账号下发不得
// 把它悄悄收回来——同 AdoptAccountDevices 的墓碑判据。
func TestRecordAccountDirect_TombstonedFingerprintIsRefused(t *testing.T) {
	Convey("a fingerprint the user removed does not get re-recorded", t, func() {
		repo, _, _, _, svc := setupSvc(t)
		repo.EXPECT().ListDeleted(gomock.Any()).Return([]*paired_agentred_entity.PairedAgentred{
			{ID: 3, DaemonFingerprint: "sha256:abc", Status: 2},
		}, nil)
		// 没有 FindByFingerprint / Create / UpsertAccountDirect 的 EXPECT。

		err := svc.RecordAccountDirect(context.Background(), validDelivery())
		So(err, ShouldBeNil)
	})
}

// 钥匙串写入失败：新建的行必须回滚，不留一行没有凭据、地址却已经写好的半成品
// （与 add.go 的 Add() 同一规则）。
func TestRecordAccountDirect_KeychainFailureRollsBackNewRow(t *testing.T) {
	Convey("keychain.Set failure after Create rolls back the row", t, func() {
		repo, _, kc, _, svc := setupSvc(t)
		repo.EXPECT().ListDeleted(gomock.Any()).Return(nil, nil)
		repo.EXPECT().FindByFingerprint(gomock.Any(), gomock.Any()).Return(nil, nil)
		repo.EXPECT().Create(gomock.Any(), gomock.Any()).DoAndReturn(
			func(_ context.Context, p *paired_agentred_entity.PairedAgentred) error { p.ID = 11; return nil })
		kc.EXPECT().Set("agentre-daemon-token-11", "opaque-cred").Return(errors.New("kc down"))
		repo.EXPECT().Delete(gomock.Any(), int64(11)).Return(nil)

		err := svc.RecordAccountDirect(context.Background(), validDelivery())
		So(err, ShouldNotBeNil)
	})
}

// 钥匙串写入失败但这一行本来就存在（再下发/升级场景）：不回滚——地址已经可用，
// 缺的只是凭据（与 add.go 的 upgradeRelayOnly 同一规则）。
func TestRecordAccountDirect_KeychainFailureOnExistingRowDoesNotRollBack(t *testing.T) {
	Convey("keychain.Set failure on an existing row leaves it in place", t, func() {
		repo, _, kc, w, svc := setupSvc(t)
		repo.EXPECT().ListDeleted(gomock.Any()).Return(nil, nil)
		adopted := &paired_agentred_entity.PairedAgentred{
			ID: 11, Name: "devbox", DaemonFingerprint: "sha256:abc", TLSMode: "default", Status: 1,
		}
		repo.EXPECT().FindByFingerprint(gomock.Any(), devicefp.Carrier("sha256:abc")).Return(adopted, nil)
		repo.EXPECT().UpsertAccountDirect(gomock.Any(), int64(11), gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
		kc.EXPECT().Set("agentre-daemon-token-11", "opaque-cred").Return(errors.New("kc down"))
		_ = w

		err := svc.RecordAccountDirect(context.Background(), validDelivery())
		So(err, ShouldNotBeNil)
	})
}

func TestRecordAccountDirect_RejectsIncompleteDelivery(t *testing.T) {
	Convey("empty fingerprint is rejected", t, func() {
		_, _, _, _, svc := setupSvc(t)
		d := validDelivery()
		d.DaemonFingerprint = ""
		err := svc.RecordAccountDirect(context.Background(), d)
		So(err, ShouldNotBeNil)
	})
	Convey("no urls is rejected", t, func() {
		_, _, _, _, svc := setupSvc(t)
		d := validDelivery()
		d.URLs = nil
		err := svc.RecordAccountDirect(context.Background(), d)
		So(err, ShouldNotBeNil)
	})
	Convey("empty cert is rejected", t, func() {
		_, _, _, _, svc := setupSvc(t)
		d := validDelivery()
		d.CertPEM = ""
		err := svc.RecordAccountDirect(context.Background(), d)
		So(err, ShouldNotBeNil)
	})
}

// D6：设备面板地址位显示最近一次直连成功的地址。
func TestRecordDirectSuccess(t *testing.T) {
	Convey("updates the address slot when the address is in the row's delivered list", t, func() {
		repo, _, _, _, svc := setupSvc(t)
		row := &paired_agentred_entity.PairedAgentred{ID: 11, Origin: "account", URL: "wss://a:7456/rpc"}
		row.SetDirectURLs([]string{"wss://a:7456/rpc", "wss://b:7456/rpc"})
		repo.EXPECT().Get(gomock.Any(), int64(11)).Return(row, nil)
		repo.EXPECT().UpdateDirectAddress(gomock.Any(), int64(11), "wss://b:7456/rpc").Return(nil)

		err := svc.RecordDirectSuccess(context.Background(), 11, "wss://b:7456/rpc")
		So(err, ShouldBeNil)
	})
	Convey("no-ops when the row is not account-direct (e.g. manually paired)", t, func() {
		repo, _, _, _, svc := setupSvc(t)
		row := &paired_agentred_entity.PairedAgentred{ID: 11, URL: "ws://h/rpc"}
		repo.EXPECT().Get(gomock.Any(), int64(11)).Return(row, nil)
		// 没有 UpdateDirectAddress 的 EXPECT。

		err := svc.RecordDirectSuccess(context.Background(), 11, "ws://h/rpc")
		So(err, ShouldBeNil)
	})
	Convey("no-ops when the address is not among the row's delivered addresses", t, func() {
		repo, _, _, _, svc := setupSvc(t)
		row := &paired_agentred_entity.PairedAgentred{ID: 11, Origin: "account", URL: "wss://a:7456/rpc"}
		row.SetDirectURLs([]string{"wss://a:7456/rpc"})
		repo.EXPECT().Get(gomock.Any(), int64(11)).Return(row, nil)
		// 没有 UpdateDirectAddress 的 EXPECT：不是这一行下发过的地址,不采信。

		err := svc.RecordDirectSuccess(context.Background(), 11, "wss://intruder:7456/rpc")
		So(err, ShouldBeNil)
	})
	Convey("no-ops when the device row does not exist", t, func() {
		repo, _, _, _, svc := setupSvc(t)
		repo.EXPECT().Get(gomock.Any(), int64(99)).Return(nil, nil)
		err := svc.RecordDirectSuccess(context.Background(), 99, "wss://a:7456/rpc")
		So(err, ShouldBeNil)
	})
}
