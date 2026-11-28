package data_svc_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/cago-frame/cago/pkg/consts"
	. "github.com/smartystreets/goconvey/convey"
	"go.uber.org/mock/gomock"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/agent_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/department_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/device_pref_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/llm_provider_entity"
	"github.com/agentre-hub/agentre/internal/repository/agent_backend_repo"
	"github.com/agentre-hub/agentre/internal/repository/agent_backend_repo/mock_agent_backend_repo"
	"github.com/agentre-hub/agentre/internal/repository/device_pref_repo"
	"github.com/agentre-hub/agentre/internal/repository/device_pref_repo/mock_device_pref_repo"
	"github.com/agentre-hub/agentre/internal/service/data_svc"
	"github.com/agentre-hub/agentre/pkg/syncwire"
	"github.com/agentre-hub/agentre/pkg/wire/devicefp"
)

type engineMocks struct {
	profiles *mock_agent_backend_repo.MockEngineProfileRepo
	prefs    *mock_device_pref_repo.MockDevicePrefRepo
	paths    *mock_device_pref_repo.MockCLIPathRepo
}

func setupEngineMocks(t *testing.T) *engineMocks {
	t.Helper()
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)
	e := &engineMocks{
		profiles: mock_agent_backend_repo.NewMockEngineProfileRepo(ctrl),
		prefs:    mock_device_pref_repo.NewMockDevicePrefRepo(ctrl),
		paths:    mock_device_pref_repo.NewMockCLIPathRepo(ctrl),
	}
	agent_backend_repo.RegisterEngineProfile(e.profiles)
	device_pref_repo.RegisterDevicePref(e.prefs)
	device_pref_repo.RegisterCLIPath(e.paths)
	return e
}

func TestExport_EngineProfiles_CarryConfigAndDefaultMarkButNoDeviceOrCLIPath(t *testing.T) {
	m := setupDataSvcTest(t)
	e := setupEngineMocks(t)
	e.profiles.EXPECT().List(gomock.Any()).Return([]*agent_backend_entity.EngineProfile{{
		ID: 3, Type: "codex", Name: "Codex 主力", LLMProviderKey: "openai-main", LLMModelKey: "gpt",
		EnvJSON: `{"K":"V"}`, ReasoningEffort: "high", Sandbox: "workspace-write", IsAccountDefault: true,
	}}, nil)

	Convey("engine-profiles scope", t, func() {
		res, err := m.svc.Export(m.ctx, &data_svc.ExportRequest{Scopes: []string{string(data_svc.ScopeEngineProfiles)}})
		So(err, ShouldBeNil)
		var b data_svc.BundleV1
		So(json.Unmarshal(res.JSON, &b), ShouldBeNil)
		So(b.Items.EngineProfiles, ShouldHaveLength, 1)
		p := b.Items.EngineProfiles[0]
		So(p.ExportKey, ShouldNotBeEmpty)
		So(p.Type, ShouldEqual, "codex")
		So(p.LLMProviderKey, ShouldEqual, "openai-main")
		So(p.Config.Sandbox, ShouldEqual, "workspace-write")
		So(p.AccountDefault, ShouldBeTrue)
		So(res.Summary[string(data_svc.ScopeEngineProfiles)], ShouldEqual, 1)
		So(string(res.JSON), ShouldNotContainSubstring, "cliPath")
		So(string(res.JSON), ShouldNotContainSubstring, "deviceId")
	})
}

