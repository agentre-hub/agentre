package data_svc_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/cago-frame/cago/database/db"
	"github.com/cago-frame/cago/pkg/consts"
	"github.com/cago-frame/cago/pkg/utils/testutils"
	. "github.com/smartystreets/goconvey/convey"
	"go.uber.org/mock/gomock"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/agent_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/department_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/llm_provider_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/llm_provider_model_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/paired_agentred_entity"
	"github.com/agentre-hub/agentre/internal/repository/agent_backend_repo"
	"github.com/agentre-hub/agentre/internal/repository/agent_backend_repo/mock_agent_backend_repo"
	"github.com/agentre-hub/agentre/internal/repository/agent_repo"
	"github.com/agentre-hub/agentre/internal/repository/agent_repo/mock_agent_repo"
	"github.com/agentre-hub/agentre/internal/repository/department_repo"
	"github.com/agentre-hub/agentre/internal/repository/department_repo/mock_department_repo"
	"github.com/agentre-hub/agentre/internal/repository/llm_provider_repo"
	"github.com/agentre-hub/agentre/internal/repository/llm_provider_repo/mock_llm_provider_repo"
	"github.com/agentre-hub/agentre/internal/repository/remote_device_repo"
	"github.com/agentre-hub/agentre/internal/repository/remote_device_repo/mock_remote_device_repo"
	"github.com/agentre-hub/agentre/internal/service/data_svc"
)

type dataSvcMocks struct {
	ctx         context.Context
	providers   *mock_llm_provider_repo.MockLLMProviderRepo
	backends    *mock_agent_backend_repo.MockAgentBackendRepo
	depts       *mock_department_repo.MockDepartmentRepo
	agents      *mock_agent_repo.MockAgentRepo
	execTargets *mock_agent_repo.MockAgentExecTargetRepo
	devices     *mock_remote_device_repo.MockPairedAgentredRepo
	dbMock      sqlmock.Sqlmock
	svc         data_svc.DataSvc
}

// setupDataSvcTest 注入 6 个 mock repo + sqlmock,返回测试句柄。
func setupDataSvcTest(t *testing.T) *dataSvcMocks {
	t.Helper()
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)

	dbCtx, _, dbMock := testutils.Database(t)
	_ = db.Ctx(dbCtx) // 提示编译器 dbCtx 已挂上 db

	m := &dataSvcMocks{
		ctx:         dbCtx,
		providers:   mock_llm_provider_repo.NewMockLLMProviderRepo(ctrl),
		backends:    mock_agent_backend_repo.NewMockAgentBackendRepo(ctrl),
		depts:       mock_department_repo.NewMockDepartmentRepo(ctrl),
		agents:      mock_agent_repo.NewMockAgentRepo(ctrl),
		execTargets: mock_agent_repo.NewMockAgentExecTargetRepo(ctrl),
		devices:     mock_remote_device_repo.NewMockPairedAgentredRepo(ctrl),
		dbMock:      dbMock,
	}
	llm_provider_repo.RegisterLLMProvider(m.providers)
	agent_backend_repo.RegisterAgentBackend(m.backends)
	department_repo.RegisterDepartment(m.depts)
	agent_repo.RegisterAgent(m.agents)
	agent_repo.RegisterAgentExecTarget(m.execTargets)
	remote_device_repo.RegisterPairedAgentred(m.devices)

	m.svc = data_svc.Default()
	return m
}

func TestExport_LLMProvidersOnly_Scrubbed(t *testing.T) {
	m := setupDataSvcTest(t)

	rows := []*llm_provider_entity.LLMProvider{
		{
			ID: 1, ProviderKey: "key-1", Type: "anthropic", Name: "Main",
			APIKey: "secret", BaseURL: "https://x",
			Enabled: llm_provider_entity.EnabledOn, DefaultModelKey: "mk-1",
			Status: consts.ACTIVE,
		},
	}
	m.providers.EXPECT().List(gomock.Any()).Return(rows, nil)
	m.providers.EXPECT().ListModels(gomock.Any(), int64(1)).Return([]*llm_provider_model_entity.LLMProviderModel{
		{
			ID: 10, ProviderID: 1, ModelKey: "mk-1", ModelID: "claude-3-5-sonnet",
			Name: "Sonnet", ContextWindow: 200000, MaxOutput: 8192,
			Enabled: llm_provider_model_entity.EnabledOn, Status: consts.ACTIVE,
		},
	}, nil)

	Convey("Export llm-providers without secrets", t, func() {
		res, err := m.svc.Export(m.ctx, &data_svc.ExportRequest{
			Scopes:         []string{string(data_svc.ScopeLLMProviders)},
			IncludeSecrets: false,
		})
		So(err, ShouldBeNil)
		So(res, ShouldNotBeNil)

		var bundle data_svc.BundleV1
		So(json.Unmarshal(res.JSON, &bundle), ShouldBeNil)
		So(bundle.Format, ShouldEqual, data_svc.BundleFormat)
		So(bundle.Version, ShouldEqual, data_svc.BundleVersion)
		So(bundle.SecretsIncluded, ShouldBeFalse)
		So(bundle.Items.LLMProviders, ShouldHaveLength, 1)

		p := bundle.Items.LLMProviders[0]
		So(p.ProviderKey, ShouldEqual, "key-1")
		So(p.Name, ShouldEqual, "Main")
		So(p.APIKey, ShouldEqual, "") // 关键断言:脱敏
		// 新 1→N 形状:默认 key + 子模型 + token 元数据原样带出
		So(p.Enabled, ShouldBeTrue)
		So(p.DefaultModelKey, ShouldEqual, "mk-1")
		So(p.Models, ShouldHaveLength, 1)
		So(p.Models[0].ModelKey, ShouldEqual, "mk-1")
		So(p.Models[0].ModelID, ShouldEqual, "claude-3-5-sonnet")
		So(p.Models[0].ContextWindow, ShouldEqual, 200000)
		So(p.Models[0].MaxOutput, ShouldEqual, 8192)
		So(p.Models[0].Enabled, ShouldBeTrue)
		So(res.Summary[string(data_svc.ScopeLLMProviders)], ShouldEqual, 1)
	})
}

func TestExport_LLMProviders_IncludeSecrets(t *testing.T) {
	m := setupDataSvcTest(t)
	m.providers.EXPECT().List(gomock.Any()).Return([]*llm_provider_entity.LLMProvider{
		{ID: 1, ProviderKey: "k1", Type: "anthropic", Name: "M", APIKey: "sk-xxx"},
	}, nil)
	m.providers.EXPECT().ListModels(gomock.Any(), int64(1)).Return(nil, nil)

	Convey("Export 携带 includeSecrets", t, func() {
		res, err := m.svc.Export(m.ctx, &data_svc.ExportRequest{
			Scopes: []string{string(data_svc.ScopeLLMProviders)}, IncludeSecrets: true,
		})
		So(err, ShouldBeNil)
		var b data_svc.BundleV1
		So(json.Unmarshal(res.JSON, &b), ShouldBeNil)
		So(b.SecretsIncluded, ShouldBeTrue)
		So(b.Items.LLMProviders[0].APIKey, ShouldEqual, "sk-xxx")
	})
}

func TestExport_Organization_CrossRefsViaExportKey(t *testing.T) {
	m := setupDataSvcTest(t)
	m.depts.EXPECT().List(gomock.Any()).Return([]*department_entity.Department{
		{ID: 10, Name: "Eng", ParentID: 0, LeadAgentID: 20},
		{ID: 11, Name: "Backend", ParentID: 10, LeadAgentID: 0},
	}, nil)
	m.agents.EXPECT().List(gomock.Any()).Return([]*agent_entity.Agent{
		{ID: 20, Name: "Lead", DepartmentID: 10, AgentBackendID: 30, ParentAgentID: 0},
		{ID: 21, Name: "IC", DepartmentID: 11, AgentBackendID: 30, ParentAgentID: 20},
	}, nil)
	m.backends.EXPECT().List(gomock.Any()).Return([]*agent_backend_entity.AgentBackend{
		{ID: 30, Type: "claudecode", Name: "Local"},
	}, nil)
	m.execTargets.EXPECT().ListByAgents(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, agentIDs []int64) (map[int64][]*agent_entity.AgentExecTarget, error) {
			out := make(map[int64][]*agent_entity.AgentExecTarget, len(agentIDs))
			for _, id := range agentIDs {
				out[id] = []*agent_entity.AgentExecTarget{
					{AgentID: id, AgentBackendID: 30, SortOrder: 0, SkillsJSON: "[]"},
				}
			}
			return out, nil
		})

	Convey("Organization scope 串好 exportKey 引用", t, func() {
		res, err := m.svc.Export(m.ctx, &data_svc.ExportRequest{
			Scopes: []string{string(data_svc.ScopeOrganization)},
		})
		So(err, ShouldBeNil)
		var b data_svc.BundleV1
		So(json.Unmarshal(res.JSON, &b), ShouldBeNil)
		So(b.Items.Departments, ShouldHaveLength, 2)
		So(b.Items.Agents, ShouldHaveLength, 2)

		// 找 "Backend" 部门,它的 parentKey 必须指向 "Eng" 的 exportKey
		var eng, back data_svc.BundleDepartment
		for _, d := range b.Items.Departments {
			if d.Name == "Eng" {
				eng = d
			}
			if d.Name == "Backend" {
				back = d
			}
		}
		So(back.ParentKey, ShouldEqual, eng.ExportKey)
		So(eng.LeadAgentKey, ShouldNotBeEmpty)
		// 找 IC,parentAgentKey 必须指向 Lead 的 exportKey
		var lead, ic data_svc.BundleAgent
		for _, a := range b.Items.Agents {
			if a.Name == "Lead" {
				lead = a
			}
			if a.Name == "IC" {
				ic = a
			}
		}
		So(ic.ParentAgentKey, ShouldEqual, lead.ExportKey)
	})
}

func TestExport_UnknownScope_Errors(t *testing.T) {
	m := setupDataSvcTest(t)
	Convey("未知 scope 应报错", t, func() {
		_, err := m.svc.Export(m.ctx, &data_svc.ExportRequest{
			Scopes: []string{"nonsense"},
		})
		So(err, ShouldNotBeNil)
	})
}

func TestPreviewImport_RejectsBadFormat(t *testing.T) {
	m := setupDataSvcTest(t)
	Convey("Format 不对应拒收", t, func() {
		_, err := m.svc.PreviewImport(m.ctx, []byte(`{"format":"foo","version":1}`))
		So(err, ShouldNotBeNil)
	})
	Convey("Version > 1 拒收", t, func() {
		_, err := m.svc.PreviewImport(m.ctx, []byte(`{"format":"agentre-data-bundle","version":2}`))
		So(err, ShouldNotBeNil)
	})
}

func TestPreviewImport_NoConflict_DefaultsCreate(t *testing.T) {
	m := setupDataSvcTest(t)
	m.providers.EXPECT().List(gomock.Any()).Return([]*llm_provider_entity.LLMProvider{}, nil)
	m.devices.EXPECT().List(gomock.Any()).Return([]*paired_agentred_entity.PairedAgentred{}, nil)
	m.backends.EXPECT().List(gomock.Any()).Return([]*agent_backend_entity.AgentBackend{}, nil)
	m.depts.EXPECT().List(gomock.Any()).Return([]*department_entity.Department{}, nil)

	bundle := data_svc.BundleV1{
		Format: data_svc.BundleFormat, Version: 1,
		Scopes: []string{string(data_svc.ScopeLLMProviders)},
		Items:  data_svc.BundleItems{LLMProviders: []data_svc.BundleLLMProvider{{ProviderKey: "k1", Name: "P1"}}},
	}
	raw, _ := json.Marshal(bundle)

	Convey("无冲突,默认 create", t, func() {
		pv, err := m.svc.PreviewImport(m.ctx, raw)
		So(err, ShouldBeNil)
		So(pv.Items, ShouldHaveLength, 1)
		So(pv.Items[0].Conflict, ShouldBeFalse)
		So(pv.Items[0].DefaultAction, ShouldEqual, data_svc.ActionCreate)
	})
}

func TestPreviewImport_ProviderKeyConflict(t *testing.T) {
	m := setupDataSvcTest(t)
	m.providers.EXPECT().List(gomock.Any()).Return([]*llm_provider_entity.LLMProvider{
		{ID: 5, ProviderKey: "k1", Name: "Local Name"},
	}, nil)
	m.devices.EXPECT().List(gomock.Any()).Return(nil, nil)
	m.backends.EXPECT().List(gomock.Any()).Return(nil, nil)
	m.depts.EXPECT().List(gomock.Any()).Return(nil, nil)

	bundle := data_svc.BundleV1{
		Format: data_svc.BundleFormat, Version: 1,
		Scopes: []string{string(data_svc.ScopeLLMProviders)},
		Items:  data_svc.BundleItems{LLMProviders: []data_svc.BundleLLMProvider{{ProviderKey: "k1", Name: "Bundle Name"}}},
	}
	raw, _ := json.Marshal(bundle)

	Convey("同 providerKey 标 conflict", t, func() {
		pv, err := m.svc.PreviewImport(m.ctx, raw)
		So(err, ShouldBeNil)
		So(pv.Items[0].Conflict, ShouldBeTrue)
		So(pv.Items[0].LocalID, ShouldEqual, 5)
		So(pv.Items[0].LocalName, ShouldEqual, "Local Name")
		So(pv.Items[0].DefaultAction, ShouldEqual, data_svc.ActionSkip)
	})
}

