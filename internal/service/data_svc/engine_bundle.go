package data_svc

import (
	"context"

	"github.com/cago-frame/cago/pkg/consts"
	"github.com/cago-frame/cago/pkg/i18n"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/agent_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/device_pref_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/llm_provider_entity"
	"github.com/agentre-hub/agentre/internal/pkg/code"
	"github.com/agentre-hub/agentre/internal/repository/agent_backend_repo"
	"github.com/agentre-hub/agentre/internal/repository/agent_repo"
	"github.com/agentre-hub/agentre/internal/repository/department_repo"
	"github.com/agentre-hub/agentre/internal/repository/device_pref_repo"
	"github.com/agentre-hub/agentre/internal/repository/llm_provider_repo"
	"github.com/agentre-hub/agentre/pkg/wire/devicefp"
)

// 引擎配置、设备偏好与 CLI 覆盖这批新结构的导出 / 预览 / 导入。引用沿用 bundle 的
// exportKey 约定；写入一律经窄写口（整行写口跳过这些列）。

const engineExportKeyPrefix = "ep-"

// ── 导出 ────────────────────────────────────────────────────────────────────

// assignEngineKeys 给全部活的引擎配置分配 exportKey。engine-profiles 与 organization
// 两个 scope 共用这一份，保证两边引用的是同一批 key。
func (s *dataSvc) assignEngineKeys(ctx context.Context) ([]*agent_backend_entity.EngineProfile, map[int64]string, error) {
	profiles, err := agent_backend_repo.EngineProfile().List(ctx)
	if err != nil {
		return nil, nil, err
	}
	keys := make(map[int64]string, len(profiles))
	for _, p := range profiles {
		keys[p.ID] = engineExportKeyPrefix + s.newUUID()
	}
	return profiles, keys, nil
}

func toBundleEngineProfile(p *agent_backend_entity.EngineProfile, exportKey string) BundleEngineProfile {
	return BundleEngineProfile{
		ExportKey: exportKey, Type: p.Type, Name: p.Name,
		LLMProviderKey: p.LLMProviderKey, LLMModelKey: p.LLMModelKey,
		EnvJSON: p.EnvJSON, ReasoningEffort: p.ReasoningEffort,
		Config:         p.AsBackend().Config(),
		AccountDefault: p.IsAccountDefault,
	}
}

func toBundleReach(p *llm_provider_entity.LLMProvider) *BundleReach {
	mode, devices := p.Reach()
	if mode != llm_provider_entity.ReachDevices {
		return nil
	}
	out := &BundleReach{Mode: mode}
	for _, d := range devices {
		out.Devices = append(out.Devices, string(d))
	}
	return out
}

// applyAgentEngineToBundle 把成员的执行设置写进 bundle 条目；全是默认值时什么都不带。
func applyAgentEngineToBundle(out *BundleAgent, a *agent_entity.Agent, engineKey map[int64]string) {
	if a.EngineProfileID > 0 {
		out.EngineKey = engineKey[a.EngineProfileID]
	}
	if c := a.GetExecConstraint(); c.Mode != agent_entity.ExecConstraintAny {
		bc := &BundleExecConstraint{Mode: c.Mode, Labels: c.Labels}
		for _, d := range c.Devices {
			bc.Devices = append(bc.Devices, string(d))
		}
		out.ExecConstraint = bc
	}
	out.SkillGrantsRuntime = a.SkillGrantsRuntime
	if a.SkillGrantsJSON != "" && a.SkillGrantsJSON != "[]" {
		out.SkillGrantsJSON = a.SkillGrantsJSON
	}
}

func (s *dataSvc) exportDevicePrefs(ctx context.Context, bundle *BundleV1) error {
	prefs, err := device_pref_repo.DevicePref().List(ctx)
	if err != nil {
		return err
	}
	paths, err := device_pref_repo.CLIPath().List(ctx)
	if err != nil {
		return err
	}
	bundle.Items.DevicePrefs = make([]BundleDevicePref, 0, len(prefs))
	for _, p := range prefs {
		bundle.Items.DevicePrefs = append(bundle.Items.DevicePrefs, BundleDevicePref{
			DeviceFingerprint: string(p.DeviceFingerprint), Labels: p.Labels(), SortOrder: p.SortOrder,
		})
	}
	bundle.Items.DeviceCLIPaths = make([]BundleDeviceCLIPath, 0, len(paths))
	for _, o := range paths {
		bundle.Items.DeviceCLIPaths = append(bundle.Items.DeviceCLIPaths, BundleDeviceCLIPath{
			RuntimeType: o.RuntimeType, DeviceFingerprint: string(o.DeviceFingerprint), CLIPath: o.CLIPath,
		})
	}
	return nil
}

