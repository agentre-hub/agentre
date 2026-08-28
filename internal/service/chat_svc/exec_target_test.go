package chat_svc

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/cago-frame/cago/pkg/utils/httputils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/agent_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/chat_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/project_location_entity"
	"github.com/agentre-hub/agentre/internal/pkg/code"
	"github.com/agentre-hub/agentre/internal/repository/agent_backend_repo"
	"github.com/agentre-hub/agentre/internal/repository/agent_backend_repo/mock_agent_backend_repo"
	"github.com/agentre-hub/agentre/internal/repository/agent_repo"
	"github.com/agentre-hub/agentre/internal/repository/agent_repo/mock_agent_repo"
	"github.com/agentre-hub/agentre/internal/repository/chat_repo"
	"github.com/agentre-hub/agentre/internal/repository/chat_repo/mock_chat_repo"
	"github.com/agentre-hub/agentre/internal/repository/project_location_repo"
	"github.com/agentre-hub/agentre/internal/repository/project_location_repo/mock_project_location_repo"
	"github.com/agentre-hub/agentre/internal/service/remote_device_svc"
	"github.com/agentre-hub/agentre/internal/service/remote_device_svc/mock_remote_device_svc"
)

type execTargetMocks struct {
	session         *mock_chat_repo.MockSessionRepo
	agent           *mock_agent_repo.MockAgentRepo
	backend         *mock_agent_backend_repo.MockAgentBackendRepo
	projectLocation *mock_project_location_repo.MockProjectLocationRepo
}

func setupExecTargetTest(t *testing.T) (context.Context, *execTargetMocks, ChatSvc) {
	t.Helper()
	ctrl := gomock.NewController(t)
	m := &execTargetMocks{
		session:         mock_chat_repo.NewMockSessionRepo(ctrl),
		agent:           mock_agent_repo.NewMockAgentRepo(ctrl),
		backend:         mock_agent_backend_repo.NewMockAgentBackendRepo(ctrl),
		projectLocation: mock_project_location_repo.NewMockProjectLocationRepo(ctrl),
	}

	previousSession := chat_repo.Session()
	previousAgent := agent_repo.Agent()
	previousBackend := agent_backend_repo.AgentBackend()
	previousProjectLocation := project_location_repo.ProjectLocation()
	previousCwdResolver := resolveCwdFn
	chat_repo.RegisterSession(m.session)
	agent_repo.RegisterAgent(m.agent)
	agent_backend_repo.RegisterAgentBackend(m.backend)
	project_location_repo.RegisterProjectLocation(m.projectLocation)
	RegisterCwdResolver(nil)
	t.Cleanup(func() {
		chat_repo.RegisterSession(previousSession)
		agent_repo.RegisterAgent(previousAgent)
		agent_backend_repo.RegisterAgentBackend(previousBackend)
		project_location_repo.RegisterProjectLocation(previousProjectLocation)
		RegisterCwdResolver(previousCwdResolver)
	})

	return context.Background(), m, NewChat(NoopEmitter{})
}

// pairExecTargetDevices 把给定几台机器登记成本机配对表的全部内容 —— backend 的
// DeviceID 是规范指纹，范围解析要在那张表里找得到它才算「可派发」。
func pairExecTargetDevices(t *testing.T, deviceIDs ...int64) {
	t.Helper()
	ctrl := gomock.NewController(t)
	rows := make([]*remote_device_svc.DeviceView, 0, len(deviceIDs))
	for _, id := range deviceIDs {
		rows = append(rows, &remote_device_svc.DeviceView{
			ID: id, DaemonFingerprint: fmt.Sprintf("sha256:device-%d", id), Online: true,
		})
	}
	rds := mock_remote_device_svc.NewMockRemoteDeviceSvc(ctrl)
	rds.EXPECT().DeviceFingerprint().Return("sha256:self", nil).AnyTimes()
	rds.EXPECT().List(gomock.Any()).Return(rows, nil).AnyTimes()
	prev := remote_device_svc.Default()
	remote_device_svc.SetDefault(rds)
	t.Cleanup(func() { remote_device_svc.SetDefault(prev) })
}