func TestPreviewImport_BackendRefsMissingProvider_Dangling(t *testing.T) {
	m := setupDataSvcTest(t)
	m.providers.EXPECT().List(gomock.Any()).Return(nil, nil)
	m.devices.EXPECT().List(gomock.Any()).Return(nil, nil)
	m.backends.EXPECT().List(gomock.Any()).Return(nil, nil)
	m.depts.EXPECT().List(gomock.Any()).Return(nil, nil)

	bundle := data_svc.BundleV1{
		Format: data_svc.BundleFormat, Version: 1,
		Scopes: []string{string(data_svc.ScopeAgentBackends)},
		Items: data_svc.BundleItems{
			AgentBackends: []data_svc.BundleAgentBackend{
				{ExportKey: "ab-1", Name: "B1", LLMProviderKey: "missing-key"},
			},
		},
	}
	raw, _ := json.Marshal(bundle)

	Convey("backend 引用未导入的 provider → dangling + 强制 skip", t, func() {
		pv, err := m.svc.PreviewImport(m.ctx, raw)
		So(err, ShouldBeNil)
		So(pv.Items[0].Dangling, ShouldBeTrue)
		So(pv.Items[0].DefaultAction, ShouldEqual, data_svc.ActionSkip)
	})
}

func TestApplyImport_Providers_Create(t *testing.T) {
	m := setupDataSvcTest(t)

	// PreviewImport calls providers+devices+backends+depts once each.
	// applyProviders calls providers.List again; applyRemoteDevices calls devices.List again;
	// applyAgentBackends calls backends.List again.
	m.providers.EXPECT().List(gomock.Any()).Return([]*llm_provider_entity.LLMProvider{}, nil).Times(2)
	m.devices.EXPECT().List(gomock.Any()).Return(nil, nil).Times(2)
	m.backends.EXPECT().List(gomock.Any()).Return(nil, nil).Times(2)
	m.depts.EXPECT().List(gomock.Any()).Return(nil, nil)

	m.providers.EXPECT().CreateWithModels(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, p *llm_provider_entity.LLMProvider, models []*llm_provider_model_entity.LLMProviderModel, defaultKey string) error {
			// 连接配置 + 默认 key + 子模型(含 token 元数据)一个业务操作落库
			So(p.ProviderKey, ShouldEqual, "k1")
			So(p.APIKey, ShouldEqual, "sk-x")
			So(defaultKey, ShouldEqual, "mk-1")
			So(models, ShouldHaveLength, 1)
			So(models[0].ModelKey, ShouldEqual, "mk-1") // 稳定 key 原样保留
			So(models[0].ModelID, ShouldEqual, "claude-3-5-sonnet")
			So(models[0].ContextWindow, ShouldEqual, 200000)
			So(models[0].MaxOutput, ShouldEqual, 8192)
			So(models[0].Enabled, ShouldEqual, llm_provider_model_entity.EnabledOn)
			p.ID = 100
			return nil
		})

	m.dbMock.ExpectBegin()
	m.dbMock.ExpectCommit()

	bundle := data_svc.BundleV1{
		Format: data_svc.BundleFormat, Version: 1,
		Scopes: []string{string(data_svc.ScopeLLMProviders)},
		Items: data_svc.BundleItems{LLMProviders: []data_svc.BundleLLMProvider{
			{
				ProviderKey: "k1", Name: "P1", Type: "anthropic", APIKey: "sk-x",
				Enabled: true, DefaultModelKey: "mk-1",
				Models: []data_svc.BundleLLMProviderModel{
					{ModelKey: "mk-1", ModelID: "claude-3-5-sonnet", ContextWindow: 200000, MaxOutput: 8192, Enabled: true},
				},
			},
		}},
	}
	raw, _ := json.Marshal(bundle)

	Convey("create 新行,事务提交", t, func() {
		res, err := m.svc.ApplyImport(m.ctx, &data_svc.ApplyImportRequest{
			Raw:              raw,
			FallbackStrategy: data_svc.ActionCreate,
		})
		So(err, ShouldBeNil)
		So(res.Counts["created"], ShouldEqual, 1)
		So(m.dbMock.ExpectationsWereMet(), ShouldBeNil)
	})
}

func TestApplyImport_Providers_SkipConflict(t *testing.T) {
	m := setupDataSvcTest(t)
	existing := []*llm_provider_entity.LLMProvider{{ID: 5, ProviderKey: "k1", Name: "P1"}}
	m.providers.EXPECT().List(gomock.Any()).Return(existing, nil).Times(2)
	m.devices.EXPECT().List(gomock.Any()).Return(nil, nil).Times(2)
	m.backends.EXPECT().List(gomock.Any()).Return(nil, nil).Times(2)
	m.depts.EXPECT().List(gomock.Any()).Return(nil, nil)
	// 不 EXPECT Create / Update — 必须不调

	m.dbMock.ExpectBegin()
	m.dbMock.ExpectCommit()

	bundle := data_svc.BundleV1{
		Format: data_svc.BundleFormat, Version: 1,
		Scopes: []string{string(data_svc.ScopeLLMProviders)},
		Items: data_svc.BundleItems{LLMProviders: []data_svc.BundleLLMProvider{
			{ProviderKey: "k1", Name: "P1"},
		}},
	}
	raw, _ := json.Marshal(bundle)

	Convey("skip 不调写方法", t, func() {
		res, err := m.svc.ApplyImport(m.ctx, &data_svc.ApplyImportRequest{
			Raw:              raw,
			FallbackStrategy: data_svc.ActionSkip,
		})
		So(err, ShouldBeNil)
		So(res.Counts["skipped"], ShouldEqual, 1)
	})
}

func TestApplyImport_Providers_Overwrite(t *testing.T) {
	m := setupDataSvcTest(t)
	existing := []*llm_provider_entity.LLMProvider{{ID: 5, ProviderKey: "k1", Name: "Old", Status: consts.ACTIVE}}
	m.providers.EXPECT().List(gomock.Any()).Return(existing, nil).Times(2)
	m.devices.EXPECT().List(gomock.Any()).Return(nil, nil).Times(2)
	m.backends.EXPECT().List(gomock.Any()).Return(nil, nil).Times(2)
	m.depts.EXPECT().List(gomock.Any()).Return(nil, nil)

	m.providers.EXPECT().Update(gomock.Any(), gomock.AssignableToTypeOf(&llm_provider_entity.LLMProvider{})).
		DoAndReturn(func(_ context.Context, p *llm_provider_entity.LLMProvider) error {
			So(p.ID, ShouldEqual, 5)
			So(p.Name, ShouldEqual, "New")
			So(p.Status, ShouldEqual, consts.ACTIVE) // 保留本地原 status
			// 新形状:连接字段外还写 enabled + 默认模型 key
			So(p.Enabled, ShouldEqual, llm_provider_entity.EnabledOn)
			So(p.DefaultModelKey, ShouldEqual, "mk-1")
			return nil
		})

	m.dbMock.ExpectBegin()
	m.dbMock.ExpectCommit()

	bundle := data_svc.BundleV1{
		Format: data_svc.BundleFormat, Version: 1,
		Scopes: []string{string(data_svc.ScopeLLMProviders)},
		Items: data_svc.BundleItems{LLMProviders: []data_svc.BundleLLMProvider{
			{ProviderKey: "k1", Name: "New", Enabled: true, DefaultModelKey: "mk-1"},
		}},
	}
	raw, _ := json.Marshal(bundle)

	Convey("overwrite 调 Update,保留 status", t, func() {
		res, err := m.svc.ApplyImport(m.ctx, &data_svc.ApplyImportRequest{
			Raw:              raw,
			FallbackStrategy: data_svc.ActionOverwrite,
		})
		So(err, ShouldBeNil)
		So(res.Counts["overwrote"], ShouldEqual, 1)
	})
}

// TestApplyImport_Providers_Overwrite_UpsertsModels 覆盖已有 Provider 时按稳定
// ModelKey 做 upsert:bundle 里的模型已存在则更新 token 元数据,缺失则新建。
func TestApplyImport_Providers_Overwrite_UpsertsModels(t *testing.T) {
	m := setupDataSvcTest(t)
	existing := []*llm_provider_entity.LLMProvider{{ID: 5, ProviderKey: "k1", Name: "Old", Status: consts.ACTIVE}}
	m.providers.EXPECT().List(gomock.Any()).Return(existing, nil).Times(2)
	m.devices.EXPECT().List(gomock.Any()).Return(nil, nil).Times(2)
	m.backends.EXPECT().List(gomock.Any()).Return(nil, nil).Times(2)
	m.depts.EXPECT().List(gomock.Any()).Return(nil, nil)

	m.providers.EXPECT().Update(gomock.Any(), gomock.AssignableToTypeOf(&llm_provider_entity.LLMProvider{})).
		DoAndReturn(func(_ context.Context, p *llm_provider_entity.LLMProvider) error {
			So(p.ID, ShouldEqual, 5)
			return nil
		})

	// 已有模型 mk-1 → 更新;mk-2 不存在 → 新建
	m.providers.EXPECT().FindModelByKey(gomock.Any(), "mk-1").Return(
		&llm_provider_model_entity.LLMProviderModel{
			ID: 11, ProviderID: 5, ModelKey: "mk-1", ModelID: "old-id",
			ContextWindow: 1000, MaxOutput: 1000, Enabled: llm_provider_model_entity.EnabledOn,
		}, nil)
	m.providers.EXPECT().FindModelByKey(gomock.Any(), "mk-2").Return(nil, nil)
	m.providers.EXPECT().UpdateModel(gomock.Any(), gomock.AssignableToTypeOf(&llm_provider_model_entity.LLMProviderModel{})).
		DoAndReturn(func(_ context.Context, mdl *llm_provider_model_entity.LLMProviderModel) error {
			So(mdl.ID, ShouldEqual, 11)
			So(mdl.ModelID, ShouldEqual, "claude-3-5-sonnet") // 可编辑字段被覆盖
			So(mdl.ContextWindow, ShouldEqual, 200000)
			So(mdl.MaxOutput, ShouldEqual, 8192)
			So(mdl.ModelKey, ShouldEqual, "mk-1") // 稳定 key 不变
			return nil
		})
	m.providers.EXPECT().CreateModel(gomock.Any(), gomock.AssignableToTypeOf(&llm_provider_model_entity.LLMProviderModel{})).
		DoAndReturn(func(_ context.Context, mdl *llm_provider_model_entity.LLMProviderModel) error {
			So(mdl.ProviderID, ShouldEqual, 5)
			So(mdl.ModelKey, ShouldEqual, "mk-2")
			So(mdl.ModelID, ShouldEqual, "claude-3-5-haiku")
			So(mdl.ContextWindow, ShouldEqual, 100000)
			So(mdl.Enabled, ShouldEqual, llm_provider_model_entity.EnabledOff)
			return nil
		})

	m.dbMock.ExpectBegin()
	m.dbMock.ExpectCommit()

	bundle := data_svc.BundleV1{
		Format: data_svc.BundleFormat, Version: 1,
		Scopes: []string{string(data_svc.ScopeLLMProviders)},
		Items: data_svc.BundleItems{LLMProviders: []data_svc.BundleLLMProvider{
			{
				ProviderKey: "k1", Name: "New", Enabled: true, DefaultModelKey: "mk-1",
				Models: []data_svc.BundleLLMProviderModel{
					{ModelKey: "mk-1", ModelID: "claude-3-5-sonnet", ContextWindow: 200000, MaxOutput: 8192, Enabled: true},
					{ModelKey: "mk-2", ModelID: "claude-3-5-haiku", ContextWindow: 100000, MaxOutput: 4096, Enabled: false},
				},
			},
		}},
	}
	raw, _ := json.Marshal(bundle)

	Convey("overwrite 按 ModelKey upsert 子模型,保留 token 元数据", t, func() {
		res, err := m.svc.ApplyImport(m.ctx, &data_svc.ApplyImportRequest{
			Raw:              raw,
			FallbackStrategy: data_svc.ActionOverwrite,
		})
		So(err, ShouldBeNil)
		So(res.Counts["overwrote"], ShouldEqual, 1)
	})
}

func TestApplyImport_Backend_ResolvesProviderRef(t *testing.T) {
	m := setupDataSvcTest(t)
	m.providers.EXPECT().List(gomock.Any()).Return([]*llm_provider_entity.LLMProvider{}, nil).Times(2)
	m.devices.EXPECT().List(gomock.Any()).Return(nil, nil).Times(2)
	m.backends.EXPECT().List(gomock.Any()).Return([]*agent_backend_entity.AgentBackend{}, nil).Times(2)
	m.depts.EXPECT().List(gomock.Any()).Return(nil, nil)

	// 先 Create provider(经 CreateWithModels 一个事务写入)
	m.providers.EXPECT().CreateWithModels(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, p *llm_provider_entity.LLMProvider, _ []*llm_provider_model_entity.LLMProviderModel, _ string) error {
			p.ID = 50
			return nil
		})
	// 再 Create backend,其 llm_provider_key 必须等于 bundle 里的 key1(provider_key 是 stable,本就传过去)
	m.backends.EXPECT().Create(gomock.Any(), gomock.AssignableToTypeOf(&agent_backend_entity.AgentBackend{})).
		DoAndReturn(func(_ context.Context, bk *agent_backend_entity.AgentBackend) error {
			So(bk.LLMProviderKey, ShouldEqual, "key1")
			bk.ID = 60
			return nil
		})

	m.dbMock.ExpectBegin()
	m.dbMock.ExpectCommit()

	bundle := data_svc.BundleV1{
		Format: data_svc.BundleFormat, Version: 1,
		Scopes: []string{string(data_svc.ScopeLLMProviders), string(data_svc.ScopeAgentBackends)},
		Items: data_svc.BundleItems{
			LLMProviders: []data_svc.BundleLLMProvider{{ProviderKey: "key1", Name: "P"}},
			AgentBackends: []data_svc.BundleAgentBackend{
				{ExportKey: "ab-1", Type: "claudecode", Name: "B", LLMProviderKey: "key1"},
			},
		},
	}
	raw, _ := json.Marshal(bundle)

	Convey("backend 引用 provider,wire 后保留 stable key", t, func() {
		res, err := m.svc.ApplyImport(m.ctx, &data_svc.ApplyImportRequest{
			Raw: raw, FallbackStrategy: data_svc.ActionCreate,
		})
		So(err, ShouldBeNil)
		So(res.Counts["created"], ShouldEqual, 2)
	})
}