// ── 预览 ────────────────────────────────────────────────────────────────────

func cliPathSourceKey(runtimeType, fingerprint string) string { return runtimeType + "|" + fingerprint }

// previewEngineProfiles 引擎配置按名称匹配本地（与后端同一套规则：同名多条无法自动覆盖）。
func previewEngineProfiles(ctx context.Context, b BundleV1) ([]ImportItem, error) {
	if len(b.Items.EngineProfiles) == 0 {
		return nil, nil
	}
	local, err := agent_backend_repo.EngineProfile().List(ctx)
	if err != nil {
		return nil, err
	}
	byName := map[string]int64{}
	nameCount := map[string]int{}
	for _, p := range local {
		nameCount[p.Name]++
		byName[p.Name] = p.ID
	}
	items := make([]ImportItem, 0, len(b.Items.EngineProfiles))
	for _, p := range b.Items.EngineProfiles {
		it := ImportItem{Scope: string(ScopeEngineProfiles), SourceKey: p.ExportKey, Name: p.Name}
		if nameCount[p.Name] > 1 {
			it.Dangling = true
			it.DanglingHint = "本地存在多条同名引擎配置,无法自动覆盖"
		} else if id, ok := byName[p.Name]; ok {
			it.Conflict, it.LocalID, it.LocalName = true, id, p.Name
		}
		it.DefaultAction = defaultActionFor(it)
		items = append(items, it)
	}
	return items, nil
}

// previewDevicePrefs 设备偏好按设备指纹、CLI 覆盖按（运行时类型, 设备指纹）匹配本地。
func previewDevicePrefs(ctx context.Context, b BundleV1) ([]ImportItem, error) {
	var items []ImportItem
	if len(b.Items.DevicePrefs) > 0 {
		local, err := device_pref_repo.DevicePref().List(ctx)
		if err != nil {
			return nil, err
		}
		have := map[string]int64{}
		for _, p := range local {
			have[string(p.DeviceFingerprint)] = p.ID
		}
		for _, p := range b.Items.DevicePrefs {
			it := ImportItem{Scope: string(ScopeDevicePrefs), SourceKey: p.DeviceFingerprint, Name: p.DeviceFingerprint}
			if id, ok := have[p.DeviceFingerprint]; ok {
				it.Conflict, it.LocalID = true, id
			}
			it.DefaultAction = defaultActionFor(it)
			items = append(items, it)
		}
	}
	if len(b.Items.DeviceCLIPaths) > 0 {
		local, err := device_pref_repo.CLIPath().List(ctx)
		if err != nil {
			return nil, err
		}
		have := map[string]int64{}
		for _, o := range local {
			have[cliPathSourceKey(o.RuntimeType, string(o.DeviceFingerprint))] = o.ID
		}
		for _, o := range b.Items.DeviceCLIPaths {
			key := cliPathSourceKey(o.RuntimeType, o.DeviceFingerprint)
			it := ImportItem{Scope: string(ScopeDevicePrefs), SourceKey: key, Name: key}
			if id, ok := have[key]; ok {
				it.Conflict, it.LocalID = true, id
			}
			it.DefaultAction = defaultActionFor(it)
			items = append(items, it)
		}
	}
	return items, nil
}

// ── 导入 ────────────────────────────────────────────────────────────────────

// engineProfileFromBundle 把 bundle 条目摊成引擎配置实体并过一遍既有校验。
func engineProfileFromBundle(ctx context.Context, row *agent_backend_entity.EngineProfile, bp BundleEngineProfile, now int64) error {
	row.Type, row.Name = bp.Type, bp.Name
	row.LLMProviderKey, row.LLMModelKey = bp.LLMProviderKey, bp.LLMModelKey
	row.EnvJSON, row.ReasoningEffort = bp.EnvJSON, bp.ReasoningEffort
	// 类型专属设置整份替换：经 AgentBackend 的同一份 SetConfig 转换，规则只写一份。
	cfg := &agent_backend_entity.AgentBackend{}
	cfg.SetConfig(bp.Config)
	if err := cfg.MarshalConfig(); err != nil {
		return i18n.NewError(ctx, code.DataBundleFormatInvalid)
	}
	row.ConfigJSON = cfg.ConfigJSON
	if err := row.UnmarshalConfig(); err != nil {
		return i18n.NewError(ctx, code.DataBundleFormatInvalid)
	}
	row.Status = consts.ACTIVE
	row.Updatetime = now
	return row.Check(ctx)
}

