package ctl_svc

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/agentre-hub/agentre/internal/model/entity/project_entity"
	"github.com/agentre-hub/agentre/internal/service/agent_backend_svc"
	"github.com/agentre-hub/agentre/internal/service/department_svc"
	"github.com/agentre-hub/agentre/internal/service/llm_provider_svc"
	"github.com/agentre-hub/agentre/internal/service/project_location_svc"
	"github.com/agentre-hub/agentre/internal/service/project_svc"
	"github.com/agentre-hub/agentre/pkg/wire/agentrewire"
)

// 生产网关把服务层的读模型投影成 ctl 文档；这里钉死每个字段的来处。

func TestAgentDocFrom(t *testing.T) {
	got := agentDocFrom(&department_svc.AgentItem{
		ID: 3, Name: "architect", Description: "d", DepartmentID: 2, Pinned: true,
		AvatarColor: "blue", AvatarIcon: "bot", SystemBadge: "system",
		ExecTargets: []department_svc.AgentExecTargetItem{{ID: 1, AgentBackendID: 9}, {ID: 2, AgentBackendID: 5}},
	})
	assert.Equal(t, &agentrewire.CtlAgent{
		Id: 3, Name: "architect", Description: "d", DepartmentId: 2, BackendIds: []int64{9, 5}, Pinned: true,
		AvatarColor: "blue", AvatarIcon: "bot", SystemBadge: "system",
	}, got, "执行目标按原顺序转成后端 id")
}

func TestDepartmentDocFrom(t *testing.T) {
	got := departmentDocFrom(&department_svc.DepartmentItem{
		ID: 2, Name: "eng", Description: "d", Icon: "i", AccentColor: "red", ParentID: 1, LeadAgentID: 3,
	})
	assert.Equal(t, &agentrewire.CtlDepartment{
		Id: 2, Name: "eng", Description: "d", Icon: "i", AccentColor: "red", ParentId: 1, LeadAgentId: 3,
	}, got)
}

func TestProjectDocFrom(t *testing.T) {
	got := projectDocFrom(
		&project_entity.Project{ID: 1, ParentID: 0, Name: "agentre", Icon: "i", Color: "c", Description: "d", Path: "/src/agentre"},
		[]*project_svc.ProjectAgentMember{{AgentID: 3}, {AgentID: 4}},
		[]*project_location_svc.ProjectLocationView{{DeviceFingerprint: "sha256:ab", DeviceName: "box", Path: "/srv/agentre"}},
	)
	assert.Equal(t, &agentrewire.CtlProject{
		Id: 1, Name: "agentre", Icon: "i", Color: "c", Description: "d", Path: "/src/agentre",
		MemberAgentIds: []int64{3, 4},
		Locations:      []*agentrewire.CtlProjectLocation{{DeviceId: "sha256:ab", DeviceName: "box", Path: "/srv/agentre"}},
	}, got)
}

func TestProviderDocFrom_UsesServiceMask(t *testing.T) {
	got := providerDocFrom(&llm_provider_svc.ProviderItem{ //nolint:gosec // 掩码后的假数据，不是凭据。
		ID: 1, Name: "anthropic", Type: "anthropic", BaseURL: "https://x", Enabled: true,
		MaskedAPIKey: "sk-a••••••1111", HasAPIKey: true, DefaultModelKey: "mk-1",
	}, 2)
	assert.Equal(t, &agentrewire.CtlProvider{ //nolint:gosec // 掩码后的假数据，不是凭据。
		Id: 1, Name: "anthropic", Type: "anthropic", BaseUrl: "https://x", Enabled: true,
		ApiKey: "sk-a••••••1111", ApiKeySet: true, DefaultModelKey: "mk-1", BackendRefs: 2,
	}, got)
}

