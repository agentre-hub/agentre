package sync_svc

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/cago-frame/cago/pkg/consts"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/agent_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/department_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/device_pref_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/llm_provider_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/project_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/syncmeta_entity"
	"github.com/agentre-hub/agentre/internal/repository/agent_backend_repo"
	"github.com/agentre-hub/agentre/internal/repository/agent_backend_repo/mock_agent_backend_repo"
	"github.com/agentre-hub/agentre/internal/repository/agent_repo"
	"github.com/agentre-hub/agentre/internal/repository/agent_repo/mock_agent_repo"
	"github.com/agentre-hub/agentre/internal/repository/department_repo"
	"github.com/agentre-hub/agentre/internal/repository/department_repo/mock_department_repo"
	"github.com/agentre-hub/agentre/internal/repository/device_pref_repo"
	"github.com/agentre-hub/agentre/internal/repository/device_pref_repo/mock_device_pref_repo"
	"github.com/agentre-hub/agentre/internal/repository/llm_provider_repo"
	"github.com/agentre-hub/agentre/internal/repository/llm_provider_repo/mock_llm_provider_repo"
	"github.com/agentre-hub/agentre/internal/repository/project_repo"
	"github.com/agentre-hub/agentre/internal/repository/project_repo/mock_project_repo"
	"github.com/agentre-hub/agentre/internal/repository/syncstate_repo"
	"github.com/agentre-hub/agentre/internal/repository/syncstate_repo/mock_syncstate_repo"
	"github.com/agentre-hub/agentre/pkg/syncwire"
	"github.com/agentre-hub/agentre/pkg/wire/devicefp"
)

// stateFinds 让 FindRow(kind, syncID) 把 row 拷进 dest。
func stateFinds(state *mock_syncstate_repo.MockSyncStateRepo, kind, syncID string, fill func(dest any)) {
	state.EXPECT().FindRow(gomock.Any(), kind, syncID, gomock.Any()).
		DoAndReturn(func(_ context.Context, _, _ string, dest any) (bool, error) {
			fill(dest)
			return true, nil
		})
}

func stateMisses(state *mock_syncstate_repo.MockSyncStateRepo, kind, syncID string) {
	state.EXPECT().FindRow(gomock.Any(), kind, syncID, gomock.Any()).Return(false, nil)
}

// ── engine_profile ──────────────────────────────────────────────────────────

func TestEngineProfileAdapter_LoadCarriesIdentityConfigAndDefaultMarkWithoutDeviceOrCLIPath(t *testing.T) {
	ctrl := gomock.NewController(t)
	state := mock_syncstate_repo.NewMockSyncStateRepo(ctrl)
	stateFinds(state, syncwire.KindEngineProfile, "ep-1", func(dest any) {
		*dest.(*agent_backend_entity.EngineProfile) = agent_backend_entity.EngineProfile{
			ID: 3, Type: "codex", Name: "Codex 主力", LLMProviderKey: "openai-main", LLMModelKey: "gpt",
			EnvJSON: `{"K":"V"}`, ReasoningEffort: "high", IsAccountDefault: true,
			ConfigJSON: `{"sandbox":"workspace-write","approval":"on-request"}`,
			SyncMeta:   syncmeta_entity.SyncMeta{SyncID: "ep-1"},
		}
	})
	syncstate_repo.RegisterSyncState(state)

	out, err := engineProfileAdapter{}.load(context.Background(), "ep-1")
	require.NoError(t, err)
	require.NotNil(t, out)
	assert.Empty(t, out.AgentredFingerprint, "引擎配置没有设备")
	assert.JSONEq(t, `{"type":"codex","name":"Codex 主力","provider_key":"openai-main","model_key":"gpt",`+
		`"env_json":"{\"K\":\"V\"}","reasoning_effort":"high",`+
		`"config":{"sandbox":"workspace-write","approval":"on-request"},"account_default":true}`, string(out.Payload))
	assert.NoError(t, syncwire.GuardPayload(syncwire.KindEngineProfile, out.Payload))
}

