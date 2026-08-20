package skill_svc

import (
	"context"
	"strings"

	"github.com/agentre-ai/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-ai/agentre/internal/model/entity/agent_entity"
	"github.com/agentre-ai/agentre/internal/pkg/agentskill"
	"github.com/agentre-ai/agentre/internal/service/remote_device_svc"
)

// Service 技能包组合服务。依赖通过消费者侧窄接口注入(DIP)。
type Service struct {
	agent      AgentLookup
	backend    BackendLookup
	execTarget ExecTargetLookup // 技能授权挂在执行目标行上(R15e),不再挂在 Agent 行
	remote     RemoteDiscoverer // 远端 backend 走 daemon 发现;本地 backend 不用
}

type discoveryResult struct {
	backendType agent_backend_entity.BackendType
	backend     *agent_backend_entity.AgentBackend
	packs       []agentskill.SkillPack
}

// discover 拿该 agent backend 的已安装包(无发现器的 backend 为空)。
func (s *Service) discover(ctx context.Context, a *agent_entity.Agent) (discoveryResult, error) {
	be, err := s.backend.Find(ctx, a.AgentBackendID)
	if err != nil || be == nil {
		return discoveryResult{}, err
	}
	return s.discoverForBackend(ctx, be)
}

// discoverForBackend 是 discover 的核心：拿指定 backend 的已安装包。抽成独立函数是
// 因为任务 12(组织架构页"一档一块")需要按**给定的执行目标**发现，不是按 Agent
// 的主档——discover 本身保持不变(仍按 a.AgentBackendID 找 backend 再委派到这里)。
func (s *Service) discoverForBackend(ctx context.Context, be *agent_backend_entity.AgentBackend) (discoveryResult, error) {
	backendType := agent_backend_entity.BackendType(be.Type)
	// 远端 backend:技能包装在 daemon 那台机器上,desktop 本地的 claude plugin list
	// 看不到。经 RemoteDiscoverer 走 daemon skills.list 发现(借 device 连接池)。
	// 指向本机指纹的档(R13 认领后本机 backend 的 DeviceID == 本机指纹)不是远端:
	// 它跟 DeviceID 空一样走本地 Discoverer。
	if remote_device_svc.TargetsAnotherMachine(be.DeviceID) {
		deviceID, ok := be.DeviceIDInt()
		if !ok || s.remote == nil {
			return discoveryResult{backendType: backendType, backend: be, packs: []agentskill.SkillPack{}}, nil
		}
		packs, err := s.remote.ListSkills(ctx, deviceID, be.Type)
		if err != nil {
			return discoveryResult{}, err
		}
		if packs == nil {
			packs = []agentskill.SkillPack{}
		}
		return discoveryResult{backendType: backendType, backend: be, packs: packs}, nil
	}
	d, ok := agentskill.DiscovererFor(backendType)
	if !ok {
		return discoveryResult{backendType: backendType, backend: be, packs: []agentskill.SkillPack{}}, nil
	}
	packs, err := d.Discover(ctx, agentskill.DiscoverQuery{
		BackendType: backendType,
		CLIPath:     be.CLIPath,
	})
	return discoveryResult{backendType: backendType, backend: be, packs: packs}, err
}

// authorizedSkills 取 agentID 主档(sort_order 最小的一档)的技能授权。存放位置
// 已从 agents.skills_json 下沉到 agent_exec_targets(R15e),这里不再读 Agent 行 ——
// 也不做跨档并集:agentID 有几档就有几份互不相干的授权,这里只取最靠前那一档的。
func (s *Service) authorizedSkills(ctx context.Context, agentID int64) ([]agent_entity.AgentSkillItem, error) {
	return s.authorizedSkillsForTarget(ctx, agentID, 0)
}

