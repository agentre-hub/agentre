package data_svc

import (
	"context"
	"encoding/json"
	"strconv"
	"time"

	"github.com/cago-frame/cago/pkg/i18n"

	"github.com/agentre-hub/agentre/internal/buildinfo"
	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/agent_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/department_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/llm_provider_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/paired_agentred_entity"
	"github.com/agentre-hub/agentre/internal/pkg/code"
	"github.com/agentre-hub/agentre/internal/repository/agent_backend_repo"
	"github.com/agentre-hub/agentre/internal/repository/agent_repo"
	"github.com/agentre-hub/agentre/internal/repository/department_repo"
	"github.com/agentre-hub/agentre/internal/repository/llm_provider_repo"
	"github.com/agentre-hub/agentre/internal/repository/remote_device_repo"
	"github.com/agentre-hub/agentre/internal/service/remote_device_svc"
)

const (
	backendExportKeyPrefix = "ab-"
	deptExportKeyPrefix    = "dept-"
	agentExportKeyPrefix   = "ag-"
)

// Export 收集所选 scope 快照并返回 JSON bytes。
func (s *dataSvc) Export(ctx context.Context, req *ExportRequest) (*ExportResult, error) {
	if err := ValidateScopes(ctx, req.Scopes); err != nil {
		return nil, err
	}
	set := scopeSet(req.Scopes)

	bundle := BundleV1{
		Format:          BundleFormat,
		Version:         BundleVersion,
		ExportedAt:      time.UnixMilli(s.now()).Format(time.RFC3339),
		ExportedFrom:    BundleOrigin{Commit: buildinfo.CommitID},
		Scopes:          req.Scopes,
		SecretsIncluded: req.IncludeSecrets,
	}
	summary := map[string]int{}
	var remoteRows []*paired_agentred_entity.PairedAgentred
	remoteRowsLoaded := false

	if _, ok := set[ScopeLLMProviders]; ok {
		rows, err := llm_provider_repo.LLMProvider().List(ctx)
		if err != nil {
			return nil, err
		}
		bundle.Items.LLMProviders = make([]BundleLLMProvider, 0, len(rows))
		for _, r := range rows {
			item, err := toBundleProvider(ctx, r, req.IncludeSecrets)
			if err != nil {
				return nil, err
			}
			bundle.Items.LLMProviders = append(bundle.Items.LLMProviders, item)
		}
		summary[string(ScopeLLMProviders)] = len(bundle.Items.LLMProviders)
	}
	if _, ok := set[ScopeRemoteDevices]; ok {
		rows, err := remote_device_repo.PairedAgentred().List(ctx)
		if err != nil {
			return nil, err
		}
		remoteRows = rows
		remoteRowsLoaded = true
		bundle.Items.RemoteDevices = make([]BundleRemoteDevice, 0, len(rows))
		for _, r := range rows {
			bundle.Items.RemoteDevices = append(bundle.Items.RemoteDevices, toBundleDevice(r, req.IncludeSecrets))
		}
		summary[string(ScopeRemoteDevices)] = len(bundle.Items.RemoteDevices)
	}

	// Build a shared backendKey map once if either scope that uses it is requested.
	// This guarantees AgentBackends and Organization reference the same exportKey values.
	var backendKey map[int64]string
	_, needBackends := set[ScopeAgentBackends]
	_, needOrg := set[ScopeOrganization]
	if needBackends || needOrg {
		backends, err := agent_backend_repo.AgentBackend().List(ctx)
		if err != nil {
			return nil, err
		}
		backendKey = make(map[int64]string, len(backends))
		deviceUUIDByID := map[string]string{}
		if needBackends && backendsHaveDeviceID(backends) {
			rows := remoteRows
			if !remoteRowsLoaded {
				rows, err = remote_device_repo.PairedAgentred().List(ctx)
				if err != nil {
					return nil, err
				}
			}
			deviceUUIDByID = deviceUUIDByRowID(rows)
		}
		for _, b := range backends {
			backendKey[b.ID] = backendExportKeyPrefix + s.newUUID()
		}
		if needBackends {
			bundle.Items.AgentBackends = make([]BundleAgentBackend, 0, len(backends))
			for _, b := range backends {
				item, err := toBundleBackend(b, backendKey[b.ID], deviceUUIDByID)
				if err != nil {
					return nil, err
				}
				bundle.Items.AgentBackends = append(bundle.Items.AgentBackends, item)
			}
			summary[string(ScopeAgentBackends)] = len(bundle.Items.AgentBackends)
		}
	}

	if needOrg {
		// 先 list 全部 department + agent,再分配 exportKey,最后串好 parent / lead / backend ref
		depts, err := department_repo.Department().List(ctx)
		if err != nil {
			return nil, err
		}
		agents, err := agent_repo.Agent().List(ctx)
		if err != nil {
			return nil, err
		}
		agentIDs := make([]int64, 0, len(agents))
		for _, a := range agents {
			agentIDs = append(agentIDs, a.ID)
		}
		// 每个 Agent 的完整有序执行目标列表（R15f）：AgentBackendID / SkillsJSON
		// 只是 sort_order 最小那一档的派生值,老 bundle 兼容字段继续靠它们撑,但
		// 真正的往返载荷是这里取到的整张列表。
		targetsByAgent, err := agent_repo.AgentExecTarget().ListByAgents(ctx, agentIDs)
		if err != nil {
			return nil, err
		}

		deptKey := make(map[int64]string, len(depts))
		agentKey := make(map[int64]string, len(agents))
		for _, d := range depts {
			deptKey[d.ID] = deptExportKeyPrefix + s.newUUID()
		}
		for _, a := range agents {
			agentKey[a.ID] = agentExportKeyPrefix + s.newUUID()
		}

		bundle.Items.Departments = make([]BundleDepartment, 0, len(depts))
		for _, d := range depts {
			bundle.Items.Departments = append(bundle.Items.Departments, toBundleDept(d, deptKey, agentKey))
		}
		bundle.Items.Agents = make([]BundleAgent, 0, len(agents))
		for _, a := range agents {
			bundle.Items.Agents = append(bundle.Items.Agents, toBundleAgent(a, deptKey, agentKey, backendKey, targetsByAgent[a.ID]))
		}
		summary[string(ScopeOrganization)] = len(bundle.Items.Departments) + len(bundle.Items.Agents)
	}

	raw, err := json.MarshalIndent(bundle, "", "  ")
	if err != nil {
		return nil, i18n.NewError(ctx, code.DataExportEncodeFailed)
	}
	return &ExportResult{JSON: raw, Summary: summary}, nil
}

