package data_svc

import (
	"context"
	"encoding/json"
	"strconv"

	"github.com/cago-frame/cago/pkg/i18n"

	"github.com/agentre-hub/agentre/internal/pkg/code"
	"github.com/agentre-hub/agentre/internal/repository/agent_backend_repo"
	"github.com/agentre-hub/agentre/internal/repository/department_repo"
	"github.com/agentre-hub/agentre/internal/repository/llm_provider_repo"
	"github.com/agentre-hub/agentre/internal/repository/remote_device_repo"
)

// PreviewImport 解析 raw,与本地数据对比,返回逐条 diff。不写库。
func (s *dataSvc) PreviewImport(ctx context.Context, raw []byte) (*ImportPreview, error) {
	var b BundleV1
	if err := json.Unmarshal(raw, &b); err != nil {
		return nil, i18n.NewError(ctx, code.DataBundleFormatInvalid)
	}
	if b.Format != BundleFormat {
		return nil, i18n.NewError(ctx, code.DataBundleFormatInvalid)
	}
	if b.Version != BundleVersion {
		return nil, i18n.NewError(ctx, code.DataBundleVersionUnsupported)
	}
	if err := ValidateScopes(ctx, b.Scopes); err != nil {
		return nil, err
	}

	// 一次性 load 本地全量,后续在内存里比对
	localProviders, err := llm_provider_repo.LLMProvider().List(ctx)
	if err != nil {
		return nil, err
	}
	localDevices, err := remote_device_repo.PairedAgentred().List(ctx)
	if err != nil {
		return nil, err
	}
	localBackends, err := agent_backend_repo.AgentBackend().List(ctx)
	if err != nil {
		return nil, err
	}
	localDepts, err := department_repo.Department().List(ctx)
	if err != nil {
		return nil, err
	}
	provByKey := map[string]int64{}
	provNameByKey := map[string]string{}
	for _, p := range localProviders {
		provByKey[p.ProviderKey] = p.ID
		provNameByKey[p.ProviderKey] = p.Name
	}
	devByUUID := map[string]int64{}
	devNameByUUID := map[string]string{}
	for _, d := range localDevices {
		devByUUID[d.InstanceUUID] = d.ID
		devNameByUUID[d.InstanceUUID] = d.Name
	}
	deviceRefs := newDeviceRefResolver(localDevices, b.Items.RemoteDevices)
	// agentBackend by name(多条同名 → ambiguous)
	backendByName := map[string]int64{}
	backendNameCount := map[string]int{}
	for _, bk := range localBackends {
		backendNameCount[bk.Name]++
		backendByName[bk.Name] = bk.ID
	}
	// department by (parentID, name)
	deptByKey := map[string]int64{} // key = "<parentID>|<name>"
	deptNameMap := map[string]string{}
	for _, d := range localDepts {
		k := deptKey(d.ParentID, d.Name)
		deptByKey[k] = d.ID
		deptNameMap[k] = d.Name
	}
	// bundle 内部 exportKey 集合,用于 dangling 检测
	bundleProviderKeys := map[string]struct{}{}
	bundleBackendKeys := map[string]struct{}{}
	bundleDeptKeys := map[string]struct{}{}
	bundleAgentKeys := map[string]struct{}{}
	for _, p := range b.Items.LLMProviders {
		bundleProviderKeys[p.ProviderKey] = struct{}{}
	}
	for _, bk := range b.Items.AgentBackends {
		bundleBackendKeys[bk.ExportKey] = struct{}{}
	}
	for _, d := range b.Items.Departments {
		bundleDeptKeys[d.ExportKey] = struct{}{}
	}
	for _, a := range b.Items.Agents {
		bundleAgentKeys[a.ExportKey] = struct{}{}
	}

	items := make([]ImportItem, 0, 32)

	for _, p := range b.Items.LLMProviders {
		it := ImportItem{Scope: string(ScopeLLMProviders), SourceKey: p.ProviderKey, Name: p.Name}
		if id, ok := provByKey[p.ProviderKey]; ok {
			it.Conflict = true
			it.LocalID = id
			it.LocalName = provNameByKey[p.ProviderKey]
		}
		it.DefaultAction = defaultActionFor(it)
		items = append(items, it)
	}
	for _, d := range b.Items.RemoteDevices {
		it := ImportItem{Scope: string(ScopeRemoteDevices), SourceKey: d.InstanceUUID, Name: d.Name}
		if id, ok := devByUUID[d.InstanceUUID]; ok {
			it.Conflict = true
			it.LocalID = id
			it.LocalName = devNameByUUID[d.InstanceUUID]
		}
		it.DefaultAction = defaultActionFor(it)
		items = append(items, it)
	}
	for _, bk := range b.Items.AgentBackends {
		it := ImportItem{Scope: string(ScopeAgentBackends), SourceKey: bk.ExportKey, Name: bk.Name}
		if bk.LLMProviderKey != "" {
			if _, ok := bundleProviderKeys[bk.LLMProviderKey]; !ok {
				if _, ok2 := provByKey[bk.LLMProviderKey]; !ok2 {
					it.Dangling = true
					it.DanglingHint = "引用的 LLM 供应商既不在导入范围内,本地也找不到"
				}
			}
		}
		if bk.DeviceID != "" && !it.Dangling {
			if _, ok := deviceRefs.StableKey(bk.DeviceID); !ok {
				it.Dangling = true
				it.DanglingHint = "引用的远端设备既不在导入范围内,本地也找不到"
			}
		}
		if !it.Dangling && backendNameCount[bk.Name] > 1 {
			it.Dangling = true
			it.DanglingHint = "本地存在多条同名后端,无法自动覆盖"
		} else if !it.Dangling {
			if id, ok := backendByName[bk.Name]; ok {
				it.Conflict = true
				it.LocalID = id
				it.LocalName = bk.Name
			}
		}
		it.DefaultAction = defaultActionFor(it)
		items = append(items, it)
	}
	for _, d := range b.Items.Departments {
		it := ImportItem{Scope: string(ScopeOrganization), SourceKey: d.ExportKey, Name: d.Name}
		// parent dangling 检查只对非空 parentKey 生效
		if d.ParentKey != "" {
			if _, ok := bundleDeptKeys[d.ParentKey]; !ok {
				it.Dangling = true
				it.DanglingHint = "父部门不在导入范围内"
			}
		}
		// 找冲突:只能用 bundle 内的父→已存在本地父 ID 反查;若父也是本次新增就先不算冲突
		// 简化:这里只做 root 级 + 本地匹配 root parent 0 的精确比对
		if !it.Dangling && d.ParentKey == "" {
			k := deptKey(0, d.Name)
			if id, ok := deptByKey[k]; ok {
				it.Conflict = true
				it.LocalID = id
				it.LocalName = deptNameMap[k]
			}
		}
		it.DefaultAction = defaultActionFor(it)
		items = append(items, it)
	}
	for _, a := range b.Items.Agents {
		it := ImportItem{Scope: string(ScopeOrganization), SourceKey: a.ExportKey, Name: a.Name}
		if a.DepartmentKey != "" {
			if _, ok := bundleDeptKeys[a.DepartmentKey]; !ok {
				it.Dangling = true
				it.DanglingHint = "所属部门不在导入范围内"
			}
		}
		for _, target := range a.ExecTargets {
			if target.BackendKey != "" && !it.Dangling {
				if _, ok := bundleBackendKeys[target.BackendKey]; !ok {
					it.Dangling = true
					it.DanglingHint = "绑定的 Agent 后端不在导入范围内"
				}
			}
		}
		if a.ParentAgentKey != "" && !it.Dangling {
			if _, ok := bundleAgentKeys[a.ParentAgentKey]; !ok {
				it.Dangling = true
				it.DanglingHint = "上级 Agent 不在导入范围内"
			}
		}
		it.DefaultAction = defaultActionFor(it)
		items = append(items, it)
	}

	return &ImportPreview{
		Format:          b.Format,
		Version:         b.Version,
		SecretsIncluded: b.SecretsIncluded,
		Items:           items,
	}, nil
}

func defaultActionFor(it ImportItem) ItemAction {
	if it.Dangling {
		return ActionSkip
	}
	if it.Conflict {
		return ActionSkip
	}
	return ActionCreate
}

func deptKey(parentID int64, name string) string {
	return strconv.FormatInt(parentID, 10) + "|" + name
}