func TestResolveLocalCommandScope_GivenExistingLocalSession_WhenResolved_ThenReturnsCurrentCwdAndLocalDevice(t *testing.T) {
	ctx, m, svc := setupExecTargetTest(t)
	sess := &chat_entity.Session{ID: 71, AgentID: 31, ProjectID: 41}
	agent := &agent_entity.Agent{ID: 31, AgentBackendID: 51}
	backend := &agent_backend_entity.AgentBackend{
		ID:   51,
		Type: string(agent_backend_entity.TypeClaudeCode),
	}
	m.session.EXPECT().Find(ctx, int64(71)).Return(sess, nil)
	m.agent.EXPECT().Find(ctx, int64(31)).Return(agent, nil)
	m.backend.EXPECT().Find(ctx, int64(51)).Return(backend, nil)
	RegisterCwdResolver(func(_ context.Context, got *chat_entity.Session) (string, error) {
		require.Equal(t, sess, got)
		return "/workspace/current-local", nil
	})

	scope, err := svc.ResolveLocalCommandScope(ctx, &ResolveLocalCommandScopeRequest{SessionID: 71})
	require.NoError(t, err)
	assert.Equal(t, &LocalCommandScope{DeviceID: "", Cwd: "/workspace/current-local"}, scope)
}

// TestResolveLocalCommandScope_GivenSelfBackend_ThenLocalScope R13 认领后本机
// backend 的 DeviceID 是本机指纹。"!" 命令范围解析必须把这种档当成本机档：
// DeviceID 为空、cwd 走本机 CwdResolver——而不是因 self 指纹在本机配对表里查不到而
// 误报 AgentBackendInvalidDevice。
func TestResolveLocalCommandScope_GivenSelfBackend_ThenLocalScope(t *testing.T) {
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)
	rds := mock_remote_device_svc.NewMockRemoteDeviceSvc(ctrl)
	rds.EXPECT().DeviceFingerprint().Return("sha256:self", nil).AnyTimes()
	rds.EXPECT().List(gomock.Any()).Return(nil, nil).AnyTimes()
	prevSvc := remote_device_svc.Default()
	remote_device_svc.SetDefault(rds)
	t.Cleanup(func() { remote_device_svc.SetDefault(prevSvc) })

	ctx, m, svc := setupExecTargetTest(t)
	sess := &chat_entity.Session{ID: 78, AgentID: 34, ProjectID: 44, ExecAgentBackendID: 58}
	agent := &agent_entity.Agent{ID: 34, AgentBackendID: 50}
	backend := &agent_backend_entity.AgentBackend{
		ID: 58, Type: string(agent_backend_entity.TypeClaudeCode), DeviceFingerprint: "sha256:self",
	}
	m.session.EXPECT().Find(ctx, int64(78)).Return(sess, nil)
	m.agent.EXPECT().Find(ctx, int64(34)).Return(agent, nil)
	m.backend.EXPECT().Find(ctx, int64(58)).Return(backend, nil)
	RegisterCwdResolver(func(_ context.Context, got *chat_entity.Session) (string, error) {
		return "/workspace/self", nil
	})
	t.Cleanup(func() { RegisterCwdResolver(nil) })
	// 本机档不查 project_locations——self 不是配对 agentred，该表没有以 self 指纹为键的行。

	scope, err := svc.ResolveLocalCommandScope(ctx, &ResolveLocalCommandScopeRequest{SessionID: 78})
	require.NoError(t, err)
	assert.Equal(t, &LocalCommandScope{DeviceID: "", Cwd: "/workspace/self"}, scope)
}

// TestResolveLocalCommandScope_GivenSessionWithStickyExecTarget_ThenScopeFollowsPinnedBackendNotAgentPrimary
// 锁住 R15b / 决策36 在 "!" 命令范围解析这条旁路上的一致性:会话已经钉住某一档时,
// "!" 命令必须跟着那一档跑,不能各算各的——否则续轮实际用的是钉住的远端 backend,
// 而"!"命令却按 Agent 当前主档(本机)算出一份不同的执行范围。
func TestResolveLocalCommandScope_GivenSessionWithStickyExecTarget_ThenScopeFollowsPinnedBackendNotAgentPrimary(t *testing.T) {
	ctx, m, svc := setupExecTargetTest(t)
	pairExecTargetDevices(t, 12)
	sess := &chat_entity.Session{ID: 74, AgentID: 40, ProjectID: 0, ExecAgentBackendID: 59}
	agent := &agent_entity.Agent{ID: 40, AgentBackendID: 50}
	pinnedBackend := &agent_backend_entity.AgentBackend{
		ID:                59,
		Type:              string(agent_backend_entity.TypeClaudeCode),
		DeviceFingerprint: "sha256:device-12",
	}
	m.session.EXPECT().Find(ctx, int64(74)).Return(sess, nil)
	m.agent.EXPECT().Find(ctx, int64(40)).Return(agent, nil)
	m.backend.EXPECT().Find(ctx, int64(59)).Return(pinnedBackend, nil)

	scope, err := svc.ResolveLocalCommandScope(ctx, &ResolveLocalCommandScopeRequest{SessionID: 74})
	require.NoError(t, err)
	assert.Equal(t, "sha256:device-12", scope.DeviceID, "范围必须跟着会话钉住的档(59/设备12),不是 Agent 当前主档(50/本机)")
}