// authorizedSkillsForTarget 取 agentID 名下**指定那一档**的技能授权(R15e)。
// agentBackendID <= 0 时回落到主档 —— 老会话没钉过档,行为必须与钉档前一致。
//
// 指定了一档、但它已经不在列表里(用户把这一档从组织架构页删了,backend 本身还在,
// 会话仍钉在它上面)时返回空授权,**不**回落到主档:回落会把用户在别的档上授权、
// 甚至显式关掉的技能,注入到一个从没授权过它们的 backend 上。
func (s *Service) authorizedSkillsForTarget(
	ctx context.Context, agentID, agentBackendID int64,
) ([]agent_entity.AgentSkillItem, error) {
	targets, err := s.execTarget.ListByAgent(ctx, agentID)
	if err != nil {
		return nil, err
	}
	if len(targets) == 0 {
		return nil, nil
	}
	if agentBackendID > 0 {
		for _, t := range targets {
			if t != nil && t.AgentBackendID == agentBackendID {
				return t.GetSkills(), nil
			}
		}
		return nil, nil
	}
	if targets[0] == nil {
		return nil, nil
	}
	return targets[0].GetSkills(), nil
}

// ListAgentSkillPacks 合并推荐 + 发现 + agent 授权,产出目录。refresh 预留(未来强制重发现),当前忽略。
func (s *Service) ListAgentSkillPacks(ctx context.Context, agentID int64, _ bool) (SkillCatalogDTO, error) {
	a, err := s.agent.Find(ctx, agentID)
	if err != nil || a == nil {
		return SkillCatalogDTO{}, err
	}
	discovered, err := s.discover(ctx, a)
	if err != nil {
		return SkillCatalogDTO{}, err
	}
	authorized, err := s.authorizedSkills(ctx, agentID)
	if err != nil {
		return SkillCatalogDTO{}, err
	}
	return catalogOf(discovered, authorized), nil
}

// catalogOf 把「发现到的包 + 这份授权」合并成目录 DTO。两个 List…Packs… 的出口
// 逐字相同过，只有取包与取授权的来源不同——合并与映射只留这一份，免得两边各改
// 各的又漂开。
func catalogOf(discovered discoveryResult, authorized []agent_entity.AgentSkillItem) SkillCatalogDTO {
	entries := agentskill.MergeCatalog(agentskill.RecommendedFor(discovered.backendType), discovered.packs, authorized)
	dto := make([]SkillPackDTO, 0, len(entries))
	for _, e := range entries {
		dto = append(dto, SkillPackDTO{
			ID:               e.Pack.ID,
			Name:             e.Pack.Name,
			Description:      e.Pack.Description,
			Skills:           e.Pack.Skills,
			Source:           string(e.Pack.Source),
			Recommended:      e.Pack.Recommended,
			Installed:        e.Pack.Installed,
			Enabled:          e.Enabled,
			GloballyEnabled:  e.Pack.GloballyEnabled,
			EffectiveEnabled: e.EffectiveEnabled,
		})
	}
	return SkillCatalogDTO{Packs: dto}
}

// ListAgentSkillPacksForTarget 同 ListAgentSkillPacks，但发现来源与授权都钉死在
// agentID 名下 agentBackendID 对应的那一档执行目标上（R15e，任务 12"组织架构页
// 一档一块"）：一档一块，互不干扰、不做并集——不像 ListAgentSkillPacks 只看
// sort_order 最小的主档。找不到该档（agentBackendID 不在这个 Agent 的列表里，例如
// 前端还没保存完就切换了）返回空目录、不是错误，与 agentID 找不到时的既有处理口径
// 一致（见上面 ListAgentSkillPacks 对 a==nil 的处理）。
func (s *Service) ListAgentSkillPacksForTarget(ctx context.Context, agentID, agentBackendID int64, _ bool) (SkillCatalogDTO, error) {
	targets, err := s.execTarget.ListByAgent(ctx, agentID)
	if err != nil {
		return SkillCatalogDTO{}, err
	}
	var target *agent_entity.AgentExecTarget
	for _, t := range targets {
		if t.AgentBackendID == agentBackendID {
			target = t
			break
		}
	}
	if target == nil {
		return SkillCatalogDTO{}, nil
	}
	be, err := s.backend.Find(ctx, agentBackendID)
	if err != nil || be == nil {
		return SkillCatalogDTO{}, err
	}
	discovered, err := s.discoverForBackend(ctx, be)
	if err != nil {
		return SkillCatalogDTO{}, err
	}
	return catalogOf(discovered, target.GetSkills()), nil
}

