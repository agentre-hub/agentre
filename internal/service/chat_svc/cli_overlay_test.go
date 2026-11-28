package chat_svc

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/agent_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/chat_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/device_pref_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/syncmeta_entity"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/capability"
	"github.com/agentre-hub/agentre/internal/repository/device_pref_repo"
	"github.com/agentre-hub/agentre/internal/repository/device_pref_repo/mock_device_pref_repo"
	"github.com/agentre-hub/agentre/internal/service/exec_target_svc"
	"github.com/agentre-hub/agentre/pkg/wire/devicefp"
)

// installCLIPathRepo 注册按（运行时类型, 设备指纹）存的 CLI 路径覆盖仓储 mock（spec
// 2026-11-25 决策 13）。旧的按「后端 × 设备」覆盖表不再是这一轮的读路径：这里不注册它，
// 一旦实现还去读它就直接空指针失败。
func installCLIPathRepo(t *testing.T, ctrl *gomock.Controller) *mock_device_pref_repo.MockCLIPathRepo {
	t.Helper()
	repo := mock_device_pref_repo.NewMockCLIPathRepo(ctrl)
	prev := device_pref_repo.CLIPath()
	device_pref_repo.RegisterCLIPath(repo)
	t.Cleanup(func() { device_pref_repo.RegisterCLIPath(prev) })
	return repo
}

// Given 本机执行的一轮，When 组 RunRequest，Then 交给 runtime 的后端副本带上本机这一种运行时
// 的 CLI 路径覆盖；后端视图里设备为空（引擎配置视图本就不带设备）时按本机指纹读。库里解析出
// 的那份后端不被写脏；没有覆盖 / 覆盖为空时保持为空（runtime 回落 PATH）。
func TestPrepareTurnRun_LocalCLIPathFromRuntimeAndDevice(t *testing.T) {
	cases := []struct {
		name     string
		override *device_pref_entity.CLIPathOverride
		wantPath string
	}{
		{"override path reaches the runtime request", &device_pref_entity.CLIPathOverride{CLIPath: "/x/cli"}, "/x/cli"},
		{"no override row leaves CLIPath empty", nil, ""},
		{"empty override path leaves CLIPath empty", &device_pref_entity.CLIPathOverride{}, ""},
	}
	for _, bt := range []agent_backend_entity.BackendType{
		agent_backend_entity.TypeAntigravity, agent_backend_entity.TypeClaudeCode,
		agent_backend_entity.TypeCodex, agent_backend_entity.TypePiAgent,
	} {
		for _, device := range []devicefp.Carrier{"", "sha256:self"} {
			for _, tc := range cases {
				t.Run(string(bt)+"/"+string(device)+"/"+tc.name, func(t *testing.T) {
					env := newMemoryTurnEnv(t)
					repo := installCLIPathRepo(t, env.ctrl)
					repo.EXPECT().Find(gomock.Any(), string(bt), devicefp.Carrier("sha256:self")).Return(tc.override, nil)
					be := env.local(bt, capability.Capabilities{})
					be.ID, be.DeviceFingerprint = 0, device
					be.SyncMeta = syncmeta_entity.SyncMeta{SyncID: "profile-12"}

					req, err := env.turn(&chat_entity.Session{ID: 401, AgentID: 7}, memoryAgent(7, false), be, "")
					require.NoError(t, err)
					require.NotNil(t, req.Backend)
					assert.Equal(t, tc.wantPath, req.Backend.CLIPath)
					assert.Empty(t, be.CLIPath, "库里解析出的后端不能被覆盖路径写脏")
				})
			}
		}
	}
}

// Given 发往远端设备的一轮，When 组 RunRequest，Then 带上**那台目标设备**上这种运行时的覆盖：
// 局域网直连的 agentred 没有账号快照，只能靠线上这份路径启动 CLI。按目标设备指纹读，本机
// 那份覆盖绝不漏给远端 —— gomock 只认目标指纹这一次调用。
func TestPrepareTurnRun_RemoteTurnGetsTargetDeviceCLIPath(t *testing.T) {
	cases := []struct {
		name     string
		override *device_pref_entity.CLIPathOverride
		wantPath string
	}{
		{"target device override reaches the wire backend", &device_pref_entity.CLIPathOverride{CLIPath: "/remote/claude"}, "/remote/claude"},
		{"no override row leaves CLIPath empty", nil, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := newMemoryTurnEnv(t)
			repo := installCLIPathRepo(t, env.ctrl)
			be := env.remote(capability.Capabilities{})
			repo.EXPECT().Find(gomock.Any(), string(agent_backend_entity.TypeClaudeCode), be.DeviceFingerprint).Return(tc.override, nil)

			req, err := env.turn(&chat_entity.Session{ID: 402, AgentID: 7}, memoryAgent(7, false), be, "")
			require.NoError(t, err)
			require.NotNil(t, req.Backend)
			assert.Equal(t, tc.wantPath, req.Backend.CLIPath)
			assert.Empty(t, be.CLIPath, "库里解析出的后端不能被覆盖路径写脏")
		})
	}
}

// Given 没有 CLI 的运行时（OpenClaw / Hermes 等），When 组 RunRequest，Then 不去查 CLI 路径
// 覆盖 —— 这里注册的 mock 没有任何期望，查一次就判失败。
func TestPrepareTurnRun_NoCLIPathLookupForRuntimeWithoutCLI(t *testing.T) {
	env := newMemoryTurnEnv(t)
	installCLIPathRepo(t, env.ctrl)
	be := env.local(agent_backend_entity.TypeHermes, capability.Capabilities{})

	req, err := env.turn(&chat_entity.Session{ID: 403, AgentID: 7}, memoryAgent(7, false), be, "")
	require.NoError(t, err)
	require.NotNil(t, req.Backend)
	assert.Empty(t, req.Backend.CLIPath)
}

// Given 会话跑在一份引擎配置上（视图本身不带 CLI 路径），本机给这种运行时配了 CLI 路径覆盖，
// When 复制启动命令，Then 命令里的可执行文件就是那条覆盖 —— 与这条会话续轮真正启动的同一个。
func TestGetLaunchCommand_UsesDeviceCLIPath(t *testing.T) {
	ctx, m, svc := setupEnginePinTest(t)
	ctrl := gomock.NewController(t)
	installPairedDevice(t, ctrl, 7)
	memberEffective(t, claudeProfile(31))
	installCLIPathRepo(t, ctrl).EXPECT().
		Find(gomock.Any(), string(agent_backend_entity.TypeClaudeCode), devicefp.Carrier("sha256:self")).
		Return(&device_pref_entity.CLIPathOverride{CLIPath: "/x/claude"}, nil)
	prevCwd := exec_target_svc.CurrentCwdResolver()
	t.Cleanup(func() { exec_target_svc.RegisterCwdResolver(prevCwd) })
	dir := t.TempDir()
	exec_target_svc.RegisterCwdResolver(func(context.Context, *chat_entity.Session) (string, error) { return dir, nil })
	m.session.EXPECT().Find(ctx, int64(77)).Return(&chat_entity.Session{ID: 77, AgentID: 401, ProviderSessionID: "ps-1"}, nil)
	m.agent.EXPECT().Find(ctx, int64(401)).Return(&agent_entity.Agent{ID: 401}, nil)

	resp, err := svc.GetLaunchCommand(ctx, &LaunchCommandRequest{SessionID: 77})
	require.NoError(t, err)
	assert.Contains(t, resp.Command, "/x/claude")
}