func TestExport_Organization_CarriesEngineRefsThroughSharedExportKeys(t *testing.T) {
	m := setupDataSvcTest(t)
	e := setupEngineMocks(t)
	e.profiles.EXPECT().List(gomock.Any()).Return([]*agent_backend_entity.EngineProfile{
		{ID: 3, Type: "codex", Name: "Codex"},
	}, nil)
	m.depts.EXPECT().List(gomock.Any()).Return([]*department_entity.Department{
		{ID: 10, Name: "Eng", DefaultEngineProfileID: 3},
	}, nil)
	m.agents.EXPECT().List(gomock.Any()).Return([]*agent_entity.Agent{{
		ID: 20, Name: "Lead", DepartmentID: 10, EngineProfileID: 3,
		ExecConstraintJSON: `{"mode":"labels","labels":["gpu"]}`,
		SkillGrantsRuntime: "codex", SkillGrantsJSON: `[{"id":"s"}]`,
	}}, nil)
	m.backends.EXPECT().List(gomock.Any()).Return(nil, nil)
	m.execTargets.EXPECT().ListByAgents(gomock.Any(), gomock.Any()).Return(nil, nil)

	Convey("organization + engine-profiles 共用同一份 exportKey", t, func() {
		res, err := m.svc.Export(m.ctx, &data_svc.ExportRequest{
			Scopes: []string{string(data_svc.ScopeOrganization), string(data_svc.ScopeEngineProfiles)}})
		So(err, ShouldBeNil)
		var b data_svc.BundleV1
		So(json.Unmarshal(res.JSON, &b), ShouldBeNil)
		key := b.Items.EngineProfiles[0].ExportKey
		So(b.Items.Departments[0].DefaultEngineKey, ShouldEqual, key)
		a := b.Items.Agents[0]
		So(a.EngineKey, ShouldEqual, key)
		So(a.ExecConstraint, ShouldResemble, &data_svc.BundleExecConstraint{Mode: "labels", Labels: []string{"gpu"}})
		So(a.SkillGrantsRuntime, ShouldEqual, "codex")
		So(a.SkillGrantsJSON, ShouldEqual, `[{"id":"s"}]`)
	})
}

func TestExport_LLMProviders_CarryReachOnlyWhenRestricted(t *testing.T) {
	m := setupDataSvcTest(t)
	m.providers.EXPECT().List(gomock.Any()).Return([]*llm_provider_entity.LLMProvider{
		{ID: 1, ProviderKey: "local", ReachMode: llm_provider_entity.ReachDevices, ReachDevicesJSON: `["sha256:dev-a"]`},
		{ID: 2, ProviderKey: "cloud", ReachMode: llm_provider_entity.ReachAny},
	}, nil)
	m.providers.EXPECT().ListModels(gomock.Any(), gomock.Any()).Return(nil, nil).Times(2)

	Convey("供应商可达范围", t, func() {
		res, err := m.svc.Export(m.ctx, &data_svc.ExportRequest{Scopes: []string{string(data_svc.ScopeLLMProviders)}})
		So(err, ShouldBeNil)
		var b data_svc.BundleV1
		So(json.Unmarshal(res.JSON, &b), ShouldBeNil)
		So(b.Items.LLMProviders[0].Reach, ShouldResemble, &data_svc.BundleReach{Mode: "devices", Devices: []string{"sha256:dev-a"}})
		So(b.Items.LLMProviders[1].Reach, ShouldBeNil)
	})
}

func TestExport_DevicePrefs_CarryLabelsOrderAndCLIPaths(t *testing.T) {
	m := setupDataSvcTest(t)
	e := setupEngineMocks(t)
	e.prefs.EXPECT().List(gomock.Any()).Return([]*device_pref_entity.DevicePref{
		{DeviceFingerprint: "sha256:dev-a", LabelsJSON: `["gpu"]`, SortOrder: 1},
	}, nil)
	e.paths.EXPECT().List(gomock.Any()).Return([]*device_pref_entity.CLIPathOverride{
		{RuntimeType: "claudecode", DeviceFingerprint: "sha256:dev-a", CLIPath: "/opt/claude"},
	}, nil)

	Convey("device-prefs scope", t, func() {
		res, err := m.svc.Export(m.ctx, &data_svc.ExportRequest{Scopes: []string{string(data_svc.ScopeDevicePrefs)}})
		So(err, ShouldBeNil)
		var b data_svc.BundleV1
		So(json.Unmarshal(res.JSON, &b), ShouldBeNil)
		So(b.Items.DevicePrefs, ShouldResemble, []data_svc.BundleDevicePref{
			{DeviceFingerprint: "sha256:dev-a", Labels: []string{"gpu"}, SortOrder: 1}})
		So(b.Items.DeviceCLIPaths, ShouldResemble, []data_svc.BundleDeviceCLIPath{
			{RuntimeType: "claudecode", DeviceFingerprint: "sha256:dev-a", CLIPath: "/opt/claude"}})
		So(res.Summary[string(data_svc.ScopeDevicePrefs)], ShouldEqual, 2)
	})
}

