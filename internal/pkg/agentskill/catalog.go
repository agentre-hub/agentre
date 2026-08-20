package agentskill

import (
	"github.com/agentre-ai/agentre/internal/model/entity/agent_entity"
)

// CatalogEntry 是技能目录里的一行:包本身 + 这一档执行目标对它的标注。
//
// Enabled 与 EffectiveEnabled 分开是刻意的:前者是用户在这一档上按下的那个开关
// (「有没有显式说过话」),后者是最终真的传给 CLI 的生效态。没显式说过话的行,
// 生效态继承 CLI 全局启用态 —— 界面上的三态「继承全局 / 强制开 / 强制关」正是
// 这两格的组合。
type CatalogEntry struct {
	Pack             SkillPack
	Enabled          bool
	EffectiveEnabled bool
}

// MergeCatalog 把「agentre 的推荐包 + 这台机器上已装的包 + 这一档的授权」合并成
// 一份目录:按 id 去重(已装的先入、推荐的后 OR 入 Recommended 旗标),逐行标注
// Enabled / EffectiveEnabled。
//
// 它住在 pkg 而不是 skill_svc,是因为**两边都要答同一份目录**:桌面端经
// skill_svc 答(拿得到组织架构库),执行端 agentred 经 skills.catalog RPC 答
// (拿不到库,授权由调用方随请求带上)。合并规则只能有一份实现,不然两条路会各自漂开。
//
// overrides 用 agent_entity.AgentSkillItem 而不是另造一个同形结构:那就是桌面端
// agent_exec_targets.skills_json 里存的东西,换个名字只会多一次没有意义的搬运。
func MergeCatalog(recommended, installed []SkillPack, overrides []agent_entity.AgentSkillItem) []CatalogEntry {
	overrideByID := make(map[string]bool, len(overrides))
	for _, override := range overrides {
		overrideByID[override.ID] = override.Enabled
	}

	byID := map[string]*CatalogEntry{}
	order := make([]string, 0, len(installed)+len(recommended))

	add := func(p SkillPack) {
		if ex, ok := byID[p.ID]; ok {
			if p.Recommended {
				ex.Pack.Recommended = true
			}
			if p.Installed {
				ex.Pack.Installed = true
				ex.Pack.Source = SourceInstalled
			}
			return
		}
		byID[p.ID] = &CatalogEntry{Pack: p}
		order = append(order, p.ID)
	}

	for _, p := range installed {
		add(p)
	}
	for _, p := range recommended {
		add(p)
	}

	out := make([]CatalogEntry, 0, len(order))
	for _, id := range order {
		e := byID[id]
		override, overridden := overrideByID[id]
		e.Enabled = overridden && override
		e.EffectiveEnabled = e.Pack.Installed && e.Pack.GloballyEnabled
		if overridden {
			e.EffectiveEnabled = e.Pack.Installed && override
		}
		out = append(out, *e)
	}
	return out
}
