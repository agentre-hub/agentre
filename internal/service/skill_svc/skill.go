package skill_svc

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strconv"
	"strings"

	"github.com/cago-frame/cago/pkg/i18n"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/agent_entity"
	"github.com/agentre-hub/agentre/internal/pkg/agentskill"
	"github.com/agentre-hub/agentre/internal/pkg/code"
)

// Service 技能包组合服务。依赖通过消费者侧窄接口注入(DIP)。
//
// 技能授权挂在成员上、按生效引擎配置的运行时校验(spec 2026-11-25 决策 9);目录从一台
// 能运行该引擎配置的在线设备读取,本机优先;授权开着的包在某台能跑的设备上没装时照常
// 派过去,只在目录里标出那台设备。
type Service struct {
	agent     AgentLookup
	engine    EngineLookup
	placement Placement
	cliPaths  CLIPaths
	remote    RemoteDiscoverer // agentred 上的包走 daemon 发现

	// cache 记忆发现结果:每次发现都要起 CLI 子进程(~3s),composer 每次挂载都会问。
	cache *agentskill.DiscoveryCache
	// localStamp 给本机条目取插件注册文件指纹;nil = agentskill.RegistryStamp。
	localStamp func() string
}

// host 是这一次发现由哪台机器回答:本机(带本机 CLI 路径覆盖)或一台已配对的 agentred。
type host struct {
	runtime  string
	remote   bool
	deviceID int64  // remote 时为本机配对表行 ID
	cliPath  string // 本机时的 CLI 路径覆盖
	device   Device
}

// scope 缓存作用域前缀「机器|运行时|」:手动刷新按它清掉同一机器 + 运行时的全部条目
// (packs 与各 cwd 的 commands)。远端按桌面端 paired 行 ID 区分设备。
func (h *host) scope() string {
	if h.remote {
		return "dev" + strconv.FormatInt(h.deviceID, 10) + "|" + h.runtime + "|"
	}
	return "local|" + h.runtime + "|"
}

// stampFor 本机条目带插件注册文件指纹(mtime 变化即失效);远端观测不到文件,只靠 TTL。
func (s *Service) stampFor(remote bool) string {
	if remote {
		return ""
	}
	if s.localStamp != nil {
		return s.localStamp()
	}
	return agentskill.RegistryStamp()
}

// authKey 把授权列表摊成稳定短摘要(进缓存 key:远端命令清单依赖它)。
func authKey(authorized []agent_entity.AgentSkillItem) string {
	b, _ := json.Marshal(authorized)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:8])
}

// member 成员与它生效的引擎配置。成员不存在回 (nil, nil, nil);没有生效配置报需要引擎配置。
func (s *Service) member(ctx context.Context, agentID int64) (*agent_entity.Agent, *agent_backend_entity.EngineProfile, error) {
	a, err := s.agent.Find(ctx, agentID)
	if err != nil || a == nil {
		return nil, nil, err
	}
	p, err := s.engine.Effective(ctx, agentID)
	if err != nil {
		return nil, nil, err
	}
	if p == nil {
		return nil, nil, i18n.NewError(ctx, code.EngineProfileRequired)
	}
	return a, p, nil
}

// pickHost 选回答这次发现的机器:本机能跑就是本机;否则按派发顺序第一台能跑的已配对
// agentred;都没有回 nil(不发现)。别的桌面端没有可借的通道,不选。
func (s *Service) pickHost(ctx context.Context, runtime string, devices []Device) (*host, error) {
	for _, d := range devices {
		if d.IsSelf && d.CanRun {
			cliPath, err := s.cliPaths.LocalCLIPath(ctx, runtime)
			if err != nil {
				return nil, err
			}
			return &host{runtime: runtime, cliPath: cliPath, device: d}, nil
		}
	}
	for _, d := range devices {
		if d.CanRun && !d.IsSelf && d.PairedID > 0 && s.remote != nil {
			return &host{runtime: runtime, remote: true, deviceID: d.PairedID, device: d}, nil
		}
	}
	return nil, nil
}