func TestApplyImport_Backend_ResolvesRemoteDeviceRef(t *testing.T) {
	m := setupDataSvcTest(t)
	localDevice := &paired_agentred_entity.PairedAgentred{ID: 5, InstanceUUID: "uuid-1", Name: "Server1"}
	m.providers.EXPECT().List(gomock.Any()).Return(nil, nil).Times(2)
	m.devices.EXPECT().List(gomock.Any()).Return([]*paired_agentred_entity.PairedAgentred{localDevice}, nil).Times(2)
	m.backends.EXPECT().List(gomock.Any()).Return([]*agent_backend_entity.AgentBackend{}, nil).Times(2)
	m.depts.EXPECT().List(gomock.Any()).Return(nil, nil).Times(1)

	m.backends.EXPECT().Create(gomock.Any(), gomock.AssignableToTypeOf(&agent_backend_entity.AgentBackend{})).
		DoAndReturn(func(_ context.Context, bk *agent_backend_entity.AgentBackend) error {
			So(bk.DeviceFingerprint, ShouldEqual, "5")
			bk.ID = 60
			return nil
		})

	m.dbMock.ExpectBegin()
	m.dbMock.ExpectCommit()

	bundle := data_svc.BundleV1{
		Format: data_svc.BundleFormat, Version: 1,
		Scopes: []string{string(data_svc.ScopeRemoteDevices), string(data_svc.ScopeAgentBackends)},
		Items: data_svc.BundleItems{
			RemoteDevices: []data_svc.BundleRemoteDevice{
				{InstanceUUID: "uuid-1", Name: "Server1"},
			},
			AgentBackends: []data_svc.BundleAgentBackend{
				{ExportKey: "ab-1", Type: "codex", Name: "Remote Codex", DeviceID: "uuid-1"},
			},
		},
	}
	raw, _ := json.Marshal(bundle)

	Convey("backend 引用远端设备 instanceUUID 时,落库为本地 row ID", t, func() {
		res, err := m.svc.ApplyImport(m.ctx, &data_svc.ApplyImportRequest{
			Raw: raw, FallbackStrategy: data_svc.ActionSkip,
		})
		So(err, ShouldBeNil)
		So(res.Counts["skipped"], ShouldEqual, 1)
		So(res.Counts["created"], ShouldEqual, 1)
	})
}

func TestApplyImport_Backend_FollowsDuplicatedRemoteDevice(t *testing.T) {
	m := setupDataSvcTest(t)
	localDevice := &paired_agentred_entity.PairedAgentred{ID: 5, InstanceUUID: "uuid-1", Name: "Server1"}
	m.providers.EXPECT().List(gomock.Any()).Return(nil, nil).Times(2)
	m.devices.EXPECT().List(gomock.Any()).Return([]*paired_agentred_entity.PairedAgentred{localDevice}, nil).Times(2)
	m.backends.EXPECT().List(gomock.Any()).Return([]*agent_backend_entity.AgentBackend{}, nil).Times(2)
	m.depts.EXPECT().List(gomock.Any()).Return(nil, nil).Times(1)

	m.devices.EXPECT().Create(gomock.Any(), gomock.AssignableToTypeOf(&paired_agentred_entity.PairedAgentred{})).
		DoAndReturn(func(_ context.Context, d *paired_agentred_entity.PairedAgentred) error {
			So(d.Name, ShouldEqual, "Server1 (copy)")
			d.ID = 99
			return nil
		})
	m.backends.EXPECT().Create(gomock.Any(), gomock.AssignableToTypeOf(&agent_backend_entity.AgentBackend{})).
		DoAndReturn(func(_ context.Context, bk *agent_backend_entity.AgentBackend) error {
			So(bk.DeviceFingerprint, ShouldEqual, "99")
			bk.ID = 60
			return nil
		})

	m.dbMock.ExpectBegin()
	m.dbMock.ExpectCommit()

	bundle := data_svc.BundleV1{
		Format: data_svc.BundleFormat, Version: 1,
		Scopes: []string{string(data_svc.ScopeRemoteDevices), string(data_svc.ScopeAgentBackends)},
		Items: data_svc.BundleItems{
			RemoteDevices: []data_svc.BundleRemoteDevice{
				{InstanceUUID: "uuid-1", Name: "Server1"},
			},
			AgentBackends: []data_svc.BundleAgentBackend{
				{ExportKey: "ab-1", Type: "codex", Name: "Remote Codex", DeviceID: "uuid-1"},
			},
		},
	}
	raw, _ := json.Marshal(bundle)

	Convey("稳定 deviceId 在远端设备 duplicate 后绑定新设备", t, func() {
		res, err := m.svc.ApplyImport(m.ctx, &data_svc.ApplyImportRequest{
			Raw: raw,
			Actions: map[string]data_svc.ItemAction{
				"remote-devices:uuid-1": data_svc.ActionDuplicate,
			},
			FallbackStrategy: data_svc.ActionCreate,
		})
		So(err, ShouldBeNil)
		So(res.Counts["duplicated"], ShouldEqual, 1)
		So(res.Counts["created"], ShouldEqual, 1)
	})
}

func TestApplyImport_Org_TwoPassBackfill(t *testing.T) {
	m := setupDataSvcTest(t)
	// PreviewImport calls providers+devices+backends+depts once each.
	// applyProviders/applyRemoteDevices/applyAgentBackends each call their respective List once.
	// applyDepartments uses FindByName (not List). agents.List is never called.
	m.providers.EXPECT().List(gomock.Any()).Return(nil, nil).Times(2)
	m.devices.EXPECT().List(gomock.Any()).Return(nil, nil).Times(2)
	m.backends.EXPECT().List(gomock.Any()).Return(nil, nil).Times(2)
	m.depts.EXPECT().List(gomock.Any()).Return(nil, nil).Times(1)
	// agents.List is NOT called (preview doesn't call it, applyAgents uses ListByDepartment)

	// 部门两个:Eng(根) + Backend(parent=Eng);Eng.LeadAgentKey 指向 Lead
	// Agent 两个:Lead(部门 Eng) + IC(部门 Backend,parent Lead)

	// applyDepartments calls FindByName for each dept (Eng: parentID=0, Backend: parentID=100)
	m.depts.EXPECT().FindByName(gomock.Any(), "Eng", int64(0)).Return(nil, nil)
	m.depts.EXPECT().FindByName(gomock.Any(), "Backend", int64(100)).Return(nil, nil)

	// applyAgents calls ListByDepartment per agent
	m.agents.EXPECT().ListByDepartment(gomock.Any(), int64(100)).Return([]*agent_entity.Agent{}, nil)
	m.agents.EXPECT().ListByDepartment(gomock.Any(), int64(101)).Return([]*agent_entity.Agent{}, nil)

	m.depts.EXPECT().Create(gomock.Any(), gomock.AssignableToTypeOf(&department_entity.Department{})).
		DoAndReturn(func(_ context.Context, d *department_entity.Department) error {
			switch d.Name {
			case "Eng":
				d.ID = 100
				So(d.ParentID, ShouldEqual, 0)
			case "Backend":
				d.ID = 101
				So(d.ParentID, ShouldEqual, 100) // 已通过 keymap 解析到 Eng
			}
			return nil
		}).Times(2)
	m.agents.EXPECT().Create(gomock.Any(), gomock.AssignableToTypeOf(&agent_entity.Agent{})).
		DoAndReturn(func(_ context.Context, a *agent_entity.Agent) error {
			switch a.Name {
			case "Lead":
				a.ID = 200
				So(a.DepartmentID, ShouldEqual, 100)
				So(a.ParentAgentID, ShouldEqual, 0) // 第一遍尚未回填
			case "IC":
				a.ID = 201
				So(a.DepartmentID, ShouldEqual, 101)
				So(a.ParentAgentID, ShouldEqual, 0) // 第一遍尚未回填
			}
			return nil
		}).Times(2)

	// backfillOrg: Find Eng dept (ID=100) to set LeadAgentID=200
	m.depts.EXPECT().Find(gomock.Any(), int64(100)).Return(&department_entity.Department{ID: 100, Name: "Eng"}, nil)
	// backfillOrg: Find IC agent (ID=201) to set ParentAgentID=200
	m.agents.EXPECT().Find(gomock.Any(), int64(201)).Return(&agent_entity.Agent{ID: 201, Name: "IC"}, nil)

	// 第一遍每个 Agent 都落一次执行目标列表(这两个都没绑 backend,是空列表)。
	m.execTargets.EXPECT().Replace(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).Times(2)

	// 第二遍:Update Eng 把 LeadAgentID=200,UpdateRow IC 把 ParentAgentID=200
	// (UpdateRow 只落 Agent 行,不碰刚落好的执行目标列表)。
	m.depts.EXPECT().Update(gomock.Any(), gomock.AssignableToTypeOf(&department_entity.Department{})).
		DoAndReturn(func(_ context.Context, d *department_entity.Department) error {
			So(d.ID, ShouldEqual, 100)
			So(d.LeadAgentID, ShouldEqual, 200)
			return nil
		})
	m.agents.EXPECT().UpdateRow(gomock.Any(), gomock.AssignableToTypeOf(&agent_entity.Agent{})).
		DoAndReturn(func(_ context.Context, a *agent_entity.Agent) error {
			So(a.ID, ShouldEqual, 201)
			So(a.ParentAgentID, ShouldEqual, 200)
			return nil
		})

	m.dbMock.ExpectBegin()
	m.dbMock.ExpectCommit()

	bundle := data_svc.BundleV1{
		Format: data_svc.BundleFormat, Version: 1,
		Scopes: []string{string(data_svc.ScopeOrganization)},
		Items: data_svc.BundleItems{
			Departments: []data_svc.BundleDepartment{
				{ExportKey: "dept-eng", Name: "Eng", ParentKey: "", LeadAgentKey: "ag-lead"},
				{ExportKey: "dept-be", Name: "Backend", ParentKey: "dept-eng"},
			},
			Agents: []data_svc.BundleAgent{
				{ExportKey: "ag-lead", Name: "Lead", DepartmentKey: "dept-eng"},
				{ExportKey: "ag-ic", Name: "IC", DepartmentKey: "dept-be", ParentAgentKey: "ag-lead"},
			},
		},
	}
	raw, _ := json.Marshal(bundle)

	Convey("org 两遍 backfill 串好 parent/lead", t, func() {
		res, err := m.svc.ApplyImport(m.ctx, &data_svc.ApplyImportRequest{
			Raw: raw, FallbackStrategy: data_svc.ActionCreate,
		})
		So(err, ShouldBeNil)
		So(res.Counts["created"], ShouldEqual, 4)
	})
}

func TestApplyImport_Org_OverwriteExistingDept(t *testing.T) {
	m := setupDataSvcTest(t)
	m.providers.EXPECT().List(gomock.Any()).Return(nil, nil).Times(2)
	m.devices.EXPECT().List(gomock.Any()).Return(nil, nil).Times(2)
	m.backends.EXPECT().List(gomock.Any()).Return(nil, nil).Times(2)
	// depts.List called once in PreviewImport; existing Eng causes conflict for bundle Eng
	m.depts.EXPECT().List(gomock.Any()).Return([]*department_entity.Department{
		{ID: 10, Name: "Eng", ParentID: 0},
	}, nil).Times(1)

	// applyDepartments: FindByName finds the existing dept (same name, same parent)
	m.depts.EXPECT().FindByName(gomock.Any(), "Eng", int64(0)).
		Return(&department_entity.Department{ID: 10, Name: "Eng", ParentID: 0}, nil)

	// overwrite calls Update
	m.depts.EXPECT().Update(gomock.Any(), gomock.AssignableToTypeOf(&department_entity.Department{})).
		DoAndReturn(func(_ context.Context, d *department_entity.Department) error {
			So(d.ID, ShouldEqual, 10)
			So(d.Description, ShouldEqual, "Engineering team")
			return nil
		})

	m.dbMock.ExpectBegin()
	m.dbMock.ExpectCommit()

	bundle := data_svc.BundleV1{
		Format: data_svc.BundleFormat, Version: 1,
		Scopes: []string{string(data_svc.ScopeOrganization)},
		Items: data_svc.BundleItems{
			Departments: []data_svc.BundleDepartment{
				// Same name "Eng" → conflict detected by PreviewImport → overwrite fallback used
				{ExportKey: "dept-eng", Name: "Eng", Description: "Engineering team", ParentKey: ""},
			},
		},
	}
	raw, _ := json.Marshal(bundle)

	Convey("overwrite 已有部门调 Update", t, func() {
		res, err := m.svc.ApplyImport(m.ctx, &data_svc.ApplyImportRequest{
			Raw: raw, FallbackStrategy: data_svc.ActionOverwrite,
		})
		So(err, ShouldBeNil)
		So(res.Counts["overwrote"], ShouldEqual, 1)
	})
}