// ListAgentSkillCommands 返回当前 agent 在 cwd 中可调用的 Skill 命令。
// 已安装 plugin 的生效态由目录合并结果决定；本地 backend 再合并 CLI 自己解析的
// user/project/system Skill。远端 backend 当前由 daemon 的 plugin 目录提供命令。
func (s *Service) ListAgentSkillCommands(ctx context.Context, agentID int64, cwd string) (SkillCommandCatalogDTO, error) {
	a, err := s.agent.Find(ctx, agentID)
	if err != nil || a == nil {
		return SkillCommandCatalogDTO{}, err
	}
	discovered, err := s.discover(ctx, a)
	if err != nil {
		return SkillCommandCatalogDTO{}, err
	}
	authorized, err := s.authorizedSkills(ctx, agentID)
	if err != nil {
		return SkillCommandCatalogDTO{}, err
	}

	entries := agentskill.MergeCatalog(agentskill.RecommendedFor(discovered.backendType), discovered.packs, authorized)
	commands := make([]SkillCommandDTO, 0)
	seen := map[string]struct{}{}
	appendCommand := func(name, description string) {
		name = strings.TrimSpace(name)
		if name == "" {
			return
		}
		if _, ok := seen[name]; ok {
			return
		}
		seen[name] = struct{}{}
		commands = append(commands, SkillCommandDTO{
			Name:        name,
			Description: strings.TrimSpace(description),
		})
	}

	for _, entry := range entries {
		if !entry.EffectiveEnabled {
			continue
		}
		pack := entry.Pack
		for _, rawSkill := range pack.Skills {
			skill := strings.TrimSpace(rawSkill)
			if skill == "" {
				continue
			}
			name := skill
			if !strings.Contains(skill, ":") && strings.TrimSpace(pack.Name) != "" {
				name = strings.TrimSpace(pack.Name) + ":" + skill
			}
			appendCommand(name, pack.Description)
		}
	}

	// 指向本机指纹的档（R13 认领后本机 backend 的 DeviceID == 本机指纹）跟 DeviceID 空
	// 一样是本地档：CLI 自己解析的 user/project/system 命令也必须合并进来，不能因为
	// DeviceID 非空就按远端档跳过（discoverForBackend 已把 self 当本地发现，这里只差
	// 原生命令这一半边）。
	if discovered.backend != nil && !remote_device_svc.TargetsAnotherMachine(discovered.backend.DeviceID) {
		if commandDiscoverer, ok := agentskill.CommandDiscovererFor(discovered.backendType); ok {
			native, err := commandDiscoverer.DiscoverCommands(ctx, agentskill.CommandDiscoverQuery{
				BackendType:    discovered.backendType,
				CLIPath:        discovered.backend.CLIPath,
				Cwd:            strings.TrimSpace(cwd),
				EnabledPlugins: enabledPlugins(authorized),
			})
			if err != nil {
				return SkillCommandCatalogDTO{}, err
			}
			for _, command := range native {
				appendCommand(command.Name, command.Description)
			}
		}
	}

	return SkillCommandCatalogDTO{Commands: commands}, nil
}

func enabledPlugins(items []agent_entity.AgentSkillItem) map[string]bool {
	out := make(map[string]bool, len(items))
	for _, item := range items {
		out[item.ID] = item.Enabled
	}
	return out
}

// EnabledPluginsMapForTarget 同 EnabledPluginsMap,但授权取自 agentBackendID 指名的
// **那一档**执行目标(R15b / R15e):技能授权已经下沉到单个执行目标,一轮该注入哪一份
// 由「这一轮落到的那一档」回答,不是「Agent 的主档」—— 同一台机器上可以有多档,
// 续轮取的必须是会话钉住的那一档的那份,不是同机第一档。agentBackendID <= 0
// (老会话尚未钉档)回落到主档。
func (s *Service) EnabledPluginsMapForTarget(
	ctx context.Context, agentID, agentBackendID int64,
) (map[string]bool, error) {
	a, err := s.agent.Find(ctx, agentID)
	if err != nil || a == nil {
		return nil, err
	}
	authorized, err := s.authorizedSkillsForTarget(ctx, agentID, agentBackendID)
	if err != nil {
		return nil, err
	}
	return enabledPlugins(authorized), nil
}
