package agent_backend_svc

import (
	"context"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/cago-frame/cago/database/db"
	"github.com/cago-frame/cago/pkg/consts"
	"github.com/cago-frame/cago/pkg/i18n"
	"gorm.io/gorm"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/llm_provider_entity"
	"github.com/agentre-hub/agentre/internal/pkg/code"
	"github.com/agentre-hub/agentre/internal/repository/agent_backend_repo"
	"github.com/agentre-hub/agentre/internal/repository/agent_repo"
	"github.com/agentre-hub/agentre/internal/repository/department_repo"
	"github.com/agentre-hub/agentre/internal/repository/llm_provider_repo"
)

// EngineProfileSvc 引擎配置应用服务（spec 2026-11-25 决策 2）。
//
// 这一轮是增量的：旧的后端（AgentBackendSvc）与它的执行目标照常工作，引擎配置的同步、
// 派发与凭据由后续任务接上。
type EngineProfileSvc interface {
	List(ctx context.Context, req *ListEngineProfilesRequest) (*ListEngineProfilesResponse, error)
	// Get 不看状态：已钉在被删配置上的会话按它最后的设置继续，界面也要读得到它。
	Get(ctx context.Context, req *GetEngineProfileRequest) (*GetEngineProfileResponse, error)
	Create(ctx context.Context, req *CreateEngineProfileRequest) (*CreateEngineProfileResponse, error)
	Update(ctx context.Context, req *UpdateEngineProfileRequest) (*UpdateEngineProfileResponse, error)
	// ListReferences 直接引用这份配置的成员与部门（删除确认里列出）。
	ListReferences(ctx context.Context, req *ListEngineProfileReferencesRequest) (*EngineProfileReferences, error)
	// Delete 软删；直接引用它的成员与部门改为跟随上一级。账号默认那一份不能删。
	Delete(ctx context.Context, req *DeleteEngineProfileRequest) (*DeleteEngineProfileResponse, error)
	SetAccountDefault(ctx context.Context, req *SetAccountDefaultEngineProfileRequest) (*SetAccountDefaultEngineProfileResponse, error)
}

// TxRunner 把一次删除的全部写入收进一个原子单元。抽成端口是因为服务单测不连库——真实现
// 走 db.Ctx(ctx).Transaction，单测顶一个直接调用回调的假实现（同 agent_svc）。
type TxRunner interface {
	RunInTx(ctx context.Context, fn func(ctx context.Context) error) error
}

type dbTxRunner struct{}

func (dbTxRunner) RunInTx(ctx context.Context, fn func(ctx context.Context) error) error {
	return db.Ctx(ctx).Transaction(func(tx *gorm.DB) error {
		return fn(db.WithContextDB(ctx, tx))
	})
}

type engineProfileSvc struct {
	now func() int64
	tx  TxRunner
	// backends 提供与后端共用的那几条规则：供应商 / 模型 / 分级路由的保存校验与展示投影。
	backends *agentBackendSvc
}

var defaultEngineProfile EngineProfileSvc = &engineProfileSvc{
	now:      func() int64 { return time.Now().UnixMilli() },
	tx:       dbTxRunner{},
	backends: defaultAgentBackend.(*agentBackendSvc),
}

// EngineProfile 取默认服务单例。
func EngineProfile() EngineProfileSvc { return defaultEngineProfile }

func (s *engineProfileSvc) List(ctx context.Context, _ *ListEngineProfilesRequest) (*ListEngineProfilesResponse, error) {
	rows, err := agent_backend_repo.EngineProfile().List(ctx)
	if err != nil {
		return nil, err
	}
	ids := make([]int64, 0, len(rows))
	views := make([]*agent_backend_entity.AgentBackend, 0, len(rows))
	providerKeys := map[string]struct{}{}
	for _, p := range rows {
		ids = append(ids, p.ID)
		views = append(views, p.AsBackend())
		if p.LLMProviderKey != "" {
			providerKeys[p.LLMProviderKey] = struct{}{}
		}
	}
	counts, err := agent_repo.Agent().CountByEngineProfiles(ctx, ids)
	if err != nil {
		return nil, err
	}
	// 软删的供应商仍显示名字、Active=false，与后端列表同口径。
	providers := map[string]*llm_provider_entity.LLMProvider{}
	if len(providerKeys) > 0 {
		providers, err = llm_provider_repo.LLMProvider().ListByKeysAnyStatus(ctx, slices.Collect(maps.Keys(providerKeys)))
		if err != nil {
			return nil, err
		}
	}
	lookup := prefetchItemLookup(ctx, views, providers)
	items := make([]*EngineProfileItem, 0, len(rows))
	for i, p := range rows {
		item := toEngineProfileItem(p, s.backends.buildItem(views[i], providers[p.LLMProviderKey], lookup))
		item.AgentCount = counts[p.ID]
		items = append(items, item)
	}
	return &ListEngineProfilesResponse{Items: items}, nil
}