func TestApplyImport_Org_OverwriteExistingAgent(t *testing.T) {
	m := setupDataSvcTest(t)
	m.providers.EXPECT().List(gomock.Any()).Return(nil, nil).Times(2)
	m.devices.EXPECT().List(gomock.Any()).Return(nil, nil).Times(2)
	m.backends.EXPECT().List(gomock.Any()).Return(nil, nil).Times(2)
	// No local depts or agents — no conflict detected by PreviewImport
	m.depts.EXPECT().List(gomock.Any()).Return(nil, nil).Times(1)

	// applyDepartments: new dept, create
	m.depts.EXPECT().FindByName(gomock.Any(), "Eng", int64(0)).Return(nil, nil)
	m.depts.EXPECT().Create(gomock.Any(), gomock.AssignableToTypeOf(&department_entity.Department{})).
		DoAndReturn(func(_ context.Context, d *department_entity.Department) error {
			d.ID = 10
			return nil
		})

	// applyAgents: ListByDepartment returns existing agent "Lead" for overwrite
	m.agents.EXPECT().ListByDepartment(gomock.Any(), int64(10)).
		Return([]*agent_entity.Agent{{ID: 20, Name: "Lead", DepartmentID: 10}}, nil)
	m.agents.EXPECT().UpdateWithTargets(gomock.Any(), gomock.AssignableToTypeOf(&agent_entity.Agent{}), gomock.Any()).
		DoAndReturn(func(_ context.Context, a *agent_entity.Agent, _ []*agent_entity.AgentExecTarget) error {
			So(a.ID, ShouldEqual, 20)
			So(a.Description, ShouldEqual, "lead agent")
			return nil
		})

	m.dbMock.ExpectBegin()
	m.dbMock.ExpectCommit()

	bundle := data_svc.BundleV1{
		Format: data_svc.BundleFormat, Version: 1,
		Scopes: []string{string(data_svc.ScopeOrganization)},
		Items: data_svc.BundleItems{
			Departments: []data_svc.BundleDepartment{
				{ExportKey: "dept-eng", Name: "Eng", ParentKey: ""},
			},
			Agents: []data_svc.BundleAgent{
				// Same name "Lead" so ListByDepartment finds it; explicit action=overwrite
				{ExportKey: "ag-lead", Name: "Lead", Description: "lead agent", DepartmentKey: "dept-eng"},
			},
		},
	}
	raw, _ := json.Marshal(bundle)

	Convey("overwrite 已有 agent 调 Update (explicit action)", t, func() {
		res, err := m.svc.ApplyImport(m.ctx, &data_svc.ApplyImportRequest{
			Raw: raw,
			// Explicitly override action for this agent
			Actions: map[string]data_svc.ItemAction{
				"organization:ag-lead": data_svc.ActionOverwrite,
			},
			FallbackStrategy: data_svc.ActionCreate,
		})
		So(err, ShouldBeNil)
		So(res.Counts["created"], ShouldEqual, 1)   // dept created
		So(res.Counts["overwrote"], ShouldEqual, 1) // agent overwrote
	})
}

func TestPreviewImport_OrgScope_AgentsAndDepts(t *testing.T) {
	m := setupDataSvcTest(t)
	m.providers.EXPECT().List(gomock.Any()).Return(nil, nil)
	m.devices.EXPECT().List(gomock.Any()).Return(nil, nil)
	m.backends.EXPECT().List(gomock.Any()).Return(nil, nil)
	m.depts.EXPECT().List(gomock.Any()).Return([]*department_entity.Department{
		{ID: 10, Name: "Eng", ParentID: 0},
	}, nil)

	bundle := data_svc.BundleV1{
		Format: data_svc.BundleFormat, Version: 1,
		Scopes: []string{string(data_svc.ScopeOrganization)},
		Items: data_svc.BundleItems{
			Departments: []data_svc.BundleDepartment{
				{ExportKey: "dept-eng", Name: "Eng", ParentKey: ""},            // conflict (same root name)
				{ExportKey: "dept-be", Name: "Backend", ParentKey: "dept-eng"}, // parent in bundle → no dangling
				{ExportKey: "dept-x", Name: "X", ParentKey: "dept-missing"},    // parent NOT in bundle → dangling
			},
			Agents: []data_svc.BundleAgent{
				{ExportKey: "ag-1", Name: "A1", DepartmentKey: "dept-eng"},                                            // dept in bundle
				{ExportKey: "ag-2", Name: "A2", DepartmentKey: "dept-gone"},                                           // dept NOT in bundle → dangling
				{ExportKey: "ag-3", Name: "A3", ExecTargets: []data_svc.BundleExecTarget{{BackendKey: "ab-missing"}}}, // backend NOT in bundle → dangling
				{ExportKey: "ag-4", Name: "A4", ParentAgentKey: "ag-gone"},                                            // parent NOT in bundle → dangling
			},
		},
	}
	raw, _ := json.Marshal(bundle)

	Convey("org preview: conflict, dangling dept parent, dangling agent refs", t, func() {
		pv, err := m.svc.PreviewImport(m.ctx, raw)
		So(err, ShouldBeNil)
		So(pv.Items, ShouldHaveLength, 7)

		// Find Eng dept item — should be conflict
		var engItem data_svc.ImportItem
		for _, it := range pv.Items {
			if it.Scope == string(data_svc.ScopeOrganization) && it.Name == "Eng" {
				engItem = it
			}
		}
		So(engItem.Conflict, ShouldBeTrue)
		So(engItem.DefaultAction, ShouldEqual, data_svc.ActionSkip)
	})
}

func TestPreviewImport_BackendNumericDeviceIDIsDangling(t *testing.T) {
	m := setupDataSvcTest(t)
	m.providers.EXPECT().List(gomock.Any()).Return(nil, nil)
	m.devices.EXPECT().List(gomock.Any()).Return([]*paired_agentred_entity.PairedAgentred{
		{ID: 1, InstanceUUID: "uuid-1", Name: "Server1"},
	}, nil)
	m.backends.EXPECT().List(gomock.Any()).Return(nil, nil)
	m.depts.EXPECT().List(gomock.Any()).Return(nil, nil)

	bundle := data_svc.BundleV1{
		Format: data_svc.BundleFormat, Version: 1,
		Scopes: []string{string(data_svc.ScopeRemoteDevices), string(data_svc.ScopeAgentBackends)},
		Items: data_svc.BundleItems{
			RemoteDevices: []data_svc.BundleRemoteDevice{
				{InstanceUUID: "uuid-1", Name: "Server1"},
			},
			AgentBackends: []data_svc.BundleAgentBackend{
				{ExportKey: "ab-1", Type: "codex", Name: "Remote Codex", DeviceID: "1"},
			},
		},
	}
	raw, _ := json.Marshal(bundle)

	Convey("当 backend 使用非稳定的数字 deviceId，则预览将其标记为 dangling", t, func() {
		pv, err := m.svc.PreviewImport(m.ctx, raw)
		So(err, ShouldBeNil)
		So(pv.Items, ShouldHaveLength, 2)
		var backend data_svc.ImportItem
		for _, it := range pv.Items {
			if it.Scope == string(data_svc.ScopeAgentBackends) {
				backend = it
			}
		}
		So(backend.Dangling, ShouldBeTrue)
		So(backend.DefaultAction, ShouldEqual, data_svc.ActionSkip)
	})
}

func TestExport_RemoteDevices(t *testing.T) {
	m := setupDataSvcTest(t)
	m.devices.EXPECT().List(gomock.Any()).Return([]*paired_agentred_entity.PairedAgentred{
		{ID: 1, InstanceUUID: "uuid-1", Name: "Server1", URL: "http://x", TLSCertPEM: "pem"},
	}, nil)

	Convey("export remote-devices scope without secrets", t, func() {
		res, err := m.svc.Export(m.ctx, &data_svc.ExportRequest{
			Scopes:         []string{string(data_svc.ScopeRemoteDevices)},
			IncludeSecrets: false,
		})
		So(err, ShouldBeNil)
		var b data_svc.BundleV1
		So(json.Unmarshal(res.JSON, &b), ShouldBeNil)
		So(b.Items.RemoteDevices, ShouldHaveLength, 1)
		So(b.Items.RemoteDevices[0].InstanceUUID, ShouldEqual, "uuid-1")
		So(b.Items.RemoteDevices[0].TLSCertPEM, ShouldEqual, "") // scrubbed
	})
}

func TestExport_AgentBackends_RemoteDeviceUsesInstanceUUID(t *testing.T) {
	m := setupDataSvcTest(t)
	m.backends.EXPECT().List(gomock.Any()).Return([]*agent_backend_entity.AgentBackend{
		{ID: 10, Type: "codex", Name: "Remote Codex", DeviceFingerprint: "7"},
	}, nil)
	m.devices.EXPECT().List(gomock.Any()).Return([]*paired_agentred_entity.PairedAgentred{
		{ID: 7, InstanceUUID: "uuid-7", Name: "Server7"},
	}, nil)

	Convey("export agent-backends 将本地 device row ID 转成可迁移的 instanceUUID", t, func() {
		res, err := m.svc.Export(m.ctx, &data_svc.ExportRequest{
			Scopes: []string{string(data_svc.ScopeAgentBackends)},
		})
		So(err, ShouldBeNil)
		var b data_svc.BundleV1
		So(json.Unmarshal(res.JSON, &b), ShouldBeNil)
		So(b.Items.AgentBackends, ShouldHaveLength, 1)
		So(b.Items.AgentBackends[0].DeviceID, ShouldEqual, "uuid-7")
	})
}

// TestExport_AgentBackend_TypedModelTargetAndStructuredRoutes 导出侧验收：backend 的
// ModelTarget（LLMProviderKey + LLMModelKey 两个稳定字符串键）与结构化 Route target
// 原样落入 bundle，modelRoutes 是对象而非字符串，不携带任何 Provider/模型正文。
func TestExport_AgentBackend_TypedModelTargetAndStructuredRoutes(t *testing.T) {
	m := setupDataSvcTest(t)
	m.backends.EXPECT().List(gomock.Any()).Return([]*agent_backend_entity.AgentBackend{
		{ID: 10, Type: "claudecode", Name: "Local",
			LLMProviderKey: "anthropic-main", LLMModelKey: "anthropic-opus-01",
			ModelRoutes: `{"OPUS":{"providerKey":"anthropic-main","modelKey":"anthropic-opus-01"}` +
				`,"HAIKU":{"providerKey":"anthropic-main","modelKey":"anthropic-haiku-01"}}`},
	}, nil)

	Convey("export backend 携带类型化 ModelTarget 与结构化 Route target", t, func() {
		res, err := m.svc.Export(m.ctx, &data_svc.ExportRequest{
			Scopes: []string{string(data_svc.ScopeAgentBackends)},
		})
		So(err, ShouldBeNil)
		var b data_svc.BundleV1
		So(json.Unmarshal(res.JSON, &b), ShouldBeNil)
		So(b.Items.AgentBackends, ShouldHaveLength, 1)
		bk := b.Items.AgentBackends[0]
		So(bk.LLMProviderKey, ShouldEqual, "anthropic-main")
		So(bk.LLMModelKey, ShouldEqual, "anthropic-opus-01")
		So(bk.ModelRoutes, ShouldResemble, map[string]data_svc.BundleRouteTarget{
			"OPUS":  {ProviderKey: "anthropic-main", ModelKey: "anthropic-opus-01"},
			"HAIKU": {ProviderKey: "anthropic-main", ModelKey: "anthropic-haiku-01"},
		})
	})
}

// TestApplyImport_Backend_TypedModelTargetAndStructuredRoutes 导入侧验收：bundle 的
// 类型化 ModelTarget 与结构化 Route target 落回 backend 实体——LLMModelKey 原样保留，
// ModelRoutes 序列化回结构化 JSON 字符串。
func TestApplyImport_Backend_TypedModelTargetAndStructuredRoutes(t *testing.T) {
	m := setupDataSvcTest(t)
	m.providers.EXPECT().List(gomock.Any()).Return([]*llm_provider_entity.LLMProvider{}, nil).Times(2)
	m.devices.EXPECT().List(gomock.Any()).Return(nil, nil).Times(2)
	m.backends.EXPECT().List(gomock.Any()).Return([]*agent_backend_entity.AgentBackend{}, nil).Times(2)
	m.depts.EXPECT().List(gomock.Any()).Return(nil, nil).Times(1)

	// provider 在导入范围内，backend 的 provider_key 引用才不 dangling。
	m.providers.EXPECT().CreateWithModels(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, p *llm_provider_entity.LLMProvider, _ []*llm_provider_model_entity.LLMProviderModel, _ string) error {
			p.ID = 50
			return nil
		})

	m.backends.EXPECT().Create(gomock.Any(), gomock.AssignableToTypeOf(&agent_backend_entity.AgentBackend{})).
		DoAndReturn(func(_ context.Context, bk *agent_backend_entity.AgentBackend) error {
			So(bk.LLMProviderKey, ShouldEqual, "anthropic-main")
			So(bk.LLMModelKey, ShouldEqual, "anthropic-opus-01")
			So(bk.ModelRoutes, ShouldEqual, `{"OPUS":{"providerKey":"anthropic-main","modelKey":"anthropic-opus-01"}}`)
			bk.ID = 60
			return nil
		})

	m.dbMock.ExpectBegin()
	m.dbMock.ExpectCommit()

	bundle := data_svc.BundleV1{
		Format: data_svc.BundleFormat, Version: 1,
		Scopes: []string{string(data_svc.ScopeLLMProviders), string(data_svc.ScopeAgentBackends)},
		Items: data_svc.BundleItems{
			LLMProviders: []data_svc.BundleLLMProvider{{ProviderKey: "anthropic-main", Name: "P"}},
			AgentBackends: []data_svc.BundleAgentBackend{
				{ExportKey: "ab-1", Type: "claudecode", Name: "B",
					LLMProviderKey: "anthropic-main", LLMModelKey: "anthropic-opus-01",
					ModelRoutes: map[string]data_svc.BundleRouteTarget{
						"OPUS": {ProviderKey: "anthropic-main", ModelKey: "anthropic-opus-01"},
					}},
			},
		},
	}
	raw, _ := json.Marshal(bundle)

	Convey("import backend 携带类型化 ModelTarget 与结构化 Route target", t, func() {
		res, err := m.svc.ApplyImport(m.ctx, &data_svc.ApplyImportRequest{
			Raw: raw, FallbackStrategy: data_svc.ActionCreate,
		})
		So(err, ShouldBeNil)
		So(res.Counts["created"], ShouldEqual, 2)
	})
}