func TestPreviewImport_EngineProfileConflictsByName(t *testing.T) {
	m := setupDataSvcTest(t)
	e := setupEngineMocks(t)
	m.providers.EXPECT().List(gomock.Any()).Return(nil, nil)
	m.devices.EXPECT().List(gomock.Any()).Return(nil, nil)
	m.backends.EXPECT().List(gomock.Any()).Return(nil, nil)
	m.depts.EXPECT().List(gomock.Any()).Return(nil, nil)
	e.profiles.EXPECT().List(gomock.Any()).Return([]*agent_backend_entity.EngineProfile{
		{ID: 3, Type: "codex", Name: "Codex"}}, nil)

	raw, _ := json.Marshal(data_svc.BundleV1{
		Format: data_svc.BundleFormat, Version: 1, Scopes: []string{string(data_svc.ScopeEngineProfiles)},
		Items: data_svc.BundleItems{EngineProfiles: []data_svc.BundleEngineProfile{
			{ExportKey: "ep-a", Type: "codex", Name: "Codex"}, {ExportKey: "ep-b", Type: "codex", Name: "Fresh"}}},
	})
	Convey("同名 = 冲突，默认跳过；新名 = 创建", t, func() {
		p, err := m.svc.PreviewImport(m.ctx, raw)
		So(err, ShouldBeNil)
		So(p.Items, ShouldHaveLength, 2)
		So(p.Items[0].Conflict, ShouldBeTrue)
		So(p.Items[0].LocalID, ShouldEqual, 3)
		So(p.Items[0].DefaultAction, ShouldEqual, data_svc.ActionSkip)
		So(p.Items[1].Conflict, ShouldBeFalse)
		So(p.Items[1].DefaultAction, ShouldEqual, data_svc.ActionCreate)
	})
}

func TestApplyImport_EngineProfilesAndRefsLandThroughNarrowSetters(t *testing.T) {
	m := setupDataSvcTest(t)
	e := setupEngineMocks(t)
	m.providers.EXPECT().List(gomock.Any()).Return(nil, nil).Times(2)
	m.devices.EXPECT().List(gomock.Any()).Return(nil, nil).Times(2)
	m.backends.EXPECT().List(gomock.Any()).Return(nil, nil).Times(2)
	m.depts.EXPECT().List(gomock.Any()).Return(nil, nil)
	e.profiles.EXPECT().List(gomock.Any()).Return(nil, nil).Times(2)

	var created *agent_backend_entity.EngineProfile
	e.profiles.EXPECT().Create(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, p *agent_backend_entity.EngineProfile) error {
			p.ID = 55
			created = p
			return nil
		})
	// 账号默认是本机决定：导入的标记不抢默认（没有 SetAccountDefault 期望）。
	m.depts.EXPECT().FindByName(gomock.Any(), "Eng", int64(0)).Return(nil, nil)
	m.depts.EXPECT().Create(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, d *department_entity.Department) error { d.ID = 100; return nil })
	m.depts.EXPECT().UpdateDefaultEngineProfile(gomock.Any(), int64(100), int64(55)).Return(nil)
	m.agents.EXPECT().ListByDepartment(gomock.Any(), int64(100)).Return(nil, nil)
	m.agents.EXPECT().Create(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, a *agent_entity.Agent) error { a.ID = 200; return nil })
	m.execTargets.EXPECT().Replace(gomock.Any(), int64(200), gomock.Any()).Return(nil)
	var settings *agent_entity.Agent
	m.agents.EXPECT().UpdateEngineSettings(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, a *agent_entity.Agent) error { settings = a; return nil })
	m.dbMock.ExpectBegin()
	m.dbMock.ExpectCommit()

	raw, _ := json.Marshal(data_svc.BundleV1{
		Format: data_svc.BundleFormat, Version: 1,
		Scopes: []string{string(data_svc.ScopeEngineProfiles), string(data_svc.ScopeOrganization)},
		Items: data_svc.BundleItems{
			EngineProfiles: []data_svc.BundleEngineProfile{{
				ExportKey: "ep-a", Type: "codex", Name: "Codex", AccountDefault: true,
				Config: syncwire.AgentBackendConfig{Sandbox: "read-only"},
			}},
			Departments: []data_svc.BundleDepartment{{ExportKey: "dept-eng", Name: "Eng", DefaultEngineKey: "ep-a"}},
			Agents: []data_svc.BundleAgent{{
				ExportKey: "ag-1", Name: "Lead", DepartmentKey: "dept-eng", EngineKey: "ep-a",
				ExecConstraint:     &data_svc.BundleExecConstraint{Mode: "devices", Devices: []string{"sha256:dev-a"}},
				SkillGrantsRuntime: "codex", SkillGrantsJSON: "[]",
			}},
		},
	})
	Convey("导入引擎配置并解析成员/部门引用", t, func() {
		res, err := m.svc.ApplyImport(m.ctx, &data_svc.ApplyImportRequest{Raw: raw, FallbackStrategy: data_svc.ActionCreate})
		So(err, ShouldBeNil)
		So(res.Counts["created"], ShouldEqual, 3)
		So(created.Name, ShouldEqual, "Codex")
		So(created.Sandbox, ShouldEqual, "read-only")
		So(created.Status, ShouldEqual, consts.ACTIVE)
		So(settings.ID, ShouldEqual, 200)
		So(settings.EngineProfileID, ShouldEqual, 55)
		So(settings.GetExecConstraint(), ShouldResemble, agent_entity.ExecConstraint{
			Mode: "devices", Devices: []devicefp.Carrier{"sha256:dev-a"}})
		So(settings.SkillGrantsRuntime, ShouldEqual, "codex")
	})
}

