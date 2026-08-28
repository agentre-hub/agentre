package remote_device_svc_test

import (
	"context"
	"errors"
	"testing"

	. "github.com/smartystreets/goconvey/convey"
	"go.uber.org/mock/gomock"

	"github.com/agentre-hub/agentre/internal/model/entity/paired_agentred_entity"
	"github.com/agentre-hub/agentre/internal/repository/remote_device_repo/mock_remote_device_repo"
	"github.com/agentre-hub/agentre/internal/service/remote_device_svc"
	"github.com/agentre-hub/agentre/internal/service/remote_device_svc/mock_remote_device_svc"
)

func setupSvc(t *testing.T) (
	*mock_remote_device_repo.MockPairedAgentredRepo,
	*mock_remote_device_svc.MockDaemonDialPort,
	*mock_remote_device_svc.MockKeychainPort,
	*mock_remote_device_svc.MockWatcherPort,
	remote_device_svc.RemoteDeviceSvc,
) {
	t.Helper()
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)
	repo := mock_remote_device_repo.NewMockPairedAgentredRepo(ctrl)
	dial := mock_remote_device_svc.NewMockDaemonDialPort(ctrl)
	kc := mock_remote_device_svc.NewMockKeychainPort(ctrl)
	w := mock_remote_device_svc.NewMockWatcherPort(ctrl)
	svc := remote_device_svc.New(repo, dial, kc, nil)
	svc.SetWatcher(w)
	return repo, dial, kc, w, svc
}

func TestList(t *testing.T) {
	Convey("List projects repo rows to DeviceView", t, func() {
		repo, _, _, _, svc := setupSvc(t)
		repo.EXPECT().List(gomock.Any()).Return([]*paired_agentred_entity.PairedAgentred{
			{ID: 1, Name: "a", URL: "ws://a/rpc", TLSMode: "default"},
			{ID: 2, Name: "b", URL: "ws://b/rpc", TLSMode: "pin-cert", TLSCertPEM: "X"},
		}, nil)
		got, err := svc.List(context.Background())
		So(err, ShouldBeNil)
		So(got, ShouldHaveLength, 2)
		So(got[0].Name, ShouldEqual, "a")
		So(got[1].TLSCertPEM, ShouldEqual, "X")
	})
	Convey("List propagates repo error", t, func() {
		repo, _, _, _, svc := setupSvc(t)
		repo.EXPECT().List(gomock.Any()).Return(nil, errors.New("db down"))
		_, err := svc.List(context.Background())
		So(err, ShouldNotBeNil)
	})
}