// TestApplyImport_Backend_LegacyStringModelRoutes_Rejected 预发布 bundle 直接切换
// 到新形状：modelRoutes 仍是 JSON 字符串的旧 fixture 在解析阶段就被明确拒绝，不保留
// 兼容解析。ApplyImport 在反序列化阶段就返回，不会触达任何 repo。
func TestApplyImport_Backend_LegacyStringModelRoutes_Rejected(t *testing.T) {
	m := setupDataSvcTest(t)

	raw := []byte(`{"format":"agentre-data-bundle","version":1,` +
		`"scopes":["agent_backends"],` +
		`"items":{"agentBackends":[{"exportKey":"ab-1","type":"claudecode",` +
		`"name":"B","modelRoutes":"{\"OPUS\":{\"providerKey\":\"p\",\"modelKey\":\"m\"}}"}]}}`)

	Convey("旧 string modelRoutes fixture 被明确拒绝", t, func() {
		_, err := m.svc.ApplyImport(m.ctx, &data_svc.ApplyImportRequest{
			Raw: raw, FallbackStrategy: data_svc.ActionCreate,
		})
		So(err, ShouldNotBeNil)
	})
}

// TestExportImport_RoundTrip_TypedModelTargetAndStructuredRoutes 验收判据本身：设备 A
// 导出携带类型化 ModelTarget 与结构化 Route target 的 backend（连同它引用的
// Provider），把导出的 bundle 喂给一台全新的设备 B，LLMModelKey 与 ModelRoutes
// （结构化 JSON）必须逐字段保留——即便两台设备上 backend 的本地自增 ID 完全不同。
func TestExportImport_RoundTrip_TypedModelTargetAndStructuredRoutes(t *testing.T) {
	mA := setupDataSvcTest(t)
	mA.providers.EXPECT().List(gomock.Any()).Return([]*llm_provider_entity.LLMProvider{
		{ID: 1, ProviderKey: "anthropic-main", Name: "Anthropic", Type: "anthropic"},
	}, nil)
	mA.providers.EXPECT().ListModels(gomock.Any(), int64(1)).Return(nil, nil)
	mA.backends.EXPECT().List(gomock.Any()).Return([]*agent_backend_entity.AgentBackend{
		{ID: 30, Type: "claudecode", Name: "B",
			LLMProviderKey: "anthropic-main", LLMModelKey: "anthropic-opus-01",
			ModelRoutes: `{"OPUS":{"providerKey":"anthropic-main","modelKey":"anthropic-opus-01"}` +
				`,"HAIKU":{"providerKey":"anthropic-main","modelKey":"anthropic-haiku-01"}}`},
	}, nil)

	exportRes, err := mA.svc.Export(mA.ctx, &data_svc.ExportRequest{
		Scopes: []string{string(data_svc.ScopeLLMProviders), string(data_svc.ScopeAgentBackends)},
	})
	if err != nil {
		t.Fatalf("export: %v", err)
	}

	var srcBundle data_svc.BundleV1
	if err := json.Unmarshal(exportRes.JSON, &srcBundle); err != nil {
		t.Fatalf("unmarshal exported bundle: %v", err)
	}
	srcBk := srcBundle.Items.AgentBackends[0]
	if srcBk.LLMModelKey != "anthropic-opus-01" {
		t.Fatalf("exported bundle lost llmModelKey: %q", srcBk.LLMModelKey)
	}
	if len(srcBk.ModelRoutes) != 2 {
		t.Fatalf("expected 2 structured routes in exported bundle, got %d", len(srcBk.ModelRoutes))
	}

	// 设备 B：全新空库，provider 与 backend 都拿到与设备 A 不同的本地 ID。
	mB := setupDataSvcTest(t)
	mB.providers.EXPECT().List(gomock.Any()).Return([]*llm_provider_entity.LLMProvider{}, nil).Times(2)
	mB.devices.EXPECT().List(gomock.Any()).Return(nil, nil).Times(2)
	mB.backends.EXPECT().List(gomock.Any()).Return([]*agent_backend_entity.AgentBackend{}, nil).Times(2)
	mB.depts.EXPECT().List(gomock.Any()).Return([]*department_entity.Department{}, nil)

	mB.providers.EXPECT().CreateWithModels(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, p *llm_provider_entity.LLMProvider, _ []*llm_provider_model_entity.LLMProviderModel, _ string) error {
			p.ID = 7
			return nil
		})

	var got *agent_backend_entity.AgentBackend
	mB.backends.EXPECT().Create(gomock.Any(), gomock.AssignableToTypeOf(&agent_backend_entity.AgentBackend{})).
		DoAndReturn(func(_ context.Context, bk *agent_backend_entity.AgentBackend) error {
			got = bk
			bk.ID = 100
			return nil
		})

	mB.dbMock.ExpectBegin()
	mB.dbMock.ExpectCommit()

	raw, err := json.Marshal(srcBundle)
	if err != nil {
		t.Fatalf("marshal src bundle: %v", err)
	}

	applyRes, err := mB.svc.ApplyImport(mB.ctx, &data_svc.ApplyImportRequest{
		Raw: raw, FallbackStrategy: data_svc.ActionCreate,
	})
	if err != nil {
		t.Fatalf("apply import: %v", err)
	}
	if applyRes.Counts["created"] == 0 {
		t.Fatalf("expected creations, got counts=%v", applyRes.Counts)
	}
	if got == nil {
		t.Fatalf("backend was not created on device B")
	}
	if got.LLMProviderKey != "anthropic-main" || got.LLMModelKey != "anthropic-opus-01" {
		t.Fatalf("typed ModelTarget lost in round-trip: provider=%q model=%q", got.LLMProviderKey, got.LLMModelKey)
	}
	// MarshalModelRoutes 按 alias 字典序序列化（HAIKU < OPUS）。
	if got.ModelRoutes != `{"HAIKU":{"providerKey":"anthropic-main","modelKey":"anthropic-haiku-01"},`+
		`"OPUS":{"providerKey":"anthropic-main","modelKey":"anthropic-opus-01"}}` {
		t.Fatalf("structured routes lost in round-trip: %s", got.ModelRoutes)
	}
}

func TestApplyImport_RemoteDevice_Create(t *testing.T) {
	m := setupDataSvcTest(t)
	m.providers.EXPECT().List(gomock.Any()).Return(nil, nil).Times(2)
	m.devices.EXPECT().List(gomock.Any()).Return([]*paired_agentred_entity.PairedAgentred{}, nil).Times(2)
	m.backends.EXPECT().List(gomock.Any()).Return(nil, nil).Times(2)
	m.depts.EXPECT().List(gomock.Any()).Return(nil, nil).Times(1)

	m.devices.EXPECT().Create(gomock.Any(), gomock.AssignableToTypeOf(&paired_agentred_entity.PairedAgentred{})).
		DoAndReturn(func(_ context.Context, d *paired_agentred_entity.PairedAgentred) error {
			So(d.InstanceUUID, ShouldEqual, "uuid-1")
			So(d.Name, ShouldEqual, "Server1")
			d.ID = 50
			return nil
		})

	m.dbMock.ExpectBegin()
	m.dbMock.ExpectCommit()

	bundle := data_svc.BundleV1{
		Format: data_svc.BundleFormat, Version: 1,
		Scopes: []string{string(data_svc.ScopeRemoteDevices)},
		Items: data_svc.BundleItems{
			RemoteDevices: []data_svc.BundleRemoteDevice{
				{InstanceUUID: "uuid-1", Name: "Server1", URL: "http://x"},
			},
		},
	}
	raw, _ := json.Marshal(bundle)

	Convey("create 远端设备", t, func() {
		res, err := m.svc.ApplyImport(m.ctx, &data_svc.ApplyImportRequest{
			Raw: raw, FallbackStrategy: data_svc.ActionCreate,
		})
		So(err, ShouldBeNil)
		So(res.Counts["created"], ShouldEqual, 1)
	})
}

func TestApplyImport_Backend_Overwrite(t *testing.T) {
	m := setupDataSvcTest(t)
	m.providers.EXPECT().List(gomock.Any()).Return(nil, nil).Times(2)
	m.devices.EXPECT().List(gomock.Any()).Return(nil, nil).Times(2)
	m.backends.EXPECT().List(gomock.Any()).Return([]*agent_backend_entity.AgentBackend{
		{ID: 10, Name: "Local", Type: "claudecode"},
	}, nil).Times(2)
	m.depts.EXPECT().List(gomock.Any()).Return(nil, nil).Times(1)

	m.backends.EXPECT().Update(gomock.Any(), gomock.AssignableToTypeOf(&agent_backend_entity.AgentBackend{})).
		DoAndReturn(func(_ context.Context, bk *agent_backend_entity.AgentBackend) error {
			So(bk.ID, ShouldEqual, 10)
			So(bk.CLIPath, ShouldEqual, "/usr/local/bin/claude")
			return nil
		})

	m.dbMock.ExpectBegin()
	m.dbMock.ExpectCommit()

	bundle := data_svc.BundleV1{
		Format: data_svc.BundleFormat, Version: 1,
		Scopes: []string{string(data_svc.ScopeAgentBackends)},
		Items: data_svc.BundleItems{
			AgentBackends: []data_svc.BundleAgentBackend{
				{ExportKey: "ab-1", Name: "Local", Type: "claudecode", CLIPath: "/usr/local/bin/claude"},
			},
		},
	}
	raw, _ := json.Marshal(bundle)

	Convey("overwrite backend 调 Update", t, func() {
		res, err := m.svc.ApplyImport(m.ctx, &data_svc.ApplyImportRequest{
			Raw:              raw,
			FallbackStrategy: data_svc.ActionOverwrite,
		})
		So(err, ShouldBeNil)
		So(res.Counts["overwrote"], ShouldEqual, 1)
	})
}

func TestApplyImport_RemoteDevice_Skip(t *testing.T) {
	m := setupDataSvcTest(t)
	existing := []*paired_agentred_entity.PairedAgentred{{ID: 5, InstanceUUID: "uuid-1", Name: "S1"}}
	m.providers.EXPECT().List(gomock.Any()).Return(nil, nil).Times(2)
	m.devices.EXPECT().List(gomock.Any()).Return(existing, nil).Times(2)
	m.backends.EXPECT().List(gomock.Any()).Return(nil, nil).Times(2)
	m.depts.EXPECT().List(gomock.Any()).Return(nil, nil).Times(1)

	m.dbMock.ExpectBegin()
	m.dbMock.ExpectCommit()

	bundle := data_svc.BundleV1{
		Format: data_svc.BundleFormat, Version: 1,
		Scopes: []string{string(data_svc.ScopeRemoteDevices)},
		Items: data_svc.BundleItems{
			RemoteDevices: []data_svc.BundleRemoteDevice{
				{InstanceUUID: "uuid-1", Name: "S1"},
			},
		},
	}
	raw, _ := json.Marshal(bundle)

	Convey("skip 远端设备冲突", t, func() {
		res, err := m.svc.ApplyImport(m.ctx, &data_svc.ApplyImportRequest{
			Raw: raw, FallbackStrategy: data_svc.ActionSkip,
		})
		So(err, ShouldBeNil)
		So(res.Counts["skipped"], ShouldEqual, 1)
	})
}

func TestApplyImport_RemoteDevice_Overwrite(t *testing.T) {
	m := setupDataSvcTest(t)
	existing := []*paired_agentred_entity.PairedAgentred{{ID: 5, InstanceUUID: "uuid-1", Name: "S1", TLSMode: "none"}}
	m.providers.EXPECT().List(gomock.Any()).Return(nil, nil).Times(2)
	m.devices.EXPECT().List(gomock.Any()).Return(existing, nil).Times(2)
	m.backends.EXPECT().List(gomock.Any()).Return(nil, nil).Times(2)
	m.depts.EXPECT().List(gomock.Any()).Return(nil, nil).Times(1)

	// updateRemoteDevice calls UpdateTLS + UpdateEndpoint + Rename
	m.devices.EXPECT().UpdateTLS(gomock.Any(), int64(5), gomock.Any(), gomock.Any()).Return(nil)
	m.devices.EXPECT().UpdateEndpoint(gomock.Any(), int64(5), gomock.Any(), gomock.Any()).Return(nil)
	m.devices.EXPECT().Rename(gomock.Any(), int64(5), "S1 Renamed").Return(nil)

	m.dbMock.ExpectBegin()
	m.dbMock.ExpectCommit()

	bundle := data_svc.BundleV1{
		Format: data_svc.BundleFormat, Version: 1,
		Scopes: []string{string(data_svc.ScopeRemoteDevices)},
		Items: data_svc.BundleItems{
			RemoteDevices: []data_svc.BundleRemoteDevice{
				{InstanceUUID: "uuid-1", Name: "S1 Renamed"},
			},
		},
	}
	raw, _ := json.Marshal(bundle)

	Convey("overwrite 远端设备调 UpdateTLS+Rename", t, func() {
		res, err := m.svc.ApplyImport(m.ctx, &data_svc.ApplyImportRequest{
			Raw: raw, FallbackStrategy: data_svc.ActionOverwrite,
		})
		So(err, ShouldBeNil)
		So(res.Counts["overwrote"], ShouldEqual, 1)
	})
}

