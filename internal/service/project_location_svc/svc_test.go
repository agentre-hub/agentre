package project_location_svc

import (
	"context"
	"testing"

	"github.com/cago-frame/cago/pkg/consts"
	. "github.com/smartystreets/goconvey/convey"
	"go.uber.org/mock/gomock"
	"gorm.io/gorm"

	"github.com/agentre-hub/agentre/internal/model/entity/project_location_entity"
	"github.com/agentre-hub/agentre/internal/repository/project_location_repo"
	"github.com/agentre-hub/agentre/internal/repository/project_location_repo/mock_project_location_repo"
	"github.com/agentre-hub/agentre/internal/service/remote_device_svc"
	mockRD "github.com/agentre-hub/agentre/internal/service/remote_device_svc/mock_remote_device_svc"
)

func setupSvc(t *testing.T) (context.Context, *mock_project_location_repo.MockProjectLocationRepo, *mockRD.MockRemoteDeviceSvc, *projectLocationImpl) {
	t.Helper()
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)
	repo := mock_project_location_repo.NewMockProjectLocationRepo(ctrl)
	rd := mockRD.NewMockRemoteDeviceSvc(ctrl)
	project_location_repo.RegisterProjectLocation(repo)
	remote_device_svc.SetDefault(rd)
	return context.Background(), repo, rd, &projectLocationImpl{}
}

func TestUpsert(t *testing.T) {
	Convey("Upsert", t, func() {
		Convey("新建：no existing row → repo.Create", func() {
			ctx, repo, rd, svc := setupSvc(t)
			rd.EXPECT().Get(ctx, int64(7)).Return(
				&remote_device_svc.DeviceView{ID: 7, Name: "linux-srv", Online: true, DaemonFingerprint: "fp-7"}, nil,
			).AnyTimes()
			repo.EXPECT().FindByProjectAndFingerprint(ctx, int64(1), "fp-7").Return(nil, gorm.ErrRecordNotFound)
			repo.EXPECT().Create(ctx, gomock.Any()).DoAndReturn(
				func(_ context.Context, p *project_location_entity.ProjectLocation) error {
					p.ID = 42
					return nil
				},
			)
			v, err := svc.Upsert(ctx, 1, "7", "/home/me/foo")
			So(err, ShouldBeNil)
			So(v.ID, ShouldEqual, 42)
			So(v.Path, ShouldEqual, "/home/me/foo")
			So(v.DeviceName, ShouldEqual, "linux-srv")
			So(v.Online, ShouldBeTrue)
		})
		Convey("更新：existing row（同一指纹，device_id 缓存未过期）→ repo.UpdatePath，不动缓存", func() {
			ctx, repo, rd, svc := setupSvc(t)
			rd.EXPECT().Get(ctx, int64(7)).Return(
				&remote_device_svc.DeviceView{ID: 7, Name: "linux-srv", Online: true, DaemonFingerprint: "fp-7"}, nil,
			).AnyTimes()
			existing := &project_location_entity.ProjectLocation{ID: 42, ProjectID: 1, DeviceID: "7", DeviceFingerprint: "fp-7", Path: "/old", Status: consts.ACTIVE}
			repo.EXPECT().FindByProjectAndFingerprint(ctx, int64(1), "fp-7").Return(existing, nil)
			repo.EXPECT().UpdatePath(ctx, int64(42), "/new").Return(nil)
			v, err := svc.Upsert(ctx, 1, "7", "/new")
			So(err, ShouldBeNil)
			So(v.Path, ShouldEqual, "/new")
		})
		Convey("更新：existing row 的 device_id 缓存过期（重新配对换了本地 id）→ 一并回填缓存", func() {
			ctx, repo, rd, svc := setupSvc(t)
			rd.EXPECT().Get(ctx, int64(9)).Return(
				&remote_device_svc.DeviceView{ID: 9, Name: "linux-srv", Online: true, DaemonFingerprint: "fp-7"}, nil,
			).AnyTimes()
			existing := &project_location_entity.ProjectLocation{ID: 42, ProjectID: 1, DeviceID: "7", DeviceFingerprint: "fp-7", Path: "/old", Status: consts.ACTIVE}
			repo.EXPECT().FindByProjectAndFingerprint(ctx, int64(1), "fp-7").Return(existing, nil)
			repo.EXPECT().UpdatePath(ctx, int64(42), "/new").Return(nil)
			repo.EXPECT().UpdateDeviceID(ctx, int64(42), "9").Return(nil)
			v, err := svc.Upsert(ctx, 1, "9", "/new")
			So(err, ShouldBeNil)
			So(v.DeviceID, ShouldEqual, "9")
		})
		Convey("远端 device 不存在 → AgentBackendInvalidDevice", func() {
			ctx, _, rd, svc := setupSvc(t)
			rd.EXPECT().Get(ctx, int64(99)).Return(nil, gorm.ErrRecordNotFound)
			_, err := svc.Upsert(ctx, 1, "99", "/foo")
			So(err, ShouldNotBeNil)
		})
		Convey("路径校验失败 → ProjectLocationInvalidPath", func() {
			ctx, _, rd, svc := setupSvc(t)
			rd.EXPECT().Get(ctx, int64(7)).Return(
				&remote_device_svc.DeviceView{ID: 7, Name: "linux-srv", Online: true, DaemonFingerprint: "fp-7"}, nil,
			).AnyTimes()
			_, err := svc.Upsert(ctx, 1, "7", "relative/path")
			So(err, ShouldNotBeNil)
		})
	})
}