// discoverPacks 那台机器上已装的包;h 为 nil 或运行时没有发现器时为空。
func (s *Service) discoverPacks(ctx context.Context, h *host, refresh bool) ([]agentskill.SkillPack, error) {
	if h == nil {
		return []agentskill.SkillPack{}, nil
	}
	if h.remote {
		packs, err := agentskill.CachedDiscovery(s.cache, h.scope()+"packs", s.stampFor(true), refresh,
			func() ([]agentskill.SkillPack, error) { return s.remote.ListSkills(ctx, h.deviceID, h.runtime) })
		if packs == nil && err == nil {
			packs = []agentskill.SkillPack{}
		}
		return packs, err
	}
	d, ok := agentskill.DiscovererFor(agent_backend_entity.BackendType(h.runtime))
	if !ok {
		return []agentskill.SkillPack{}, nil
	}
	return s.localPacks(ctx, d, h, refresh)
}

// localPacks 本机已装包,按(运行时、CLI 路径)缓存。
func (s *Service) localPacks(ctx context.Context, d agentskill.Discoverer, h *host, refresh bool) ([]agentskill.SkillPack, error) {
	return agentskill.CachedDiscovery(s.cache, h.scope()+"packs|"+h.cliPath, s.stampFor(false), refresh,
		func() ([]agentskill.SkillPack, error) {
			return d.Discover(ctx, agentskill.DiscoverQuery{CLIPath: h.cliPath})
		})
}

// catalogOf 把「发现到的包 + 这份授权」合并成目录 DTO。
func catalogOf(runtime string, packs []agentskill.SkillPack, authorized []agent_entity.AgentSkillItem) []SkillPackDTO {
	entries := agentskill.MergeCatalog(agentskill.RecommendedFor(agent_backend_entity.BackendType(runtime)), packs, authorized)
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
	return dto
}

// MemberSkillCatalog 成员详情「技能」一块:合并推荐 + 发现 + 授权,并标出失效授权与缺包
// 的设备。成员不存在返回空目录、不是错误(前端还没保存完就切换了)。
func (s *Service) MemberSkillCatalog(ctx context.Context, agentID int64, refresh bool) (MemberSkillCatalogDTO, error) {
	a, p, err := s.member(ctx, agentID)
	if err != nil || a == nil {
		return MemberSkillCatalogDTO{Packs: []SkillPackDTO{}}, err
	}
	valid, invalid := a.SkillGrantsFor(p.Type)
	out := MemberSkillCatalogDTO{Runtime: p.Type, Packs: []SkillPackDTO{}, InvalidGrants: invalid}
	if _, ok := agentskill.DiscovererFor(agent_backend_entity.BackendType(p.Type)); !ok {
		return out, nil
	}
	out.Supported = true
	devices, err := s.placement.MemberDevices(ctx, agentID)
	if err != nil {
		return MemberSkillCatalogDTO{}, err
	}
	h, err := s.pickHost(ctx, p.Type, devices)
	if err != nil {
		return MemberSkillCatalogDTO{}, err
	}
	if h != nil {
		out.SourceDevice, out.SourceDeviceName = h.device.Fingerprint, h.device.Name
		if refresh {
			// 手动刷新:同一机器 + 运行时的 commands 清单也一并作废,免得 composer 还拿旧的。
			s.cache.InvalidatePrefix(h.scope())
		}
	}
	packs, err := s.discoverPacks(ctx, h, refresh)
	if err != nil {
		return MemberSkillCatalogDTO{}, err
	}
	out.Packs = catalogOf(p.Type, packs, valid)
	out.MissingPacks = missingPacks(valid, devices)
	return out, nil
}

// missingPacks 授权开着的包在哪些能跑这名成员、且上报了技能包的设备上没装。没上报的设备
// 不下结论;跑不了的设备不派过去,也不提示。
func missingPacks(granted []agent_entity.AgentSkillItem, devices []Device) []MissingPackDTO {
	var out []MissingPackDTO
	for _, g := range granted {
		if !g.Enabled {
			continue
		}
		var missing []DeviceRefDTO
		for _, d := range devices {
			if !d.CanRun || !d.PacksKnown || contains(d.InstalledPacks, g.ID) {
				continue
			}
			missing = append(missing, DeviceRefDTO{Fingerprint: d.Fingerprint, Name: d.Name, IsSelf: d.IsSelf})
		}
		if len(missing) > 0 {
			out = append(out, MissingPackDTO{PackID: g.ID, Devices: missing})
		}
	}
	return out
}

func contains(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}

