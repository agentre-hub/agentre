package chat_svc_test

import (
	"context"
	"testing"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/agent_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/chat_entity"
	"github.com/agentre-hub/agentre/internal/repository/chat_repo"
	"github.com/agentre-hub/agentre/internal/repository/chat_repo/mock_chat_repo"
	"github.com/agentre-hub/agentre/internal/service/chat_svc"
	"github.com/agentre-hub/agentre/internal/service/remote_device_svc"
	"github.com/agentre-hub/agentre/internal/service/remote_device_svc/mock_remote_device_svc"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
)

func TestResolveSessionCwd_Exported(t *testing.T) {
	t.Cleanup(func() { chat_svc.RegisterCwdResolver(nil) })
	chat_svc.RegisterCwdResolver(func(_ context.Context, _ *chat_entity.Session) (string, error) {
		return "/from/resolver", nil
	})
	cwd, err := chat_svc.ResolveSessionCwd(context.Background(),
		&chat_entity.Session{ID: 1, AgentID: 7},
		&agent_backend_entity.AgentBackend{DeviceFingerprint: ""},
	)
	require.NoError(t, err)
	assert.Equal(t, "/from/resolver", cwd)
}

// registerWorkspaceRepos 注册 chat_repo.Session 的 mock,配合
// registerCapabilityRepos 的 agent / backend mock 走通
// session → agent → backend → {deviceID, cwd} 这条解析链(不连 DB)。
func registerWorkspaceRepos(t *testing.T, ctrl *gomock.Controller) *mock_chat_repo.MockSessionRepo {
	t.Helper()
	sessionMock := mock_chat_repo.NewMockSessionRepo(ctrl)
	prev := chat_repo.Session()
	chat_repo.RegisterSession(sessionMock)
	t.Cleanup(func() { chat_repo.RegisterSession(prev) })
	return sessionMock
}

