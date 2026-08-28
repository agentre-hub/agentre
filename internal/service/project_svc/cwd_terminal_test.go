package project_svc_test

import (
	"context"
	"testing"

	"github.com/cago-frame/cago/pkg/consts"
	"github.com/cago-frame/cago/pkg/utils/httputils"
	"github.com/smartystreets/goconvey/convey"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
	"gorm.io/gorm"

	"github.com/agentre-hub/agentre/internal/model/entity/project_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/project_location_entity"
	"github.com/agentre-hub/agentre/internal/pkg/code"
	"github.com/agentre-hub/agentre/internal/repository/project_location_repo"
	"github.com/agentre-hub/agentre/internal/repository/project_location_repo/mock_project_location_repo"
	"github.com/agentre-hub/agentre/internal/repository/project_repo"
	"github.com/agentre-hub/agentre/internal/repository/project_repo/mock_project_repo"
	"github.com/agentre-hub/agentre/internal/service/project_svc"
)

type cwdTestMocks struct {
	proj *mock_project_repo.MockProjectRepo
	loc  *mock_project_location_repo.MockProjectLocationRepo
}

func setupCwdTestFull(t *testing.T) (context.Context, *cwdTestMocks, project_svc.ProjectSvc) {
	t.Helper()
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)

	mockProj := mock_project_repo.NewMockProjectRepo(ctrl)
	mockPA := mock_project_repo.NewMockProjectAgentRepo(ctrl)
	mockLoc := mock_project_location_repo.NewMockProjectLocationRepo(ctrl)

	project_repo.RegisterProject(mockProj)
	project_repo.RegisterProjectAgent(mockPA)
	project_location_repo.RegisterProjectLocation(mockLoc)

	mocks := &cwdTestMocks{proj: mockProj, loc: mockLoc}
	return context.Background(), mocks, project_svc.New()
}

func TestResolveProjectCwd(t *testing.T) {
	convey.Convey("ResolveProjectCwd", t, func() {
		convey.Convey("本地: deviceID 空 → project.Path", func() {
			ctx, mocks, svc := setupCwdTestFull(t)
			mocks.proj.EXPECT().Find(ctx, int64(7)).Return(
				&project_entity.Project{ID: 7, Path: "/repo", Status: consts.ACTIVE}, nil)
			cwd, err := svc.ResolveProjectCwd(ctx, 7, "")
			convey.So(err, convey.ShouldBeNil)
			convey.So(cwd, convey.ShouldEqual, "/repo")
		})
		convey.Convey("本地: 本机未配置路径 (R10) → ProjectLocalPathMissing,可与 ProjectLocationMissing 区分", func() {
			ctx, mocks, svc := setupCwdTestFull(t)
			mocks.proj.EXPECT().Find(ctx, int64(7)).Return(
				&project_entity.Project{ID: 7, Path: "", Status: consts.ACTIVE, LocalPathMissing: true}, nil)
			cwd, err := svc.ResolveProjectCwd(ctx, 7, "")
			convey.So(cwd, convey.ShouldEqual, "")
			convey.So(err, convey.ShouldNotBeNil)
			var httpErr *httputils.Error
			require.ErrorAs(t, err, &httpErr)
			assert.Equal(t, code.ProjectLocalPathMissing, httpErr.Code)
			assert.NotEqual(t, code.ProjectLocationMissing, httpErr.Code)
		})
		convey.Convey("本地: 项目不存在 → 报错", func() {
			ctx, mocks, svc := setupCwdTestFull(t)
			mocks.proj.EXPECT().Find(ctx, int64(7)).Return(nil, nil)
			_, err := svc.ResolveProjectCwd(ctx, 7, "")
			convey.So(err, convey.ShouldNotBeNil)
			var httpErr *httputils.Error
			require.ErrorAs(t, err, &httpErr)
			assert.Equal(t, code.ProjectNotFound, httpErr.Code)
		})
		convey.Convey("远端: 命中 → loc.Path", func() {
			ctx, mocks, svc := setupCwdTestFull(t)
			mocks.loc.EXPECT().FindByProjectAndDevice(ctx, int64(7), "42").Return(
				&project_location_entity.ProjectLocation{Path: "/remote/repo"}, nil)
			cwd, err := svc.ResolveProjectCwd(ctx, 7, "42")
			convey.So(err, convey.ShouldBeNil)
			convey.So(cwd, convey.ShouldEqual, "/remote/repo")
		})
		convey.Convey("远端: 未配置 → ProjectLocationMissing", func() {
			ctx, mocks, svc := setupCwdTestFull(t)
			mocks.loc.EXPECT().FindByProjectAndDevice(ctx, int64(7), "42").Return(nil, gorm.ErrRecordNotFound)
			_, err := svc.ResolveProjectCwd(ctx, 7, "42")
			convey.So(err, convey.ShouldNotBeNil)
			var httpErr *httputils.Error
			require.ErrorAs(t, err, &httpErr)
			assert.Equal(t, code.ProjectLocationMissing, httpErr.Code)
		})
		convey.Convey("本地: 项目已软删 (inactive) → ProjectNotFound", func() {
			ctx, mocks, svc := setupCwdTestFull(t)
			mocks.proj.EXPECT().Find(ctx, int64(7)).Return(
				&project_entity.Project{ID: 7, Path: "/repo", Status: consts.DELETE}, nil)
			_, err := svc.ResolveProjectCwd(ctx, 7, "")
			convey.So(err, convey.ShouldNotBeNil)
			var httpErr *httputils.Error
			require.ErrorAs(t, err, &httpErr)
			assert.Equal(t, code.ProjectNotFound, httpErr.Code)
		})
	})
}