func (s *engineProfileSvc) Get(ctx context.Context, req *GetEngineProfileRequest) (*GetEngineProfileResponse, error) {
	if req == nil {
		return nil, i18n.NewError(ctx, code.InvalidParameter)
	}
	p, err := agent_backend_repo.EngineProfile().FindAnyStatus(ctx, req.ID)
	if err != nil {
		return nil, err
	}
	if p == nil {
		return nil, i18n.NewError(ctx, code.EngineProfileNotFound)
	}
	return &GetEngineProfileResponse{Item: s.item(ctx, p)}, nil
}

func (s *engineProfileSvc) Create(ctx context.Context, req *CreateEngineProfileRequest) (*CreateEngineProfileResponse, error) {
	if req == nil {
		return nil, i18n.NewError(ctx, code.InvalidParameter)
	}
	routes, err := marshalRouteTargets(req.ModelRoutes)
	if err != nil {
		return nil, i18n.NewError(ctx, code.AgentBackendUnknownAlias)
	}
	now := s.now()
	p := &agent_backend_entity.EngineProfile{
		Type:       strings.TrimSpace(req.Type),
		Name:       strings.TrimSpace(req.Name),
		Status:     consts.ACTIVE,
		Createtime: now,
		Updatetime: now,
	}
	applyEngineSettings(p, engineSettings{
		LLMProviderKey: req.LLMProviderKey, LLMModelKey: req.LLMModelKey, ModelRoutes: routes,
		Sandbox: req.Sandbox, Approval: req.Approval, EnvJSON: req.EnvJSON, ReasoningEffort: req.ReasoningEffort,
		DefaultPermissionMode: req.DefaultPermissionMode, DefaultModel: req.DefaultModel,
		OpenClawGatewayURL: req.OpenClawGatewayURL, OpenClawAgentID: req.OpenClawAgentID,
		OpenClawDefaultModel: req.OpenClawDefaultModel, OpenClawSessionMode: req.OpenClawSessionMode,
		HermesURL: req.HermesURL, HermesAuthProvider: req.HermesAuthProvider, HermesUserID: req.HermesUserID,
		ACPCommand: req.ACPCommand, ACPArgs: req.ACPArgs,
	})
	// 新建时默认名称为运行时名；同名允许存在（不查重）。
	if p.Name == "" {
		p.Name = agent_backend_entity.DefaultEngineProfileName(p.Type)
	}
	if err := s.validate(ctx, p); err != nil {
		return nil, err
	}
	// 账号默认永远有一份：第一份配置就是它。
	def, err := agent_backend_repo.EngineProfile().FindAccountDefault(ctx)
	if err != nil {
		return nil, err
	}
	p.IsAccountDefault = def == nil
	if err := agent_backend_repo.EngineProfile().Create(ctx, p); err != nil {
		return nil, err
	}
	return &CreateEngineProfileResponse{Item: s.item(ctx, p)}, nil
}

func (s *engineProfileSvc) Update(ctx context.Context, req *UpdateEngineProfileRequest) (*UpdateEngineProfileResponse, error) {
	if req == nil {
		return nil, i18n.NewError(ctx, code.InvalidParameter)
	}
	p, err := agent_backend_repo.EngineProfile().Find(ctx, req.ID)
	if err != nil {
		return nil, err
	}
	if p == nil {
		return nil, i18n.NewError(ctx, code.EngineProfileNotFound)
	}
	routes, err := marshalRouteTargets(req.ModelRoutes)
	if err != nil {
		return nil, i18n.NewError(ctx, code.AgentBackendUnknownAlias)
	}
	p.Name = strings.TrimSpace(req.Name)
	applyEngineSettings(p, engineSettings{
		LLMProviderKey: req.LLMProviderKey, LLMModelKey: req.LLMModelKey, ModelRoutes: routes,
		Sandbox: req.Sandbox, Approval: req.Approval, EnvJSON: req.EnvJSON, ReasoningEffort: req.ReasoningEffort,
		DefaultPermissionMode: req.DefaultPermissionMode, DefaultModel: req.DefaultModel,
		OpenClawGatewayURL: req.OpenClawGatewayURL, OpenClawAgentID: req.OpenClawAgentID,
		OpenClawDefaultModel: req.OpenClawDefaultModel, OpenClawSessionMode: req.OpenClawSessionMode,
		HermesURL: req.HermesURL, HermesAuthProvider: req.HermesAuthProvider, HermesUserID: req.HermesUserID,
		ACPCommand: req.ACPCommand, ACPArgs: req.ACPArgs,
	})
	p.Updatetime = s.now()
	if err := s.validate(ctx, p); err != nil {
		return nil, err
	}
	if err := agent_backend_repo.EngineProfile().Update(ctx, p); err != nil {
		return nil, err
	}
	return &UpdateEngineProfileResponse{Item: s.item(ctx, p)}, nil
}

