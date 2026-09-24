package ctl_svc

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/agentre-hub/agentre/internal/model/entity/project_entity"
	"github.com/agentre-hub/agentre/internal/service/agent_backend_svc"
	"github.com/agentre-hub/agentre/internal/service/agent_svc"
	"github.com/agentre-hub/agentre/internal/service/ctl_svc/mock_ctl_svc"
	"github.com/agentre-hub/agentre/internal/service/department_svc"
	"github.com/agentre-hub/agentre/internal/service/llm_provider_svc"
	"github.com/agentre-hub/agentre/internal/service/project_svc"
	"github.com/agentre-hub/agentre/internal/service/remote_device_svc"
	"github.com/agentre-hub/agentre/pkg/wire/agentrewire"
	"github.com/agentre-hub/agentre/pkg/wire/devicefp"
)

type mockPorts struct {
	depts     *mock_ctl_svc.MockDepartmentService
	agents    *mock_ctl_svc.MockAgentService
	projects  *mock_ctl_svc.MockProjectService
	providers *mock_ctl_svc.MockProviderService
	backends  *mock_ctl_svc.MockBackendService
	devices   *mock_ctl_svc.MockDeviceDirectory
	ports     servicePorts
}

func newMockPorts(t *testing.T) *mockPorts {
	ctrl := gomock.NewController(t)
	m := &mockPorts{
		depts:     mock_ctl_svc.NewMockDepartmentService(ctrl),
		agents:    mock_ctl_svc.NewMockAgentService(ctrl),
		projects:  mock_ctl_svc.NewMockProjectService(ctrl),
		providers: mock_ctl_svc.NewMockProviderService(ctrl),
		backends:  mock_ctl_svc.NewMockBackendService(ctrl),
		devices:   mock_ctl_svc.NewMockDeviceDirectory(ctrl),
	}
	m.ports = servicePorts{
		departments: func() DepartmentService { return m.depts },
		agents:      func() AgentService { return m.agents },
		projects:    func() ProjectService { return m.projects },
		providers:   func() ProviderService { return m.providers },
		backends:    func() BackendService { return m.backends },
		devices:     func() DeviceDirectory { return m.devices },
	}
	return m
}

func fieldSet(fields ...string) map[string]bool {
	out := map[string]bool{}
	for _, f := range fields {
		out[f] = true
	}
	return out
}

func agentRes(a *agentrewire.CtlAgent) *agentrewire.CtlResource { return agentDocRes(a) }
func deptRes(d *agentrewire.CtlDepartment) *agentrewire.CtlResource {
	return &agentrewire.CtlResource{Doc: &agentrewire.CtlResource_Department{Department: d}}
}
func projectRes(p *agentrewire.CtlProject) *agentrewire.CtlResource {
	return &agentrewire.CtlResource{Doc: &agentrewire.CtlResource_Project{Project: p}}
}
func providerRes(p *agentrewire.CtlProvider) *agentrewire.CtlResource {
	return &agentrewire.CtlResource{Doc: &agentrewire.CtlResource_Provider{Provider: p}}
}
func modelRes(m *agentrewire.CtlModel) *agentrewire.CtlResource {
	return &agentrewire.CtlResource{Doc: &agentrewire.CtlResource_Model{Model: m}}
}
func backendRes(b *agentrewire.CtlBackend) *agentrewire.CtlResource {
	return &agentrewire.CtlResource{Doc: &agentrewire.CtlResource_Backend{Backend: b}}
}

var ctx = context.Background()

// ---- agents ----