func TestApplyImport_AgentReferencingEngineOutsideTheBundle_LandsWithoutEngine(t *testing.T) {
	m := setupDataSvcTest(t)
	setupEngineMocks(t)
	m.providers.EXPECT().List(gomock.Any()).Return(nil, nil).Times(2)
	m.devices.EXPECT().List(gomock.Any()).Return(nil, nil).Times(2)
	m.backends.EXPECT().List(gomock.Any()).Return(nil, nil).Times(2)
	m.depts.EXPECT().List(gomock.Any()).Return(nil, nil)
	m.agents.EXPECT().ListByDepartment(gomock.Any(), int64(0)).Return(nil, nil)
	m.agents.EXPECT().Create(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, a *agent_entity.Agent) error { a.ID = 200; return nil })
	m.execTargets.EXPECT().Replace(gomock.Any(), int64(200), gomock.Any()).Return(nil)
	// 引用解析不出且没有别的执行设置：不写（没有 UpdateEngineSettings 期望）。
	m.dbMock.ExpectBegin()
	m.dbMock.ExpectCommit()

	raw, _ := json.Marshal(data_svc.BundleV1{
		Format: data_svc.BundleFormat, Version: 1, Scopes: []string{string(data_svc.ScopeOrganization)},
		Items: data_svc.BundleItems{Agents: []data_svc.BundleAgent{{ExportKey: "ag-1", Name: "Solo", EngineKey: "ep-missing"}}},
	})
	Convey("引擎不在导入范围内：成员照常导入、回落到继承", t, func() {
		res, err := m.svc.ApplyImport(m.ctx, &data_svc.ApplyImportRequest{Raw: raw, FallbackStrategy: data_svc.ActionCreate})
		So(err, ShouldBeNil)
		So(res.Counts["created"], ShouldEqual, 1)
	})
}

