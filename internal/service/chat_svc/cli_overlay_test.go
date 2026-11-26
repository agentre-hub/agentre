package chat_svc

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/chat_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/syncmeta_entity"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/capability"
	"github.com/agentre-hub/agentre/internal/repository/agent_backend_repo"
	"github.com/agentre-hub/agentre/internal/repository/agent_backend_repo/mock_agent_backend_repo"
	"github.com/agentre-hub/agentre/pkg/wire/devicefp"
)

// installOverlayRepo 注册 agent_backend_repo 的 mock;overlays 的 key 是 (后端 SyncID, 设备指纹)。
func installOverlayRepo(t *testing.T, ctrl *gomock.Controller) *mock_agent_backend_repo.MockAgentBackendRepo {
	t.Helper()
	repo := mock_agent_backend_repo.NewMockAgentBackendRepo(ctrl)
	prev := agent_backend_repo.AgentBackend()
	agent_backend_repo.RegisterAgentBackend(repo)
	t.Cleanup(func() { agent_backend_repo.RegisterAgentBackend(prev) })
	return repo
}

// 本机执行的一轮:按本机指纹读到的 cli_path 覆盖进入 runtime 收到的后端副本,
// 库里的后端行不被改写;覆盖为空 / 没有覆盖时保持原样(runtime 回落到 PATH)。
func TestPrepareTurnRun_LocalCLIPathOverlay(t *testing.T) {
	cases := []struct {
		name     string
		overlay  *agent_backend_entity.CLIOverlay
		wantPath string
	}{
		{"overlay path reaches the runtime request", &agent_backend_entity.CLIOverlay{CLIPath: "/x/agy"}, "/x/agy"},
		{"no overlay row leaves CLIPath empty", nil, ""},
		{"empty overlay path leaves CLIPath empty", &agent_backend_entity.CLIOverlay{}, ""},
	}
	for _, bt := range []agent_backend_entity.BackendType{
		agent_backend_entity.TypeAntigravity, agent_backend_entity.TypeClaudeCode,
		agent_backend_entity.TypeCodex, agent_backend_entity.TypePiAgent,
	} {
		for _, tc := range cases {
			t.Run(string(bt)+"/"+tc.name, func(t *testing.T) {
				env := newMemoryTurnEnv(t)
				repo := installOverlayRepo(t, env.ctrl)
				repo.EXPECT().FindCLIOverlay(gomock.Any(), "backend-12", devicefp.Carrier("sha256:self")).Return(tc.overlay, nil)
				be := env.local(bt, capability.Capabilities{})
				be.SyncMeta = syncmeta_entity.SyncMeta{SyncID: "backend-12"}

				req, err := env.turn(&chat_entity.Session{ID: 401, AgentID: 7}, memoryAgent(7, false), be, "")
				require.NoError(t, err)
				require.NotNil(t, req.Backend)
				assert.Equal(t, tc.wantPath, req.Backend.CLIPath)
				assert.Empty(t, be.CLIPath, "库里解析出的后端行不能被覆盖路径写脏")
			})
		}
	}
}

// 发往远端设备的一轮带上**那台目标设备**的覆盖:局域网直连的 agentred 没有账号快照,
// 只能靠线上这份路径启动 CLI(有账号快照时 agentred 侧的权威覆盖照旧替换它)。按目标
// 设备指纹读,本机那份覆盖绝不漏给远端 —— gomock 只认目标指纹这一次调用。
func TestPrepareTurnRun_RemoteTurnGetsTargetDeviceCLIPathOverlay(t *testing.T) {
	cases := []struct {
		name     string
		overlay  *agent_backend_entity.CLIOverlay
		wantPath string
	}{
		{"target device overlay reaches the wire backend", &agent_backend_entity.CLIOverlay{CLIPath: "/remote/agy"}, "/remote/agy"},
		{"no overlay row leaves CLIPath empty", nil, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := newMemoryTurnEnv(t)
			repo := installOverlayRepo(t, env.ctrl)
			be := env.remote(capability.Capabilities{})
			be.SyncMeta = syncmeta_entity.SyncMeta{SyncID: "backend-12"}
			repo.EXPECT().FindCLIOverlay(gomock.Any(), "backend-12", be.DeviceFingerprint).Return(tc.overlay, nil)

			req, err := env.turn(&chat_entity.Session{ID: 402, AgentID: 7}, memoryAgent(7, false), be, "")
			require.NoError(t, err)
			require.NotNil(t, req.Backend)
			assert.Equal(t, tc.wantPath, req.Backend.CLIPath)
			assert.Empty(t, be.CLIPath, "库里解析出的后端行不能被覆盖路径写脏")
		})
	}
}