func applyEngineProfiles(ctx context.Context, b BundleV1, actions map[string]ItemAction, km *keyMap, counts map[string]int, now int64) error {
	if len(b.Items.EngineProfiles) == 0 {
		return nil
	}
	existing, err := agent_backend_repo.EngineProfile().List(ctx)
	if err != nil {
		return err
	}
	byName := map[string]*agent_backend_entity.EngineProfile{}
	nameCount := map[string]int{}
	for _, p := range existing {
		nameCount[p.Name]++
		byName[p.Name] = p
	}
	for _, bp := range b.Items.EngineProfiles {
		act := actions[actionKey(string(ScopeEngineProfiles), bp.ExportKey)]
		var local *agent_backend_entity.EngineProfile
		if nameCount[bp.Name] == 1 {
			local = byName[bp.Name]
		}
		switch act {
		case ActionSkip:
			counts["skipped"]++
			if local != nil {
				km.engines[bp.ExportKey] = local.ID
			}
		case ActionOverwrite:
			if local == nil {
				return i18n.NewError(ctx, code.DataImportInvalidAction)
			}
			if err := engineProfileFromBundle(ctx, local, bp, now); err != nil {
				return err
			}
			if err := agent_backend_repo.EngineProfile().Update(ctx, local); err != nil {
				return err
			}
			km.engines[bp.ExportKey] = local.ID
			counts["overwrote"]++
		case ActionDuplicate, ActionCreate:
			if act == ActionCreate && local != nil {
				return i18n.NewError(ctx, code.DataImportInvalidAction)
			}
			row := &agent_backend_entity.EngineProfile{Createtime: now}
			if err := engineProfileFromBundle(ctx, row, bp, now); err != nil {
				return err
			}
			if act == ActionDuplicate {
				row.Name = uniqueName(bp.Name, func(n string) bool { return byName[n] != nil })
			}
			if err := agent_backend_repo.EngineProfile().Create(ctx, row); err != nil {
				return err
			}
			km.engines[bp.ExportKey] = row.ID
			if act == ActionDuplicate {
				counts["duplicated"]++
			} else {
				counts["created"]++
			}
		default:
			return i18n.NewError(ctx, code.DataImportInvalidAction)
		}
	}
	return nil
}

// applyProviderReach 把 bundle 的可达范围经窄写口落到供应商。缺席 = 不碰本地。
func applyProviderReach(ctx context.Context, providerID int64, p BundleLLMProvider) error {
	if p.Reach == nil {
		return nil
	}
	devices := make([]devicefp.Carrier, 0, len(p.Reach.Devices))
	for _, d := range p.Reach.Devices {
		devices = append(devices, devicefp.Carrier(d))
	}
	tmp := &llm_provider_entity.LLMProvider{}
	if err := tmp.SetReach(ctx, p.Reach.Mode, devices); err != nil {
		return err
	}
	return llm_provider_repo.LLMProvider().UpdateReach(ctx, providerID, tmp.ReachMode, tmp.ReachDevicesJSON)
}

// applyDepartmentEngine 经窄写口落成员默认引擎；key 解析不出（引擎不在导入范围内）时
// 不碰本地——部门照常导入，继续跟随上级。
func applyDepartmentEngine(ctx context.Context, deptID int64, d BundleDepartment, km *keyMap) error {
	engineID, ok := km.engines[d.DefaultEngineKey]
	if d.DefaultEngineKey == "" || !ok {
		return nil
	}
	return department_repo.Department().UpdateDefaultEngineProfile(ctx, deptID, engineID)
}

// applyAgentEngineSettings 经窄写口落成员的执行设置。bundle 没带任何执行设置、或带的
// 引用都解析不出时不写；引用解析不出而其它设置在时，保留行上现有的引擎引用。
func applyAgentEngineSettings(ctx context.Context, row *agent_entity.Agent, a BundleAgent, km *keyMap) error {
	engineID, resolved := km.engines[a.EngineKey]
	if a.EngineKey == "" {
		resolved = false
	}
	if !resolved && a.ExecConstraint == nil && a.SkillGrantsRuntime == "" && a.SkillGrantsJSON == "" {
		return nil
	}
	if resolved {
		row.EngineProfileID = engineID
	}
	if a.ExecConstraint != nil {
		devices := make([]devicefp.Carrier, 0, len(a.ExecConstraint.Devices))
		for _, d := range a.ExecConstraint.Devices {
			devices = append(devices, devicefp.Carrier(d))
		}
		if err := row.SetExecConstraint(ctx, agent_entity.ExecConstraint{
			Mode: a.ExecConstraint.Mode, Labels: a.ExecConstraint.Labels, Devices: devices,
		}); err != nil {
			return err
		}
	}
	if a.SkillGrantsRuntime != "" || a.SkillGrantsJSON != "" {
		row.SkillGrantsRuntime = a.SkillGrantsRuntime
		row.SkillGrantsJSON = a.SkillGrantsJSON
		if row.SkillGrantsJSON == "" {
			row.SkillGrantsJSON = "[]"
		}
	}
	return agent_repo.Agent().UpdateEngineSettings(ctx, row)
}