func TestEngineProfileAdapter_ApplyCreatesRowAndMarksAccountDefault(t *testing.T) {
	ctrl := gomock.NewController(t)
	state := mock_syncstate_repo.NewMockSyncStateRepo(ctrl)
	stateMisses(state, syncwire.KindEngineProfile, "ep-1")
	syncstate_repo.RegisterSyncState(state)
	profiles := mock_agent_backend_repo.NewMockEngineProfileRepo(ctrl)
	var created *agent_backend_entity.EngineProfile
	profiles.EXPECT().Create(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, p *agent_backend_entity.EngineProfile) error {
			p.ID = 9
			created = p
			return nil
		})
	profiles.EXPECT().SetAccountDefault(gomock.Any(), int64(9)).Return(nil)
	agent_backend_repo.RegisterEngineProfile(profiles)

	err := engineProfileAdapter{}.apply(context.Background(), &inbound{
		Kind: syncwire.KindEngineProfile, SyncID: "ep-1",
		Payload: []byte(`{"type":"codex","name":"Codex","provider_key":"","model_key":"",` +
			`"env_json":"{}","reasoning_effort":"","config":{"sandbox":"read-only"},"account_default":true}`),
	}, nil)
	require.NoError(t, err)
	assert.Equal(t, "ep-1", created.SyncID, "沿用源端同步标识")
	assert.Equal(t, "read-only", created.Sandbox, "config 摊回独占字段")
	assert.Equal(t, consts.ACTIVE, created.Status)
}

func TestEngineProfileAdapter_ApplyExistingRowKeepsDefaultMarkUntouchedWhenNotFlagged(t *testing.T) {
	ctrl := gomock.NewController(t)
	state := mock_syncstate_repo.NewMockSyncStateRepo(ctrl)
	stateFinds(state, syncwire.KindEngineProfile, "ep-1", func(dest any) {
		*dest.(*agent_backend_entity.EngineProfile) = agent_backend_entity.EngineProfile{
			ID: 3, Type: "codex", Name: "Old", IsAccountDefault: true,
			SyncMeta: syncmeta_entity.SyncMeta{SyncID: "ep-1"},
		}
	})
	syncstate_repo.RegisterSyncState(state)
	profiles := mock_agent_backend_repo.NewMockEngineProfileRepo(ctrl)
	profiles.EXPECT().Update(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, p *agent_backend_entity.EngineProfile) error {
			assert.Equal(t, "Renamed", p.Name)
			assert.Equal(t, int64(3), p.ID)
			return nil
		})
	agent_backend_repo.RegisterEngineProfile(profiles) // 没有 SetAccountDefault 的期望：调用即失败

	require.NoError(t, engineProfileAdapter{}.apply(context.Background(), &inbound{
		Kind: syncwire.KindEngineProfile, SyncID: "ep-1",
		Payload: []byte(`{"type":"codex","name":"Renamed","config":{}}`),
	}, nil))
}

func TestEngineProfileAdapter_GivenInvalidPayload_ThenNothingIsWritten(t *testing.T) {
	ctrl := gomock.NewController(t)
	state := mock_syncstate_repo.NewMockSyncStateRepo(ctrl)
	stateMisses(state, syncwire.KindEngineProfile, "ep-bad")
	syncstate_repo.RegisterSyncState(state)
	agent_backend_repo.RegisterEngineProfile(mock_agent_backend_repo.NewMockEngineProfileRepo(ctrl))

	err := engineProfileAdapter{}.apply(context.Background(), &inbound{
		Kind: syncwire.KindEngineProfile, SyncID: "ep-bad",
		Payload: []byte(`{"type":"no-such-runtime","name":"x","config":{}}`),
	}, nil)
	assert.Error(t, err)
}

func TestEngineProfileAdapter_RemoveSoftDeletesAndFallsBackReferences(t *testing.T) {
	ctrl := gomock.NewController(t)
	state := mock_syncstate_repo.NewMockSyncStateRepo(ctrl)
	state.EXPECT().FindLocalID(gomock.Any(), syncwire.KindEngineProfile, "ep-1").Return(int64(3), nil)
	syncstate_repo.RegisterSyncState(state)
	profiles := mock_agent_backend_repo.NewMockEngineProfileRepo(ctrl)
	profiles.EXPECT().Delete(gomock.Any(), int64(3)).Return(nil)
	agent_backend_repo.RegisterEngineProfile(profiles)
	agents := mock_agent_repo.NewMockAgentRepo(ctrl)
	agents.EXPECT().ClearEngineProfile(gomock.Any(), int64(3)).Return(nil)
	agent_repo.RegisterAgent(agents)
	depts := mock_department_repo.NewMockDepartmentRepo(ctrl)
	depts.EXPECT().ClearDefaultEngineProfile(gomock.Any(), int64(3)).Return(nil)
	department_repo.RegisterDepartment(depts)

	require.NoError(t, engineProfileAdapter{}.remove(context.Background(),
		&inbound{Kind: syncwire.KindEngineProfile, SyncID: "ep-1", DeletedAt: 1}))
}

// ── agent 执行设置 ──────────────────────────────────────────────────────────