// ListAgentSkillCommands 返回成员在 cwd 中可调用的 Skill 命令。
//
// 「谁跑这一轮谁说得出自己有什么」是这个方法的判据:回答的机器与目录同一选法(本机优先,
// 否则第一台能跑的已配对 agentred)。
//
//   - **本机**:本地枚举包,再交给 agentskill.BuildCommands 合并。
//   - **agentred**:整份清单问那台机器要(skills.commands),插件包与 CLI 原生解析的
//     user / project / system skill 都在里面 —— 本机发现器只看得见桌面端自己这台机器上
//     的目录,拿它答远端等于答错人。
//
// 两条路的合并规则是同一份实现(agentskill.BuildCommands):本机在这里调,远端在那台
// 机器的 handler 里调。授权两条路都由这一侧给出 —— 组织架构库只在桌面端。
func (s *Service) ListAgentSkillCommands(ctx context.Context, agentID int64, cwd string) (SkillCommandCatalogDTO, error) {
	a, p, err := s.member(ctx, agentID)
	if err != nil || a == nil {
		return SkillCommandCatalogDTO{}, err
	}
	devices, err := s.placement.MemberDevices(ctx, agentID)
	if err != nil {
		return SkillCommandCatalogDTO{}, err
	}
	h, err := s.pickHost(ctx, p.Type, devices)
	if err != nil || h == nil {
		// 没有能跑它的机器:回空清单而不是错误,输入框照常能用,只是没有补全。
		return SkillCommandCatalogDTO{}, err
	}
	authorized, _ := a.SkillGrantsFor(p.Type)
	commands, err := s.cachedCommands(ctx, h, strings.TrimSpace(cwd), authorized)
	if err != nil {
		return SkillCommandCatalogDTO{}, err
	}
	dto := make([]SkillCommandDTO, 0, len(commands))
	for _, command := range commands {
		dto = append(dto, SkillCommandDTO{Name: command.Name, Description: command.Description})
	}
	return SkillCommandCatalogDTO{Commands: dto}, nil
}

// cachedCommands 给 discoverCommands 套上缓存:key 含机器、运行时,本机再含 CLI 路径,
// 并都含 cwd 与授权摘要(项目级 skill 依赖 cwd;授权决定哪些包出命令)。
func (s *Service) cachedCommands(
	ctx context.Context, h *host, cwd string, authorized []agent_entity.AgentSkillItem,
) ([]agentskill.SkillCommand, error) {
	key := h.scope() + "cmds|"
	if !h.remote {
		key += h.cliPath + "|"
	}
	key += cwd + "|" + authKey(authorized)
	return agentskill.CachedDiscovery(s.cache, key, s.stampFor(h.remote), false,
		func() ([]agentskill.SkillCommand, error) { return s.discoverCommands(ctx, h, cwd, authorized) })
}

// discoverCommands 按回答的机器选出答这份清单的那一方。
func (s *Service) discoverCommands(
	ctx context.Context, h *host, cwd string, authorized []agent_entity.AgentSkillItem,
) ([]agentskill.SkillCommand, error) {
	if h.remote {
		return s.remote.ListSkillCommands(ctx, h.deviceID, h.runtime, cwd, authorized)
	}
	runtime := agent_backend_entity.BackendType(h.runtime)
	var installed []agentskill.SkillPack
	if d, ok := agentskill.DiscovererFor(runtime); ok {
		packs, err := s.localPacks(ctx, d, h, false)
		if err != nil {
			return nil, err
		}
		installed = packs
	}
	return agentskill.BuildCommands(ctx, agentskill.CommandsQuery{
		BackendType: runtime,
		CLIPath:     h.cliPath,
		Cwd:         cwd,
		Installed:   installed,
		Authorized:  authorized,
	})
}

// EnabledPluginsMapFor 一轮注入的技能包覆盖 map(仅显式授权):授权取成员上的那份,按这一
// 轮运行时校验 —— 授权时的运行时对不上就整份不发。runtimeType 为空时按成员生效的引擎配置
// 的运行时取。
func (s *Service) EnabledPluginsMapFor(ctx context.Context, a *agent_entity.Agent, runtimeType string) (map[string]bool, error) {
	if a == nil {
		return nil, nil
	}
	if runtimeType == "" {
		p, err := s.engine.Effective(ctx, a.ID)
		if err != nil || p == nil {
			return nil, err
		}
		runtimeType = p.Type
	}
	valid, _ := a.SkillGrantsFor(runtimeType)
	return agentskill.EnabledPluginsMap(valid), nil
}