func TestAgentWriter(t *testing.T) {
	item := &department_svc.AgentItem{
		ID: 12, Name: "reviewer", DepartmentID: 2, Prompt: []string{"p"},
		ExecTargets: []department_svc.AgentExecTargetItem{{AgentBackendID: 5, Skills: []department_svc.AgentSkillDTO{{ID: "s", Enabled: true}}}},
		Tools:       []department_svc.AgentToolDTO{{Key: "org"}},
	}
	org := &department_svc.LoadOrgResponse{Agents: []*department_svc.AgentItem{item}}

	t.Run("只换部门 → 只走 Move", func(t *testing.T) {
		m := newMockPorts(t)
		m.depts.EXPECT().Load(gomock.Any(), gomock.Any()).Return(org, nil)
		m.agents.EXPECT().Move(gomock.Any(), &agent_svc.MoveAgentRequest{ID: 12, NewDepartmentID: 6}).Return(&agent_svc.MoveAgentResponse{}, nil)
		next := agentDocFrom(item)
		next.DepartmentId = 6
		require.NoError(t, agentWriter{m.ports}.Update(ctx, Write{Cur: agentRes(agentDocFrom(item)), Next: agentRes(next)}))
	})
	t.Run("换执行目标 → Update 整份写，已有目标的技能与提示词、工具原样带回", func(t *testing.T) {
		m := newMockPorts(t)
		m.depts.EXPECT().Load(gomock.Any(), gomock.Any()).Return(org, nil)
		m.agents.EXPECT().Update(gomock.Any(), &agent_svc.UpdateAgentRequest{
			ID: 12, Name: "reviewer", Prompt: []string{"p"}, Tools: item.Tools,
			ExecTargets: []agent_svc.ExecTargetInputDTO{{AgentBackendID: 9}, {AgentBackendID: 5, Skills: item.ExecTargets[0].Skills}},
		}).Return(&agent_svc.UpdateAgentResponse{}, nil)
		m.agents.EXPECT().SetPinned(gomock.Any(), &agent_svc.SetPinnedRequest{ID: 12, Pinned: true}).Return(&agent_svc.SetPinnedResponse{}, nil)
		next := agentDocFrom(item)
		next.BackendIds, next.Pinned = []int64{9, 5}, true
		require.NoError(t, agentWriter{m.ports}.Update(ctx, Write{Cur: agentRes(agentDocFrom(item)), Next: agentRes(next)}))
	})
	t.Run("create 带两个执行目标 → 先以第一个创建，再补齐整个列表", func(t *testing.T) {
		m := newMockPorts(t)
		created := &department_svc.AgentItem{ID: 30, Name: "new", DepartmentID: 2, ExecTargets: []department_svc.AgentExecTargetItem{{AgentBackendID: 5}}}
		m.agents.EXPECT().Create(gomock.Any(), &agent_svc.CreateAgentRequest{Name: "new", DepartmentID: 2, AgentBackendID: 5}).
			Return(&agent_svc.CreateAgentResponse{Item: created}, nil)
		m.agents.EXPECT().Update(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, req *agent_svc.UpdateAgentRequest) (*agent_svc.UpdateAgentResponse, error) {
			assert.Equal(t, []agent_svc.ExecTargetInputDTO{{AgentBackendID: 5}, {AgentBackendID: 9}}, req.ExecTargets)
			return &agent_svc.UpdateAgentResponse{}, nil
		})
		id, err := agentWriter{m.ports}.Create(ctx, Write{Next: agentRes(&agentrewire.CtlAgent{Name: "new", DepartmentId: 2, BackendIds: []int64{5, 9}})})
		require.NoError(t, err)
		assert.Equal(t, int64(30), id)
	})
	t.Run("服务层拒绝原样上抛", func(t *testing.T) {
		m := newMockPorts(t)
		m.agents.EXPECT().Delete(gomock.Any(), &agent_svc.DeleteAgentRequest{ID: 12}).Return(nil, errors.New("系统 Agent 不能删除"))
		err := agentWriter{m.ports}.Delete(ctx, Write{Cur: agentRes(agentDocFrom(item))})
		assert.EqualError(t, err, "系统 Agent 不能删除")
	})
}

// ---- departments ----

