package ctl_svc

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/project_entity"
	"github.com/agentre-hub/agentre/internal/service/agent_backend_svc"
	"github.com/agentre-hub/agentre/internal/service/department_svc"
	"github.com/agentre-hub/agentre/internal/service/llm_provider_svc"
	"github.com/agentre-hub/agentre/internal/service/project_location_svc"
	"github.com/agentre-hub/agentre/internal/service/project_svc"
	"github.com/agentre-hub/agentre/pkg/syncwire"
	"github.com/agentre-hub/agentre/pkg/wire/agentrewire"
)

// 生产用资源网关：每次调用现取服务单例（不在构造期钉死），只读服务层已有的读模型，
// 所以密钥在这里就已经只剩服务层的掩码 / 布尔。

// ProductionResources 返回接到现有服务层上的资源网关。
func ProductionResources() Resources {
	return Resources{
		Org:       orgSvcResources{},
		Projects:  projectSvcResources{},
		Providers: providerSvcResources{},
		Backends:  backendSvcResources{},
	}
}

// ---- agents / departments ----

type orgSvcResources struct{}

func (orgSvcResources) ListAgents(ctx context.Context) ([]*agentrewire.CtlAgent, error) {
	org, err := department_svc.Department().Load(ctx, &department_svc.LoadOrgRequest{})
	if err != nil {
		return nil, err
	}
	out := make([]*agentrewire.CtlAgent, 0, len(org.Agents))
	for _, a := range org.Agents {
		out = append(out, agentDocFrom(a))
	}
	return out, nil
}

func (orgSvcResources) ListDepartments(ctx context.Context) ([]*agentrewire.CtlDepartment, error) {
	org, err := department_svc.Department().Load(ctx, &department_svc.LoadOrgRequest{})
	if err != nil {
		return nil, err
	}
	out := make([]*agentrewire.CtlDepartment, 0, len(org.Departments))
	for _, d := range org.Departments {
		out = append(out, departmentDocFrom(d))
	}
	return out, nil
}

func agentDocFrom(a *department_svc.AgentItem) *agentrewire.CtlAgent {
	backendIDs := make([]int64, 0, len(a.ExecTargets))
	for _, t := range a.ExecTargets {
		backendIDs = append(backendIDs, t.AgentBackendID)
	}
	return &agentrewire.CtlAgent{
		Id:           a.ID,
		Name:         a.Name,
		Description:  a.Description,
		DepartmentId: a.DepartmentID,
		BackendIds:   backendIDs,
		Pinned:       a.Pinned,
		AvatarColor:  a.AvatarColor,
		AvatarIcon:   a.AvatarIcon,
		SystemBadge:  a.SystemBadge,
	}
}

func departmentDocFrom(d *department_svc.DepartmentItem) *agentrewire.CtlDepartment {
	return &agentrewire.CtlDepartment{
		Id:          d.ID,
		Name:        d.Name,
		Description: d.Description,
		Icon:        d.Icon,
		AccentColor: d.AccentColor,
		ParentId:    d.ParentID,
		LeadAgentId: d.LeadAgentID,
	}
}

// ---- projects ----

type projectSvcResources struct{}

func (projectSvcResources) ListProjects(ctx context.Context) ([]*agentrewire.CtlProject, error) {
	tree, err := project_svc.Default().ListTree(ctx)
	if err != nil {
		return nil, err
	}
	var out []*agentrewire.CtlProject
	var walk func(nodes []*project_svc.ProjectNode) error
	walk = func(nodes []*project_svc.ProjectNode) error {
		for _, n := range nodes {
			if n == nil || n.Project == nil {
				continue
			}
			detail, err := project_svc.Default().Get(ctx, n.Project.ID)
			if err != nil {
				return err
			}
			locs, err := project_location_svc.Default().ListByProject(ctx, n.Project.ID)
			if err != nil {
				return err
			}
			out = append(out, projectDocFrom(n.Project, detail.DirectMembers, locs))
			if err := walk(n.Children); err != nil {
				return err
			}
		}
		return nil
	}
	if err := walk(tree); err != nil {
		return nil, err
	}
	return out, nil
}