func TestResolveLocalCommandScope_GivenExistingRemoteSession_WhenLocationChanges_ThenRereadsCurrentLocation(t *testing.T) {
	ctx, m, svc := setupExecTargetTest(t)
	pairExecTargetDevices(t, 9)
	sess := &chat_entity.Session{ID: 72, AgentID: 32, ProjectID: 42}
	agent := &agent_entity.Agent{ID: 32, AgentBackendID: 52}
	backend := &agent_backend_entity.AgentBackend{
		ID:                52,
		Type:              string(agent_backend_entity.TypeClaudeCode),
		DeviceFingerprint: "sha256:device-9",
	}
	m.session.EXPECT().Find(ctx, int64(72)).Return(sess, nil).Times(2)
	m.agent.EXPECT().Find(ctx, int64(32)).Return(agent, nil).Times(2)
	m.backend.EXPECT().Find(ctx, int64(52)).Return(backend, nil).Times(2)
	gomock.InOrder(
		m.projectLocation.EXPECT().FindByProjectAndFingerprint(ctx, int64(42), "sha256:device-9").
			Return(&project_location_entity.ProjectLocation{Path: "/remote/old"}, nil),
		m.projectLocation.EXPECT().FindByProjectAndFingerprint(ctx, int64(42), "sha256:device-9").
			Return(&project_location_entity.ProjectLocation{Path: "/remote/current"}, nil),
	)

	first, err := svc.ResolveLocalCommandScope(ctx, &ResolveLocalCommandScopeRequest{SessionID: 72})
	require.NoError(t, err)
	second, err := svc.ResolveLocalCommandScope(ctx, &ResolveLocalCommandScopeRequest{SessionID: 72})
	require.NoError(t, err)

	assert.Equal(t, &LocalCommandScope{DeviceID: "sha256:device-9", Cwd: "/remote/old"}, first)
	assert.Equal(t, &LocalCommandScope{DeviceID: "sha256:device-9", Cwd: "/remote/current"}, second)
}

func TestResolveLocalCommandScope_GivenPreSessionLocalProject_WhenResolved_ThenUsesUnpersistedSessionWithoutCreatingOne(t *testing.T) {
	ctx, m, svc := setupExecTargetTest(t)
	agent := &agent_entity.Agent{ID: 33, AgentBackendID: 53}
	backend := &agent_backend_entity.AgentBackend{
		ID:   53,
		Type: string(agent_backend_entity.TypeClaudeCode),
	}
	m.session.EXPECT().Create(gomock.Any(), gomock.Any()).Times(0)
	m.agent.EXPECT().Find(ctx, int64(33)).Return(agent, nil)
	m.backend.EXPECT().Find(ctx, int64(53)).Return(backend, nil)
	RegisterCwdResolver(func(_ context.Context, sess *chat_entity.Session) (string, error) {
		require.Equal(t, int64(0), sess.ID)
		require.Equal(t, int64(33), sess.AgentID)
		require.Equal(t, int64(43), sess.ProjectID)
		return "/local/project-current", nil
	})
	scope, err := svc.ResolveLocalCommandScope(ctx, &ResolveLocalCommandScopeRequest{
		AgentID:   33,
		ProjectID: 43,
	})
	require.NoError(t, err)
	assert.Equal(t, &LocalCommandScope{DeviceID: "", Cwd: "/local/project-current"}, scope)
}