func TestDepartmentWriter(t *testing.T) {
	t.Run("create 带负责人 → Create 后补 Update", func(t *testing.T) {
		m := newMockPorts(t)
		m.depts.EXPECT().Create(gomock.Any(), &department_svc.CreateDepartmentRequest{Name: "ops", ParentID: 2}).
			Return(&department_svc.CreateDepartmentResponse{Item: &department_svc.DepartmentItem{ID: 40}}, nil)
		m.depts.EXPECT().Update(gomock.Any(), &department_svc.UpdateDepartmentRequest{ID: 40, Name: "ops", LeadAgentID: 3}).
			Return(&department_svc.UpdateDepartmentResponse{}, nil)
		id, err := departmentWriter{m.ports}.Create(ctx, Write{Next: deptRes(&agentrewire.CtlDepartment{Name: "ops", ParentId: 2, LeadAgentId: 3})})
		require.NoError(t, err)
		assert.Equal(t, int64(40), id)
	})
	t.Run("只换父部门 → 只走 Move", func(t *testing.T) {
		m := newMockPorts(t)
		m.depts.EXPECT().Move(gomock.Any(), &department_svc.MoveDepartmentRequest{ID: 7, NewParentID: 6}).Return(&department_svc.MoveDepartmentResponse{}, nil)
		require.NoError(t, departmentWriter{m.ports}.Update(ctx, Write{
			Cur:  deptRes(&agentrewire.CtlDepartment{Id: 7, Name: "tmp", ParentId: 2}),
			Next: deptRes(&agentrewire.CtlDepartment{Id: 7, Name: "tmp", ParentId: 6}),
		}))
	})
	t.Run("delete：默认上移，--cascade 级联", func(t *testing.T) {
		m := newMockPorts(t)
		m.depts.EXPECT().Delete(gomock.Any(), &department_svc.DeleteDepartmentRequest{ID: 7, Strategy: "reparent"}).Return(&department_svc.DeleteDepartmentResponse{}, nil)
		m.depts.EXPECT().Delete(gomock.Any(), &department_svc.DeleteDepartmentRequest{ID: 7, Strategy: "cascade"}).Return(&department_svc.DeleteDepartmentResponse{}, nil)
		cur := deptRes(&agentrewire.CtlDepartment{Id: 7})
		require.NoError(t, departmentWriter{m.ports}.Delete(ctx, Write{Cur: cur}))
		require.NoError(t, departmentWriter{m.ports}.Delete(ctx, Write{Cur: cur, Cascade: true}))
	})
	t.Run("级联影响：交给服务层按级联删除的口径计数", func(t *testing.T) {
		m := newMockPorts(t)
		m.depts.EXPECT().CascadeImpact(gomock.Any(), int64(2)).Return(2, 3, nil)
		depts, agents, err := departmentWriter{m.ports}.CascadeImpact(ctx, 2)
		require.NoError(t, err)
		assert.Equal(t, 2, depts)
		assert.Equal(t, 3, agents)
	})
}

// ---- projects ----

func TestProjectWriter(t *testing.T) {
	t.Run("create 带初始成员与路径", func(t *testing.T) {
		m := newMockPorts(t)
		m.projects.EXPECT().Create(gomock.Any(), &project_svc.CreateProjectRequest{Name: "docs", ParentID: 1, Path: "/src/docs", InitialAgentIDs: []int64{3}}).
			Return(&project_entity.Project{ID: 50}, nil)
		id, err := projectWriter{m.ports}.Create(ctx, Write{Next: projectRes(&agentrewire.CtlProject{Name: "docs", ParentId: 1, Path: "/src/docs", MemberAgentIds: []int64{3}})})
		require.NoError(t, err)
		assert.Equal(t, int64(50), id)
	})
	t.Run("清空路径 → ClearLocalPath；成员按差集增减；其它字段没变不调 Update", func(t *testing.T) {
		m := newMockPorts(t)
		m.projects.EXPECT().ClearLocalPath(gomock.Any(), int64(1)).Return(&project_entity.Project{}, nil)
		m.projects.EXPECT().AddMember(gomock.Any(), int64(1), int64(12)).Return(nil)
		m.projects.EXPECT().RemoveMember(gomock.Any(), int64(1), int64(3)).Return(nil)
		require.NoError(t, projectWriter{m.ports}.Update(ctx, Write{
			Cur:  projectRes(&agentrewire.CtlProject{Id: 1, Name: "agentre", Path: "/src/agentre", MemberAgentIds: []int64{3}}),
			Next: projectRes(&agentrewire.CtlProject{Id: 1, Name: "agentre", MemberAgentIds: []int64{12}}),
		}))
	})
	t.Run("改路径 → SetLocalPath；改父项目 → Move", func(t *testing.T) {
		m := newMockPorts(t)
		m.projects.EXPECT().Move(gomock.Any(), &project_svc.MoveProjectRequest{ID: 8, NewParentID: 2}).Return(&project_entity.Project{}, nil)
		m.projects.EXPECT().SetLocalPath(gomock.Any(), int64(8), "/new").Return(&project_entity.Project{}, nil)
		require.NoError(t, projectWriter{m.ports}.Update(ctx, Write{
			Cur:  projectRes(&agentrewire.CtlProject{Id: 8, Name: "docs", ParentId: 1, Path: "/old"}),
			Next: projectRes(&agentrewire.CtlProject{Id: 8, Name: "docs", ParentId: 2, Path: "/new"}),
		}))
	})
}

