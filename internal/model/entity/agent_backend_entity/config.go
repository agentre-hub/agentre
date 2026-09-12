package agent_backend_entity

import (
	"encoding/json"
	"fmt"
	"strings"
)

// backendConfig 是九个**单类型独占**设置的持久化形态。
//
// 为什么它们合成一列而不是各占一列：agent_backends 是所有后端类型共用的一张表，
// 而这九格里没有一格被两种类型同时认领 —— model_routes / default_permission_mode /
// default_model 只有 claudecode 认，sandbox / approval 只有 codex 认，四个 openclaw_*
// 只有 openclaw 认。其余四种类型的行上它们恒为空串，且 kinds.go 的 ValidateExtra
// 会逐条拒绝写入。列的形态因此表达不出任何约束，只是把「谁认识哪些字段」这件事
// 在 schema 里又抄了一遍 —— 而那件事的真相源是 BackendKind。
//
// 代价是这九格不能再当查询条件（JSON 列上没有索引）。这不损失任何东西：全仓没有
// 一处按它们查询或排序，它们只在「取出这条后端 → 起子进程」这一条路上被读。
//
// 跨机同步与导入导出**不经过这里**：它们读写的是 AgentBackend 上那九个 Go 字段
// （adapter_org.go 的 model_routes / sandbox / … 与 data_svc 的 bundle），线格式
// 一个字节都没变。改的只是本机怎么存。
type backendConfig struct {
	// ModelRoutes 是嵌套的 JSON 对象而不是字符串：套一层字符串会把
	// `{"OPUS":{…}}` 转义成 `"{\"OPUS\":…}"`，人读不了，工具也进不去。
	ModelRoutes           json.RawMessage `json:"modelRoutes,omitempty"`
	Sandbox               string          `json:"sandbox,omitempty"`
	Approval              string          `json:"approval,omitempty"`
	DefaultPermissionMode string          `json:"defaultPermissionMode,omitempty"`
	DefaultModel          string          `json:"defaultModel,omitempty"`
	OpenClawGatewayURL    string          `json:"openclawGatewayUrl,omitempty"`
	OpenClawAgentID       string          `json:"openclawAgentId,omitempty"`
	OpenClawDefaultModel  string          `json:"openclawDefaultModel,omitempty"`
	OpenClawSessionMode   string          `json:"openclawSessionMode,omitempty"`
}

// emptyModelRoutes 是 model_routes 这一格的「没配」形态。历史列带
// NOT NULL DEFAULT '{}'，读出来永远是 "{}" 而不是空串，消费侧（isEmptyJSONObject /
// ParseModelRoutes）也按这个形状写的 —— UnmarshalConfig 因此把缺键还原成 "{}"
// 而不是 ""，让换存储这件事对上层完全不可见。
const emptyModelRoutes = "{}"

// MarshalConfig 把九个独占字段收进 b.ConfigJSON。写库前调用（见
// agent_backend_repo 的 Create / Update）。
//
// 全空时给出 "{}" 而不是 "null"：列是 NOT NULL DEFAULT '{}'，而 "null" 会让下一次
// UnmarshalConfig 解出一个 nil 配置。
func (b *AgentBackend) MarshalConfig() error {
	if b == nil {
		return nil
	}
	cfg := backendConfig{
		Sandbox:               b.Sandbox,
		Approval:              b.Approval,
		DefaultPermissionMode: b.DefaultPermissionMode,
		DefaultModel:          b.DefaultModel,
		OpenClawGatewayURL:    b.OpenClawGatewayURL,
		OpenClawAgentID:       b.OpenClawAgentID,
		OpenClawDefaultModel:  b.OpenClawDefaultModel,
		OpenClawSessionMode:   b.OpenClawSessionMode,
	}
	// 空路由不留键：否则每一行非 claudecode 后端都平白带一个 {"modelRoutes":{}}。
	if routes := strings.TrimSpace(b.ModelRoutes); !isEmptyJSONObject(routes) {
		cfg.ModelRoutes = json.RawMessage(routes)
	}
	// json.Marshal 会校验 RawMessage 的内容，坏路由在这里就报出来而不是写进库。
	out, err := json.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("marshal agent backend config: %w", err)
	}
	b.ConfigJSON = string(out)
	return nil
}

// UnmarshalConfig 把 b.ConfigJSON 摊回九个独占字段。读库后调用（见
// agent_backend_repo 的 hydrateConfig）。
//
// 坏 JSON 报错而不是静默清零：这九格的零值都是**合法取值**（空 sandbox = 走 CLI
// 默认，空网关地址 = 还没配），清零之后没有任何一处会觉得不对。
func (b *AgentBackend) UnmarshalConfig() error {
	if b == nil {
		return nil
	}
	b.ModelRoutes = emptyModelRoutes
	raw := strings.TrimSpace(b.ConfigJSON)
	if raw == "" || raw == emptyModelRoutes {
		return nil
	}
	var cfg backendConfig
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		return fmt.Errorf("unmarshal agent backend config: %w", err)
	}
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
	return nil
}
