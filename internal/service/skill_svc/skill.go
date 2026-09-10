package skill_svc

import (
	"context"
	"strings"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/agent_entity"
	"github.com/agentre-hub/agentre/internal/pkg/agentskill"
	"github.com/agentre-hub/agentre/internal/service/remote_device_svc"
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
	if remote_device_svc.TargetsAnotherMachine(be.DeviceFingerprint) {
		deviceID, ok := pairedDeviceID(ctx, be.DeviceFingerprint)
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

// pairedDeviceID 把 backend 的 DeviceID（规范指纹）解析成本机 paired_agentreds 的
// 行 ID —— 远端发现借的是 device 连接池，那个子系统按数值行 ID 建键。指纹在本机配
// 对表里查不到（这台 daemon 没在本机配对）返回 (0,false)，调用方据此回空包而不是
// 猜一个行号去拨号。与 chat_svc.localPairedDeviceID 同一取法，skill_svc 侧独立声明
// 以保持 consumer-side 窄依赖。
func pairedDeviceID(ctx context.Context, fingerprint string) (int64, bool) {
	rds := remote_device_svc.Default()
	if rds == nil {
		return 0, false
	}
	rows, err := rds.List(ctx)
	if err != nil {
		return 0, false
	}
	for _, row := range rows {
		if row != nil && row.DaemonFingerprint == fingerprint {
			return row.ID, true
		}
	}
	return 0, false
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
//
// 「谁跑这一轮谁说得出自己有什么」是这个方法的判据,于是它按执行目标落在哪台机器
// 上分成两条:
//
//   - **本机档**(DeviceFingerprint 空,或 R13 认领后等于本机指纹):本地枚举包,再
//     交给 agentskill.BuildCommands 合并。
//   - **远端档**:整份清单问那台机器要(skills.commands)。此前这里只问得到插件包
//     那一半,CLI 原生解析的 user / project / system skill 被整段跳过 —— 本机发现器
//     只看得见桌面端自己这台机器上的目录,拿它答远端档等于答错人,而界面上看不出
//     少了东西。
//
// 两条路的合并规则是同一份实现(agentskill.BuildCommands):本机在这里调,远端在那台
// 机器的 handler 里调。授权两条路都由这一侧给出 —— 组织架构库只在桌面端。
func (s *Service) ListAgentSkillCommands(ctx context.Context, agentID int64, cwd string) (SkillCommandCatalogDTO, error) {
	a, err := s.agent.Find(ctx, agentID)
	if err != nil || a == nil {
		return SkillCommandCatalogDTO{}, err
	}
	be, err := s.backend.Find(ctx, a.AgentBackendID)
	if err != nil || be == nil {
		return SkillCommandCatalogDTO{}, err
	}
	authorized, err := s.authorizedSkills(ctx, agentID)
	if err != nil {
		return SkillCommandCatalogDTO{}, err
	}

	commands, err := s.discoverCommands(ctx, be, strings.TrimSpace(cwd), authorized)
	if err != nil {
		return SkillCommandCatalogDTO{}, err
	}
	dto := make([]SkillCommandDTO, 0, len(commands))
	for _, command := range commands {
		dto = append(dto, SkillCommandDTO{Name: command.Name, Description: command.Description})
	}
	return SkillCommandCatalogDTO{Commands: dto}, nil
}

// discoverCommands 按执行目标落在哪台机器上,选出答这份清单的那一方。
func (s *Service) discoverCommands(
	ctx context.Context, be *agent_backend_entity.AgentBackend, cwd string,
	authorized []agent_entity.AgentSkillItem,
) ([]agentskill.SkillCommand, error) {
	if remote_device_svc.TargetsAnotherMachine(be.DeviceFingerprint) {
		deviceID, ok := pairedDeviceID(ctx, be.DeviceFingerprint)
		if !ok || s.remote == nil {
			// 那台机器没在本机配对过,没有可拨的对象。回空清单而不是错误:输入框
			// 照常能用,只是没有补全 —— 与 ListAgentSkillPacks 对同一情形的处置一致。
			return nil, nil
		}
		return s.remote.ListSkillCommands(ctx, deviceID, be.Type, cwd, authorized)
	}

	backendType := agent_backend_entity.BackendType(be.Type)
	var installed []agentskill.SkillPack
	if d, ok := agentskill.DiscovererFor(backendType); ok {
		packs, err := d.Discover(ctx, agentskill.DiscoverQuery{BackendType: backendType, CLIPath: be.CLIPath})
		if err != nil {
			return nil, err
		}
		installed = packs
	}
	return agentskill.BuildCommands(ctx, agentskill.CommandsQuery{
		BackendType: backendType,
		CLIPath:     be.CLIPath,
		Cwd:         cwd,
		Installed:   installed,
		Authorized:  authorized,
	})
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
	return agentskill.EnabledPluginsMap(authorized), nil
}