func TestAgentAdapter_LoadCarriesEngineSettingsByProfileSyncID(t *testing.T) {
	ctrl := gomock.NewController(t)
	state := mock_syncstate_repo.NewMockSyncStateRepo(ctrl)
	stateFinds(state, syncwire.KindAgent, "ag-1", func(dest any) {
		*dest.(*agent_entity.Agent) = agent_entity.Agent{
			ID: 4, Name: "Nova", EngineProfileID: 3,
			ExecConstraintJSON: `{"mode":"devices","devices":["sha256:dev-a"]}`,
			SkillGrantsRuntime: "claudecode", SkillGrantsJSON: `[{"id":"s"}]`,
			SyncMeta: syncmeta_entity.SyncMeta{SyncID: "ag-1"},
		}
	})
	syncstate_repo.RegisterSyncState(state)
	profiles := mock_agent_backend_repo.NewMockEngineProfileRepo(ctrl)
	profiles.EXPECT().Find(gomock.Any(), int64(3)).Return(&agent_backend_entity.EngineProfile{
		ID: 3, SyncMeta: syncmeta_entity.SyncMeta{SyncID: "ep-1"},
	}, nil)
	agent_backend_repo.RegisterEngineProfile(profiles)

	out, err := (&agentAdapter{}).load(context.Background(), "ag-1")
	require.NoError(t, err)
	var payload syncwire.AgentPayload
	require.NoError(t, json.Unmarshal(out.Payload, &payload))
	require.NotNil(t, payload.Engine)
	assert.Equal(t, "ep-1", payload.Engine.EngineProfileSyncID, "引用走同步标识，不是本地 id")
	assert.Equal(t, syncwire.ExecConstraintPayload{Mode: "devices", Devices: []string{"sha256:dev-a"}}, payload.Engine.ExecConstraint)
	assert.Equal(t, "claudecode", payload.Engine.SkillGrantsRuntime)
	assert.Equal(t, `[{"id":"s"}]`, payload.Engine.SkillGrantsJSON)
	assert.NoError(t, syncwire.GuardPayload(syncwire.KindAgent, out.Payload))
}

func TestAgentAdapter_RefsIncludeTheEngineProfile(t *testing.T) {
	refs := (&agentAdapter{}).refs(&inbound{Payload: []byte(`{"engine":{"engine_profile_sync_id":"ep-1","exec_constraint":{"mode":"any"}}}`)})
	assert.Contains(t, refs, ref{Kind: syncwire.KindEngineProfile, SyncID: "ep-1"})
}

func TestAgentAdapter_ApplyWritesEngineSettingsThroughTheNarrowSetter(t *testing.T) {
	ctrl := gomock.NewController(t)
	state := mock_syncstate_repo.NewMockSyncStateRepo(ctrl)
	stateFinds(state, syncwire.KindAgent, "ag-1", func(dest any) {
		*dest.(*agent_entity.Agent) = agent_entity.Agent{ID: 4, SyncMeta: syncmeta_entity.SyncMeta{SyncID: "ag-1"}}
	})
	syncstate_repo.RegisterSyncState(state)
	agents := mock_agent_repo.NewMockAgentRepo(ctrl)
	agents.EXPECT().UpdateRow(gomock.Any(), gomock.Any()).Return(nil)
	var settings *agent_entity.Agent
	agents.EXPECT().UpdateEngineSettings(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, a *agent_entity.Agent) error { settings = a; return nil })
	agent_repo.RegisterAgent(agents)

	err := (&agentAdapter{}).apply(context.Background(), &inbound{
		Kind: syncwire.KindAgent, SyncID: "ag-1",
		Payload: []byte(`{"name":"Nova","engine":{"engine_profile_sync_id":"ep-1",` +
			`"exec_constraint":{"mode":"labels","labels":["gpu"]},"skill_grants_runtime":"codex","skill_grants_json":"[]"}}`),
	}, map[string]int64{"engine_profile:ep-1": 3})
	require.NoError(t, err)
	assert.Equal(t, int64(4), settings.ID)
	assert.Equal(t, int64(3), settings.EngineProfileID, "同步标识解析成本地 id")
	assert.Equal(t, agent_entity.ExecConstraint{Mode: "labels", Labels: []string{"gpu"}}, settings.GetExecConstraint())
	assert.Equal(t, "codex", settings.SkillGrantsRuntime)
}