func TestApplyImport_Agent_CreateGuardedByExisting(t *testing.T) {
	m := setupDataSvcTest(t)
	m.providers.EXPECT().List(gomock.Any()).Return(nil, nil).Times(2)
	m.devices.EXPECT().List(gomock.Any()).Return(nil, nil).Times(2)
	m.backends.EXPECT().List(gomock.Any()).Return(nil, nil).Times(2)
	// No local depts — no conflict in preview
	m.depts.EXPECT().List(gomock.Any()).Return(nil, nil).Times(1)

	// dept is new
	m.depts.EXPECT().FindByName(gomock.Any(), "Eng", int64(0)).Return(nil, nil)
	m.depts.EXPECT().Create(gomock.Any(), gomock.AssignableToTypeOf(&department_entity.Department{})).
		DoAndReturn(func(_ context.Context, d *department_entity.Department) error {
			d.ID = 10
			return nil
		})

	// agent "Coder" already exists in dept 10 — apply-time conflict
	existingAgent := &agent_entity.Agent{ID: 77, Name: "Coder", DepartmentID: 10}
	m.agents.EXPECT().ListByDepartment(gomock.Any(), int64(10)).
		Return([]*agent_entity.Agent{existingAgent}, nil)
	// Create must NOT be called — no EXPECT().Create

	m.dbMock.ExpectBegin()
	m.dbMock.ExpectCommit()

	bundle := data_svc.BundleV1{
		Format: data_svc.BundleFormat, Version: 1,
		Scopes: []string{string(data_svc.ScopeOrganization)},
		Items: data_svc.BundleItems{
			Departments: []data_svc.BundleDepartment{
				{ExportKey: "dept-eng", Name: "Eng"},
			},
			Agents: []data_svc.BundleAgent{
				{ExportKey: "ag-coder", Name: "Coder", DepartmentKey: "dept-eng"},
			},
		},
	}
	raw, _ := json.Marshal(bundle)

	Convey("agent create guarded by existing local: skipped, keymap wired to existing ID", t, func() {
		res, err := m.svc.ApplyImport(m.ctx, &data_svc.ApplyImportRequest{
			Raw:              raw,
			FallbackStrategy: data_svc.ActionCreate,
		})
		So(err, ShouldBeNil)
		So(res.Counts["skipped"], ShouldEqual, 1) // agent silently skipped
		So(res.Counts["created"], ShouldEqual, 1) // dept still created
	})
}

func TestApplyImport_NestedDept_CreateGuardedByExisting(t *testing.T) {
	m := setupDataSvcTest(t)
	m.providers.EXPECT().List(gomock.Any()).Return(nil, nil).Times(2)
	m.devices.EXPECT().List(gomock.Any()).Return(nil, nil).Times(2)
	m.backends.EXPECT().List(gomock.Any()).Return(nil, nil).Times(2)
	// No local depts — no conflict in preview (child dept can't be compared without parent resolution)
	m.depts.EXPECT().List(gomock.Any()).Return(nil, nil).Times(1)

	// Root Eng dept is new
	m.depts.EXPECT().FindByName(gomock.Any(), "Eng", int64(0)).Return(nil, nil)
	m.depts.EXPECT().Create(gomock.Any(), gomock.AssignableToTypeOf(&department_entity.Department{})).
		DoAndReturn(func(_ context.Context, d *department_entity.Department) error {
			if d.Name == "Eng" {
				d.ID = 10
			}
			return nil
		})

	// Child "Backend" already exists under Eng (parentID=10) at apply-time
	existingChild := &department_entity.Department{ID: 55, Name: "Backend", ParentID: 10}
	m.depts.EXPECT().FindByName(gomock.Any(), "Backend", int64(10)).Return(existingChild, nil)
	// Create for Backend must NOT be called

	m.dbMock.ExpectBegin()
	m.dbMock.ExpectCommit()

	bundle := data_svc.BundleV1{
		Format: data_svc.BundleFormat, Version: 1,
		Scopes: []string{string(data_svc.ScopeOrganization)},
		Items: data_svc.BundleItems{
			Departments: []data_svc.BundleDepartment{
				{ExportKey: "dept-eng", Name: "Eng", ParentKey: ""},
				{ExportKey: "dept-be", Name: "Backend", ParentKey: "dept-eng"},
			},
		},
	}
	raw, _ := json.Marshal(bundle)

	Convey("nested dept create guarded by existing: skipped, keymap wired to existing ID", t, func() {
		res, err := m.svc.ApplyImport(m.ctx, &data_svc.ApplyImportRequest{
			Raw:              raw,
			FallbackStrategy: data_svc.ActionCreate,
		})
		So(err, ShouldBeNil)
		So(res.Counts["skipped"], ShouldEqual, 1) // Backend silently skipped
		So(res.Counts["created"], ShouldEqual, 1) // Eng created
	})
}

func TestApplyImport_RemoteDevice_Overwrite_UpdatesURL(t *testing.T) {
	m := setupDataSvcTest(t)
	existing := []*paired_agentred_entity.PairedAgentred{{
		ID:                5,
		InstanceUUID:      "uuid-1",
		Name:              "OldName",
		URL:               "ws://old-host:9000",
		DaemonFingerprint: "old-fp",
		TLSMode:           "default",
	}}
	m.providers.EXPECT().List(gomock.Any()).Return(nil, nil).Times(2)
	m.devices.EXPECT().List(gomock.Any()).Return(existing, nil).Times(2)
	m.backends.EXPECT().List(gomock.Any()).Return(nil, nil).Times(2)
	m.depts.EXPECT().List(gomock.Any()).Return(nil, nil).Times(1)

	m.devices.EXPECT().UpdateTLS(gomock.Any(), int64(5), gomock.Any(), gomock.Any()).Return(nil)
	// Key assertion: UpdateEndpoint must be called with new URL + fingerprint
	m.devices.EXPECT().UpdateEndpoint(gomock.Any(), int64(5), "ws://new-host:9000", "new-fp").Return(nil)
	m.devices.EXPECT().Rename(gomock.Any(), int64(5), "NewName").Return(nil)

	m.dbMock.ExpectBegin()
	m.dbMock.ExpectCommit()

	bundle := data_svc.BundleV1{
		Format: data_svc.BundleFormat, Version: 1,
		Scopes: []string{string(data_svc.ScopeRemoteDevices)},
		Items: data_svc.BundleItems{
			RemoteDevices: []data_svc.BundleRemoteDevice{
				{
					InstanceUUID:      "uuid-1",
					Name:              "NewName",
					URL:               "ws://new-host:9000",
					DaemonFingerprint: "new-fp",
					TLSMode:           "default",
				},
			},
		},
	}
	raw, _ := json.Marshal(bundle)

	Convey("overwrite 远端设备时 URL 和 DaemonFingerprint 被持久化", t, func() {
		res, err := m.svc.ApplyImport(m.ctx, &data_svc.ApplyImportRequest{
			Raw: raw, FallbackStrategy: data_svc.ActionOverwrite,
		})
		So(err, ShouldBeNil)
		So(res.Counts["overwrote"], ShouldEqual, 1)
	})
}

func TestApplyImport_Backend_Skip(t *testing.T) {
	m := setupDataSvcTest(t)
	existing := []*agent_backend_entity.AgentBackend{{ID: 10, Name: "Local"}}
	m.providers.EXPECT().List(gomock.Any()).Return(nil, nil).Times(2)
	m.devices.EXPECT().List(gomock.Any()).Return(nil, nil).Times(2)
	m.backends.EXPECT().List(gomock.Any()).Return(existing, nil).Times(2)
	m.depts.EXPECT().List(gomock.Any()).Return(nil, nil).Times(1)

	m.dbMock.ExpectBegin()
	m.dbMock.ExpectCommit()

	bundle := data_svc.BundleV1{
		Format: data_svc.BundleFormat, Version: 1,
		Scopes: []string{string(data_svc.ScopeAgentBackends)},
		Items: data_svc.BundleItems{
			AgentBackends: []data_svc.BundleAgentBackend{
				{ExportKey: "ab-1", Name: "Local"},
			},
		},
	}
	raw, _ := json.Marshal(bundle)

	Convey("skip backend 冲突", t, func() {
		res, err := m.svc.ApplyImport(m.ctx, &data_svc.ApplyImportRequest{
			Raw: raw, FallbackStrategy: data_svc.ActionSkip,
		})
		So(err, ShouldBeNil)
		So(res.Counts["skipped"], ShouldEqual, 1)
	})
}

func TestApplyImport_Org_SkipAgent(t *testing.T) {
	m := setupDataSvcTest(t)
	m.providers.EXPECT().List(gomock.Any()).Return(nil, nil).Times(2)
	m.devices.EXPECT().List(gomock.Any()).Return(nil, nil).Times(2)
	m.backends.EXPECT().List(gomock.Any()).Return(nil, nil).Times(2)
	m.depts.EXPECT().List(gomock.Any()).Return(nil, nil).Times(1)

	// dept create
	m.depts.EXPECT().FindByName(gomock.Any(), "Eng", int64(0)).Return(nil, nil)
	m.depts.EXPECT().Create(gomock.Any(), gomock.AssignableToTypeOf(&department_entity.Department{})).
		DoAndReturn(func(_ context.Context, d *department_entity.Department) error { d.ID = 10; return nil })

	// agent skip — ListByDepartment returns existing match
	m.agents.EXPECT().ListByDepartment(gomock.Any(), int64(10)).
		Return([]*agent_entity.Agent{{ID: 20, Name: "Lead", DepartmentID: 10}}, nil)

	m.dbMock.ExpectBegin()
	m.dbMock.ExpectCommit()

	bundle := data_svc.BundleV1{
		Format: data_svc.BundleFormat, Version: 1,
		Scopes: []string{string(data_svc.ScopeOrganization)},
		Items: data_svc.BundleItems{
			Departments: []data_svc.BundleDepartment{
				{ExportKey: "dept-eng", Name: "Eng"},
			},
			Agents: []data_svc.BundleAgent{
				{ExportKey: "ag-lead", Name: "Lead", DepartmentKey: "dept-eng"},
			},
		},
	}
	raw, _ := json.Marshal(bundle)

	Convey("skip agent via explicit action", t, func() {
		res, err := m.svc.ApplyImport(m.ctx, &data_svc.ApplyImportRequest{
			Raw: raw,
			Actions: map[string]data_svc.ItemAction{
				"organization:ag-lead": data_svc.ActionSkip,
			},
			FallbackStrategy: data_svc.ActionCreate,
		})
		So(err, ShouldBeNil)
		So(res.Counts["skipped"], ShouldEqual, 1)
		So(res.Counts["created"], ShouldEqual, 1)
	})
}

func TestApplyImport_Provider_Duplicate(t *testing.T) {
	m := setupDataSvcTest(t)
	existing := []*llm_provider_entity.LLMProvider{{ID: 5, ProviderKey: "k1", Name: "P1"}}
	m.providers.EXPECT().List(gomock.Any()).Return(existing, nil).Times(2)
	m.devices.EXPECT().List(gomock.Any()).Return(nil, nil).Times(2)
	m.backends.EXPECT().List(gomock.Any()).Return(nil, nil).Times(2)
	m.depts.EXPECT().List(gomock.Any()).Return(nil, nil).Times(1)

	// Duplicate 创建新行(新 UUID key + (copy) 后缀),模型重 mint 新 ModelKey,
	// 默认 key 也重映射到新 key(本地已有同 key 模型,唯一索引不允许复用)。
	m.providers.EXPECT().CreateWithModels(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, p *llm_provider_entity.LLMProvider, models []*llm_provider_model_entity.LLMProviderModel, defaultKey string) error {
			// uniqueName appends " (copy)" since "P1" is taken
			So(p.Name, ShouldEqual, "P1 (copy)")
			So(p.ProviderKey, ShouldNotEqual, "k1")
			So(models, ShouldHaveLength, 1)
			So(models[0].ModelKey, ShouldNotEqual, "mk-1") // 重 mint,不复用旧 key
			So(models[0].ModelID, ShouldEqual, "claude-3-5-sonnet")
			So(defaultKey, ShouldEqual, models[0].ModelKey) // 默认 key 重映射
			p.ID = 99
			return nil
		})

	m.dbMock.ExpectBegin()
	m.dbMock.ExpectCommit()

	bundle := data_svc.BundleV1{
		Format: data_svc.BundleFormat, Version: 1,
		Scopes: []string{string(data_svc.ScopeLLMProviders)},
		Items: data_svc.BundleItems{LLMProviders: []data_svc.BundleLLMProvider{
			{
				ProviderKey: "k1", Name: "P1", Type: "anthropic",
				Enabled: true, DefaultModelKey: "mk-1",
				Models: []data_svc.BundleLLMProviderModel{
					{ModelKey: "mk-1", ModelID: "claude-3-5-sonnet", Enabled: true},
				},
			},
		}},
	}
	raw, _ := json.Marshal(bundle)

	Convey("duplicate 走 uniqueName 加 (copy) 后缀", t, func() {
		res, err := m.svc.ApplyImport(m.ctx, &data_svc.ApplyImportRequest{
			Raw: raw, FallbackStrategy: data_svc.ActionDuplicate,
		})
		So(err, ShouldBeNil)
		So(res.Counts["duplicated"], ShouldEqual, 1)
	})
}

