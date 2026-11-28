package project_svc_test

import (
	"context"
	"testing"

	"github.com/cago-frame/cago/pkg/consts"
	"github.com/cago-frame/cago/pkg/utils/httputils"
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

	"github.com/agentre-hub/agentre/pkg/wire/devicefp"
)

const (
	selfFP devicefp.Carrier = "sha256:self"
	boxFP  devicefp.Carrier = "sha256:box"
)

func setupDefaultDevice(t *testing.T) (context.Context, *mock_project_repo.MockProjectRepo, *mock_project_location_repo.MockProjectLocationRepo, project_svc.ProjectSvc) {
	t.Helper()
	ctrl := gomock.NewController(t)
	mp := mock_project_repo.NewMockProjectRepo(ctrl)
	ml := mock_project_location_repo.NewMockProjectLocationRepo(ctrl)
	project_repo.RegisterProject(mp)
	project_location_repo.RegisterProjectLocation(ml)
	svc := project_svc.New(project_svc.WithSelfDevice(func() (devicefp.Carrier, error) { return selfFP, nil }))
	return context.Background(), mp, ml, svc
}

func requireProjectCode(t *testing.T, err error, want int) {
	t.Helper()
	var httpErr *httputils.Error
	require.ErrorAs(t, err, &httpErr)
	assert.Equal(t, want, httpErr.Code)
}

// Given 本机有路径的项目 When 把本机设为默认设备 Then 写入默认设备。
func TestSetDefaultDevice_GivenSelfWithPath_ThenStored(t *testing.T) {
	ctx, mp, _, svc := setupDefaultDevice(t)
	mp.EXPECT().Find(ctx, int64(5)).Return(&project_entity.Project{ID: 5, Path: "/code/a", Status: consts.ACTIVE}, nil)
	mp.EXPECT().SetDefaultDevice(ctx, int64(5), selfFP).Return(nil)
	require.NoError(t, svc.SetDefaultDevice(ctx, 5, selfFP))
}

// Given 本机没配路径的项目 When 把本机设为默认设备 Then 拒绝：默认设备只能是有路径的设备。
func TestSetDefaultDevice_GivenSelfWithoutPath_ThenRefused(t *testing.T) {
	ctx, mp, _, svc := setupDefaultDevice(t)
	mp.EXPECT().Find(ctx, int64(5)).Return(&project_entity.Project{ID: 5, LocalPathMissing: true, Status: consts.ACTIVE}, nil)
	requireProjectCode(t, svc.SetDefaultDevice(ctx, 5, selfFP), code.ProjectDefaultDeviceNoPath)
}

// Given 其它设备在该项目里有路径记录 When 设为默认 Then 写入；没有记录则拒绝。
func TestSetDefaultDevice_Remote(t *testing.T) {
	t.Run("with a path record", func(t *testing.T) {
		ctx, mp, ml, svc := setupDefaultDevice(t)
		mp.EXPECT().Find(ctx, int64(5)).Return(&project_entity.Project{ID: 5, Path: "/code/a", Status: consts.ACTIVE}, nil)
		ml.EXPECT().FindByProjectAndFingerprint(ctx, int64(5), boxFP).Return(&project_location_entity.ProjectLocation{Path: "/srv/a", Status: consts.ACTIVE}, nil)
		mp.EXPECT().SetDefaultDevice(ctx, int64(5), boxFP).Return(nil)
		require.NoError(t, svc.SetDefaultDevice(ctx, 5, boxFP))
	})
	t.Run("without a path record", func(t *testing.T) {
		ctx, mp, ml, svc := setupDefaultDevice(t)
		mp.EXPECT().Find(ctx, int64(5)).Return(&project_entity.Project{ID: 5, Path: "/code/a", Status: consts.ACTIVE}, nil)
		ml.EXPECT().FindByProjectAndFingerprint(ctx, int64(5), boxFP).Return(nil, gorm.ErrRecordNotFound)
		requireProjectCode(t, svc.SetDefaultDevice(ctx, 5, boxFP), code.ProjectDefaultDeviceNoPath)
	})
	t.Run("with a removed path record", func(t *testing.T) {
		ctx, mp, ml, svc := setupDefaultDevice(t)
		mp.EXPECT().Find(ctx, int64(5)).Return(&project_entity.Project{ID: 5, Path: "/code/a", Status: consts.ACTIVE}, nil)
		ml.EXPECT().FindByProjectAndFingerprint(ctx, int64(5), boxFP).Return(&project_location_entity.ProjectLocation{Status: consts.DELETE}, nil)
		requireProjectCode(t, svc.SetDefaultDevice(ctx, 5, boxFP), code.ProjectDefaultDeviceNoPath)
	})
}

// Given 已设默认设备 When 传空 Then 清除（点「默认」再点一次取消）。
func TestSetDefaultDevice_EmptyClears(t *testing.T) {
	ctx, mp, _, svc := setupDefaultDevice(t)
	mp.EXPECT().Find(ctx, int64(5)).Return(&project_entity.Project{ID: 5, DefaultDeviceFingerprint: boxFP, Status: consts.ACTIVE}, nil)
	mp.EXPECT().SetDefaultDevice(ctx, int64(5), devicefp.Carrier("")).Return(nil)
	require.NoError(t, svc.SetDefaultDevice(ctx, 5, ""))
}

func TestSetDefaultDevice_UnknownProject(t *testing.T) {
	ctx, mp, _, svc := setupDefaultDevice(t)
	mp.EXPECT().Find(ctx, int64(9)).Return(nil, nil)
	requireProjectCode(t, svc.SetDefaultDevice(ctx, 9, selfFP), code.ProjectNotFound)
}

func TestGetDefaultDevice(t *testing.T) {
	ctx, mp, _, svc := setupDefaultDevice(t)
	mp.EXPECT().Find(ctx, int64(5)).Return(&project_entity.Project{ID: 5, DefaultDeviceFingerprint: boxFP, Status: consts.ACTIVE}, nil)
	got, err := svc.GetDefaultDevice(ctx, 5)
	require.NoError(t, err)
	assert.Equal(t, boxFP, got)
}

// Given 本机是默认设备 When 清掉本机路径 Then 默认设备一并清除；默认是别的设备时不动。
func TestClearLocalPath_ClearsSelfDefault(t *testing.T) {
	ctx, mp, _, svc := setupDefaultDevice(t)
	mp.EXPECT().Find(ctx, int64(5)).Return(&project_entity.Project{ID: 5, Path: "/code/a", DefaultDeviceFingerprint: selfFP, Status: consts.ACTIVE}, nil)
	mp.EXPECT().Update(ctx, gomock.Any()).Return(nil)
	mp.EXPECT().SetDefaultDevice(ctx, int64(5), devicefp.Carrier("")).Return(nil)
	_, err := svc.ClearLocalPath(ctx, 5)
	require.NoError(t, err)
}

func TestClearLocalPath_KeepsOtherDeviceDefault(t *testing.T) {
	ctx, mp, _, svc := setupDefaultDevice(t)
	mp.EXPECT().Find(ctx, int64(5)).Return(&project_entity.Project{ID: 5, Path: "/code/a", DefaultDeviceFingerprint: boxFP, Status: consts.ACTIVE}, nil)
	mp.EXPECT().Update(ctx, gomock.Any()).Return(nil)
	_, err := svc.ClearLocalPath(ctx, 5)
	require.NoError(t, err)
}