func TestAgentAdapter_GivenPayloadWithoutEngine_ThenLocalEngineSettingsAreLeftAlone(t *testing.T) {
	ctrl := gomock.NewController(t)
	state := mock_syncstate_repo.NewMockSyncStateRepo(ctrl)
	stateFinds(state, syncwire.KindAgent, "ag-1", func(dest any) {
		*dest.(*agent_entity.Agent) = agent_entity.Agent{ID: 4, EngineProfileID: 3, SyncMeta: syncmeta_entity.SyncMeta{SyncID: "ag-1"}}
	})
	syncstate_repo.RegisterSyncState(state)
	agents := mock_agent_repo.NewMockAgentRepo(ctrl)
	agents.EXPECT().UpdateRow(gomock.Any(), gomock.Any()).Return(nil) // 没有 UpdateEngineSettings 期望
	agent_repo.RegisterAgent(agents)

	require.NoError(t, (&agentAdapter{}).apply(context.Background(), &inbound{
		Kind: syncwire.KindAgent, SyncID: "ag-1", Payload: []byte(`{"name":"Nova"}`),
	}, map[string]int64{}))
}

func TestAgentAdapter_GivenInvalidConstraint_ThenApplyFailsBeforeAnyWrite(t *testing.T) {
	ctrl := gomock.NewController(t)
	state := mock_syncstate_repo.NewMockSyncStateRepo(ctrl)
	stateFinds(state, syncwire.KindAgent, "ag-1", func(dest any) {
		*dest.(*agent_entity.Agent) = agent_entity.Agent{ID: 4, SyncMeta: syncmeta_entity.SyncMeta{SyncID: "ag-1"}}
	})
	syncstate_repo.RegisterSyncState(state)
	agent_repo.RegisterAgent(mock_agent_repo.NewMockAgentRepo(ctrl))

	err := (&agentAdapter{}).apply(context.Background(), &inbound{
		Kind: syncwire.KindAgent, SyncID: "ag-1",
		Payload: []byte(`{"name":"Nova","engine":{"exec_constraint":{"mode":"labels"}}}`),
	}, map[string]int64{})
	assert.Error(t, err, "labels 模式至少一个标签")
}

// ── department / project / provider ────────────────────────────────────────

func TestDepartmentAdapter_RoundTripsDefaultEngineProfile(t *testing.T) {
	ctrl := gomock.NewController(t)
	state := mock_syncstate_repo.NewMockSyncStateRepo(ctrl)
	stateFinds(state, syncwire.KindDepartment, "d-1", func(dest any) {
		*dest.(*department_entity.Department) = department_entity.Department{
			ID: 2, Name: "Ops", DefaultEngineProfileID: 3, SyncMeta: syncmeta_entity.SyncMeta{SyncID: "d-1"},
		}
	})
	syncstate_repo.RegisterSyncState(state)
	profiles := mock_agent_backend_repo.NewMockEngineProfileRepo(ctrl)
	profiles.EXPECT().Find(gomock.Any(), int64(3)).Return(&agent_backend_entity.EngineProfile{
		ID: 3, SyncMeta: syncmeta_entity.SyncMeta{SyncID: "ep-1"},
	}, nil)
	agent_backend_repo.RegisterEngineProfile(profiles)

	out, err := departmentAdapter{}.load(context.Background(), "d-1")
	require.NoError(t, err)
	var payload syncwire.DepartmentPayload
	require.NoError(t, json.Unmarshal(out.Payload, &payload))
	require.NotNil(t, payload.DefaultEngineProfileSyncID)
	assert.Equal(t, "ep-1", *payload.DefaultEngineProfileSyncID)

	assert.Contains(t, departmentAdapter{}.refs(&inbound{Payload: out.Payload}),
		ref{Kind: syncwire.KindEngineProfile, SyncID: "ep-1"})
}

func TestDepartmentAdapter_ApplyWritesDefaultEngineThroughTheNarrowSetter(t *testing.T) {
	ctrl := gomock.NewController(t)
	state := mock_syncstate_repo.NewMockSyncStateRepo(ctrl)
	stateMisses(state, syncwire.KindDepartment, "d-1")
	syncstate_repo.RegisterSyncState(state)
	depts := mock_department_repo.NewMockDepartmentRepo(ctrl)
	depts.EXPECT().Create(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, d *department_entity.Department) error { d.ID = 7; return nil })
	depts.EXPECT().UpdateDefaultEngineProfile(gomock.Any(), int64(7), int64(3)).Return(nil)
	department_repo.RegisterDepartment(depts)

	require.NoError(t, departmentAdapter{}.apply(context.Background(), &inbound{
		Kind: syncwire.KindDepartment, SyncID: "d-1",
		Payload: []byte(`{"name":"Ops","default_engine_profile_sync_id":"ep-1"}`),
	}, map[string]int64{"engine_profile:ep-1": 3}))
}

