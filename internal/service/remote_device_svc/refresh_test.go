package remote_device_svc_test

import (
	"context"
	"errors"
	"testing"

	. "github.com/smartystreets/goconvey/convey"
	"go.uber.org/mock/gomock"

	"github.com/agentre-hub/agentre/internal/model/entity/paired_agentred_entity"
	"github.com/agentre-hub/agentre/internal/service/remote_device_svc"
)

func storedRow() *paired_agentred_entity.PairedAgentred {
	return &paired_agentred_entity.PairedAgentred{
		ID: 1, Name: "x", URL: "ws://h/rpc", DaemonFingerprint: "sha256:abc",
		InstanceUUID: "u", TLSMode: "default", LastSeenAt: 0, Status: 1,
	}
}

func TestRefresh(t *testing.T) {
	Convey("missing row returns RemoteDeviceNotFound", t, func() {
		repo, _, _, _, svc := setupSvc(t)
		repo.EXPECT().Get(gomock.Any(), int64(1)).Return(nil, nil)
		_, err := svc.Refresh(context.Background(), 1)
		So(err, ShouldNotBeNil)
	})
	Convey("missing token: marks unauthorized + does not bump last_seen", t, func() {
		repo, _, kc, _, svc := setupSvc(t)
		repo.EXPECT().Get(gomock.Any(), int64(1)).Return(storedRow(), nil)
		kc.EXPECT().Get("agentre-daemon-token-1").Return("", errors.New("not found"))
		repo.EXPECT().UpdateLastSeen(gomock.Any(), int64(1), int64(0), "unauthorized").Return(nil)
		got, err := svc.Refresh(context.Background(), 1)
		So(err, ShouldBeNil)
		So(got.Online, ShouldBeFalse)
		So(got.LastError, ShouldEqual, "unauthorized")
	})
	Convey("happy path bumps last_seen and clears last_error", t, func() {
		repo, dial, kc, _, svc := setupSvc(t)
		repo.EXPECT().Get(gomock.Any(), int64(1)).Return(storedRow(), nil)
		kc.EXPECT().Get("agentre-daemon-token-1").Return("tok", nil)
		kc.EXPECT().Get("agentre-device-fingerprint").Return("fp", nil)
		dial.EXPECT().Connect(gomock.Any(), gomock.Any()).Return(remote_device_svc.ConnectResult{InstanceUUID: "u"}, nil)
		var savedTs int64
		repo.EXPECT().UpdateLastSeen(gomock.Any(), int64(1), gomock.Any(), "").DoAndReturn(
			func(_ context.Context, _, ts int64, _ string) error { savedTs = ts; return nil })
		got, err := svc.Refresh(context.Background(), 1)
		So(err, ShouldBeNil)
		So(got.LastError, ShouldEqual, "")
		So(savedTs, ShouldBeGreaterThan, int64(0))
	})
	Convey("dial failure: stores dial_failed, returns nil (UI shows offline)", t, func() {
		repo, dial, kc, _, svc := setupSvc(t)
		repo.EXPECT().Get(gomock.Any(), int64(1)).Return(storedRow(), nil)
		kc.EXPECT().Get("agentre-daemon-token-1").Return("tok", nil)
		kc.EXPECT().Get("agentre-device-fingerprint").Return("fp", nil)
		dial.EXPECT().Connect(gomock.Any(), gomock.Any()).Return(remote_device_svc.ConnectResult{}, errors.New("dial failed: ECONNREFUSED"))
		repo.EXPECT().UpdateLastSeen(gomock.Any(), int64(1), int64(0), gomock.Any()).DoAndReturn(
			func(_ context.Context, _, _ int64, le string) error {
				So(le, ShouldStartWith, "dial_failed:")
				return nil
			})
		got, err := svc.Refresh(context.Background(), 1)
		So(err, ShouldBeNil)
		So(got.Online, ShouldBeFalse)
	})
	Convey("TOFU mismatch: stores tofu_mismatch, returns nil", t, func() {
		repo, dial, kc, _, svc := setupSvc(t)
		repo.EXPECT().Get(gomock.Any(), int64(1)).Return(storedRow(), nil)
		kc.EXPECT().Get("agentre-daemon-token-1").Return("tok", nil)
		kc.EXPECT().Get("agentre-device-fingerprint").Return("fp", nil)
		dial.EXPECT().Connect(gomock.Any(), gomock.Any()).Return(
			remote_device_svc.ConnectResult{}, remote_device_svc.ErrTOFUMismatch)
		repo.EXPECT().UpdateLastSeen(gomock.Any(), int64(1), int64(0), "tofu_mismatch").Return(nil)
		got, err := svc.Refresh(context.Background(), 1)
		So(err, ShouldBeNil)
		So(got.LastError, ShouldEqual, "tofu_mismatch")
	})
}