// ResolveSessionWorkspace 是 workspace_fs_svc 的 SessionWorkspaceResolver 实现:
// 它必须同时给出 deviceID 与 cwd —— 前者决定 workspace_fs_svc 走本机还是远端。
func TestResolveSessionWorkspace(t *testing.T) {
	t.Run("本地会话 → deviceID 0 + CwdResolver 给出的 cwd", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		t.Cleanup(ctrl.Finish)
		ctx := context.Background()
		sessionMock := registerWorkspaceRepos(t, ctrl)
		agentMock, backendMock := registerCapabilityRepos(t, ctrl)
		t.Cleanup(func() { chat_svc.RegisterCwdResolver(nil) })
		chat_svc.RegisterCwdResolver(func(_ context.Context, _ *chat_entity.Session) (string, error) {
			return "/local/project", nil
		})

		sessionMock.EXPECT().Find(ctx, int64(5)).Return(&chat_entity.Session{ID: 5, AgentID: 11}, nil)
		agentMock.EXPECT().Find(ctx, int64(11)).Return(&agent_entity.Agent{ID: 11, AgentBackendID: 12}, nil)
		backendMock.EXPECT().Find(ctx, int64(12)).Return(&agent_backend_entity.AgentBackend{
			ID: 12, Type: string(agent_backend_entity.TypeClaudeCode), DeviceFingerprint: "",
		}, nil)

		deviceID, cwd, err := chat_svc.NewChat(chat_svc.NoopEmitter{}).ResolveSessionWorkspace(ctx, 5)
		require.NoError(t, err)
		assert.Equal(t, int64(0), deviceID)
		assert.Equal(t, "/local/project", cwd)
	})

	// R15b / 决策 36：会话钉住某一档之后，一切按会话解析 backend 的路径都必须回到
	// **那一档**，不能回到 Agent 的主档。文件面板的 cwd 走的就是这条链：主档在本机、
	// 钉住的那一档在某台 agentred 时，回到主档会拿本机路径去列远端机器的文件。
	t.Run("会话已钉档 → 用钉住那一档的 backend，不是 Agent 主档", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		t.Cleanup(ctrl.Finish)
		ctx := context.Background()
		sessionMock := registerWorkspaceRepos(t, ctrl)
		agentMock, backendMock := registerCapabilityRepos(t, ctrl)
		sessionMock.EXPECT().Find(ctx, int64(6)).Return(&chat_entity.Session{
			ID: 6, AgentID: 11, ExecAgentBackendID: 13,
		}, nil)
		agentMock.EXPECT().Find(ctx, int64(11)).Return(&agent_entity.Agent{ID: 11, AgentBackendID: 12}, nil)
		// 主档 12(本机)一次都不该被查；钉住的 13 才是这条会话的档。
		backendMock.EXPECT().Find(ctx, int64(13)).Return(&agent_backend_entity.AgentBackend{
			ID: 13, Type: string(agent_backend_entity.TypeClaudeCode), DeviceFingerprint: "sha256:device-4",
		}, nil)
		pairChatTestDevices(t, 4)

		// deviceID 取自钉住那一档的 backend(device 4)；回到主档 12(本机)会得到 0，
		// 文件面板就会拿本机路径去列远端机器的文件。
		deviceID, _, err := chat_svc.NewChat(chat_svc.NoopEmitter{}).ResolveSessionWorkspace(ctx, 6)
		require.NoError(t, err)
		assert.Equal(t, int64(4), deviceID)
	})

	t.Run("会话不存在 → 报错", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		t.Cleanup(ctrl.Finish)
		ctx := context.Background()
		sessionMock := registerWorkspaceRepos(t, ctrl)
		sessionMock.EXPECT().Find(ctx, int64(404)).Return(nil, nil)

		_, _, err := chat_svc.NewChat(chat_svc.NoopEmitter{}).ResolveSessionWorkspace(ctx, 404)
		assert.Error(t, err)
	})

	t.Run("sessionID 非法 → 不查库", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		t.Cleanup(ctrl.Finish)
		registerWorkspaceRepos(t, ctrl) // 没有任何 EXPECT:一旦查库 gomock 判错

		_, _, err := chat_svc.NewChat(chat_svc.NoopEmitter{}).ResolveSessionWorkspace(context.Background(), 0)
		assert.Error(t, err)
	})

	// R13 认领后本机 backend 的 DeviceID 是本机指纹:ResolveSessionWorkspace 必须把它
	// 当本机档(deviceID 0 + 本机 cwd),而不是当远端档去配对表里找行报
	// RemoteDeviceNotFound —— 文件面板在本地会话上就靠这条链拿 cwd。
	t.Run("self 档 backend → deviceID 0 + 本机 cwd", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		t.Cleanup(ctrl.Finish)
		ctx := context.Background()
		sessionMock := registerWorkspaceRepos(t, ctrl)
		agentMock, backendMock := registerCapabilityRepos(t, ctrl)
		t.Cleanup(func() { chat_svc.RegisterCwdResolver(nil) })
		chat_svc.RegisterCwdResolver(func(_ context.Context, _ *chat_entity.Session) (string, error) {
			return "/local/project", nil
		})

		rds := mock_remote_device_svc.NewMockRemoteDeviceSvc(ctrl)
		rds.EXPECT().DeviceFingerprint().Return("sha256:self", nil).AnyTimes()
		rds.EXPECT().List(gomock.Any()).Return(nil, nil).AnyTimes()
		prevSvc := remote_device_svc.Default()
		remote_device_svc.SetDefault(rds)
		t.Cleanup(func() { remote_device_svc.SetDefault(prevSvc) })

		sessionMock.EXPECT().Find(ctx, int64(5)).Return(&chat_entity.Session{ID: 5, AgentID: 11, ProjectID: 3}, nil)
		agentMock.EXPECT().Find(ctx, int64(11)).Return(&agent_entity.Agent{ID: 11, AgentBackendID: 12}, nil)
		backendMock.EXPECT().Find(ctx, int64(12)).Return(&agent_backend_entity.AgentBackend{
			ID: 12, Type: string(agent_backend_entity.TypeClaudeCode), DeviceFingerprint: "sha256:self",
		}, nil)

		deviceID, cwd, err := chat_svc.NewChat(chat_svc.NoopEmitter{}).ResolveSessionWorkspace(ctx, 5)
		require.NoError(t, err)
		assert.Equal(t, int64(0), deviceID)
		assert.Equal(t, "/local/project", cwd)
	})

	// 远端 backend 但 DeviceID 解析不出整数时,绝不能退化成 deviceID=0 —— 那会让
	// workspace_fs_svc 拿着远端机器的路径去列本机文件系统。
	t.Run("远端 backend 的 DeviceID 无法解析 → 报错而不是回落本机", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		t.Cleanup(ctrl.Finish)
		ctx := context.Background()
		sessionMock := registerWorkspaceRepos(t, ctrl)
		agentMock, backendMock := registerCapabilityRepos(t, ctrl)

		sessionMock.EXPECT().Find(ctx, int64(5)).Return(&chat_entity.Session{ID: 5, AgentID: 11, ProjectID: 3}, nil)
		agentMock.EXPECT().Find(ctx, int64(11)).Return(&agent_entity.Agent{ID: 11, AgentBackendID: 12}, nil)
		backendMock.EXPECT().Find(ctx, int64(12)).Return(&agent_backend_entity.AgentBackend{
			ID: 12, Type: string(agent_backend_entity.TypeClaudeCode), DeviceFingerprint: "not-a-number",
		}, nil)

		deviceID, _, err := chat_svc.NewChat(chat_svc.NoopEmitter{}).ResolveSessionWorkspace(ctx, 5)
		require.Error(t, err)
		assert.Equal(t, int64(0), deviceID)
	})
}