func TestModelDocFrom(t *testing.T) {
	got := modelDocFrom(&llm_provider_svc.ModelItem{
		ID: 21, ProviderID: 1, ModelKey: "mk-1", ModelID: "claude-opus-5", Name: "Opus",
		ContextWindow: 200000, MaxOutput: 32000, Enabled: true, IsDefault: true,
	}, 1)
	assert.Equal(t, &agentrewire.CtlModel{
		Id: 21, ProviderId: 1, Key: "mk-1", ModelId: "claude-opus-5", Name: "Opus",
		ContextWindow: 200000, MaxOutput: 32000, Enabled: true, IsDefault: true, BackendRefs: 1,
	}, got)
}

func TestBackendDocFrom(t *testing.T) {
	ids := keyIndex{providers: map[string]int64{"pk-1": 4}, models: map[string]int64{"mk-1": 21}}

	t.Run("绑定、设备、env、独占配置与 token 状态", func(t *testing.T) {
		got, err := backendDocFrom(&agent_backend_svc.BackendItem{
			ID: 5, Name: "cc", Type: "claudecode", LLMProviderKey: "pk-1", LLMModelKey: "mk-1",
			DeviceID: "sha256:ab", DeviceName: "box", ReasoningEffort: "high", EnvJSON: `{"A":"1"}`,
			DefaultPermissionMode: "plan",
			ModelRoutes:           map[string]agent_backend_svc.RouteTarget{"OPUS": {ProviderKey: "pk-1", ModelKey: "mk-1"}},
		}, ids, "/usr/local/bin/claude")
		require.NoError(t, err)
		assert.Equal(t, int64(5), got.GetId())
		assert.Equal(t, int64(4), got.GetProviderId())
		assert.Equal(t, int64(21), got.GetModelId())
		assert.Equal(t, "box", got.GetDevice(), "有名字就用名字")
		assert.Equal(t, "/usr/local/bin/claude", got.GetCliPath())
		assert.Equal(t, "high", got.GetReasoningEffort())
		assert.Equal(t, map[string]string{"A": "1"}, got.GetEnv())
		assert.False(t, got.GetTokenSet())
		var cfg map[string]any
		require.NoError(t, json.Unmarshal([]byte(got.GetConfigJson()), &cfg))
		assert.Equal(t, "plan", cfg["defaultPermissionMode"])
		assert.Equal(t, map[string]any{"OPUS": map[string]any{"providerKey": "pk-1", "modelKey": "mk-1"}}, cfg["modelRoutes"])
	})
	t.Run("本机、CLI 登录态、openclaw token 已设置", func(t *testing.T) {
		got, err := backendDocFrom(&agent_backend_svc.BackendItem{
			ID: 6, Name: "claw", Type: "openclaw", HasToken: true, OpenClawGatewayURL: "ws://127.0.0.1:1",
		}, ids, "")
		require.NoError(t, err)
		assert.Equal(t, int64(0), got.GetProviderId())
		assert.Equal(t, int64(0), got.GetModelId())
		assert.Empty(t, got.GetDevice())
		assert.Empty(t, got.GetToken())
		assert.True(t, got.GetTokenSet())
		assert.JSONEq(t, `{"openclawGatewayUrl":"ws://127.0.0.1:1"}`, got.GetConfigJson())
	})
	t.Run("设备没名字 → 回落指纹", func(t *testing.T) {
		got, err := backendDocFrom(&agent_backend_svc.BackendItem{ID: 7, Type: "codex", DeviceID: "sha256:cd"}, ids, "")
		require.NoError(t, err)
		assert.Equal(t, "sha256:cd", got.GetDevice())
	})
	t.Run("acp 的命令与参数进 config", func(t *testing.T) {
		got, err := backendDocFrom(&agent_backend_svc.BackendItem{
			ID: 8, Type: "acp", ACPCommand: "gemini", ACPArgs: []string{"--acp"},
		}, ids, "")
		require.NoError(t, err)
		assert.JSONEq(t, `{"acpCommand":"gemini","acpArgs":["--acp"]}`, got.GetConfigJson())
	})
	t.Run("坏 env_json → 报错", func(t *testing.T) {
		_, err := backendDocFrom(&agent_backend_svc.BackendItem{ID: 9, Type: "codex", EnvJSON: `{"broken"`}, ids, "")
		assert.Error(t, err)
	})
}