func TestDepartmentAdapter_GivenEmptyDefaultEngine_ThenItClearsTheLocalOne(t *testing.T) {
	ctrl := gomock.NewController(t)
	state := mock_syncstate_repo.NewMockSyncStateRepo(ctrl)
	stateFinds(state, syncwire.KindDepartment, "d-1", func(dest any) {
		*dest.(*department_entity.Department) = department_entity.Department{ID: 7, DefaultEngineProfileID: 3,
			SyncMeta: syncmeta_entity.SyncMeta{SyncID: "d-1"}}
	})
	syncstate_repo.RegisterSyncState(state)
	depts := mock_department_repo.NewMockDepartmentRepo(ctrl)
	depts.EXPECT().Update(gomock.Any(), gomock.Any()).Return(nil)
	depts.EXPECT().UpdateDefaultEngineProfile(gomock.Any(), int64(7), int64(0)).Return(nil)
	department_repo.RegisterDepartment(depts)

	require.NoError(t, departmentAdapter{}.apply(context.Background(), &inbound{
		Kind: syncwire.KindDepartment, SyncID: "d-1",
		Payload: []byte(`{"name":"Ops","default_engine_profile_sync_id":""}`),
	}, map[string]int64{}))
}

func TestProjectAdapter_RoundTripsDefaultDevice(t *testing.T) {
	ctrl := gomock.NewController(t)
	state := mock_syncstate_repo.NewMockSyncStateRepo(ctrl)
	stateFinds(state, syncwire.KindProject, "p-1", func(dest any) {
		*dest.(*project_entity.Project) = project_entity.Project{ID: 5, Name: "Atlas",
			DefaultDeviceFingerprint: "sha256:dev-a", SyncMeta: syncmeta_entity.SyncMeta{SyncID: "p-1"}}
	})
	syncstate_repo.RegisterSyncState(state)
	out, err := projectAdapter{}.load(context.Background(), "p-1")
	require.NoError(t, err)
	var payload syncwire.ProjectPayload
	require.NoError(t, json.Unmarshal(out.Payload, &payload))
	require.NotNil(t, payload.DefaultDeviceFingerprint)
	assert.Equal(t, "sha256:dev-a", *payload.DefaultDeviceFingerprint)

	state2 := mock_syncstate_repo.NewMockSyncStateRepo(ctrl)
	stateFinds(state2, syncwire.KindProject, "p-1", func(dest any) {
		*dest.(*project_entity.Project) = project_entity.Project{ID: 5, SyncMeta: syncmeta_entity.SyncMeta{SyncID: "p-1"}}
	})
	syncstate_repo.RegisterSyncState(state2)
	projects := mock_project_repo.NewMockProjectRepo(ctrl)
	projects.EXPECT().Update(gomock.Any(), gomock.Any()).Return(nil)
	projects.EXPECT().SetDefaultDevice(gomock.Any(), int64(5), devicefp.Carrier("sha256:dev-a")).Return(nil)
	project_repo.RegisterProject(projects)
	require.NoError(t, projectAdapter{}.apply(context.Background(), &inbound{
		Kind: syncwire.KindProject, SyncID: "p-1", Payload: out.Payload}, map[string]int64{}))
}

func TestProjectAdapter_GivenPayloadWithoutDefaultDevice_ThenLocalOneIsKept(t *testing.T) {
	ctrl := gomock.NewController(t)
	state := mock_syncstate_repo.NewMockSyncStateRepo(ctrl)
	stateFinds(state, syncwire.KindProject, "p-1", func(dest any) {
		*dest.(*project_entity.Project) = project_entity.Project{ID: 5, SyncMeta: syncmeta_entity.SyncMeta{SyncID: "p-1"}}
	})
	syncstate_repo.RegisterSyncState(state)
	projects := mock_project_repo.NewMockProjectRepo(ctrl)
	projects.EXPECT().Update(gomock.Any(), gomock.Any()).Return(nil)
	project_repo.RegisterProject(projects)
	require.NoError(t, projectAdapter{}.apply(context.Background(), &inbound{
		Kind: syncwire.KindProject, SyncID: "p-1", Payload: []byte(`{"name":"x"}`)}, map[string]int64{}))
}