// ---- providers / models ----

func TestProviderWriter(t *testing.T) {
	cur := &agentrewire.CtlProvider{Id: 1, Name: "anthropic", BaseUrl: "https://x", Enabled: false, ApiKey: "sk-l••••••cdef", DefaultModelKey: "mk-1"} //nolint:gosec // 掩码后的假数据，不是凭据。
	t.Run("写 api key → Update 带明文；设默认模型、启用各走各的方法", func(t *testing.T) {
		m := newMockPorts(t)
		gomock.InOrder(
			m.providers.EXPECT().Update(gomock.Any(), &llm_provider_svc.UpdateProviderRequest{ID: 1, Name: "anthropic", BaseURL: "https://x", APIKey: "sk-new"}).
				Return(&llm_provider_svc.UpdateProviderResponse{}, nil),
			m.providers.EXPECT().SetModelDefault(gomock.Any(), &llm_provider_svc.SetModelDefaultRequest{ProviderID: 1, ModelKey: "mk-2"}).
				Return(&llm_provider_svc.SetModelDefaultResponse{}, nil),
			m.providers.EXPECT().SetProviderEnabled(gomock.Any(), &llm_provider_svc.SetProviderEnabledRequest{ID: 1, Enabled: true}).
				Return(&llm_provider_svc.SetProviderEnabledResponse{}, nil),
		)
		require.NoError(t, providerWriter{m.ports}.Update(ctx, Write{
			Cur:  providerRes(cur),
			Next: providerRes(&agentrewire.CtlProvider{Id: 1, Name: "anthropic", BaseUrl: "https://x", Enabled: true, ApiKey: "sk-new", DefaultModelKey: "mk-2"}),
		}))
	})
	t.Run("只启用 → 不调 Update（空 api key 表示沿用）", func(t *testing.T) {
		m := newMockPorts(t)
		m.providers.EXPECT().SetProviderEnabled(gomock.Any(), gomock.Any()).Return(&llm_provider_svc.SetProviderEnabledResponse{}, nil)
		require.NoError(t, providerWriter{m.ports}.Update(ctx, Write{
			Cur:  providerRes(cur),
			Next: providerRes(&agentrewire.CtlProvider{Id: 1, Name: "anthropic", BaseUrl: "https://x", Enabled: true, DefaultModelKey: "mk-1"}),
		}))
	})
	t.Run("delete --force → ConfirmReference", func(t *testing.T) {
		m := newMockPorts(t)
		m.providers.EXPECT().Delete(gomock.Any(), &llm_provider_svc.DeleteProviderRequest{ID: 1, ConfirmReference: true}).Return(&llm_provider_svc.DeleteProviderResponse{}, nil)
		require.NoError(t, providerWriter{m.ports}.Delete(ctx, Write{Cur: providerRes(cur), Force: true}))
	})
	t.Run("create --enable → 创建后交给服务层判能否启用", func(t *testing.T) {
		m := newMockPorts(t)
		m.providers.EXPECT().Create(gomock.Any(), &llm_provider_svc.CreateProviderRequest{Type: "anthropic", Name: "a2", APIKey: "sk-x"}).
			Return(&llm_provider_svc.CreateProviderResponse{Item: &llm_provider_svc.ProviderItem{ID: 3}}, nil)
		m.providers.EXPECT().SetProviderEnabled(gomock.Any(), &llm_provider_svc.SetProviderEnabledRequest{ID: 3, Enabled: true}).
			Return(nil, errors.New("需要先设置默认模型"))
		id, err := providerWriter{m.ports}.Create(ctx, Write{
			Next: providerRes(&agentrewire.CtlProvider{Type: "anthropic", Name: "a2", ApiKey: "sk-x", Enabled: true}), Fields: fieldSet("type", "name", "apiKey", "enabled"),
		})
		assert.Equal(t, int64(3), id)
		assert.EqualError(t, err, "需要先设置默认模型")
	})
}

