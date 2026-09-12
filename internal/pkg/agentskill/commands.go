package agentskill

import (
	"context"
	"strings"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/agent_entity"
)

// CommandsQuery 是「合成一份可调用 skill 命令清单」要的全部输入。
//
// Installed 与 Authorized 分别由**两个不同的人**掌握,这是这个结构的形状由来:
// 已装包只有那台机器答得出(枚举 CLI),而授权(R15e「一档一块」)存在组织架构库里,
// 执行端没有那个库。所以这个函数两样都收,谁掌握谁带进来。
type CommandsQuery struct {
	BackendType agent_backend_entity.BackendType
	// CLIPath 定位这台机器上的那个 CLI(空 = 默认 binary)。
	CLIPath string
	// Cwd 是这一轮的工作目录:项目级 skill(`<cwd>/.claude/skills`)要靠它才解析得出。
	Cwd string
	// Installed 是这台机器上已装的包(枚举结果)。
	Installed []SkillPack
	// Authorized 是这一档执行目标对各个包的显式授权。
	Authorized []agent_entity.AgentSkillItem
}

// BuildCommands 把「已装包 + 这一档的授权 + 本机 CLI 自己解析的 skill」合成一份
// 可调用命令清单。名字不带输入前缀(Codex 是 `$`,Claude Code / Pi 是 `/`)——
// 前缀是输入法层面的事,由前端按 backend 加。
//
// 它住在 agentskill 而不是 skill_svc,判据与隔壁 MergeCatalog 逐字相同:**三个
// 调用方要答同一份清单** —— 桌面端对本机档(拿得到组织架构库)、agentred 经
// skills.commands RPC 对远端档(拿不到库,授权由调用方带上)、以及浏览器控制台经
// 中继问同一个 RPC。三边各写一份合并就是三份会各自漂开的真相。
//
// 顺序是有意义的:包里的命令在前、CLI 原生解析的在后,两边重名时**保留先出现的
// 那条**——包里那条带得出包的描述,原生那条往往只有一个裸名字。
func BuildCommands(ctx context.Context, q CommandsQuery) ([]SkillCommand, error) {
	commands := make([]SkillCommand, 0)
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
		commands = append(commands, SkillCommand{
			Name:        name,
			Description: strings.TrimSpace(description),
		})
	}

	entries := MergeCatalog(RecommendedFor(q.BackendType), q.Installed, q.Authorized)
	for _, entry := range entries {
		// 生效态才出命令:没装的推荐包、以及这一档强制关掉的包,CLI 那一轮根本
		// 挂不上去,列出来就是一条按下去会报「没有这个 skill」的命令。
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
			// 包内 skill 的调用名是 `包名:skill`;已经带冒号的说明发现器给的就是
			// 全名(Pi 的包会这样),不再冠一次。
			if !strings.Contains(skill, ":") && strings.TrimSpace(pack.Name) != "" {
				name = strings.TrimSpace(pack.Name) + ":" + skill
			}
			appendCommand(name, pack.Description)
		}
	}

	// 原生一半:CLI 自己解析的 user / project / system skill。没有发现器是**稳定
	// 答案**(这种 backend 就没有这一档),不是失败;真失败要往上抛 —— 拿半份清单
	// 冒充答案的话,用户会以为那些 skill 不存在。
	if discoverer, ok := CommandDiscovererFor(q.BackendType); ok {
		native, err := discoverer.DiscoverCommands(ctx, CommandDiscoverQuery{
			BackendType:    q.BackendType,
			CLIPath:        q.CLIPath,
			Cwd:            strings.TrimSpace(q.Cwd),
			EnabledPlugins: EnabledPluginsMap(q.Authorized),
		})
		if err != nil {
			return nil, err
		}
		for _, command := range native {
			appendCommand(command.Name, command.Description)
		}
	}

	return commands, nil
}

// EnabledPluginsMap 把授权集摊成 CLI 那一侧要的 id → 开关表。CLI 靠它决定这一轮
// 把哪些 plugin 挂上去,所以发现命令与真正起轮次时必须读同一份映射。
func EnabledPluginsMap(items []agent_entity.AgentSkillItem) map[string]bool {
	out := make(map[string]bool, len(items))
	for _, item := range items {
		out[item.ID] = item.Enabled
	}
	return out
}