func TestResolveLocalCommandScope_GivenPreSessionLocalFreeChat_WhenResolved_ThenUsesAgentCwdWithoutCreatingSession(t *testing.T) {
	dataDir := t.TempDir()
	t.Setenv("AGENTRE_DATA_DIR", dataDir)
	ctx, m, svc := setupExecTargetTest(t)
	agent := &agent_entity.Agent{ID: 34, AgentBackendID: 54}
	backend := &agent_backend_entity.AgentBackend{
		ID:   54,
		Type: string(agent_backend_entity.TypeClaudeCode),
	}
	m.session.EXPECT().Create(gomock.Any(), gomock.Any()).Times(0)
	m.agent.EXPECT().Find(ctx, int64(34)).Return(agent, nil)
	m.backend.EXPECT().Find(ctx, int64(54)).Return(backend, nil)

	scope, err := svc.ResolveLocalCommandScope(ctx, &ResolveLocalCommandScopeRequest{AgentID: 34, ProjectID: 0})
	require.NoError(t, err)
	assert.Equal(t, &LocalCommandScope{
		DeviceID: "",
		Cwd:      filepath.Join(dataDir, "agents", "34"),
	}, scope)
}

func TestResolveLocalCommandScope_GivenPreSessionRemoteProject_WhenResolved_ThenUsesCurrentDeviceLocationWithoutCreatingSession(t *testing.T) {
	ctx, m, svc := setupExecTargetTest(t)
	pairExecTargetDevices(t, 10)
	agent := &agent_entity.Agent{ID: 35, AgentBackendID: 55}
	backend := &agent_backend_entity.AgentBackend{
		ID:                55,
		Type:              string(agent_backend_entity.TypeClaudeCode),
		DeviceFingerprint: "sha256:device-10",
	}
	m.session.EXPECT().Create(gomock.Any(), gomock.Any()).Times(0)
	m.agent.EXPECT().Find(ctx, int64(35)).Return(agent, nil)
	m.backend.EXPECT().Find(ctx, int64(55)).Return(backend, nil)
	m.projectLocation.EXPECT().FindByProjectAndFingerprint(ctx, int64(45), "sha256:device-10").
		Return(&project_location_entity.ProjectLocation{Path: "/remote/project-current"}, nil)

	scope, err := svc.ResolveLocalCommandScope(ctx, &ResolveLocalCommandScopeRequest{
		AgentID:   35,
		ProjectID: 45,
	})
	require.NoError(t, err)
	assert.Equal(t, &LocalCommandScope{
		DeviceID: "sha256:device-10",
		Cwd:      "/remote/project-current",
	}, scope)
}

func TestResolveLocalCommandScope_GivenPreSessionRemoteFreeChat_WhenResolved_ThenUsesDeviceDefaultScopeWithoutCreatingSession(t *testing.T) {
	ctx, m, svc := setupExecTargetTest(t)
	pairExecTargetDevices(t, 11)
	agent := &agent_entity.Agent{ID: 36, AgentBackendID: 56}
	backend := &agent_backend_entity.AgentBackend{
		ID:                56,
		Type:              string(agent_backend_entity.TypeClaudeCode),
		DeviceFingerprint: "sha256:device-11",
	}
	m.session.EXPECT().Create(gomock.Any(), gomock.Any()).Times(0)
	m.agent.EXPECT().Find(ctx, int64(36)).Return(agent, nil)
	m.backend.EXPECT().Find(ctx, int64(56)).Return(backend, nil)

	scope, err := svc.ResolveLocalCommandScope(ctx, &ResolveLocalCommandScopeRequest{
		AgentID:   36,
		ProjectID: 0,
	})
	require.NoError(t, err)
	assert.Equal(t, &LocalCommandScope{DeviceID: "sha256:device-11", Cwd: ""}, scope)
}