func TestModelWriter(t *testing.T) {
	t.Run("create → ImportModels 一条，--default 再设默认", func(t *testing.T) {
		m := newMockPorts(t)
		m.providers.EXPECT().ListModels(gomock.Any(), &llm_provider_svc.ListModelsRequest{ID: 1}).
			Return(&llm_provider_svc.ListModelsResponse{Items: []*llm_provider_svc.ModelItem{{ID: 21, ModelID: "claude-opus-5"}}}, nil)
		m.providers.EXPECT().ImportModels(gomock.Any(), &llm_provider_svc.ImportModelsRequest{ProviderID: 1, Models: []*llm_provider_svc.ModelInput{{ModelID: "claude-haiku-5", ContextWindow: 200000}}}).
			Return(&llm_provider_svc.ImportModelsResponse{Items: []*llm_provider_svc.ModelItem{
				{ID: 21, ModelID: "claude-opus-5"}, {ID: 23, ProviderID: 1, ModelKey: "mk-3", ModelID: "claude-haiku-5", Enabled: true},
			}}, nil)
		m.providers.EXPECT().SetModelDefault(gomock.Any(), &llm_provider_svc.SetModelDefaultRequest{ProviderID: 1, ModelKey: "mk-3"}).
			Return(&llm_provider_svc.SetModelDefaultResponse{}, nil)
		id, err := modelWriter{m.ports}.Create(ctx, Write{
			Next:   modelRes(&agentrewire.CtlModel{ProviderId: 1, ModelId: "claude-haiku-5", ContextWindow: 200000, IsDefault: true}),
			Fields: fieldSet("providerId", "modelId", "contextWindow", "isDefault"),
		})
		require.NoError(t, err)
		assert.Equal(t, int64(23), id)
	})
	t.Run("create 已有的 ModelID → 拒绝，不悄悄改掉已有模型", func(t *testing.T) {
		m := newMockPorts(t)
		m.providers.EXPECT().ListModels(gomock.Any(), gomock.Any()).
			Return(&llm_provider_svc.ListModelsResponse{Items: []*llm_provider_svc.ModelItem{{ID: 21, ModelID: "claude-opus-5"}}}, nil)
		_, err := modelWriter{m.ports}.Create(ctx, Write{Next: modelRes(&agentrewire.CtlModel{ProviderId: 1, ModelId: "claude-opus-5"}), Fields: fieldSet("modelId")})
		assert.ErrorContains(t, err, "already exists")
	})
	t.Run("update 元数据 → UpdateModel；--disable → SetModelEnabled", func(t *testing.T) {
		m := newMockPorts(t)
		m.providers.EXPECT().UpdateModel(gomock.Any(), &llm_provider_svc.UpdateModelRequest{ID: 22, ModelID: "claude-sonnet-5", Name: "Sonnet"}).
			Return(&llm_provider_svc.UpdateModelResponse{}, nil)
		m.providers.EXPECT().SetModelEnabled(gomock.Any(), &llm_provider_svc.SetModelEnabledRequest{ID: 22, Enabled: false}).
			Return(&llm_provider_svc.SetModelEnabledResponse{}, nil)
		require.NoError(t, modelWriter{m.ports}.Update(ctx, Write{
			Cur:  modelRes(&agentrewire.CtlModel{Id: 22, ProviderId: 1, Key: "mk-2", ModelId: "claude-sonnet-5", Enabled: true}),
			Next: modelRes(&agentrewire.CtlModel{Id: 22, ProviderId: 1, Key: "mk-2", ModelId: "claude-sonnet-5", Name: "Sonnet"}),
		}))
	})
	t.Run("取消默认模型 → 报错，不调服务", func(t *testing.T) {
		m := newMockPorts(t)
		err := modelWriter{m.ports}.Update(ctx, Write{
			Cur:  modelRes(&agentrewire.CtlModel{Id: 21, ProviderId: 1, ModelId: "claude-opus-5", Enabled: true, IsDefault: true}),
			Next: modelRes(&agentrewire.CtlModel{Id: 21, ProviderId: 1, ModelId: "claude-opus-5", Enabled: true}),
		})
		assert.ErrorContains(t, err, "cannot be unset")
	})
	t.Run("delete --force → ConfirmReference", func(t *testing.T) {
		m := newMockPorts(t)
		m.providers.EXPECT().DeleteModel(gomock.Any(), &llm_provider_svc.DeleteModelRequest{ID: 22, ConfirmReference: true}).Return(&llm_provider_svc.DeleteModelResponse{}, nil)
		require.NoError(t, modelWriter{m.ports}.Delete(ctx, Write{Cur: modelRes(&agentrewire.CtlModel{Id: 22}), Force: true}))
	})
}