func TestExport_BackendKey_SharedBetweenScopes(t *testing.T) {
	m := setupDataSvcTest(t)

	// AgentBackend().List must be called exactly once — the fix merges the two
	// independent calls into a single shared backendKey map.
	m.backends.EXPECT().List(gomock.Any()).Times(1).Return([]*agent_backend_entity.AgentBackend{
		{ID: 30, Type: "claudecode", Name: "Local"},
	}, nil)
	m.depts.EXPECT().List(gomock.Any()).Return([]*department_entity.Department{
		{ID: 10, Name: "Eng"},
	}, nil)
	m.agents.EXPECT().List(gomock.Any()).Return([]*agent_entity.Agent{
		{ID: 20, Name: "Coder", DepartmentID: 10, AgentBackendID: 30},
	}, nil)
	m.execTargets.EXPECT().ListByAgents(gomock.Any(), gomock.Any()).Return(
		map[int64][]*agent_entity.AgentExecTarget{
			20: {{AgentID: 20, AgentBackendID: 30, SortOrder: 0, SkillsJSON: "[]"}},
		}, nil)

	Convey("同时请求 ScopeAgentBackends + ScopeOrganization 时 backend exportKey 保持一致", t, func() {
		res, err := m.svc.Export(m.ctx, &data_svc.ExportRequest{
			Scopes: []string{
				string(data_svc.ScopeAgentBackends),
				string(data_svc.ScopeOrganization),
			},
		})
		So(err, ShouldBeNil)

		var b data_svc.BundleV1
		So(json.Unmarshal(res.JSON, &b), ShouldBeNil)

		So(b.Items.AgentBackends, ShouldHaveLength, 1)
		So(b.Items.Agents, ShouldHaveLength, 1)

		// The agent's execution target must reference the backend's exportKey.
		backendExportKey := b.Items.AgentBackends[0].ExportKey
		So(backendExportKey, ShouldNotBeEmpty)
		So(b.Items.Agents[0].ExecTargets, ShouldHaveLength, 1)
		So(b.Items.Agents[0].ExecTargets[0].BackendKey, ShouldEqual, backendExportKey)
	})
}

// TestExportImport_RoundTrip_PreservesExecTargetOrderAndPerTargetSkills 是 R15f
// 的验收判据本身:"设备 A" 导出一个排了两档的 Agent(顺序与 backend 创建顺序刻意
// 相反),把导出的 bundle 喂给一台全新的"设备 B",执行目标的顺序与每一档各自的
// skills_json 必须逐字段保留——即便两台设备上 backend 的本地自增 ID 完全不同。
func TestExportImport_RoundTrip_PreservesExecTargetOrderAndPerTargetSkills(t *testing.T) {
	mA := setupDataSvcTest(t)
	mA.depts.EXPECT().List(gomock.Any()).Return([]*department_entity.Department{}, nil)
	mA.agents.EXPECT().List(gomock.Any()).Return([]*agent_entity.Agent{
		{ID: 20, Name: "Lead", DepartmentID: 0, AgentBackendID: 31, SkillsJSON: "[]"},
	}, nil)
	mA.backends.EXPECT().List(gomock.Any()).Return([]*agent_backend_entity.AgentBackend{
		{ID: 30, Type: "claudecode", Name: "B1"},
		{ID: 31, Type: "codex", Name: "B2"},
	}, nil)
	mA.execTargets.EXPECT().ListByAgents(gomock.Any(), gomock.Any()).Return(
		map[int64][]*agent_entity.AgentExecTarget{
			20: {
				{AgentID: 20, AgentBackendID: 31, SortOrder: 0, SkillsJSON: `[{"id":"pkg-b2","enabled":true}]`},
				{AgentID: 20, AgentBackendID: 30, SortOrder: 1, SkillsJSON: `[{"id":"pkg-b1","enabled":false}]`},
			},
		}, nil)

	exportRes, err := mA.svc.Export(mA.ctx, &data_svc.ExportRequest{
		Scopes: []string{string(data_svc.ScopeOrganization), string(data_svc.ScopeAgentBackends)},
	})
	if err != nil {
		t.Fatalf("export: %v", err)
	}

	var srcBundle data_svc.BundleV1
	if err := json.Unmarshal(exportRes.JSON, &srcBundle); err != nil {
		t.Fatalf("unmarshal exported bundle: %v", err)
	}
	if len(srcBundle.Items.Agents) != 1 {
		t.Fatalf("expected 1 agent in bundle, got %d", len(srcBundle.Items.Agents))
	}
	srcTargets := srcBundle.Items.Agents[0].ExecTargets
	if len(srcTargets) != 2 {
		t.Fatalf("expected 2 exec targets in exported bundle, got %d (bundle 没带执行目标数组)", len(srcTargets))
	}

	// "设备 B":全新空库,backend 会拿到与设备 A 不同的本地 ID。
	mB := setupDataSvcTest(t)
	mB.providers.EXPECT().List(gomock.Any()).Return(nil, nil).Times(2)
	mB.devices.EXPECT().List(gomock.Any()).Return(nil, nil).Times(2)
	mB.backends.EXPECT().List(gomock.Any()).Return([]*agent_backend_entity.AgentBackend{}, nil).Times(2)
	mB.depts.EXPECT().List(gomock.Any()).Return([]*department_entity.Department{}, nil)

	backendIDSeq := int64(100)
	mB.backends.EXPECT().Create(gomock.Any(), gomock.AssignableToTypeOf(&agent_backend_entity.AgentBackend{})).
		DoAndReturn(func(_ context.Context, bk *agent_backend_entity.AgentBackend) error {
			backendIDSeq++
			bk.ID = backendIDSeq
			return nil
		}).Times(2)

	mB.agents.EXPECT().ListByDepartment(gomock.Any(), int64(0)).Return([]*agent_entity.Agent{}, nil)
	mB.agents.EXPECT().Create(gomock.Any(), gomock.AssignableToTypeOf(&agent_entity.Agent{})).
		DoAndReturn(func(_ context.Context, a *agent_entity.Agent) error {
			a.ID = 500
			return nil
		})

	var gotAgentID int64
	var gotTargets []*agent_entity.AgentExecTarget
	mB.execTargets.EXPECT().Replace(gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, agentID int64, targets []*agent_entity.AgentExecTarget) error {
			gotAgentID = agentID
			gotTargets = targets
			return nil
		})

	mB.dbMock.ExpectBegin()
	mB.dbMock.ExpectCommit()

	raw, err := json.Marshal(srcBundle)
	if err != nil {
		t.Fatalf("marshal src bundle: %v", err)
	}

	applyRes, err := mB.svc.ApplyImport(mB.ctx, &data_svc.ApplyImportRequest{
		Raw: raw, FallbackStrategy: data_svc.ActionCreate,
	})
	if err != nil {
		t.Fatalf("apply import: %v", err)
	}
	if applyRes.Counts["created"] == 0 {
		t.Fatalf("expected creations, got counts=%v", applyRes.Counts)
	}

	if gotAgentID != 500 {
		t.Fatalf("expected exec targets replaced for agent 500, got %d", gotAgentID)
	}
	if len(gotTargets) != 2 {
		t.Fatalf("expected 2 exec targets applied, got %d (往返丢了执行目标)", len(gotTargets))
	}
	// 下标即 sort_order:必须与导出时的顺序一致(先 B2 后 B1),每档的技能授权逐字段不变。
	if gotTargets[0].SkillsJSON != `[{"id":"pkg-b2","enabled":true}]` {
		t.Fatalf("target[0] skills mismatch: %s", gotTargets[0].SkillsJSON)
	}
	if gotTargets[1].SkillsJSON != `[{"id":"pkg-b1","enabled":false}]` {
		t.Fatalf("target[1] skills mismatch: %s", gotTargets[1].SkillsJSON)
	}
	if gotTargets[0].AgentBackendID == gotTargets[1].AgentBackendID || gotTargets[0].AgentBackendID == 0 || gotTargets[1].AgentBackendID == 0 {
		t.Fatalf("targets should resolve to two distinct non-zero local backend ids, got %d and %d",
			gotTargets[0].AgentBackendID, gotTargets[1].AgentBackendID)
	}
}

func TestApplyImport_AgentWithoutExecTargetsImportsEmptyTargetList(t *testing.T) {
	m := setupDataSvcTest(t)
	m.providers.EXPECT().List(gomock.Any()).Return(nil, nil).Times(2)
	m.devices.EXPECT().List(gomock.Any()).Return(nil, nil).Times(2)
	m.backends.EXPECT().List(gomock.Any()).Return([]*agent_backend_entity.AgentBackend{}, nil).Times(2)
	m.depts.EXPECT().List(gomock.Any()).Return(nil, nil)

	m.backends.EXPECT().Create(gomock.Any(), gomock.AssignableToTypeOf(&agent_backend_entity.AgentBackend{})).
		DoAndReturn(func(_ context.Context, bk *agent_backend_entity.AgentBackend) error {
			bk.ID = 30
			return nil
		})

	m.agents.EXPECT().ListByDepartment(gomock.Any(), int64(0)).Return([]*agent_entity.Agent{}, nil)
	m.agents.EXPECT().Create(gomock.Any(), gomock.AssignableToTypeOf(&agent_entity.Agent{})).
		DoAndReturn(func(_ context.Context, a *agent_entity.Agent) error {
			if a.AgentBackendID != 0 {
				t.Errorf("expected no primary backend without execTargets, got %d", a.AgentBackendID)
			}
			if a.SkillsJSON != "" {
				t.Errorf("expected no primary skills without execTargets, got %q", a.SkillsJSON)
			}
			a.ID = 40
			return nil
		})

	var gotTargets []*agent_entity.AgentExecTarget
	m.execTargets.EXPECT().Replace(gomock.Any(), int64(40), gomock.Any()).
		DoAndReturn(func(_ context.Context, _ int64, targets []*agent_entity.AgentExecTarget) error {
			gotTargets = targets
			return nil
		})

	m.dbMock.ExpectBegin()
	m.dbMock.ExpectCommit()

	bundle := data_svc.BundleV1{
		Format: data_svc.BundleFormat, Version: 1,
		Scopes: []string{string(data_svc.ScopeOrganization)},
		Items: data_svc.BundleItems{
			AgentBackends: []data_svc.BundleAgentBackend{{ExportKey: "ab-1", Name: "B1"}},
			Agents: []data_svc.BundleAgent{
				// 注意:不设置 ExecTargets —— 序列化后 JSON 里没有 execTargets 这个 key,
				// 模拟本条规则加入之前导出的老 bundle。
				{ExportKey: "ag-1", Name: "Solo"},
			},
		},
	}
	raw, err := json.Marshal(bundle)
	if err != nil {
		t.Fatalf("marshal bundle: %v", err)
	}

	res, err := m.svc.ApplyImport(m.ctx, &data_svc.ApplyImportRequest{Raw: raw, FallbackStrategy: data_svc.ActionCreate})
	if err != nil {
		t.Fatalf("apply import: %v", err)
	}
	if res.Counts["created"] != 2 {
		t.Fatalf("expected 2 created rows (1 backend + 1 agent), got %v", res.Counts)
	}
	if len(gotTargets) != 0 {
		t.Fatalf("expected no exec targets when the current field is absent, got %d", len(gotTargets))
	}
}

// TestApplyImport_Agent_LegacyBundleEmptyBackendKey_FallsBackToEmptyList 覆盖
// "agentBackendKey 为空的 Agent 落成空列表,与迁移对 agent_backend_id = 0 的处理
// 一致"。
func TestApplyImport_Agent_LegacyBundleEmptyBackendKey_FallsBackToEmptyList(t *testing.T) {
	m := setupDataSvcTest(t)
	m.providers.EXPECT().List(gomock.Any()).Return(nil, nil).Times(2)
	m.devices.EXPECT().List(gomock.Any()).Return(nil, nil).Times(2)
	m.backends.EXPECT().List(gomock.Any()).Return([]*agent_backend_entity.AgentBackend{}, nil).Times(2)
	m.depts.EXPECT().List(gomock.Any()).Return(nil, nil)

	m.agents.EXPECT().ListByDepartment(gomock.Any(), int64(0)).Return([]*agent_entity.Agent{}, nil)
	m.agents.EXPECT().Create(gomock.Any(), gomock.AssignableToTypeOf(&agent_entity.Agent{})).
		DoAndReturn(func(_ context.Context, a *agent_entity.Agent) error {
			if a.AgentBackendID != 0 {
				t.Errorf("expected AgentBackendID=0 for empty agentBackendKey, got %d", a.AgentBackendID)
			}
			a.ID = 41
			return nil
		})

	gotTargets := []*agent_entity.AgentExecTarget{{AgentBackendID: 1}} // 哨兵:必须被空列表覆盖
	m.execTargets.EXPECT().Replace(gomock.Any(), int64(41), gomock.Any()).
		DoAndReturn(func(_ context.Context, _ int64, targets []*agent_entity.AgentExecTarget) error {
			gotTargets = targets
			return nil
		})

	m.dbMock.ExpectBegin()
	m.dbMock.ExpectCommit()

	bundle := data_svc.BundleV1{
		Format: data_svc.BundleFormat, Version: 1,
		Scopes: []string{string(data_svc.ScopeOrganization)},
		Items: data_svc.BundleItems{
			Agents: []data_svc.BundleAgent{
				{ExportKey: "ag-1", Name: "Unassigned"},
			},
		},
	}
	raw, err := json.Marshal(bundle)
	if err != nil {
		t.Fatalf("marshal bundle: %v", err)
	}

	res, err := m.svc.ApplyImport(m.ctx, &data_svc.ApplyImportRequest{Raw: raw, FallbackStrategy: data_svc.ActionCreate})
	if err != nil {
		t.Fatalf("apply import: %v", err)
	}
	if res.Counts["created"] != 1 {
		t.Fatalf("expected 1 created row, got %v", res.Counts)
	}
	if len(gotTargets) != 0 {
		t.Fatalf("expected an empty exec target list for an empty agentBackendKey, got %d", len(gotTargets))
	}
}