// TestResolveSessionCwd_PrefersStoredCwd 钉死导入进来的会话怎么找回它的工作目录
// (spec「续跑」:工作目录取磁盘转录里记录的 cwd)。
//
// 导入把磁盘转录记的 cwd 落在 chat_sessions.cwd 上,解析必须先认这一列 ——
// 而且**本地与远端两条路都要**:
//   - 本地:project.Path / AgentCwd 兜底给出的是「这个 agent 的默认目录」,而
//     claude 的 --resume 按 cwd 定位 project 目录,换个目录那条 provider session
//     id 根本不存在;
//   - 远端:自由会话那一支原本返回空串、把兜底权下放给远端 runtime(落到远端机器的
//     <AppDataDir>/agents/<id>),而机器轴导进来的会话正是这一支 —— 不认这一列的话
//     从别的机器导进来的会话永远续不上。
func TestResolveSessionCwd_PrefersStoredCwd(t *testing.T) {
	t.Run("本地档 → 存下的 cwd 压过 CwdResolver", func(t *testing.T) {
		t.Cleanup(func() { chat_svc.RegisterCwdResolver(nil) })
		chat_svc.RegisterCwdResolver(func(_ context.Context, _ *chat_entity.Session) (string, error) {
			return "/from/resolver", nil
		})
		cwd, err := chat_svc.ResolveSessionCwd(context.Background(),
			&chat_entity.Session{ID: 1, AgentID: 7, Cwd: "/disk/transcript/cwd"},
			&agent_backend_entity.AgentBackend{DeviceFingerprint: ""},
		)
		require.NoError(t, err)
		assert.Equal(t, "/disk/transcript/cwd", cwd)
	})

	t.Run("远端档的自由会话 → 存下的 cwd 压过「交给远端 runtime 兜底」的空串", func(t *testing.T) {
		cwd, err := chat_svc.ResolveSessionCwd(context.Background(),
			&chat_entity.Session{ID: 2, AgentID: 7, ProjectID: 0, Cwd: "/box/repo"},
			&agent_backend_entity.AgentBackend{DeviceFingerprint: "sha256:device-4"},
		)
		require.NoError(t, err)
		assert.Equal(t, "/box/repo", cwd)
	})

	t.Run("没存 cwd → 沿用原来的两条路", func(t *testing.T) {
		t.Cleanup(func() { chat_svc.RegisterCwdResolver(nil) })
		chat_svc.RegisterCwdResolver(func(_ context.Context, _ *chat_entity.Session) (string, error) {
			return "/from/resolver", nil
		})
		cwd, err := chat_svc.ResolveSessionCwd(context.Background(),
			&chat_entity.Session{ID: 3, AgentID: 7},
			&agent_backend_entity.AgentBackend{DeviceFingerprint: ""},
		)
		require.NoError(t, err)
		assert.Equal(t, "/from/resolver", cwd)

		cwd, err = chat_svc.ResolveSessionCwd(context.Background(),
			&chat_entity.Session{ID: 4, AgentID: 7, ProjectID: 0},
			&agent_backend_entity.AgentBackend{DeviceFingerprint: "sha256:device-4"},
		)
		require.NoError(t, err)
		assert.Empty(t, cwd, "远端自由会话仍把兜底权下放给远端 runtime")
	})
}