// toBundleProvider 把 Provider 行 + 其全部子模型合成为新 1→N bundle 形状。
// 单模型投影(provider.Model/MaxOutput/ContextWindow)已移除:连接配置留在
// Provider 层,稳定 ModelKey/ModelID 与 token 元数据逐条落到 Models,defaultModelKey
// 指回默认启用模型。APIKey 只在 includeSecrets 时带出。
func toBundleProvider(ctx context.Context, p *llm_provider_entity.LLMProvider, secrets bool) (BundleLLMProvider, error) {
	apiKey := ""
	if secrets {
		apiKey = p.APIKey
	}
	models, err := llm_provider_repo.LLMProvider().ListModels(ctx, p.ID)
	if err != nil {
		return BundleLLMProvider{}, err
	}
	out := BundleLLMProvider{
		ProviderKey:     p.ProviderKey,
		Type:            p.Type,
		Name:            p.Name,
		BaseURL:         p.BaseURL,
		Enabled:         p.IsEnabled(),
		DefaultModelKey: p.DefaultModelKey,
		APIKey:          apiKey,
		Models:          make([]BundleLLMProviderModel, 0, len(models)),
	}
	for _, m := range models {
		out.Models = append(out.Models, BundleLLMProviderModel{
			ModelKey:      m.ModelKey,
			ModelID:       m.ModelID,
			Name:          m.Name,
			ContextWindow: m.ContextWindow,
			MaxOutput:     m.MaxOutput,
			Enabled:       m.IsEnabled(),
		})
	}
	return out, nil
}

func toBundleDevice(d *paired_agentred_entity.PairedAgentred, secrets bool) BundleRemoteDevice {
	pem := ""
	if secrets {
		pem = d.TLSCertPEM
	}
	return BundleRemoteDevice{
		InstanceUUID: d.InstanceUUID, Name: d.Name, URL: d.URL,
		DaemonFingerprint: d.DaemonFingerprint, TLSMode: d.TLSMode, TLSCertPEM: pem,
		PairedAt: d.PairedAt,
	}
}

func backendsHaveDeviceID(backends []*agent_backend_entity.AgentBackend) bool {
	for _, b := range backends {
		if b != nil && remote_device_svc.TargetsAnotherMachine(b.DeviceFingerprint) {
			return true
		}
	}
	return false
}

