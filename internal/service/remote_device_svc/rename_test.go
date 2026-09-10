package remote_device_svc_test

import (
	"context"
	"testing"

	. "github.com/smartystreets/goconvey/convey"
	"go.uber.org/mock/gomock"

	"github.com/agentre-hub/agentre/internal/model/entity/paired_agentred_entity"
)

func TestRename(t *testing.T) {
	Convey("empty name returns InvalidParameter without hitting repo", t, func() {
		_, _, _, _, svc := setupSvc(t)
		err := svc.Rename(context.Background(), 1, "   ")
		So(err, ShouldNotBeNil)
	})
	Convey("missing row returns RemoteDeviceNotFound", t, func() {
		repo, _, _, _, svc := setupSvc(t)
		repo.EXPECT().Get(gomock.Any(), int64(9)).Return(nil, nil)
		err := svc.Rename(context.Background(), 9, "x")
		So(err, ShouldNotBeNil)
	})
	Convey("happy path calls repo.Rename with trimmed name", t, func() {
		repo, _, _, _, svc := setupSvc(t)
		repo.EXPECT().Get(gomock.Any(), int64(1)).Return(&paired_agentred_entity.PairedAgentred{ID: 1}, nil)
		repo.EXPECT().Rename(gomock.Any(), int64(1), "new").Return(nil)
		err := svc.Rename(context.Background(), 1, "  new  ")
		So(err, ShouldBeNil)
	})
}
