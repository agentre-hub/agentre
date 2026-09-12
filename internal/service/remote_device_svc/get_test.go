package remote_device_svc_test

import (
	"context"
	"testing"

	. "github.com/smartystreets/goconvey/convey"
	"go.uber.org/mock/gomock"
	"gorm.io/gorm"

	"github.com/agentre-hub/agentre/internal/model/entity/paired_agentred_entity"
)

func TestGet(t *testing.T) {
	Convey("Get", t, func() {
		Convey("happy path: repo returns row → DeviceView", func() {
			repo, _, _, _, svc := setupSvc(t)
			repo.EXPECT().Get(gomock.Any(), int64(1)).Return(storedRow(), nil)
			got, err := svc.Get(context.Background(), 1)
			So(err, ShouldBeNil)
			So(got, ShouldNotBeNil)
			So(got.ID, ShouldEqual, 1)
			So(got.Name, ShouldEqual, "x")
		})
		Convey("not found: repo returns gorm.ErrRecordNotFound → RemoteDeviceNotFound", func() {
			repo, _, _, _, svc := setupSvc(t)
			repo.EXPECT().Get(gomock.Any(), int64(99)).Return(nil, gorm.ErrRecordNotFound)
			_, err := svc.Get(context.Background(), 99)
			So(err, ShouldNotBeNil)
		})
		Convey("not found: repo returns (nil, nil) → RemoteDeviceNotFound", func() {
			repo, _, _, _, svc := setupSvc(t)
			repo.EXPECT().Get(gomock.Any(), int64(99)).Return(nil, nil)
			_, err := svc.Get(context.Background(), 99)
			So(err, ShouldNotBeNil)
		})
		Convey("repo error propagated", func() {
			repo, _, _, _, svc := setupSvc(t)
			repo.EXPECT().Get(gomock.Any(), int64(1)).Return(
				&paired_agentred_entity.PairedAgentred{}, gorm.ErrInvalidTransaction,
			)
			_, err := svc.Get(context.Background(), 1)
			So(err, ShouldNotBeNil)
		})
	})
}