func deviceUUIDByRowID(devices []*paired_agentred_entity.PairedAgentred) map[string]string {
	out := make(map[string]string, len(devices))
	for _, d := range devices {
		if d == nil || d.InstanceUUID == "" {
			continue
		}
		out[strconv.FormatInt(d.ID, 10)] = d.InstanceUUID
	}
	return out
}

func toBundleBackend(b *agent_backend_entity.AgentBackend, exportKey string, deviceUUIDByID map[string]string) (BundleAgentBackend, error) {
	// bundle 是可移植配置：本机档的含义是「跑在导入它的那台机器上」，因此不带设备
	// 引用。R13 认领后本机 backend 的 DeviceID 是本机指纹，照抄会让导入侧在 devices
	// 段里找不到它（本机不会和自己配对）而整条判 dangling ref —— 见
	// remote_device_svc.ExternalDeviceID。
	deviceID := remote_device_svc.ExternalDeviceID(b.DeviceFingerprint)
	if uuid, ok := deviceUUIDByID[deviceID]; ok {
		deviceID = uuid
	}
	routes, err := agent_backend_entity.ParseModelRoutes(b.ModelRoutes)
	if err != nil {
		// 已入库的 backend 在 Check 阶段就校验过 model_routes，理论到不了这里；
		// 真到了就亮失败而不是静默丢路由（结构化 Route 是 bundle 契约的一部分）。
		return BundleAgentBackend{}, err
	}
	return BundleAgentBackend{
		ExportKey: exportKey,
		Type:      b.Type, Name: b.Name,
		LLMProviderKey: b.LLMProviderKey, LLMModelKey: b.LLMModelKey,
		DeviceID: deviceID, CLIPath: b.CLIPath,
		ModelRoutes: bundleRoutesFromEntity(routes),
		Sandbox:     b.Sandbox, Approval: b.Approval,
		EnvJSON: b.EnvJSON, ReasoningEffort: b.ReasoningEffort,
		DefaultPermissionMode: b.DefaultPermissionMode,
	}, nil
}

// bundleRoutesFromEntity 把实体的 map[alias]ModelRouteTarget 转成 bundle 的
// map[alias]BundleRouteTarget（两者同形，逐字段拷贝）。
func bundleRoutesFromEntity(routes map[string]agent_backend_entity.ModelRouteTarget) map[string]BundleRouteTarget {
	if len(routes) == 0 {
		return map[string]BundleRouteTarget{}
	}
	out := make(map[string]BundleRouteTarget, len(routes))
	for alias, r := range routes {
		out[alias] = BundleRouteTarget{ProviderKey: r.ProviderKey, ModelKey: r.ModelKey}
	}
	return out
}

func toBundleDept(d *department_entity.Department, deptKey, agentKey map[int64]string) BundleDepartment {
	out := BundleDepartment{
		ExportKey: deptKey[d.ID],
		Name:      d.Name, Description: d.Description, Icon: d.Icon, AccentColor: d.AccentColor,
		SortOrder: d.SortOrder,
	}
	if d.ParentID > 0 {
		out.ParentKey = deptKey[d.ParentID]
	}
	if d.LeadAgentID > 0 {
		out.LeadAgentKey = agentKey[d.LeadAgentID]
	}
	return out
}

func toBundleAgent(a *agent_entity.Agent, deptKey, agentKey, backendKey map[int64]string, targets []*agent_entity.AgentExecTarget) BundleAgent {
	out := BundleAgent{
		ExportKey: agentKey[a.ID],
		Name:      a.Name, Description: a.Description,
		AvatarColor: a.AvatarColor, AvatarIcon: a.AvatarIcon, AvatarDataURL: a.AvatarDataURL,
		SystemBadge: a.SystemBadge,
		SortOrder:   a.SortOrder, PromptJSON: a.PromptJSON,
	}
	if a.DepartmentID > 0 {
		out.DepartmentKey = deptKey[a.DepartmentID]
	}
	if a.ParentAgentID > 0 {
		out.ParentAgentKey = agentKey[a.ParentAgentID]
	}
	// ExecTargets 一律写，哪怕是空数组。targets 已经按
	// sort_order 升序给出（agent_repo.AgentExecTarget().ListByAgents 的约定）。
	out.ExecTargets = make([]BundleExecTarget, 0, len(targets))
	for _, t := range targets {
		out.ExecTargets = append(out.ExecTargets, BundleExecTarget{
			BackendKey: backendKey[t.AgentBackendID],
			SortOrder:  t.SortOrder,
			SkillsJSON: t.SkillsJSON,
		})
	}
	return out
}
