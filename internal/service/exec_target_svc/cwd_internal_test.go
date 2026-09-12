package exec_target_svc

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/cago-frame/cago/pkg/i18n"
	"github.com/cago-frame/cago/pkg/utils/httputils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
	"gorm.io/gorm"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/chat_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/project_location_entity"
	"github.com/agentre-hub/agentre/internal/pkg/code"
	"github.com/agentre-hub/agentre/internal/repository/project_location_repo"
	"github.com/agentre-hub/agentre/internal/repository/project_location_repo/mock_project_location_repo"
	"github.com/agentre-hub/agentre/pkg/wire/devicefp"
)

// testDeviceFingerprint 是「第 n 台 daemon」的规范指纹。
func testDeviceFingerprint(deviceID int64) devicefp.Carrier {
	return devicefp.Carrier(fmt.Sprintf("sha256:device-%d", deviceID))
}

// TestResolveSessionCwd_LocalUsesCwdResolver 验证 be.IsLocal() 时走注入的 CwdResolver 回调。
func TestResolveSessionCwd_LocalUsesCwdResolver(t *testing.T) {
	prev := resolveCwdFn
	t.Cleanup(func() { resolveCwdFn = prev })
	resolveCwdFn = func(ctx context.Context, s *chat_entity.Session) (string, error) {
		return "/Users/me/proj", nil
	}
	sess := &chat_entity.Session{ID: 1, ProjectID: 10, AgentID: 7}
	be := &agent_backend_entity.AgentBackend{DeviceFingerprint: ""} // local
	cwd, err := ResolveSessionCwd(context.Background(), sess, be)
	require.NoError(t, err)
	assert.Equal(t, "/Users/me/proj", cwd)
}

// TestResolveSessionCwd_NilBackendUsesCwdResolver 验证 be 为 nil 时（back-compat）也走 CwdResolver。
func TestResolveSessionCwd_NilBackendUsesCwdResolver(t *testing.T) {
	prev := resolveCwdFn
	t.Cleanup(func() { resolveCwdFn = prev })
	resolveCwdFn = func(ctx context.Context, s *chat_entity.Session) (string, error) {
		return "/local", nil
	}
	sess := &chat_entity.Session{ID: 1, ProjectID: 10}
	cwd, err := ResolveSessionCwd(context.Background(), sess, nil)
	require.NoError(t, err)
	assert.Equal(t, "/local", cwd)
}

// TestResolveSessionCwd_RemoteHitsProjectLocation 验证 be.IsRemote() 时查 project_location_repo。
func TestResolveSessionCwd_RemoteHitsProjectLocation(t *testing.T) {
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)

	prevRepo := project_location_repo.ProjectLocation()
	mockRepo := mock_project_location_repo.NewMockProjectLocationRepo(ctrl)
	project_location_repo.RegisterProjectLocation(mockRepo)
	t.Cleanup(func() { project_location_repo.RegisterProjectLocation(prevRepo) })

	mockRepo.EXPECT().FindByProjectAndFingerprint(gomock.Any(), int64(10), testDeviceFingerprint(7)).Return(
		&project_location_entity.ProjectLocation{ID: 42, ProjectID: 10, DeviceID: string(testDeviceFingerprint(7)), Path: "/home/me/proj"}, nil,
	)

	sess := &chat_entity.Session{ID: 1, ProjectID: 10}
	be := &agent_backend_entity.AgentBackend{DeviceFingerprint: testDeviceFingerprint(7)} // remote
	cwd, err := ResolveSessionCwd(context.Background(), sess, be)
	require.NoError(t, err)
	assert.Equal(t, "/home/me/proj", cwd)
}

// TestResolveSessionCwd_RemoteFreeSessionSkipsRepo 验证 ProjectID=0（自由会话）+ 远端 backend
// 时直接返回 ("", nil)，把 cwd 兜底权下放给远端 daemon 的 runtime（cwd=="" → AgentCwd）。
// 关键约束：根本不能去查 project_location_repo —— mockRepo 没设 EXPECT，被调用就会 fail。
func TestResolveSessionCwd_RemoteFreeSessionSkipsRepo(t *testing.T) {
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)

	prevRepo := project_location_repo.ProjectLocation()
	mockRepo := mock_project_location_repo.NewMockProjectLocationRepo(ctrl)
	project_location_repo.RegisterProjectLocation(mockRepo)
	t.Cleanup(func() { project_location_repo.RegisterProjectLocation(prevRepo) })

	sess := &chat_entity.Session{ID: 1, ProjectID: 0, AgentID: 7}
	be := &agent_backend_entity.AgentBackend{DeviceFingerprint: testDeviceFingerprint(7)} // remote
	cwd, err := ResolveSessionCwd(context.Background(), sess, be)
	require.NoError(t, err)
	assert.Equal(t, "", cwd)
}