func (s *engineProfileSvc) ListReferences(ctx context.Context, req *ListEngineProfileReferencesRequest) (*EngineProfileReferences, error) {
	if req == nil {
		return nil, i18n.NewError(ctx, code.InvalidParameter)
	}
	return s.references(ctx, req.ID)
}

func (s *engineProfileSvc) Delete(ctx context.Context, req *DeleteEngineProfileRequest) (*DeleteEngineProfileResponse, error) {
	if req == nil {
		return nil, i18n.NewError(ctx, code.InvalidParameter)
	}
	p, err := agent_backend_repo.EngineProfile().Find(ctx, req.ID)
	if err != nil {
		return nil, err
	}
	if p == nil {
		return nil, i18n.NewError(ctx, code.EngineProfileNotFound)
	}
	if p.IsAccountDefault {
		return nil, i18n.NewError(ctx, code.EngineProfileAccountDefaultUndeletable)
	}
	refs, err := s.references(ctx, p.ID)
	if err != nil {
		return nil, err
	}
	// 引用与配置同生共死：只删了配置、引用还指着它，成员就会停在一份选不中的配置上。
	// 钉在它上面的会话不动——它们按这份配置最后的设置继续（FindAnyStatus 读得到）。
	err = s.tx.RunInTx(ctx, func(ctx context.Context) error {
		if err := agent_repo.Agent().ClearEngineProfile(ctx, p.ID); err != nil {
			return err
		}
		if err := department_repo.Department().ClearDefaultEngineProfile(ctx, p.ID); err != nil {
			return err
		}
		return agent_backend_repo.EngineProfile().Delete(ctx, p.ID)
	})
	if err != nil {
		return nil, err
	}
	return &DeleteEngineProfileResponse{References: *refs}, nil
}

func (s *engineProfileSvc) SetAccountDefault(ctx context.Context, req *SetAccountDefaultEngineProfileRequest) (*SetAccountDefaultEngineProfileResponse, error) {
	if req == nil {
		return nil, i18n.NewError(ctx, code.InvalidParameter)
	}
	p, err := agent_backend_repo.EngineProfile().Find(ctx, req.ID)
	if err != nil {
		return nil, err
	}
	if p == nil {
		return nil, i18n.NewError(ctx, code.EngineProfileNotFound)
	}
	if err := agent_backend_repo.EngineProfile().SetAccountDefault(ctx, p.ID); err != nil {
		return nil, err
	}
	p.IsAccountDefault = true
	return &SetAccountDefaultEngineProfileResponse{Item: s.item(ctx, p)}, nil
}

func (s *engineProfileSvc) references(ctx context.Context, id int64) (*EngineProfileReferences, error) {
	agents, err := agent_repo.Agent().ListByEngineProfile(ctx, id)
	if err != nil {
		return nil, err
	}
	depts, err := department_repo.Department().ListByDefaultEngineProfile(ctx, id)
	if err != nil {
		return nil, err
	}
	out := &EngineProfileReferences{Agents: []EngineProfileRef{}, Departments: []EngineProfileRef{}}
	for _, a := range agents {
		out.Agents = append(out.Agents, EngineProfileRef{ID: a.ID, Name: a.Name})
	}
	for _, d := range depts {
		out.Departments = append(out.Departments, EngineProfileRef{ID: d.ID, Name: d.Name})
	}
	return out, nil
}