// projectDocFrom：成员只取直接成员（增减成员改的就是它们）；设备用指纹标识。
func projectDocFrom(
	p *project_entity.Project,
	members []*project_svc.ProjectAgentMember,
	locs []*project_location_svc.ProjectLocationView,
) *agentrewire.CtlProject {
	doc := &agentrewire.CtlProject{
		Id:          p.ID,
		ParentId:    p.ParentID,
		Name:        p.Name,
		Icon:        p.Icon,
		Color:       p.Color,
		Description: p.Description,
		Path:        p.Path,
	}
	for _, m := range members {
		doc.MemberAgentIds = append(doc.MemberAgentIds, m.AgentID)
	}
	for _, l := range locs {
		doc.Locations = append(doc.Locations, &agentrewire.CtlProjectLocation{
			DeviceId:   string(l.DeviceFingerprint),
			DeviceName: l.DeviceName,
			Path:       l.Path,
		})
	}
	return doc
}

// ---- providers / models ----

type providerSvcResources struct{}

func (providerSvcResources) ListProviders(ctx context.Context) ([]*agentrewire.CtlProvider, error) {
	svc := llm_provider_svc.LLMProvider()
	resp, err := svc.List(ctx, &llm_provider_svc.ListProvidersRequest{})
	if err != nil {
		return nil, err
	}
	out := make([]*agentrewire.CtlProvider, 0, len(resp.Items))
	for _, p := range resp.Items {
		refs, err := svc.ProviderRefCounts(ctx, &llm_provider_svc.ProviderRefCountsRequest{ProviderKey: p.ProviderKey})
		if err != nil {
			return nil, err
		}
		out = append(out, providerDocFrom(p, refs.Counts.Backends))
	}
	return out, nil
}

func (providerSvcResources) ListModels(ctx context.Context) ([]*agentrewire.CtlModel, error) {
	svc := llm_provider_svc.LLMProvider()
	models, err := listAllModels(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]*agentrewire.CtlModel, 0, len(models))
	for _, m := range models {
		refs, err := svc.ModelRefCounts(ctx, &llm_provider_svc.ModelRefCountsRequest{ModelKey: m.ModelKey})
		if err != nil {
			return nil, err
		}
		out = append(out, modelDocFrom(m, refs.Counts.Backends))
	}
	return out, nil
}

// listAllModels 按提供方逐个列出全部模型。
func listAllModels(ctx context.Context) ([]*llm_provider_svc.ModelItem, error) {
	svc := llm_provider_svc.LLMProvider()
	providers, err := svc.List(ctx, &llm_provider_svc.ListProvidersRequest{})
	if err != nil {
		return nil, err
	}
	var out []*llm_provider_svc.ModelItem
	for _, p := range providers.Items {
		models, err := svc.ListModels(ctx, &llm_provider_svc.ListModelsRequest{ID: p.ID})
		if err != nil {
			return nil, err
		}
		out = append(out, models.Items...)
	}
	return out, nil
}

// providerDocFrom：API key 用服务层的掩码，明文从不经过这里。
func providerDocFrom(p *llm_provider_svc.ProviderItem, backendRefs int64) *agentrewire.CtlProvider {
	return &agentrewire.CtlProvider{
		Id:              p.ID,
		Name:            p.Name,
		Type:            p.Type,
		BaseUrl:         p.BaseURL,
		Enabled:         p.Enabled,
		ApiKey:          p.MaskedAPIKey,
		ApiKeySet:       p.HasAPIKey,
		DefaultModelKey: p.DefaultModelKey,
		BackendRefs:     int32(backendRefs),
	}
}

func modelDocFrom(m *llm_provider_svc.ModelItem, backendRefs int64) *agentrewire.CtlModel {
	return &agentrewire.CtlModel{
		Id:            m.ID,
		ProviderId:    m.ProviderID,
		Key:           m.ModelKey,
		ModelId:       m.ModelID,
		Name:          m.Name,
		ContextWindow: int64(m.ContextWindow),
		MaxOutput:     int64(m.MaxOutput),
		Enabled:       m.Enabled,
		IsDefault:     m.IsDefault,
		BackendRefs:   int32(backendRefs),
	}
}

