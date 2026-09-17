package agent_backend_entity

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/agentre-hub/agentre/pkg/syncwire"
)

// 十二个**单类型独占**设置落在 config_json 这一列，形状就是同步契约的
// syncwire.AgentBackendConfig —— 键表只在契约里定义一份，本机的列、同步载荷的
// config 对象与 web API 是同一组 camelCase 键。
//
// 为什么它们合成一列而不是各占一列：agent_backends 是所有后端类型共用的一张表，
// 而这些格里没有一格被两种类型同时认领 —— model_routes / default_permission_mode /
// default_model 只有 claudecode 认，sandbox / approval 只有 codex 认，四个 openclaw_*
// 只有 openclaw 认，hermes_* 只有 hermes 认。其余类型的行上它们恒为空串，
// 且 kinds.go 的 ValidateExtra 会逐条拒绝写入。列的形态因此表达不出任何约束，只是把
// 「谁认识哪些字段」这件事在 schema 里又抄了一遍 —— 而那件事的真相源是 BackendKind。
//
// 代价是这些格不能再当查询条件（JSON 列上没有索引）。这不损失任何东西：全仓没有
// 一处按它们查询或排序，它们只在「取出这条后端 → 发起一轮」这一条路上被读。

// emptyModelRoutes 是 model_routes 这一格的「没配」形态。历史列带
// NOT NULL DEFAULT '{}'，读出来永远是 "{}" 而不是空串，消费侧（isEmptyJSONObject /
// ParseModelRoutes）也按这个形状写的 —— SetConfig 因此把缺键还原成 "{}"
// 而不是 ""，让换存储这件事对上层完全不可见。
const emptyModelRoutes = "{}"

// Config 把 Go 字段上的独占设置收成同步契约的形状。空路由不留键：否则每一行
// 非 claudecode 后端都平白带一个 {"modelRoutes":{}}。
func (b *AgentBackend) Config() syncwire.AgentBackendConfig {
	cfg := syncwire.AgentBackendConfig{
		Sandbox:               b.Sandbox,
		Approval:              b.Approval,
		DefaultPermissionMode: b.DefaultPermissionMode,
		DefaultModel:          b.DefaultModel,
		OpenClawGatewayURL:    b.OpenClawGatewayURL,
		OpenClawAgentID:       b.OpenClawAgentID,
		OpenClawDefaultModel:  b.OpenClawDefaultModel,
		OpenClawSessionMode:   b.OpenClawSessionMode,
		HermesURL:             b.HermesURL,
		HermesAuthProvider:    b.HermesAuthProvider,
		HermesUserID:          b.HermesUserID,
	}
	if routes := strings.TrimSpace(b.ModelRoutes); !isEmptyJSONObject(routes) {
		cfg.ModelRoutes = json.RawMessage(routes)
	}
	return cfg
}

// SetConfig 用 cfg **整体替换**全部独占设置：cfg 里缺席的键一律变空，不沿用旧值。
func (b *AgentBackend) SetConfig(cfg syncwire.AgentBackendConfig) {
	b.ModelRoutes = emptyModelRoutes
	if len(cfg.ModelRoutes) > 0 {
		b.ModelRoutes = string(cfg.ModelRoutes)
	}
	b.Sandbox = cfg.Sandbox
	b.Approval = cfg.Approval
	b.DefaultPermissionMode = cfg.DefaultPermissionMode
	b.DefaultModel = cfg.DefaultModel
	b.OpenClawGatewayURL = cfg.OpenClawGatewayURL
	b.OpenClawAgentID = cfg.OpenClawAgentID
	b.OpenClawDefaultModel = cfg.OpenClawDefaultModel
	b.OpenClawSessionMode = cfg.OpenClawSessionMode
	b.HermesURL = cfg.HermesURL
	b.HermesAuthProvider = cfg.HermesAuthProvider
	b.HermesUserID = cfg.HermesUserID
}

// MarshalConfig 把独占字段收进 b.ConfigJSON。写库前调用（见
// agent_backend_repo 的 Create / Update）。
//
// 全空时给出 "{}" 而不是 "null"：列是 NOT NULL DEFAULT '{}'，而 "null" 会让下一次
// UnmarshalConfig 解出一个 nil 配置。
func (b *AgentBackend) MarshalConfig() error {
	if b == nil {
		return nil
	}
	// json.Marshal 会校验 RawMessage 的内容，坏路由在这里就报出来而不是写进库。
	out, err := json.Marshal(b.Config())
	if err != nil {
		return fmt.Errorf("marshal agent backend config: %w", err)
	}
	b.ConfigJSON = string(out)
	return nil
}

// UnmarshalConfig 把 b.ConfigJSON 摊回独占字段。读库后调用（见
// agent_backend_repo 的 hydrateConfig）；绕过仓储裸读行的路径（同步上行的
// FindRow）同样要调它，否则拿到的是一排零值。
//
// 坏 JSON 报错而不是静默清零：这些格的零值都是**合法取值**（空 sandbox = 走 CLI
// 默认，空网关地址 = 还没配），清零之后没有任何一处会觉得不对。
func (b *AgentBackend) UnmarshalConfig() error {
	if b == nil {
		return nil
	}
	var cfg syncwire.AgentBackendConfig
	if raw := strings.TrimSpace(b.ConfigJSON); raw != "" && raw != emptyModelRoutes {
		if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
			return fmt.Errorf("unmarshal agent backend config: %w", err)
		}
	}
	b.SetConfig(cfg)
	return nil
}