func TestLLMProviderAdapter_RoundTripsReach(t *testing.T) {
	ctrl := gomock.NewController(t)
	state := mock_syncstate_repo.NewMockSyncStateRepo(ctrl)
	stateFinds(state, syncwire.KindLLMProvider, "pk", func(dest any) {
		*dest.(*llm_provider_entity.LLMProvider) = llm_provider_entity.LLMProvider{
			ID: 8, ProviderKey: "pk", ReachMode: llm_provider_entity.ReachDevices, ReachDevicesJSON: `["sha256:dev-a"]`,
		}
	})
	syncstate_repo.RegisterSyncState(state)
	providers := mock_llm_provider_repo.NewMockLLMProviderRepo(ctrl)
	providers.EXPECT().ListModels(gomock.Any(), int64(8)).Return(nil, nil)
	llm_provider_repo.RegisterLLMProvider(providers)

	out, err := llmProviderAdapter{}.load(context.Background(), "pk")
	require.NoError(t, err)
	var payload syncwire.LLMProviderPayload
	require.NoError(t, json.Unmarshal(out.Payload, &payload))
	assert.Equal(t, &syncwire.LLMProviderReach{Mode: "devices", Devices: []string{"sha256:dev-a"}}, payload.Reach)
	assert.NoError(t, syncwire.GuardPayload(syncwire.KindLLMProvider, out.Payload))

	providers2 := mock_llm_provider_repo.NewMockLLMProviderRepo(ctrl)
	providers2.EXPECT().UpsertFromSync(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
	providers2.EXPECT().FindByKey(gomock.Any(), "pk").Return(&llm_provider_entity.LLMProvider{ID: 8, ProviderKey: "pk"}, nil)
	providers2.EXPECT().UpdateReach(gomock.Any(), int64(8), "devices", `["sha256:dev-a"]`).Return(nil)
	llm_provider_repo.RegisterLLMProvider(providers2)
	require.NoError(t, llmProviderAdapter{}.apply(context.Background(), &inbound{
		Kind: syncwire.KindLLMProvider, SyncID: "pk", Payload: out.Payload}, nil))
}

func TestLLMProviderAdapter_GivenDevicesReachWithoutDevices_ThenApplyFailsBeforeUpsert(t *testing.T) {
	ctrl := gomock.NewController(t)
	llm_provider_repo.RegisterLLMProvider(mock_llm_provider_repo.NewMockLLMProviderRepo(ctrl))
	err := llmProviderAdapter{}.apply(context.Background(), &inbound{
		Kind: syncwire.KindLLMProvider, SyncID: "pk",
		Payload: []byte(`{"name":"n","reach":{"mode":"devices"}}`)}, nil)
	assert.Error(t, err)
}

func TestLLMProviderAdapter_GivenPayloadWithoutReach_ThenLocalReachIsKept(t *testing.T) {
	ctrl := gomock.NewController(t)
	providers := mock_llm_provider_repo.NewMockLLMProviderRepo(ctrl)
	providers.EXPECT().UpsertFromSync(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
	llm_provider_repo.RegisterLLMProvider(providers)
	require.NoError(t, llmProviderAdapter{}.apply(context.Background(), &inbound{
		Kind: syncwire.KindLLMProvider, SyncID: "pk", Payload: []byte(`{"name":"n"}`)}, nil))
}

// ── device_pref / device_cli_path ───────────────────────────────────────────

func TestDevicePrefAdapter_LoadUsesFingerprintColumn(t *testing.T) {
	ctrl := gomock.NewController(t)
	state := mock_syncstate_repo.NewMockSyncStateRepo(ctrl)
	stateFinds(state, syncwire.KindDevicePref, "dp-1", func(dest any) {
		*dest.(*device_pref_entity.DevicePref) = device_pref_entity.DevicePref{
			DeviceFingerprint: "sha256:dev-a", LabelsJSON: `["gpu","home"]`, SortOrder: 2,
			SyncMeta: syncmeta_entity.SyncMeta{SyncID: "dp-1"},
		}
	})
	syncstate_repo.RegisterSyncState(state)
	out, err := devicePrefAdapter{}.load(context.Background(), "dp-1")
	require.NoError(t, err)
	assert.Equal(t, devicefp.Carrier("sha256:dev-a"), out.AgentredFingerprint)
	assert.JSONEq(t, `{"labels":["gpu","home"],"sort_order":2}`, string(out.Payload))
	assert.NoError(t, syncwire.GuardPayload(syncwire.KindDevicePref, out.Payload))
}

func TestDevicePrefAdapter_ApplyCreatesRowForANewDevice(t *testing.T) {
	ctrl := gomock.NewController(t)
	prefs := mock_device_pref_repo.NewMockDevicePrefRepo(ctrl)
	prefs.EXPECT().List(gomock.Any()).Return(nil, nil)
	var created *device_pref_entity.DevicePref
	prefs.EXPECT().Create(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, p *device_pref_entity.DevicePref) error { created = p; return nil })
	device_pref_repo.RegisterDevicePref(prefs)

	require.NoError(t, devicePrefAdapter{}.apply(context.Background(), &inbound{
		Kind: syncwire.KindDevicePref, SyncID: "dp-1", AgentredFingerprint: "sha256:dev-a",
		Payload: []byte(`{"labels":[" gpu ","gpu",""],"sort_order":3}`)}, nil))
	assert.Equal(t, "dp-1", created.SyncID)
	assert.Equal(t, devicefp.Carrier("sha256:dev-a"), created.DeviceFingerprint)
	assert.Equal(t, []string{"gpu"}, created.Labels(), "标签在边界归一")
	assert.Equal(t, 3, created.SortOrder)
}

func TestDevicePrefAdapter_ApplyOnHeldDeviceUsesNarrowSetters(t *testing.T) {
	ctrl := gomock.NewController(t)
	prefs := mock_device_pref_repo.NewMockDevicePrefRepo(ctrl)
	prefs.EXPECT().List(gomock.Any()).Return([]*device_pref_entity.DevicePref{{
		ID: 1, DeviceFingerprint: "sha256:dev-a", SyncMeta: syncmeta_entity.SyncMeta{SyncID: "dp-local"},
	}}, nil)
	prefs.EXPECT().SetLabels(gomock.Any(), devicefp.Carrier("sha256:dev-a"), `["gpu"]`).Return(nil)
	prefs.EXPECT().SetSortOrder(gomock.Any(), devicefp.Carrier("sha256:dev-a"), 4).Return(nil)
	device_pref_repo.RegisterDevicePref(prefs)

	require.NoError(t, devicePrefAdapter{}.apply(context.Background(), &inbound{
		Kind: syncwire.KindDevicePref, SyncID: "dp-remote", AgentredFingerprint: "sha256:dev-a",
		Payload: []byte(`{"labels":["gpu"],"sort_order":4}`)}, nil))
}

func TestDevicePrefAdapter_GivenNoFingerprint_ThenApplyFails(t *testing.T) {
	ctrl := gomock.NewController(t)
	device_pref_repo.RegisterDevicePref(mock_device_pref_repo.NewMockDevicePrefRepo(ctrl))
	assert.Error(t, devicePrefAdapter{}.apply(context.Background(), &inbound{
		Kind: syncwire.KindDevicePref, SyncID: "dp-1", Payload: []byte(`{"labels":[]}`)}, nil))
}

func TestDeviceCLIPathAdapter_LoadUsesNaturalKeyColumns(t *testing.T) {
	ctrl := gomock.NewController(t)
	state := mock_syncstate_repo.NewMockSyncStateRepo(ctrl)
	stateFinds(state, syncwire.KindDeviceCLIPath, "cp-1", func(dest any) {
		*dest.(*device_pref_entity.CLIPathOverride) = device_pref_entity.CLIPathOverride{
			RuntimeType: "claudecode", DeviceFingerprint: "sha256:dev-a", CLIPath: "/opt/claude",
			SyncMeta: syncmeta_entity.SyncMeta{SyncID: "cp-1"},
		}
	})
	syncstate_repo.RegisterSyncState(state)
	out, err := deviceCLIPathAdapter{}.load(context.Background(), "cp-1")
	require.NoError(t, err)
	assert.Equal(t, "claudecode", out.ScopeSyncID, "运行时类型在 scope 列")
	assert.Equal(t, devicefp.Carrier("sha256:dev-a"), out.AgentredFingerprint)
	assert.JSONEq(t, `{"cli_path":"/opt/claude"}`, string(out.Payload))
}

func TestDeviceCLIPathAdapter_ApplyCreatesThenRemoveDeletes(t *testing.T) {
	ctrl := gomock.NewController(t)
	state := mock_syncstate_repo.NewMockSyncStateRepo(ctrl)
	stateFinds(state, syncwire.KindDeviceCLIPath, "cp-1", func(dest any) {
		*dest.(*device_pref_entity.CLIPathOverride) = device_pref_entity.CLIPathOverride{ID: 6}
	})
	syncstate_repo.RegisterSyncState(state)
	paths := mock_device_pref_repo.NewMockCLIPathRepo(ctrl)
	paths.EXPECT().Find(gomock.Any(), "claudecode", devicefp.Carrier("sha256:dev-a")).Return(nil, nil)
	var created *device_pref_entity.CLIPathOverride
	paths.EXPECT().Create(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, o *device_pref_entity.CLIPathOverride) error { created = o; return nil })
	paths.EXPECT().Delete(gomock.Any(), int64(6)).Return(nil)
	device_pref_repo.RegisterCLIPath(paths)

	require.NoError(t, deviceCLIPathAdapter{}.apply(context.Background(), &inbound{
		Kind: syncwire.KindDeviceCLIPath, SyncID: "cp-1", ScopeSyncID: "claudecode", AgentredFingerprint: "sha256:dev-a",
		Payload: []byte(`{"cli_path":"/opt/claude"}`)}, nil))
	assert.Equal(t, "cp-1", created.SyncID)
	assert.Equal(t, "/opt/claude", created.CLIPath)
	assert.Equal(t, consts.ACTIVE, created.Status)

	require.NoError(t, deviceCLIPathAdapter{}.remove(context.Background(), &inbound{
		Kind: syncwire.KindDeviceCLIPath, SyncID: "cp-1", DeletedAt: 1}))
}

