package remote_device_svc_test

import (
	"context"
	"errors"
	"testing"

	. "github.com/smartystreets/goconvey/convey"
	"go.uber.org/mock/gomock"

	"github.com/agentre-hub/agentre/internal/model/entity/paired_agentred_entity"
	"github.com/agentre-hub/agentre/internal/pkg/keychain"
	"github.com/agentre-hub/agentre/internal/service/remote_device_svc"
)

func validAddReq() remote_device_svc.AddRequest {
	return remote_device_svc.AddRequest{
		URL:         "ws://192.168.1.100:7456/rpc",
		PairingCode: "ABC2DE",
		DisplayName: "linux-srv",
		TLSMode:     "default",
	}
}

func validPairResult() remote_device_svc.PairResult {
	return remote_device_svc.PairResult{
		DeviceToken: "tok-256bit", DaemonFingerprint: "sha256:abc", InstanceUUID: "uuid-1",
	}
}

func TestAdd(t *testing.T) {
	Convey("rejects empty URL", t, func() {
		_, _, _, _, svc := setupSvc(t)
		req := validAddReq()
		req.URL = ""
		_, err := svc.Add(context.Background(), req)
		So(err, ShouldNotBeNil)
	})
	Convey("rejects non-ws URL", t, func() {
		_, _, _, _, svc := setupSvc(t)
		req := validAddReq()
		req.URL = "http://h/rpc"
		_, err := svc.Add(context.Background(), req)
		So(err, ShouldNotBeNil)
	})
	Convey("rejects short pairing code", t, func() {
		_, _, _, _, svc := setupSvc(t)
		req := validAddReq()
		req.PairingCode = "AB1"
		_, err := svc.Add(context.Background(), req)
		So(err, ShouldNotBeNil)
	})
	Convey("rejects already-paired URL", t, func() {
		repo, _, _, _, svc := setupSvc(t)
		repo.EXPECT().FindByURL(gomock.Any(), validAddReq().URL).
			Return(&paired_agentred_entity.PairedAgentred{ID: 99}, nil)
		_, err := svc.Add(context.Background(), validAddReq())
		So(err, ShouldNotBeNil)
	})
	// 收编行（账号来源，只有中转路径）与随后的 LAN 配对指的是**同一台机器**。再建一行
	// 会让它在设备面板上变成两台、在连接池里变成两个 entry、在线状态各说各话——新加的
	// 指纹唯一索引也会直接拒掉这次写入。正确的收场是把那一行**升级**成双路径：补上
	// LAN 地址与 TLS，设备令牌落到它自己的 keychain 账号下。
	Convey("pairing a machine that was already adopted upgrades that row instead of adding a second", t, func() {
		repo, dial, kc, w, svc := setupSvc(t)
		adopted := &paired_agentred_entity.PairedAgentred{
			ID: 7, Name: "coding", DaemonFingerprint: "sha256:abc", TLSMode: "default", Status: 1,
		}
		repo.EXPECT().FindByURL(gomock.Any(), validAddReq().URL).Return(nil, nil)
		kc.EXPECT().Get("agentre-device-fingerprint").Return("existing-fp", nil)
		dial.EXPECT().Pair(gomock.Any(), gomock.Any()).Return(validPairResult(), nil)
		repo.EXPECT().FindByFingerprint(gomock.Any(), "sha256:abc").Return(adopted, nil)
		repo.EXPECT().UpdateEndpoint(gomock.Any(), int64(7), validAddReq().URL, "sha256:abc").Return(nil)
		repo.EXPECT().UpdateTLS(gomock.Any(), int64(7), "default", "").Return(nil)
		kc.EXPECT().Set("agentre-daemon-token-7", "tok-256bit").Return(nil)
		// 端点从「只有中转」变成「双路径」，长连状态机必须按新端点重来。
		w.EXPECT().Restart(gomock.Any(), int64(7)).Return(nil)
		// 没有 Create 的 EXPECT：新建任何一行都是失败。

		view, err := svc.Add(context.Background(), validAddReq())
		So(err, ShouldBeNil)
		So(view.ID, ShouldEqual, 7)
		So(view.URL, ShouldEqual, validAddReq().URL)
	})
	// 已经有 LAN 路径的机器再配一次，仍然是「已配对」，不该被上面那条路径悄悄改掉地址。
	Convey("pairing a machine that already has a LAN row is still rejected as already paired", t, func() {
		repo, dial, kc, _, svc := setupSvc(t)
		repo.EXPECT().FindByURL(gomock.Any(), validAddReq().URL).Return(nil, nil)
		kc.EXPECT().Get("agentre-device-fingerprint").Return("existing-fp", nil)
		dial.EXPECT().Pair(gomock.Any(), gomock.Any()).Return(validPairResult(), nil)
		repo.EXPECT().FindByFingerprint(gomock.Any(), "sha256:abc").Return(
			&paired_agentred_entity.PairedAgentred{ID: 9, URL: "ws://other:7456/rpc", DaemonFingerprint: "sha256:abc", Status: 1}, nil)

		_, err := svc.Add(context.Background(), validAddReq())
		So(err, ShouldNotBeNil)
	})
	Convey("first add: generates + persists device fingerprint", t, func() {
		repo, dial, kc, w, svc := setupSvc(t)
		repo.EXPECT().FindByURL(gomock.Any(), validAddReq().URL).Return(nil, nil)
		kc.EXPECT().Get("agentre-device-fingerprint").Return("", keychain.ErrNotFound)
		kc.EXPECT().Set("agentre-device-fingerprint", gomock.Any()).Return(nil)
		dial.EXPECT().Pair(gomock.Any(), gomock.Any()).Return(validPairResult(), nil)
		repo.EXPECT().FindByFingerprint(gomock.Any(), gomock.Any()).Return(nil, nil)
		repo.EXPECT().Create(gomock.Any(), gomock.Any()).DoAndReturn(
			func(_ context.Context, p *paired_agentred_entity.PairedAgentred) error {
				p.ID = 42
				return nil
			})
		kc.EXPECT().Set("agentre-daemon-token-42", "tok-256bit").Return(nil)
		w.EXPECT().Start(gomock.Any(), int64(42)).Return(nil)
		got, err := svc.Add(context.Background(), validAddReq())
		So(err, ShouldBeNil)
		So(got.ID, ShouldEqual, 42)
		So(got.DaemonFingerprint, ShouldEqual, "sha256:abc")
	})
	Convey("reuses existing device fingerprint when present", t, func() {
		repo, dial, kc, w, svc := setupSvc(t)
		repo.EXPECT().FindByURL(gomock.Any(), gomock.Any()).Return(nil, nil)
		kc.EXPECT().Get("agentre-device-fingerprint").Return("existing-fp", nil)
		dial.EXPECT().Pair(gomock.Any(), gomock.Any()).DoAndReturn(
			func(_ context.Context, args remote_device_svc.PairArgs) (remote_device_svc.PairResult, error) {
				So(args.DeviceFingerprint, ShouldEqual, "existing-fp")
				return validPairResult(), nil
			})
		repo.EXPECT().FindByFingerprint(gomock.Any(), gomock.Any()).Return(nil, nil)
		repo.EXPECT().Create(gomock.Any(), gomock.Any()).DoAndReturn(
			func(_ context.Context, p *paired_agentred_entity.PairedAgentred) error { p.ID = 7; return nil })
		kc.EXPECT().Set("agentre-daemon-token-7", "tok-256bit").Return(nil)
		w.EXPECT().Start(gomock.Any(), int64(7)).Return(nil)
		_, err := svc.Add(context.Background(), validAddReq())
		So(err, ShouldBeNil)
	})
	Convey("dial Pair failure surfaces as error; no DB write", t, func() {
		repo, dial, kc, _, svc := setupSvc(t)
		repo.EXPECT().FindByURL(gomock.Any(), gomock.Any()).Return(nil, nil)
		kc.EXPECT().Get("agentre-device-fingerprint").Return("fp", nil)
		dial.EXPECT().Pair(gomock.Any(), gomock.Any()).Return(remote_device_svc.PairResult{}, errors.New("dial failed"))
		_, err := svc.Add(context.Background(), validAddReq())
		So(err, ShouldNotBeNil)
	})
	Convey("keychain Set failure after Create rolls back row", t, func() {
		repo, dial, kc, _, svc := setupSvc(t)
		repo.EXPECT().FindByURL(gomock.Any(), gomock.Any()).Return(nil, nil)
		kc.EXPECT().Get("agentre-device-fingerprint").Return("fp", nil)
		dial.EXPECT().Pair(gomock.Any(), gomock.Any()).Return(validPairResult(), nil)
		repo.EXPECT().FindByFingerprint(gomock.Any(), gomock.Any()).Return(nil, nil)
		repo.EXPECT().Create(gomock.Any(), gomock.Any()).DoAndReturn(
			func(_ context.Context, p *paired_agentred_entity.PairedAgentred) error { p.ID = 5; return nil })
		kc.EXPECT().Set("agentre-daemon-token-5", "tok-256bit").Return(errors.New("kc down"))
		repo.EXPECT().Delete(gomock.Any(), int64(5)).Return(nil) // rollback
		_, err := svc.Add(context.Background(), validAddReq())
		So(err, ShouldNotBeNil)
	})
	Convey("DisplayName empty: derives from URL hostname", t, func() {
		repo, dial, kc, w, svc := setupSvc(t)
		repo.EXPECT().FindByURL(gomock.Any(), gomock.Any()).Return(nil, nil)
		repo.EXPECT().List(gomock.Any()).Return(nil, nil).AnyTimes()
		kc.EXPECT().Get("agentre-device-fingerprint").Return("fp", nil)
		dial.EXPECT().Pair(gomock.Any(), gomock.Any()).Return(validPairResult(), nil)
		repo.EXPECT().FindByFingerprint(gomock.Any(), gomock.Any()).Return(nil, nil)
		repo.EXPECT().Create(gomock.Any(), gomock.Any()).DoAndReturn(
			func(_ context.Context, p *paired_agentred_entity.PairedAgentred) error {
				So(p.Name, ShouldEqual, "linux-srv")
				p.ID = 1
				return nil
			})
		kc.EXPECT().Set("agentre-daemon-token-1", gomock.Any()).Return(nil)
		w.EXPECT().Start(gomock.Any(), int64(1)).Return(nil)
		req := validAddReq()
		req.URL = "ws://linux-srv.local:7456/rpc"
		req.DisplayName = ""
		_, err := svc.Add(context.Background(), req)
		So(err, ShouldBeNil)
	})
	Convey("DisplayName empty + IP host: derives `agentred-N`", t, func() {
		repo, dial, kc, w, svc := setupSvc(t)
		repo.EXPECT().FindByURL(gomock.Any(), gomock.Any()).Return(nil, nil)
		repo.EXPECT().List(gomock.Any()).Return([]*paired_agentred_entity.PairedAgentred{
			{Name: "agentred-1"},
		}, nil)
		kc.EXPECT().Get("agentre-device-fingerprint").Return("fp", nil)
		dial.EXPECT().Pair(gomock.Any(), gomock.Any()).Return(validPairResult(), nil)
		repo.EXPECT().FindByFingerprint(gomock.Any(), gomock.Any()).Return(nil, nil)
		repo.EXPECT().Create(gomock.Any(), gomock.Any()).DoAndReturn(
			func(_ context.Context, p *paired_agentred_entity.PairedAgentred) error {
				So(p.Name, ShouldEqual, "agentred-2")
				p.ID = 2
				return nil
			})
		kc.EXPECT().Set("agentre-daemon-token-2", gomock.Any()).Return(nil)
		w.EXPECT().Start(gomock.Any(), int64(2)).Return(nil)
		req := validAddReq()
		req.URL = "ws://192.168.1.100:7456/rpc"
		req.DisplayName = ""
		_, err := svc.Add(context.Background(), req)
		So(err, ShouldBeNil)
	})
}