// ---- backends ----

type backendSvcResources struct{}

func (backendSvcResources) ListBackends(ctx context.Context) ([]*agentrewire.CtlBackend, error) {
	svc := agent_backend_svc.AgentBackend()
	resp, err := svc.List(ctx, &agent_backend_svc.ListBackendsRequest{})
	if err != nil {
		return nil, err
	}
	ids, err := loadKeyIndex(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]*agentrewire.CtlBackend, 0, len(resp.Items))
	for _, b := range resp.Items {
		cliPath := ""
		if b.SyncID != "" {
			overlay, err := svc.GetCLIOverlay(ctx, &agent_backend_svc.GetCLIOverlayRequest{BackendSyncID: b.SyncID, DeviceID: string(b.DeviceID)})
			if err != nil {
				return nil, err
			}
			cliPath = overlay.CLIPath
		}
		doc, err := backendDocFrom(b, ids, cliPath)
		if err != nil {
			return nil, err
		}
		out = append(out, doc)
	}
	return out, nil
}

// keyIndex 把后端上的提供方 / 模型 key 换成 ctl 契约里的数字 id。
type keyIndex struct {
	providers map[string]int64
	models    map[string]int64
}

func loadKeyIndex(ctx context.Context) (keyIndex, error) {
	ids := keyIndex{providers: map[string]int64{}, models: map[string]int64{}}
	providers, err := llm_provider_svc.LLMProvider().List(ctx, &llm_provider_svc.ListProvidersRequest{})
	if err != nil {
		return ids, err
	}
	for _, p := range providers.Items {
		ids.providers[p.ProviderKey] = p.ID
	}
	models, err := listAllModels(ctx)
	if err != nil {
		return ids, err
	}
	for _, m := range models {
		ids.models[m.ModelKey] = m.ID
	}
	return ids, nil
}

// backendDocFrom：设备写成已配对设备的名字，解析不到名字时用指纹，本机为空；
// token 只报是否已设置；类型独占设置按同步契约的形状收进 config_json。
func backendDocFrom(b *agent_backend_svc.BackendItem, ids keyIndex, cliPath string) (*agentrewire.CtlBackend, error) {
	env, err := agent_backend_entity.ParseEnvJSON(b.EnvJSON)
	if err != nil {
		return nil, fmt.Errorf("backend %d: parse env_json: %w", b.ID, err)
	}
	cfg := syncwire.AgentBackendConfig{
		Sandbox:               b.Sandbox,
		Approval:              b.Approval,
		DefaultPermissionMode: b.DefaultPermissionMode,
		DefaultModel:          b.DefaultModel,
		OpenClawGatewayURL:    b.OpenClawGatewayURL,
		OpenClawAgentID:       b.OpenClawAgentID,
		OpenClawDefaultModel:  b.OpenClawDefaultModel,
		OpenClawSessionMode:   b.OpenClawSessionMode,
		HermesURL:             b.HermesURL,
		HermesAuthProvider:    b.HermesAuthProvider,
		HermesUserID:          b.HermesUserID,
		ACPCommand:            b.ACPCommand,
		ACPArgs:               b.ACPArgs,
	}
	if len(b.ModelRoutes) > 0 {
		routes, err := json.Marshal(b.ModelRoutes)
		if err != nil {
			return nil, fmt.Errorf("backend %d: encode model routes: %w", b.ID, err)
		}
		cfg.ModelRoutes = routes
	}
	configJSON, err := json.Marshal(cfg)
	if err != nil {
		return nil, fmt.Errorf("backend %d: encode config: %w", b.ID, err)
	}
	device := b.DeviceName
	if device == "" {
		device = string(b.DeviceID)
	}
	return &agentrewire.CtlBackend{
		Id:              b.ID,
		Name:            b.Name,
		Type:            b.Type,
		ProviderId:      ids.providers[b.LLMProviderKey],
		ModelId:         ids.models[b.LLMModelKey],
		Device:          device,
		CliPath:         cliPath,
		ReasoningEffort: b.ReasoningEffort,
		Env:             env,
		ConfigJson:      string(configJSON),
		TokenSet:        b.HasToken,
	}, nil
}
