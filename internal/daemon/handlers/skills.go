package handlers

import (
	"context"
	"strings"

	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/agent_entity"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/runtimes/remote/wire"
	"github.com/agentre-hub/agentre/internal/pkg/agentskill"
	"github.com/agentre-hub/agentre/pkg/wire/agentrewire"
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
func (h *SkillsHandlers) Catalog(ctx context.Context, req *agentrewire.SkillCatalogRequest) (*agentrewire.SkillCatalogResponse, error) {
	empty := func(discovery string) *agentrewire.SkillCatalogResponse {
		return &agentrewire.SkillCatalogResponse{Packs: []*agentrewire.SkillPackSummary{}, Discovery: discovery}
	}

	backendType := req.GetBackendType()
	bt := agent_backend_entity.BackendType(backendType)
	installed, _, discovery := h.discoverInstalled(ctx, backendType, req.GetCliPath())
	if discovery != wire.SkillDiscoveryOK {
		return empty(discovery), nil
	}

	authorized := make([]agent_entity.AgentSkillItem, 0, len(req.GetAuthorized()))
	for _, a := range req.GetAuthorized() {
		authorized = append(authorized, agent_entity.AgentSkillItem{ID: a.GetId(), Enabled: a.GetEnabled()})
	}

	entries := agentskill.MergeCatalog(agentskill.RecommendedFor(bt), installed, authorized)
	packs := make([]*agentrewire.SkillPackSummary, 0, len(entries))
	for _, e := range entries {
		packs = append(packs, &agentrewire.SkillPackSummary{
			Id:              e.Pack.ID,
			Name:            e.Pack.Name,
			Description:     e.Pack.Description,
			Skills:          e.Pack.Skills,
			Installed:       e.Pack.Installed,
			Enabled:         e.Enabled,
			GloballyEnabled: e.Pack.GloballyEnabled,
		})
	}
	return &agentrewire.SkillCatalogResponse{Packs: packs, Discovery: wire.SkillDiscoveryOK}, nil
}

// Commands 答 MethodSkillsCommands:这台机器上某一档执行目标此刻叫得动的 skill 名字。
//
// 它与 Catalog 的分工不是粒度而是**用途**:Catalog 答可配置的 plugin 包(组织架构页
// 拿它画授权表),Commands 答输入框里打得出来的名字 —— 后者还含 CLI 自己解析的
// user / project / system skill,那一半不是包、配不了,却恰恰是日常打得最多的。
//
// 合并规则不在这里,在 agentskill.BuildCommands:桌面端对本机档走同一个函数,两条路
// 不会各自漂开(与 Catalog 共用 MergeCatalog 是同一个道理)。
//
// 三态判别与 Catalog 逐字相同,且**答不出时回 nil error**:输入框仍要能用,只是没有
// 补全 —— 整块报错会把用户正在打的那句话一起打掉。
func (h *SkillsHandlers) Commands(ctx context.Context, p wire.SkillCommandsParams) (wire.SkillCommandsResult, error) {
	empty := func(discovery string) wire.SkillCommandsResult {
		return wire.SkillCommandsResult{Commands: []wire.SkillCommand{}, Discovery: discovery}
	}

	bt := agent_backend_entity.BackendType(p.BackendType)
	installed, cliPath, discovery := h.discoverInstalled(ctx, p.BackendType, p.CLIPath)
	if discovery != wire.SkillDiscoveryOK {
		return empty(discovery), nil
	}

	authorized := make([]agent_entity.AgentSkillItem, 0, len(p.Authorized))
	for _, a := range p.Authorized {
		authorized = append(authorized, agent_entity.AgentSkillItem{ID: a.ID, Enabled: a.Enabled})
	}

	commands, err := agentskill.BuildCommands(ctx, agentskill.CommandsQuery{
		BackendType: bt,
		CLIPath:     cliPath,
		Cwd:         p.Cwd,
		Installed:   installed,
		Authorized:  authorized,
	})
	if err != nil {
		// 原生那一半没问出来。半份清单比没有清单更糟:菜单里少掉的那些 skill 看起来
		// 就像不存在,用户没有任何办法发现缺了什么。
		logger.Ctx(ctx).Warn("handlers.SkillsHandlers.Commands: discover commands failed",
			zap.String("backendType", p.BackendType), zap.Error(err))
		return empty(wire.SkillDiscoveryUnavailable), nil
	}

	out := make([]wire.SkillCommand, 0, len(commands))
	for _, c := range commands {
		out = append(out, wire.SkillCommand{Name: c.Name, Description: c.Description})
	}
	return wire.SkillCommandsResult{Commands: out, Discovery: wire.SkillDiscoveryOK}, nil
}

// discoverInstalled 是 Catalog 与 Commands 共同的前半段:这台机器上装了哪些包。
//
// 回的 discovery 不是 OK 时,前两个返回值无意义 —— 调用方按自己的空壳回话。把这段
// 抽出来是因为**三态的判法必须只有一份**:哪一步失败算「答不出」、哪一步算「不支持」,
// 两个方法各写一遍迟早会漂开,而漂开的方向恰恰是最危险的那个(把问不出来当成没有)。
func (h *SkillsHandlers) discoverInstalled(
	ctx context.Context, backendType, requestedCLIPath string,
) (packs []agentskill.SkillPack, cliPath, discovery string) {
	bt := agent_backend_entity.BackendType(backendType)
	d, ok := agentskill.DiscovererFor(bt)
	if !ok {
		return nil, "", wire.SkillDiscoveryUnsupported
	}

	// 调用方指名了就用那一个:同一台机器上可以装着好几个 CLI,「这一档用哪个」是
	// 调用方的事实。没指名才由这台机器自己解析(调用方不知道对面的 claude 在哪)。
	path := strings.TrimSpace(requestedCLIPath)
	if path == "" {
		resolved, found, err := resolveCLIPathFunc(backendType)
		if err != nil || !found {
			// CLI 不在这台机器上:装了什么包无从谈起,但这是「问不出来」而不是「没有」。
			logger.Ctx(ctx).Warn("handlers.SkillsHandlers: cli not resolved",
				zap.String("backendType", backendType), zap.Error(err))
			return nil, "", wire.SkillDiscoveryUnavailable
		}
		path = resolved
	}

	installed, err := d.Discover(ctx, agentskill.DiscoverQuery{BackendType: bt, CLIPath: path})
	if err != nil {
		logger.Ctx(ctx).Warn("handlers.SkillsHandlers: discover failed",
			zap.String("backendType", backendType), zap.Error(err))
		return nil, "", wire.SkillDiscoveryUnavailable
	}
	return installed, path, wire.SkillDiscoveryOK
}