func TestDeviceCLIPathAdapter_GivenHeldNaturalKey_ThenOnlyThePathIsUpdated(t *testing.T) {
	ctrl := gomock.NewController(t)
	paths := mock_device_pref_repo.NewMockCLIPathRepo(ctrl)
	paths.EXPECT().Find(gomock.Any(), "codex", devicefp.Carrier("sha256:dev-a")).
		Return(&device_pref_entity.CLIPathOverride{ID: 2, RuntimeType: "codex", DeviceFingerprint: "sha256:dev-a"}, nil)
	paths.EXPECT().UpdatePath(gomock.Any(), int64(2), "/new/codex").Return(nil)
	device_pref_repo.RegisterCLIPath(paths)
	require.NoError(t, deviceCLIPathAdapter{}.apply(context.Background(), &inbound{
		Kind: syncwire.KindDeviceCLIPath, SyncID: "cp-remote", ScopeSyncID: "codex", AgentredFingerprint: "sha256:dev-a",
		Payload: []byte(`{"cli_path":"/new/codex"}`)}, nil))
}

func TestDeviceCLIPathAdapter_GivenRuntimeWithoutCLI_ThenApplyFails(t *testing.T) {
	ctrl := gomock.NewController(t)
	paths := mock_device_pref_repo.NewMockCLIPathRepo(ctrl)
	paths.EXPECT().Find(gomock.Any(), "hermes", gomock.Any()).Return(nil, nil).AnyTimes()
	device_pref_repo.RegisterCLIPath(paths)
	assert.Error(t, deviceCLIPathAdapter{}.apply(context.Background(), &inbound{
		Kind: syncwire.KindDeviceCLIPath, SyncID: "cp-1", ScopeSyncID: "hermes", AgentredFingerprint: "sha256:dev-a",
		Payload: []byte(`{"cli_path":"/x"}`)}, nil))
}

