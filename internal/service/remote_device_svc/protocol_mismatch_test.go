package remote_device_svc_test

import (
	"context"
	"testing"

	. "github.com/smartystreets/goconvey/convey"
	"go.uber.org/mock/gomock"

	"github.com/agentre-hub/agentre/internal/service/remote_device_svc"
)

// `make agentred-deploy` pushes agentred to a machine on its own schedule, so a
// remote box running a different release is routine. The device panel reads
// LastError and nothing else, so a protocol disagreement has to arrive there as
// its own token — folded into "dial_failed:" it reads as a network problem and
// sends the user to check the port.
func TestRefresh_GivenProtocolDisagreement_WhenRefreshing_ThenRecordsItsOwnLastError(t *testing.T) {
	Convey("远端不认识这套协议:落 protocol_unsupported", t, func() {
		repo, dial, kc, _, svc := setupSvc(t)
		repo.EXPECT().Get(gomock.Any(), int64(1)).Return(storedRow(), nil)
		kc.EXPECT().Get("agentre-daemon-token-1").Return("tok", nil)
		kc.EXPECT().Get("agentre-device-fingerprint").Return("fp", nil)
		dial.EXPECT().Connect(gomock.Any(), gomock.Any()).
			Return(remote_device_svc.ConnectResult{}, remote_device_svc.ErrProtocolUnsupported)
		repo.EXPECT().UpdateLastSeen(gomock.Any(), int64(1), int64(0), "protocol_unsupported").Return(nil)

		got, err := svc.Refresh(context.Background(), 1)

		So(err, ShouldBeNil)
		So(got.LastError, ShouldEqual, "protocol_unsupported")
		So(got.Online, ShouldBeFalse)
	})

	Convey("远端说这套协议但版本对不上:落 protocol_mismatch", t, func() {
		repo, dial, kc, _, svc := setupSvc(t)
		repo.EXPECT().Get(gomock.Any(), int64(1)).Return(storedRow(), nil)
		kc.EXPECT().Get("agentre-daemon-token-1").Return("tok", nil)
		kc.EXPECT().Get("agentre-device-fingerprint").Return("fp", nil)
		dial.EXPECT().Connect(gomock.Any(), gomock.Any()).
			Return(remote_device_svc.ConnectResult{}, remote_device_svc.ErrProtocolVersionMismatch)
		repo.EXPECT().UpdateLastSeen(gomock.Any(), int64(1), int64(0), "protocol_mismatch").Return(nil)

		got, err := svc.Refresh(context.Background(), 1)

		So(err, ShouldBeNil)
		So(got.LastError, ShouldEqual, "protocol_mismatch")
	})
}

// Pairing is where a freshly deployed agentred is met for the first time, so the
// version skew shows up there before anywhere else. "could not reach agentred,
// check network and port" is the wrong instruction for it.
func TestAdd_GivenProtocolDisagreement_WhenPairing_ThenReportsTheProtocolCodeNotDialFailed(t *testing.T) {
	Convey("远端不认识这套协议", t, func() {
		repo, dial, kc, _, svc := setupSvc(t)
		repo.EXPECT().FindByURL(gomock.Any(), gomock.Any()).Return(nil, nil)
		kc.EXPECT().Get("agentre-device-fingerprint").Return("fp", nil)
		dial.EXPECT().Pair(gomock.Any(), gomock.Any()).
			Return(remote_device_svc.PairResult{}, remote_device_svc.ErrProtocolUnsupported)

		_, err := svc.Add(context.Background(), validAddReq())

		So(err, ShouldNotBeNil)
		So(err.Error(), ShouldContainSubstring, "agentred")
		So(err.Error(), ShouldNotContainSubstring, "网络与端口")
	})

	Convey("远端版本对不上", t, func() {
		repo, dial, kc, _, svc := setupSvc(t)
		repo.EXPECT().FindByURL(gomock.Any(), gomock.Any()).Return(nil, nil)
		kc.EXPECT().Get("agentre-device-fingerprint").Return("fp", nil)
		dial.EXPECT().Pair(gomock.Any(), gomock.Any()).
			Return(remote_device_svc.PairResult{}, remote_device_svc.ErrProtocolVersionMismatch)

		_, err := svc.Add(context.Background(), validAddReq())

		So(err, ShouldNotBeNil)
		So(err.Error(), ShouldContainSubstring, "agentred")
		So(err.Error(), ShouldNotContainSubstring, "网络与端口")
	})
}