func applyDevicePrefs(ctx context.Context, b BundleV1, actions map[string]ItemAction, counts map[string]int, now int64) error {
	if len(b.Items.DevicePrefs) > 0 {
		local, err := device_pref_repo.DevicePref().List(ctx)
		if err != nil {
			return err
		}
		have := map[string]*device_pref_entity.DevicePref{}
		for _, p := range local {
			have[string(p.DeviceFingerprint)] = p
		}
		for _, bp := range b.Items.DevicePrefs {
			if err := applyOneDevicePref(ctx, bp, have[bp.DeviceFingerprint],
				actions[actionKey(string(ScopeDevicePrefs), bp.DeviceFingerprint)], counts, now); err != nil {
				return err
			}
		}
	}
	if len(b.Items.DeviceCLIPaths) > 0 {
		local, err := device_pref_repo.CLIPath().List(ctx)
		if err != nil {
			return err
		}
		have := map[string]*device_pref_entity.CLIPathOverride{}
		for _, o := range local {
			have[cliPathSourceKey(o.RuntimeType, string(o.DeviceFingerprint))] = o
		}
		for _, bo := range b.Items.DeviceCLIPaths {
			key := cliPathSourceKey(bo.RuntimeType, bo.DeviceFingerprint)
			if err := applyOneCLIPath(ctx, bo, have[key], actions[actionKey(string(ScopeDevicePrefs), key)], counts, now); err != nil {
				return err
			}
		}
	}
	return nil
}

// 设备偏好 / CLI 覆盖以自然键标识：没有「另存一份」的含义，duplicate 一律是非法动作。
func applyOneDevicePref(ctx context.Context, bp BundleDevicePref, local *device_pref_entity.DevicePref, act ItemAction, counts map[string]int, now int64) error {
	fp := devicefp.Carrier(bp.DeviceFingerprint)
	incoming := &device_pref_entity.DevicePref{DeviceFingerprint: fp, SortOrder: bp.SortOrder, Status: consts.ACTIVE, Createtime: now, Updatetime: now}
	incoming.SetLabels(bp.Labels)
	if err := incoming.Check(ctx); err != nil {
		return err
	}
	switch act {
	case ActionSkip:
		counts["skipped"]++
	case ActionOverwrite:
		if local == nil {
			return i18n.NewError(ctx, code.DataImportInvalidAction)
		}
		if err := device_pref_repo.DevicePref().SetLabels(ctx, fp, incoming.LabelsJSON); err != nil {
			return err
		}
		if err := device_pref_repo.DevicePref().SetSortOrder(ctx, fp, bp.SortOrder); err != nil {
			return err
		}
		counts["overwrote"]++
	case ActionCreate:
		if local != nil {
			return i18n.NewError(ctx, code.DataImportInvalidAction)
		}
		if err := device_pref_repo.DevicePref().Create(ctx, incoming); err != nil {
			return err
		}
		counts["created"]++
	default:
		return i18n.NewError(ctx, code.DataImportInvalidAction)
	}
	return nil
}

func applyOneCLIPath(ctx context.Context, bo BundleDeviceCLIPath, local *device_pref_entity.CLIPathOverride, act ItemAction, counts map[string]int, now int64) error {
	incoming := &device_pref_entity.CLIPathOverride{
		RuntimeType: bo.RuntimeType, DeviceFingerprint: devicefp.Carrier(bo.DeviceFingerprint), CLIPath: bo.CLIPath,
		Status: consts.ACTIVE, Createtime: now, Updatetime: now,
	}
	if err := incoming.Check(ctx); err != nil {
		return err
	}
	switch act {
	case ActionSkip:
		counts["skipped"]++
	case ActionOverwrite:
		if local == nil {
			return i18n.NewError(ctx, code.DataImportInvalidAction)
		}
		if err := device_pref_repo.CLIPath().UpdatePath(ctx, local.ID, bo.CLIPath); err != nil {
			return err
		}
		counts["overwrote"]++
	case ActionCreate:
		if local != nil {
			return i18n.NewError(ctx, code.DataImportInvalidAction)
		}
		if err := device_pref_repo.CLIPath().Create(ctx, incoming); err != nil {
			return err
		}
		counts["created"]++
	default:
		return i18n.NewError(ctx, code.DataImportInvalidAction)
	}
	return nil
}