func TestApplyImport_AgentExecTargetsAreSourceOfTruth(t *testing.T) {
	m := setupDataSvcTest(t)
	m.providers.EXPECT().List(gomock.Any()).Return(nil, nil).Times(2)
	m.devices.EXPECT().List(gomock.Any()).Return(nil, nil).Times(2)
	m.backends.EXPECT().List(gomock.Any()).Return([]*agent_backend_entity.AgentBackend{}, nil).Times(2)
	m.depts.EXPECT().List(gomock.Any()).Return(nil, nil)

	backendIDSeq := int64(59)
	mCreatedOrder := []string{}
	m.backends.EXPECT().Create(gomock.Any(), gomock.AssignableToTypeOf(&agent_backend_entity.AgentBackend{})).
		DoAndReturn(func(_ context.Context, bk *agent_backend_entity.AgentBackend) error {
			backendIDSeq++
			bk.ID = backendIDSeq
			mCreatedOrder = append(mCreatedOrder, bk.Name)
			return nil
		}).Times(2)

	m.agents.EXPECT().ListByDepartment(gomock.Any(), int64(0)).Return([]*agent_entity.Agent{}, nil)
	m.agents.EXPECT().Create(gomock.Any(), gomock.AssignableToTypeOf(&agent_entity.Agent{})).
		DoAndReturn(func(_ context.Context, a *agent_entity.Agent) error {
			a.ID = 70
			return nil
		})

	var gotTargets []*agent_entity.AgentExecTarget
	m.execTargets.EXPECT().Replace(gomock.Any(), int64(70), gomock.Any()).
		DoAndReturn(func(_ context.Context, _ int64, targets []*agent_entity.AgentExecTarget) error {
			gotTargets = targets
			return nil
		})

	m.dbMock.ExpectBegin()
	m.dbMock.ExpectCommit()

	bundle := data_svc.BundleV1{
		Format: data_svc.BundleFormat, Version: 1,
		Scopes: []string{string(data_svc.ScopeOrganization), string(data_svc.ScopeAgentBackends)},
		Items: data_svc.BundleItems{
			AgentBackends: []data_svc.BundleAgentBackend{
				{ExportKey: "ab-real", Name: "Real"},
				{ExportKey: "ab-decoy", Name: "Decoy"},
			},
			Agents: []data_svc.BundleAgent{
				{
					ExportKey: "ag-1", Name: "Solo",
					ExecTargets: []data_svc.BundleExecTarget{
						{BackendKey: "ab-real", SortOrder: 0, SkillsJSON: `[{"id":"real-pack","enabled":true}]`},
					},
				},
			},
		},
	}
	raw, err := json.Marshal(bundle)
	if err != nil {
		t.Fatalf("marshal bundle: %v", err)
	}

	res, err := m.svc.ApplyImport(m.ctx, &data_svc.ApplyImportRequest{Raw: raw, FallbackStrategy: data_svc.ActionCreate})
	if err != nil {
		t.Fatalf("apply import: %v", err)
	}
	if res.Counts["created"] != 3 {
		t.Fatalf("expected 3 created rows (2 backends + 1 agent), got %v", res.Counts)
	}
	if len(gotTargets) != 1 {
		t.Fatalf("expected 1 exec target, got %d", len(gotTargets))
	}
	if gotTargets[0].SkillsJSON != `[{"id":"real-pack","enabled":true}]` {
		t.Fatalf("expected skillsJSON from execTargets, got %q", gotTargets[0].SkillsJSON)
	}
	// "ab-real" 在 bundle 里排第一个,拿到的本地 backend id 必须是它,不是 decoy。
	if mCreatedOrder[0] != "Real" {
		t.Fatalf("test setup assumption broke: expected Real created first, order=%v", mCreatedOrder)
	}
	wantBackendID := int64(60) // 59 + 1,第一次 Create
	if gotTargets[0].AgentBackendID != wantBackendID {
		t.Fatalf("expected exec target backend id resolved from execTargets[].backendKey (ab-real=%d), got %d",
			wantBackendID, gotTargets[0].AgentBackendID)
	}
}

// TestApplyImport_Agent_MultiTargetSurvivesParentBackfill 锁住"第二遍回填 parent 不
// 碰执行目标列表"。回填走的是 AgentRepo.UpdateRow(只落 Agent 行);若走 Update,它
// 会把列表整表替换成 Agent 行上那两个保留列折出来的单元素列表——刚导进来的多档
// 当场被吞掉,只剩 ①(R15f:执行目标数组才是真相来源)。
func TestApplyImport_Agent_MultiTargetSurvivesParentBackfill(t *testing.T) {
	m := setupDataSvcTest(t)
	m.providers.EXPECT().List(gomock.Any()).Return(nil, nil).Times(2)
	m.devices.EXPECT().List(gomock.Any()).Return(nil, nil).Times(2)
	m.backends.EXPECT().List(gomock.Any()).Return([]*agent_backend_entity.AgentBackend{}, nil).Times(2)
	m.depts.EXPECT().List(gomock.Any()).Return(nil, nil)

	backendIDSeq := int64(80)
	m.backends.EXPECT().Create(gomock.Any(), gomock.AssignableToTypeOf(&agent_backend_entity.AgentBackend{})).
		DoAndReturn(func(_ context.Context, bk *agent_backend_entity.AgentBackend) error {
			backendIDSeq++
			bk.ID = backendIDSeq
			return nil
		}).Times(2)

	m.agents.EXPECT().ListByDepartment(gomock.Any(), int64(0)).Return([]*agent_entity.Agent{}, nil).Times(2)
	agentIDByName := map[string]int64{"Lead": 90, "IC": 91}
	m.agents.EXPECT().Create(gomock.Any(), gomock.AssignableToTypeOf(&agent_entity.Agent{})).
		DoAndReturn(func(_ context.Context, a *agent_entity.Agent) error {
			a.ID = agentIDByName[a.Name]
			return nil
		}).Times(2)

	targetsByAgent := map[int64][]*agent_entity.AgentExecTarget{}
	m.execTargets.EXPECT().Replace(gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, agentID int64, targets []*agent_entity.AgentExecTarget) error {
			targetsByAgent[agentID] = targets
			return nil
		}).AnyTimes()

	// 回填只读回 IC 那一行(它有 parentAgentKey)。
	m.agents.EXPECT().Find(gomock.Any(), int64(91)).
		Return(&agent_entity.Agent{ID: 91, Name: "IC", AgentBackendID: 81, SkillsJSON: `[]`}, nil)
	var backfilled *agent_entity.Agent
	m.agents.EXPECT().UpdateRow(gomock.Any(), gomock.AssignableToTypeOf(&agent_entity.Agent{})).
		DoAndReturn(func(_ context.Context, a *agent_entity.Agent) error {
			backfilled = a
			return nil
		})

	m.dbMock.ExpectBegin()
	m.dbMock.ExpectCommit()

	bundle := data_svc.BundleV1{
		Format: data_svc.BundleFormat, Version: 1,
		Scopes: []string{string(data_svc.ScopeOrganization), string(data_svc.ScopeAgentBackends)},
		Items: data_svc.BundleItems{
			AgentBackends: []data_svc.BundleAgentBackend{
				{ExportKey: "ab-1", Name: "B1"},
				{ExportKey: "ab-2", Name: "B2"},
			},
			Agents: []data_svc.BundleAgent{
				{ExportKey: "ag-lead", Name: "Lead"},
				{
					ExportKey: "ag-ic", Name: "IC", ParentAgentKey: "ag-lead",
					ExecTargets: []data_svc.BundleExecTarget{
						{BackendKey: "ab-1", SortOrder: 0, SkillsJSON: `[{"id":"p1","enabled":true}]`},
						{BackendKey: "ab-2", SortOrder: 1, SkillsJSON: `[{"id":"p2","enabled":false}]`},
					},
				},
			},
		},
	}
	raw, err := json.Marshal(bundle)
	if err != nil {
		t.Fatalf("marshal bundle: %v", err)
	}

	if _, err := m.svc.ApplyImport(m.ctx, &data_svc.ApplyImportRequest{
		Raw: raw, FallbackStrategy: data_svc.ActionCreate,
	}); err != nil {
		t.Fatalf("apply import: %v", err)
	}
	if backfilled == nil || backfilled.ParentAgentID != 90 {
		t.Fatalf("expected IC backfilled with parent 90 via UpdateRow, got %+v", backfilled)
	}
	if got := targetsByAgent[91]; len(got) != 2 {
		t.Fatalf("expected IC to keep 2 exec targets, got %d", len(got))
	}
}

// TestApplyImport_Agent_Overwrite_ExecTargetsLandInOneWrite 锁住"overwrite 一个已有
// Agent 时,整张执行目标列表随同一次写入落库"。先 Update(塌成单元素)再 Replace
// (重建)的两步写法会把幸存那几档整行删掉重插,同步标识跟着重铸——对端会把一次
// 普通的导入看成「删了又建」(R1:跨机身份终身不变)。
func TestApplyImport_Agent_Overwrite_ExecTargetsLandInOneWrite(t *testing.T) {
	m := setupDataSvcTest(t)
	m.providers.EXPECT().List(gomock.Any()).Return(nil, nil).Times(2)
	m.devices.EXPECT().List(gomock.Any()).Return(nil, nil).Times(2)
	m.backends.EXPECT().List(gomock.Any()).Return([]*agent_backend_entity.AgentBackend{}, nil).Times(2)
	m.depts.EXPECT().List(gomock.Any()).Return(nil, nil)

	backendIDSeq := int64(70)
	m.backends.EXPECT().Create(gomock.Any(), gomock.AssignableToTypeOf(&agent_backend_entity.AgentBackend{})).
		DoAndReturn(func(_ context.Context, bk *agent_backend_entity.AgentBackend) error {
			backendIDSeq++
			bk.ID = backendIDSeq
			return nil
		}).Times(2)

	m.agents.EXPECT().ListByDepartment(gomock.Any(), int64(0)).
		Return([]*agent_entity.Agent{{ID: 95, Name: "Lead", SkillsJSON: `[{"id":"old","enabled":true}]`}}, nil)

	var gotTargets []*agent_entity.AgentExecTarget
	m.agents.EXPECT().UpdateWithTargets(gomock.Any(), gomock.AssignableToTypeOf(&agent_entity.Agent{}), gomock.Any()).
		DoAndReturn(func(_ context.Context, a *agent_entity.Agent, targets []*agent_entity.AgentExecTarget) error {
			if a.ID != 95 {
				t.Errorf("expected agent 95, got %d", a.ID)
			}
			gotTargets = targets
			return nil
		})

	m.dbMock.ExpectBegin()
	m.dbMock.ExpectCommit()

	bundle := data_svc.BundleV1{
		Format: data_svc.BundleFormat, Version: 1,
		Scopes: []string{string(data_svc.ScopeOrganization), string(data_svc.ScopeAgentBackends)},
		Items: data_svc.BundleItems{
			AgentBackends: []data_svc.BundleAgentBackend{
				{ExportKey: "ab-1", Name: "B1"},
				{ExportKey: "ab-2", Name: "B2"},
			},
			Agents: []data_svc.BundleAgent{
				{
					ExportKey: "ag-lead", Name: "Lead",
					ExecTargets: []data_svc.BundleExecTarget{
						{BackendKey: "ab-1", SortOrder: 0, SkillsJSON: `[{"id":"p1","enabled":true}]`},
						{BackendKey: "ab-2", SortOrder: 1, SkillsJSON: `[{"id":"p2","enabled":false}]`},
					},
				},
			},
		},
	}
	raw, err := json.Marshal(bundle)
	if err != nil {
		t.Fatalf("marshal bundle: %v", err)
	}

	res, err := m.svc.ApplyImport(m.ctx, &data_svc.ApplyImportRequest{
		Raw:              raw,
		Actions:          map[string]data_svc.ItemAction{"organization:ag-lead": data_svc.ActionOverwrite},
		FallbackStrategy: data_svc.ActionCreate,
	})
	if err != nil {
		t.Fatalf("apply import: %v", err)
	}
	if res.Counts["overwrote"] != 1 {
		t.Fatalf("expected 1 overwrite, got %v", res.Counts)
	}
	if len(gotTargets) != 2 {
		t.Fatalf("expected the whole 2-target list in one write, got %d", len(gotTargets))
	}
	if gotTargets[0].SkillsJSON != `[{"id":"p1","enabled":true}]` || gotTargets[1].SkillsJSON != `[{"id":"p2","enabled":false}]` {
		t.Fatalf("per-target skills not preserved: %q / %q", gotTargets[0].SkillsJSON, gotTargets[1].SkillsJSON)
	}
}