// TestResolveSessionCwd_RemoteMissingLocation 验证远端找不到记录时返回 ProjectLocationMissing 错误。
func TestResolveSessionCwd_RemoteMissingLocation(t *testing.T) {
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)

	prevRepo := project_location_repo.ProjectLocation()
	mockRepo := mock_project_location_repo.NewMockProjectLocationRepo(ctrl)
	project_location_repo.RegisterProjectLocation(mockRepo)
	t.Cleanup(func() { project_location_repo.RegisterProjectLocation(prevRepo) })

	mockRepo.EXPECT().FindByProjectAndFingerprint(gomock.Any(), int64(10), testDeviceFingerprint(7)).Return(nil, gorm.ErrRecordNotFound)

	sess := &chat_entity.Session{ID: 1, ProjectID: 10}
	be := &agent_backend_entity.AgentBackend{DeviceFingerprint: testDeviceFingerprint(7)}
	_, err := ResolveSessionCwd(context.Background(), sess, be)
	var httpErr *httputils.Error
	require.ErrorAs(t, err, &httpErr)
	assert.Equal(t, code.ProjectLocationMissing, httpErr.Code)
}

// TestResolveSessionCwd_LocalPropagatesLocalPathMissing 验证 R10:CwdResolver
// (project_svc.ResolveSessionCwd)对「本机未配置路径」返回的确定错误经
// resolveSessionCwd 原样透出 —— 不折叠成 ProjectLocationMissing / WorkspaceFsNoCwd,
// 也不是 ("", nil)。本域的全部读取点都经这条路径取 cwd,因此这里
// 通过即代表它们随解析点自动生效(R11)。
func TestResolveSessionCwd_LocalPropagatesLocalPathMissing(t *testing.T) {
	prev := resolveCwdFn
	t.Cleanup(func() { resolveCwdFn = prev })
	resolveCwdFn = func(ctx context.Context, s *chat_entity.Session) (string, error) {
		return "", i18n.NewError(ctx, code.ProjectLocalPathMissing)
	}
	sess := &chat_entity.Session{ID: 1, ProjectID: 10, AgentID: 7}
	be := &agent_backend_entity.AgentBackend{DeviceFingerprint: ""} // local
	cwd, err := ResolveSessionCwd(context.Background(), sess, be)
	require.Error(t, err)
	assert.Equal(t, "", cwd)
	var httpErr *httputils.Error
	require.ErrorAs(t, err, &httpErr)
	assert.Equal(t, code.ProjectLocalPathMissing, httpErr.Code)
	assert.NotEqual(t, code.ProjectLocationMissing, httpErr.Code)
	assert.NotEqual(t, code.WorkspaceFsNoCwd, httpErr.Code)
}

// TestCwdUnavailableReasonFor 锁住 R10 的分类表：三种"没有 cwd"必须映射到三个
// 彼此可区分的取值，且未知/无归类原因的错误落空串兜底，不冒充第四种状态。
func TestCwdUnavailableReasonFor(t *testing.T) {
	ctx := context.Background()
	assert.Equal(t, "local-path-missing",
		CwdUnavailableReasonFor(i18n.NewError(ctx, code.ProjectLocalPathMissing)))
	assert.Equal(t, "location-missing",
		CwdUnavailableReasonFor(i18n.NewError(ctx, code.ProjectLocationMissing)))
	assert.Equal(t, "", CwdUnavailableReasonFor(i18n.NewError(ctx, code.WorkspaceFsNoCwd)))
	assert.Equal(t, "", CwdUnavailableReasonFor(errors.New("unrelated failure")))
	assert.Equal(t, "", CwdUnavailableReasonFor(nil))
}