func TestAgentAdapter_ApplyNewRowWritesEngineSettingsAfterCreate(t *testing.T) {
	ctrl := gomock.NewController(t)
	state := mock_syncstate_repo.NewMockSyncStateRepo(ctrl)
	stateMisses(state, syncwire.KindAgent, "ag-new")
	syncstate_repo.RegisterSyncState(state)
	agents := mock_agent_repo.NewMockAgentRepo(ctrl)
	gomock.InOrder(
		agents.EXPECT().Create(gomock.Any(), gomock.Any()).DoAndReturn(
			func(_ context.Context, a *agent_entity.Agent) error { a.ID = 12; return nil }),
		agents.EXPECT().UpdateEngineSettings(gomock.Any(), gomock.Any()).DoAndReturn(
			func(_ context.Context, a *agent_entity.Agent) error {
				assert.Equal(t, int64(12), a.ID, "窄写口要拿到刚建好的本地 id")
				assert.Equal(t, int64(3), a.EngineProfileID)
				return nil
			}),
	)
	agent_repo.RegisterAgent(agents)

	require.NoError(t, (&agentAdapter{}).apply(context.Background(), &inbound{
		Kind: syncwire.KindAgent, SyncID: "ag-new",
		Payload: []byte(`{"name":"Nova","engine":{"engine_profile_sync_id":"ep-1","exec_constraint":{"mode":"any"}}}`),
	}, map[string]int64{"engine_profile:ep-1": 3}))
}

func TestDevicePrefAdapter_RemoveSoftDeletesTheRow(t *testing.T) {
	ctrl := gomock.NewController(t)
	state := mock_syncstate_repo.NewMockSyncStateRepo(ctrl)
	stateFinds(state, syncwire.KindDevicePref, "dp-1", func(dest any) {
		*dest.(*device_pref_entity.DevicePref) = device_pref_entity.DevicePref{ID: 9, DeviceFingerprint: "sha256:dev-a"}
	})
	syncstate_repo.RegisterSyncState(state)
	prefs := mock_device_pref_repo.NewMockDevicePrefRepo(ctrl)
	prefs.EXPECT().Delete(gomock.Any(), int64(9)).Return(nil)
	device_pref_repo.RegisterDevicePref(prefs)

	require.NoError(t, devicePrefAdapter{}.remove(context.Background(), &inbound{
		Kind: syncwire.KindDevicePref, SyncID: "dp-1", DeletedAt: 1}))
}
