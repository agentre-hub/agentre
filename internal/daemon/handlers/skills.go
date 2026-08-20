package handlers

import (
	"context"
	"strings"

	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"

	"github.com/agentre-ai/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-ai/agentre/internal/model/entity/agent_entity"
	"github.com/agentre-ai/agentre/internal/pkg/agentruntime/runtimes/remote/wire"
	"github.com/agentre-ai/agentre/internal/pkg/agentskill"
)

// SkillsListParams skills.list RPC 入参:desktop 只传 backend type;CLIPath 一般留空,
// 由 daemon 自己解析本机 CLI 路径(desktop 不知道 daemon 的 claude 在哪)。
type SkillsListParams struct {
	BackendType string `json:"backendType"`
	CLIPath     string `json:"cliPath,omitempty"`
}

// SkillsListResult skills.list RPC 出参:daemon 本机已装技能包。Packs 永远非 nil。
type SkillsListResult struct {
	Packs []agentskill.SkillPack `json:"packs"`
}

// SkillsHandlers 收纳 skills.* RPC。无依赖:发现器从 agentskill 全局注册表反查
// (daemon 启动时 blank import claudeskill/codexskill 触发 init 注册)。
type SkillsHandlers struct{}

// NewSkillsHandlers 构造 skills.* handler。
func NewSkillsHandlers() *SkillsHandlers { return &SkillsHandlers{} }

// List 在 daemon 本机枚举该 backend 已装技能包(= `claude plugin list --json`),供
// desktop 给远端 agent 配 per-agent 技能时展 daemon 真实可用集(而非 desktop 的)。
// 无对应发现器 → 空(向前兼容);CLIPath 缺省时解析 daemon 本机 CLI 路径。
func (h *SkillsHandlers) List(ctx context.Context, p SkillsListParams) (SkillsListResult, error) {
	bt := agent_backend_entity.BackendType(p.BackendType)
	d, ok := agentskill.DiscovererFor(bt)
	if !ok {
		return SkillsListResult{Packs: []agentskill.SkillPack{}}, nil
	}
	cliPath := strings.TrimSpace(p.CLIPath)
	if cliPath == "" {
		if path, found, err := resolveCLIPathFunc(p.BackendType); err == nil && found {
			cliPath = path
		}
	}
	packs, err := d.Discover(ctx, agentskill.DiscoverQuery{BackendType: bt, CLIPath: cliPath})
	if err != nil {
		return SkillsListResult{}, err
	}
	if packs == nil {
		packs = []agentskill.SkillPack{}
	}
	return SkillsListResult{Packs: packs}, nil
}

// Catalog 答 MethodSkillsCatalog:这台机器上某一档执行目标的技能目录 ——
// 本机已装包(含 CLI 全局启用态)并上 agentre 的推荐包,逐行标注调用方带来的授权。
//
// 它与 List 的分工:List 是原始发现结果(desktop 拿去自己合并),Catalog 是**画得出
// 界面的那一份**(浏览器直接照着渲染)。浏览器没有 desktop 那套本地 Discoverer,也
// 拿不到推荐表,少了这一层它就只能让用户手打 skill id。
//
// 授权来自请求而不是本机:执行目标与它的技能授权(R15e「一档一块」)存在组织架构库
// 里,agentred 上没有那个库。这不是妥协 —— 谁掌握那一档的授权谁说出来,合并规则则
// 与 desktop 共用 agentskill.MergeCatalog 那一份实现,两条路不会各自漂开。
//
// 三种 discovery 判别值必须分得干净(见 wire 的常量注释):**空目录绝不能冒充
// 「这台机器上没有技能」**。答不出时回 nil error 而不是 RPC 错误,是因为规格要求
// 「列不出可添加的包,已授权的仍可移除」—— 整块报错会把已授权的那半边一起打掉。
func (h *SkillsHandlers) Catalog(ctx context.Context, p wire.SkillCatalogParams) (wire.SkillCatalogResult, error) {
	empty := func(discovery string) wire.SkillCatalogResult {
		return wire.SkillCatalogResult{Packs: []wire.SkillPackSummary{}, Discovery: discovery}
	}

	bt := agent_backend_entity.BackendType(p.BackendType)
	d, ok := agentskill.DiscovererFor(bt)
	if !ok {
		return empty(wire.SkillDiscoveryUnsupported), nil
	}

	cliPath := strings.TrimSpace(p.CLIPath)
	if cliPath == "" {
		path, found, err := resolveCLIPathFunc(p.BackendType)
		if err != nil || !found {
			// CLI 不在这台机器上:装了什么包无从谈起,但这是「问不出来」而不是「没有」。
			logger.Ctx(ctx).Warn("handlers.SkillsHandlers.Catalog: cli not resolved",
				zap.String("backendType", p.BackendType), zap.Error(err))
			return empty(wire.SkillDiscoveryUnavailable), nil
		}
		cliPath = path
	}

	installed, err := d.Discover(ctx, agentskill.DiscoverQuery{BackendType: bt, CLIPath: cliPath})
	if err != nil {
		logger.Ctx(ctx).Warn("handlers.SkillsHandlers.Catalog: discover failed",
			zap.String("backendType", p.BackendType), zap.Error(err))
		return empty(wire.SkillDiscoveryUnavailable), nil
	}

	authorized := make([]agent_entity.AgentSkillItem, 0, len(p.Authorized))
	for _, a := range p.Authorized {
		authorized = append(authorized, agent_entity.AgentSkillItem{ID: a.ID, Enabled: a.Enabled})
	}

	entries := agentskill.MergeCatalog(agentskill.RecommendedFor(bt), installed, authorized)
	packs := make([]wire.SkillPackSummary, 0, len(entries))
	for _, e := range entries {
		packs = append(packs, wire.SkillPackSummary{
			ID:              e.Pack.ID,
			Name:            e.Pack.Name,
			Description:     e.Pack.Description,
			Skills:          e.Pack.Skills,
			Installed:       e.Pack.Installed,
			Enabled:         e.Enabled,
			GloballyEnabled: e.Pack.GloballyEnabled,
		})
	}
	return wire.SkillCatalogResult{Packs: packs, Discovery: wire.SkillDiscoveryOK}, nil
}