// validate 走与后端同一套保存规则：地址规范化、实体校验、供应商 / 固定模型 / 分级路由。
// 规范化写回配置本身，校验在执行视图上做（规则只有 AgentBackend 那一份）。
func (s *engineProfileSvc) validate(ctx context.Context, p *agent_backend_entity.EngineProfile) error {
	view := p.AsBackend()
	if err := normalizeTypeSettings(ctx, view); err != nil {
		return err
	}
	p.OpenClawGatewayURL, p.OpenClawSessionMode, p.HermesURL = view.OpenClawGatewayURL, view.OpenClawSessionMode, view.HermesURL
	if err := p.Check(ctx); err != nil {
		return err
	}
	if _, err := s.backends.resolveProviderForSave(ctx, view); err != nil {
		return err
	}
	return s.backends.validateRouteProviders(ctx, view)
}

func (s *engineProfileSvc) item(ctx context.Context, p *agent_backend_entity.EngineProfile) *EngineProfileItem {
	view := p.AsBackend()
	var provider *llm_provider_entity.LLMProvider
	if p.LLMProviderKey != "" {
		// 展示口径尽力而为：查不到只让供应商摘要留空。
		provider, _ = llm_provider_repo.LLMProvider().FindByKey(ctx, p.LLMProviderKey)
	}
	return toEngineProfileItem(p, s.backends.toItem(ctx, view, provider))
}

// engineSettings 是新建与更新共用的那组可编辑设置（类型与名称之外的全部）。
type engineSettings struct {
	LLMProviderKey, LLMModelKey, ModelRoutes    string
	Sandbox, Approval, EnvJSON, ReasoningEffort string
	DefaultPermissionMode, DefaultModel         string
	OpenClawGatewayURL, OpenClawAgentID         string
	OpenClawDefaultModel, OpenClawSessionMode   string
	HermesURL, HermesAuthProvider, HermesUserID string
	ACPCommand                                  string
	ACPArgs                                     []string
}

func applyEngineSettings(p *agent_backend_entity.EngineProfile, in engineSettings) {
	p.LLMProviderKey = strings.TrimSpace(in.LLMProviderKey)
	p.LLMModelKey = strings.TrimSpace(in.LLMModelKey)
	p.ModelRoutes = in.ModelRoutes
	p.Sandbox = strings.TrimSpace(in.Sandbox)
	p.Approval = strings.TrimSpace(in.Approval)
	p.EnvJSON = strings.TrimSpace(in.EnvJSON)
	p.ReasoningEffort = strings.TrimSpace(in.ReasoningEffort)
	p.DefaultPermissionMode = strings.TrimSpace(in.DefaultPermissionMode)
	p.DefaultModel = strings.TrimSpace(in.DefaultModel)
	p.OpenClawGatewayURL = strings.TrimSpace(in.OpenClawGatewayURL)
	p.OpenClawAgentID = strings.TrimSpace(in.OpenClawAgentID)
	p.OpenClawDefaultModel = strings.TrimSpace(in.OpenClawDefaultModel)
	p.OpenClawSessionMode = strings.TrimSpace(in.OpenClawSessionMode)
	p.HermesURL = strings.TrimSpace(in.HermesURL)
	p.HermesAuthProvider = strings.TrimSpace(in.HermesAuthProvider)
	p.HermesUserID = strings.TrimSpace(in.HermesUserID)
	p.ACPCommand = strings.TrimSpace(in.ACPCommand)
	p.ACPArgs = append([]string(nil), in.ACPArgs...)
}

// toEngineProfileItem 把后端展示投影（供应商摘要、模型名、token 状态都已解析好）收成
// 引擎配置的 DTO，补上配置自己的几格。
func toEngineProfileItem(p *agent_backend_entity.EngineProfile, b *BackendItem) *EngineProfileItem {
	return &EngineProfileItem{
		ID:                    p.ID,
		SyncID:                p.SyncID,
		Type:                  b.Type,
		Name:                  b.Name,
		LLMProviderKey:        b.LLMProviderKey,
		LLMProviderName:       b.LLMProviderName,
		LLMProviderType:       b.LLMProviderType,
		LLMProviderModel:      b.LLMProviderModel,
		LLMProviderActive:     b.LLMProviderActive,
		LLMModelKey:           b.LLMModelKey,
		ModelRoutes:           b.ModelRoutes,
		Sandbox:               b.Sandbox,
		Approval:              b.Approval,
		EnvJSON:               b.EnvJSON,
		ReasoningEffort:       b.ReasoningEffort,
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
		HasToken:              b.HasToken,
		IsAccountDefault:      p.IsAccountDefault,
		Active:                p.IsActive(),
		Createtime:            p.Createtime,
		Updatetime:            p.Updatetime,
	}
}