// ---- backends ----

func (m *mockPorts) expectKeyLookups() {
	m.providers.EXPECT().List(gomock.Any(), gomock.Any()).Return(&llm_provider_svc.ListProvidersResponse{
		Items: []*llm_provider_svc.ProviderItem{{ID: 1, ProviderKey: "pk-1"}},
	}, nil).AnyTimes()
	m.providers.EXPECT().ListModels(gomock.Any(), gomock.Any()).Return(&llm_provider_svc.ListModelsResponse{
		Items: []*llm_provider_svc.ModelItem{{ID: 21, ModelKey: "mk-1"}},
	}, nil).AnyTimes()
}

func TestBackendWriter(t *testing.T) {
	t.Run("create openclaw 带 token → CreateOpenClaw；设备按名字解析", func(t *testing.T) {
		m := newMockPorts(t)
		m.expectKeyLookups()
		m.devices.EXPECT().List(gomock.Any()).Return([]*remote_device_svc.DeviceView{{Name: "build-box", DaemonFingerprint: "sha256:bb"}}, nil)
		m.backends.EXPECT().CreateOpenClaw(gomock.Any(), gomock.Any(), "tok-plain").DoAndReturn(
			func(_ context.Context, req *agent_backend_svc.CreateBackendRequest, _ string) (*agent_backend_svc.CreateBackendResponse, error) {
				assert.Equal(t, "openclaw", req.Type)
				assert.Equal(t, "sha256:bb", req.DeviceID)
				assert.Equal(t, "pk-1", req.LLMProviderKey)
				assert.Equal(t, "mk-1", req.LLMModelKey)
				assert.Equal(t, "ws://127.0.0.1:1", req.OpenClawGatewayURL)
				assert.JSONEq(t, `{"A":"1"}`, req.EnvJSON)
				return &agent_backend_svc.CreateBackendResponse{Item: &agent_backend_svc.BackendItem{ID: 60, SyncID: "sy-60", DeviceID: devicefp.Carrier("sha256:bb")}}, nil
			})
		id, err := backendWriter{m.ports}.Create(ctx, Write{
			Next: backendRes(&agentrewire.CtlBackend{
				Type: "openclaw", Name: "claw2", Device: "build-box", ProviderId: 1, ModelId: 21, Token: "tok-plain",
				Env: map[string]string{"A": "1"}, ConfigJson: `{"openclawGatewayUrl":"ws://127.0.0.1:1"}`,
			}),
			Fields: fieldSet("type", "name", "device", "providerId", "modelId", "token", "env", "configJson"),
		})
		require.NoError(t, err)
		assert.Equal(t, int64(60), id)
	})
	t.Run("update 没写的引用沿用服务层原始 key 与设备指纹", func(t *testing.T) {
		m := newMockPorts(t)
		m.backends.EXPECT().List(gomock.Any(), gomock.Any()).Return(&agent_backend_svc.ListBackendsResponse{Items: []*agent_backend_svc.BackendItem{
			{ID: 9, SyncID: "sy-9", LLMProviderKey: "pk-dangling", LLMModelKey: "mk-dangling", DeviceID: "sha256:bb"},
		}}, nil)
		m.backends.EXPECT().Update(gomock.Any(), gomock.Any()).DoAndReturn(
			func(_ context.Context, req *agent_backend_svc.UpdateBackendRequest) (*agent_backend_svc.UpdateBackendResponse, error) {
				assert.Equal(t, "pk-dangling", req.LLMProviderKey)
				assert.Equal(t, "mk-dangling", req.LLMModelKey)
				assert.Equal(t, "sha256:bb", req.DeviceID)
				assert.Equal(t, "high", req.ReasoningEffort)
				assert.Equal(t, "workspace-write", req.Sandbox)
				return &agent_backend_svc.UpdateBackendResponse{Item: &agent_backend_svc.BackendItem{ID: 9}}, nil
			})
		require.NoError(t, backendWriter{m.ports}.Update(ctx, Write{
			Cur:    backendRes(&agentrewire.CtlBackend{Id: 9, Name: "codex-remote", Type: "codex", Device: "build-box"}),
			Next:   backendRes(&agentrewire.CtlBackend{Id: 9, Name: "codex-remote", Type: "codex", Device: "build-box", ReasoningEffort: "high", ConfigJson: `{"sandbox":"workspace-write"}`}),
			Fields: fieldSet("reasoningEffort"),
		}))
	})
	t.Run("空 token → UpdateOpenClaw 清除", func(t *testing.T) {
		m := newMockPorts(t)
		m.backends.EXPECT().List(gomock.Any(), gomock.Any()).Return(&agent_backend_svc.ListBackendsResponse{Items: []*agent_backend_svc.BackendItem{{ID: 10}}}, nil)
		m.backends.EXPECT().UpdateOpenClaw(gomock.Any(), gomock.Any(), "", true).Return(&agent_backend_svc.UpdateBackendResponse{Item: &agent_backend_svc.BackendItem{ID: 10}}, nil)
		require.NoError(t, backendWriter{m.ports}.Update(ctx, Write{
			Cur: backendRes(&agentrewire.CtlBackend{Id: 10, Type: "openclaw"}), Next: backendRes(&agentrewire.CtlBackend{Id: 10, Type: "openclaw"}),
			Fields: fieldSet("token"),
		}))
	})
	t.Run("设备名重名 → 歧义错误，列出指纹；找不到 → 错误", func(t *testing.T) {
		m := newMockPorts(t)
		m.devices.EXPECT().List(gomock.Any()).Return([]*remote_device_svc.DeviceView{
			{Name: "box", DaemonFingerprint: "sha256:aa"}, {Name: "box", DaemonFingerprint: "sha256:bb"},
		}, nil).Times(2)
		_, err := backendWriter{m.ports}.deviceID(ctx, "box")
		assert.ErrorContains(t, err, "sha256:aa, sha256:bb")
		_, err = backendWriter{m.ports}.deviceID(ctx, "ghost")
		assert.ErrorContains(t, err, `device "ghost" not found`)
		fp, err := backendWriter{m.ports}.deviceID(ctx, "sha256:cc")
		require.NoError(t, err)
		assert.Equal(t, "sha256:cc", fp, "指纹原样使用")
	})
}