func TestListByProject(t *testing.T) {
	Convey("ListByProject 包含 device 状态", t, func() {
		ctx, repo, rd, svc := setupSvc(t)
		repo.EXPECT().ListByProject(ctx, int64(1)).Return([]*project_location_entity.ProjectLocation{
			{ID: 42, ProjectID: 1, DeviceID: "7", DeviceFingerprint: "fp-7", Path: "/home/me/foo", Status: consts.ACTIVE},
		}, nil)
		rd.EXPECT().List(ctx).Return([]*remote_device_svc.DeviceView{
			{ID: 7, Name: "linux-srv", Online: true, DaemonFingerprint: "fp-7"},
		}, nil)
		list, err := svc.ListByProject(ctx, 1)
		So(err, ShouldBeNil)
		So(len(list), ShouldEqual, 1)
		So(list[0].DeviceID, ShouldEqual, "7")
		So(list[0].DeviceName, ShouldEqual, "linux-srv")
		So(list[0].Online, ShouldBeTrue)
	})

	Convey("R2b：本机配对表里查不到该指纹 → 该行不呈现，且清空已缓存的 device_id（数据不丢，行还在）", t, func() {
		ctx, repo, rd, svc := setupSvc(t)
		repo.EXPECT().ListByProject(ctx, int64(1)).Return([]*project_location_entity.ProjectLocation{
			{ID: 43, ProjectID: 1, DeviceID: "9", DeviceFingerprint: "fp-unpaired", Path: "/srv/app", Status: consts.ACTIVE},
		}, nil)
		rd.EXPECT().List(ctx).Return([]*remote_device_svc.DeviceView{}, nil)
		repo.EXPECT().UpdateDeviceID(ctx, int64(43), "").Return(nil)

		list, err := svc.ListByProject(ctx, 1)
		So(err, ShouldBeNil)
		So(list, ShouldBeEmpty)
	})

	Convey("R2b：取得配对行后自动生效 → 回填 device_id 并呈现，用户不需要再做第二件事", t, func() {
		ctx, repo, rd, svc := setupSvc(t)
		repo.EXPECT().ListByProject(ctx, int64(1)).Return([]*project_location_entity.ProjectLocation{
			{ID: 44, ProjectID: 1, DeviceID: "", DeviceFingerprint: "fp-newly-paired", Path: "/srv/app2", Status: consts.ACTIVE},
		}, nil)
		rd.EXPECT().List(ctx).Return([]*remote_device_svc.DeviceView{
			{ID: 11, Name: "new-box", Online: true, DaemonFingerprint: "fp-newly-paired"},
		}, nil)
		repo.EXPECT().UpdateDeviceID(ctx, int64(44), "11").Return(nil)

		list, err := svc.ListByProject(ctx, 1)
		So(err, ShouldBeNil)
		So(len(list), ShouldEqual, 1)
		So(list[0].DeviceID, ShouldEqual, "11")
		So(list[0].DeviceName, ShouldEqual, "new-box")
	})

	Convey("多条未解析记录（不同指纹、同一项目）可并存：各自独立清空缓存，都不呈现", t, func() {
		ctx, repo, rd, svc := setupSvc(t)
		repo.EXPECT().ListByProject(ctx, int64(1)).Return([]*project_location_entity.ProjectLocation{
			{ID: 45, ProjectID: 1, DeviceID: "", DeviceFingerprint: "fp-a", Path: "/srv/a", Status: consts.ACTIVE},
			{ID: 46, ProjectID: 1, DeviceID: "", DeviceFingerprint: "fp-b", Path: "/srv/b", Status: consts.ACTIVE},
		}, nil)
		rd.EXPECT().List(ctx).Return([]*remote_device_svc.DeviceView{}, nil)

		list, err := svc.ListByProject(ctx, 1)
		So(err, ShouldBeNil)
		So(list, ShouldBeEmpty)
	})
}

func TestRemoveByProjectAndDevice(t *testing.T) {
	Convey("RemoveByProjectAndDevice → repo.Delete", t, func() {
		ctx, repo, _, svc := setupSvc(t)
		repo.EXPECT().FindByProjectAndDevice(ctx, int64(1), "7").Return(
			&project_location_entity.ProjectLocation{ID: 42}, nil,
		)
		repo.EXPECT().Delete(ctx, int64(42)).Return(nil)
		err := svc.RemoveByProjectAndDevice(ctx, 1, "7")
		So(err, ShouldBeNil)
	})
}