func TestResolveLocalCommandScope_GivenInvalidRemoteDevice_WhenResolved_ThenRejectsBeforeCwdResolution(t *testing.T) {
	tests := []struct {
		name     string
		req      *ResolveLocalCommandScopeRequest
		sess     *chat_entity.Session
		agentID  int64
		deviceID string
	}{
		{
			name:     "existing session with malformed device",
			req:      &ResolveLocalCommandScopeRequest{SessionID: 73},
			sess:     &chat_entity.Session{ID: 73, AgentID: 37, ProjectID: 47},
			agentID:  37,
			deviceID: "device-9",
		},
		{
			name:     "pre-session free chat with zero device",
			req:      &ResolveLocalCommandScopeRequest{AgentID: 38, ProjectID: 0},
			agentID:  38,
			deviceID: "0",
		},
		{
			name:     "pre-session project with negative device",
			req:      &ResolveLocalCommandScopeRequest{AgentID: 39, ProjectID: 49},
			agentID:  39,
			deviceID: "-1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, m, svc := setupExecTargetTest(t)
			backendID := tt.agentID + 20
			if tt.sess != nil {
				m.session.EXPECT().Find(ctx, tt.req.SessionID).Return(tt.sess, nil)
			}
			m.agent.EXPECT().Find(ctx, tt.agentID).
				Return(&agent_entity.Agent{ID: tt.agentID, AgentBackendID: backendID}, nil)
			m.backend.EXPECT().Find(ctx, backendID).
				Return(&agent_backend_entity.AgentBackend{
					ID:                backendID,
					Type:              string(agent_backend_entity.TypeClaudeCode),
					DeviceFingerprint: tt.deviceID,
				}, nil)
			RegisterCwdResolver(func(context.Context, *chat_entity.Session) (string, error) {
				t.Fatal("invalid remote device must not fall back to local cwd resolution")
				return "", nil
			})

			scope, err := svc.ResolveLocalCommandScope(ctx, tt.req)

			require.Nil(t, scope)
			require.Error(t, err)
			var httpErr *httputils.Error
			require.True(t, errors.As(err, &httpErr))
			assert.Equal(t, code.AgentBackendInvalidDevice, httpErr.Code)
		})
	}
}

func TestResolveLocalCommandScope_GivenStaleAgentOrBackendTarget_WhenResolved_ThenRejectsWithoutLocalFallback(t *testing.T) {
	tests := []struct {
		name      string
		setup     func(*execTargetMocks)
		errorCode int
	}{
		{
			name: "agent no longer exists",
			setup: func(m *execTargetMocks) {
				m.agent.EXPECT().Find(gomock.Any(), int64(37)).Return(nil, nil)
			},
			errorCode: code.AgentNotFound,
		},
		{
			name: "agent no longer has a backend",
			setup: func(m *execTargetMocks) {
				m.agent.EXPECT().Find(gomock.Any(), int64(37)).
					Return(&agent_entity.Agent{ID: 37}, nil)
			},
			errorCode: code.ChatAgentNoBackend,
		},
		{
			name: "referenced backend no longer exists",
			setup: func(m *execTargetMocks) {
				m.agent.EXPECT().Find(gomock.Any(), int64(37)).
					Return(&agent_entity.Agent{ID: 37, AgentBackendID: 57}, nil)
				m.backend.EXPECT().Find(gomock.Any(), int64(57)).Return(nil, nil)
			},
			errorCode: code.AgentBackendNotFound,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, m, svc := setupExecTargetTest(t)
			tt.setup(m)

			scope, err := svc.ResolveLocalCommandScope(ctx, &ResolveLocalCommandScopeRequest{
				AgentID:   37,
				ProjectID: 47,
			})

			require.Nil(t, scope)
			require.Error(t, err)
			var httpErr *httputils.Error
			require.True(t, errors.As(err, &httpErr))
			assert.Equal(t, tt.errorCode, httpErr.Code)
		})
	}
}

func TestResolveLocalCommandScope_GivenInvalidTargetForms_WhenResolved_ThenRejectsWithoutRepositoryAccess(t *testing.T) {
	tests := []struct {
		name string
		req  *ResolveLocalCommandScopeRequest
	}{
		{name: "missing request", req: nil},
		{name: "missing target", req: &ResolveLocalCommandScopeRequest{}},
		{name: "project without agent", req: &ResolveLocalCommandScopeRequest{ProjectID: 1}},
		{name: "mixed session and agent", req: &ResolveLocalCommandScopeRequest{SessionID: 1, AgentID: 2}},
		{name: "mixed session and project", req: &ResolveLocalCommandScopeRequest{SessionID: 1, ProjectID: 2}},
		{name: "negative session", req: &ResolveLocalCommandScopeRequest{SessionID: -1}},
		{name: "negative agent", req: &ResolveLocalCommandScopeRequest{AgentID: -1}},
		{name: "negative project", req: &ResolveLocalCommandScopeRequest{AgentID: 1, ProjectID: -1}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := NewChat(NoopEmitter{})
			_, err := svc.ResolveLocalCommandScope(context.Background(), tt.req)
			require.Error(t, err)
			var httpErr *httputils.Error
			require.True(t, errors.As(err, &httpErr))
			assert.Equal(t, code.InvalidParameter, httpErr.Code)
		})
	}
}