func TestApplyImport_ProviderReach_LandsThroughNarrowSetter(t *testing.T) {
	m := setupDataSvcTest(t)
	m.providers.EXPECT().List(gomock.Any()).Return(nil, nil).Times(2)
	m.devices.EXPECT().List(gomock.Any()).Return(nil, nil).Times(2)
	m.backends.EXPECT().List(gomock.Any()).Return(nil, nil).Times(2)
	m.depts.EXPECT().List(gomock.Any()).Return(nil, nil)
	m.providers.EXPECT().CreateWithModels(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, p *llm_provider_entity.LLMProvider, _ any, _ string) error {
			p.ID = 100
			return nil
		})
	m.providers.EXPECT().UpdateReach(gomock.Any(), int64(100), "devices", `["sha256:dev-a"]`).Return(nil)
	m.dbMock.ExpectBegin()
	m.dbMock.ExpectCommit()

	raw, _ := json.Marshal(data_svc.BundleV1{
		Format: data_svc.BundleFormat, Version: 1, Scopes: []string{string(data_svc.ScopeLLMProviders)},
		Items: data_svc.BundleItems{LLMProviders: []data_svc.BundleLLMProvider{{
			ProviderKey: "local", Name: "Local", Type: "openai", Enabled: true,
			Reach: &data_svc.BundleReach{Mode: "devices", Devices: []string{"sha256:dev-a"}},
		}}},
	})
	Convey("可达范围随供应商导入", t, func() {
		_, err := m.svc.ApplyImport(m.ctx, &data_svc.ApplyImportRequest{Raw: raw, FallbackStrategy: data_svc.ActionCreate})
		So(err, ShouldBeNil)
	})
}

func TestApplyImport_DevicePrefsAndCLIPaths(t *testing.T) {
	m := setupDataSvcTest(t)
	e := setupEngineMocks(t)
	m.providers.EXPECT().List(gomock.Any()).Return(nil, nil).Times(2)
	m.devices.EXPECT().List(gomock.Any()).Return(nil, nil).Times(2)
	m.backends.EXPECT().List(gomock.Any()).Return(nil, nil).Times(2)
	m.depts.EXPECT().List(gomock.Any()).Return(nil, nil)
	// 预览与应用各读一次本地。
	e.prefs.EXPECT().List(gomock.Any()).Return([]*device_pref_entity.DevicePref{
		{DeviceFingerprint: "sha256:dev-b", LabelsJSON: `[]`}}, nil).Times(2)
	e.paths.EXPECT().List(gomock.Any()).Return(nil, nil).Times(2)

	var createdPref *device_pref_entity.DevicePref
	e.prefs.EXPECT().Create(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, p *device_pref_entity.DevicePref) error { createdPref = p; return nil })
	e.prefs.EXPECT().SetLabels(gomock.Any(), devicefp.Carrier("sha256:dev-b"), `["home"]`).Return(nil)
	e.prefs.EXPECT().SetSortOrder(gomock.Any(), devicefp.Carrier("sha256:dev-b"), 5).Return(nil)
	var createdPath *device_pref_entity.CLIPathOverride
	e.paths.EXPECT().Create(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, o *device_pref_entity.CLIPathOverride) error { createdPath = o; return nil })
	m.dbMock.ExpectBegin()
	m.dbMock.ExpectCommit()

	raw, _ := json.Marshal(data_svc.BundleV1{
		Format: data_svc.BundleFormat, Version: 1, Scopes: []string{string(data_svc.ScopeDevicePrefs)},
		Items: data_svc.BundleItems{
			DevicePrefs: []data_svc.BundleDevicePref{
				{DeviceFingerprint: "sha256:dev-a", Labels: []string{"gpu"}, SortOrder: 1},
				{DeviceFingerprint: "sha256:dev-b", Labels: []string{"home"}, SortOrder: 5},
			},
			DeviceCLIPaths: []data_svc.BundleDeviceCLIPath{
				{RuntimeType: "claudecode", DeviceFingerprint: "sha256:dev-a", CLIPath: "/opt/claude"}},
		},
	})
	Convey("新设备创建，已有设备的冲突项选覆盖时走窄写口", t, func() {
		res, err := m.svc.ApplyImport(m.ctx, &data_svc.ApplyImportRequest{
			Raw: raw, FallbackStrategy: data_svc.ActionOverwrite})
		So(err, ShouldBeNil)
		So(res.Counts["created"], ShouldEqual, 2)
		So(res.Counts["overwrote"], ShouldEqual, 1)
		So(createdPref.DeviceFingerprint, ShouldEqual, devicefp.Carrier("sha256:dev-a"))
		So(createdPref.Labels(), ShouldResemble, []string{"gpu"})
		So(createdPath.RuntimeType, ShouldEqual, "claudecode")
		So(createdPath.CLIPath, ShouldEqual, "/opt/claude")
	})
}
