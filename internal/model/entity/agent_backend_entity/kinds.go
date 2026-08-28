package agent_backend_entity

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/cago-frame/cago/pkg/i18n"

	"github.com/agentre-hub/agentre/internal/model/entity/llm_provider_entity"
	"github.com/agentre-hub/agentre/internal/pkg/code"
)

// BackendKind 把「不同 backend 类型的取值约束」做成可扩展集合。
//
// 新增一种 backend 类型时实现 BackendKind 并在 backendKinds 包级 var 里登记，
// agent_backend_entity.AgentBackend.Check 会自动按类型分派。**禁止 init() 副作用**——
// 静态 map 让测试可以在不重置全局状态的情况下并行跑。
type BackendKind interface {
	// Type 该 kind 对应的字符串类型常量。
	Type() BackendType

	// KnownAliases 列出本 kind 支持的 model_routes alias 键（claudecode = OPUS/SONNET/HAIKU；
	// codex 暂为空集，强制 routes == "{}"）。
	KnownAliases() []string

	// ProviderTypeMatch 判断 LLMProvider.type 是否与本 kind 严格匹配；alias provider 同集合。
	ProviderTypeMatch(t llm_provider_entity.ProviderType) bool

	// RequiresProviderModel 是否要求绑定的 LLMProvider.Model 非空。
	// piagent 绑定时必须能通过 --model agentre-<key>/<model> 命中模型（spec 决策 #3），
	// 其它 kind（builtin / claudecode / codex）不要求，Model 空时走 CLI 自身解析。
	RequiresProviderModel() bool

	// AllowsCLIPath 是否允许 cli_path 字段非空；builtin 不允许，claudecode/codex 允许。
	AllowsCLIPath() bool

	// ValidateExtra 对 sandbox / approval / env_json 等独有字段做校验。
	// 在公共校验（name / type / provider / model_routes alias 集合 / env_json 解析）通过之后调用。
	ValidateExtra(ctx context.Context, b *AgentBackend) error
}

// backendKinds 是包级静态注册表。新增 BackendKind 实现时在这里追加一行即可，
// 不要在 init() 里改。
var backendKinds = map[BackendType]BackendKind{
	TypeBuiltin:    builtinKind{},
	TypeClaudeCode: claudeCodeKind{},
	TypeCodex:      codexKind{},
	TypePiAgent:    piAgentKind{},
	TypeOpenClaw:   openClawKind{},
}

// KindFor 查表，找不到返 nil。Service 在 Test/Create/Update 前用它分派 Prober。
func KindFor(t BackendType) BackendKind {
	if k, ok := backendKinds[t]; ok {
		return k
	}
	return nil
}

// reservedEnvKeys 是 App 自己写入 ANTHROPIC_BASE_URL 等保留键的白名单。
// 用户在 env_json 里设置以下键将被 entity.Check 拒绝（AgentBackendReservedEnvKey）；
// 其它 ANTHROPIC_* / OPENAI_* 键（如 ANTHROPIC_LOG）可自由覆盖。
var reservedEnvKeys = map[string]struct{}{
	"AGENTRE_GATEWAY_URL":            {},
	"AGENTRE_GATEWAY_TOKEN":          {},
	"ANTHROPIC_BASE_URL":             {},
	"ANTHROPIC_API_KEY":              {},
	"ANTHROPIC_AUTH_TOKEN":           {},
	"ANTHROPIC_MODEL":                {},
	"ANTHROPIC_DEFAULT_OPUS_MODEL":   {},
	"ANTHROPIC_DEFAULT_SONNET_MODEL": {},
	"ANTHROPIC_DEFAULT_HAIKU_MODEL":  {},
	"OPENAI_API_KEY":                 {},
	"OPENAI_BASE_URL":                {},
	"OPENAI_API_BASE":                {},
	"PI_OFFLINE":                     {},
	"PI_CODING_AGENT_DIR":            {},
	"PI_CODING_AGENT_SESSION_DIR":    {},
}

// IsReservedEnvKey 提供给 service / 前端预校验。
func IsReservedEnvKey(key string) bool {
	_, ok := reservedEnvKeys[key]
	return ok
}

// builtinKind builtin 不接受新列，所有四项必须保持默认空值。
type builtinKind struct{}

func (builtinKind) Type() BackendType                                         { return TypeBuiltin }
func (builtinKind) KnownAliases() []string                                    { return nil }
func (builtinKind) ProviderTypeMatch(t llm_provider_entity.ProviderType) bool { return true }
func (builtinKind) RequiresProviderModel() bool                               { return false }
func (builtinKind) AllowsCLIPath() bool                                       { return false }

func (builtinKind) ValidateExtra(ctx context.Context, b *AgentBackend) error {
	if strings.TrimSpace(b.LLMProviderKey) == "" {
		return i18n.NewError(ctx, code.AgentBackendLLMProviderRequired)
	}
	if strings.TrimSpace(b.CLIPath) != "" {
		return i18n.NewError(ctx, code.AgentBackendCLIPathNotAllowed)
	}
	// 新增列对 builtin 无意义；非默认值即报错。
	if !isEmptyJSONObject(b.ModelRoutes) ||
		strings.TrimSpace(b.Sandbox) != "" ||
		strings.TrimSpace(b.Approval) != "" ||
		!isEmptyJSONObject(b.EnvJSON) ||
		strings.TrimSpace(b.DefaultPermissionMode) != "" ||
		strings.TrimSpace(b.DefaultModel) != "" {
		return i18n.NewError(ctx, code.InvalidParameter)
	}
	return nil
}

// claudeCodeKind 走 claude CLI，匹配 anthropic provider，支持 OPUS/SONNET/HAIKU 三级路由。
type claudeCodeKind struct{}

func (claudeCodeKind) Type() BackendType      { return TypeClaudeCode }
func (claudeCodeKind) KnownAliases() []string { return []string{"OPUS", "SONNET", "HAIKU"} }
func (claudeCodeKind) ProviderTypeMatch(t llm_provider_entity.ProviderType) bool {
	return t == llm_provider_entity.TypeAnthropic
}
func (claudeCodeKind) RequiresProviderModel() bool { return false }
func (claudeCodeKind) AllowsCLIPath() bool         { return true }

func (claudeCodeKind) ValidateExtra(ctx context.Context, b *AgentBackend) error {
	// LLMProviderKey == "" 表示不关联供应商，走 claude CLI 自身的登录态（claude login）。
	if strings.TrimSpace(b.Sandbox) != "" || strings.TrimSpace(b.Approval) != "" {
		return i18n.NewError(ctx, code.InvalidParameter)
	}
	if mode := strings.TrimSpace(b.DefaultPermissionMode); mode != "" && !IsValidPermissionMode(mode) {
		return i18n.NewError(ctx, code.InvalidParameter)
	}
	return nil
}

// codexKind 走 codex CLI，严格匹配 openai-response。
// model_routes 必须为空（codex 没有 tier 概念）。
type codexKind struct{}

func (codexKind) Type() BackendType      { return TypeCodex }
func (codexKind) KnownAliases() []string { return nil }
func (codexKind) ProviderTypeMatch(t llm_provider_entity.ProviderType) bool {
	return t == llm_provider_entity.TypeOpenAIResponse
}
func (codexKind) RequiresProviderModel() bool { return false }
func (codexKind) AllowsCLIPath() bool         { return true }

func (codexKind) ValidateExtra(ctx context.Context, b *AgentBackend) error {
	// LLMProviderKey == "" 表示不关联供应商，走 codex CLI 自身的登录态（codex login）。
	if !isEmptyJSONObject(b.ModelRoutes) {
		return i18n.NewError(ctx, code.AgentBackendUnknownAlias)
	}
	if err := validateSandbox(ctx, b.Sandbox); err != nil {
		return err
	}
	if err := validateApproval(ctx, b.Approval); err != nil {
		return err
	}
	// default_permission_mode / default_model 都是 claudecode 专属字段；codex 自有
	// sandbox/approval 通道与自己的模型解析，不复用这两个字段。
	if strings.TrimSpace(b.DefaultPermissionMode) != "" ||
		strings.TrimSpace(b.DefaultModel) != "" {
		return i18n.NewError(ctx, code.InvalidParameter)
	}
	return nil
}

// piAgentKind 走 Pi coding agent RPC mode。
// 可绑定 Agentre 自定义 LLM 供应商：三类 Type（anthropic / openai-chat / openai-response）全收，
// 对应 Pi 原生 API 形状 anthropic-messages / openai-completions / openai-responses。
// 未绑定供应商时 Pi 自己读取 ~/.pi/agent 的模型与认证配置，Agentre 不干预。
type piAgentKind struct{}

func (piAgentKind) Type() BackendType      { return TypePiAgent }
func (piAgentKind) KnownAliases() []string { return nil }
func (piAgentKind) ProviderTypeMatch(t llm_provider_entity.ProviderType) bool {
	return t == llm_provider_entity.TypeAnthropic ||
		t == llm_provider_entity.TypeOpenAIChat ||
		t == llm_provider_entity.TypeOpenAIResponse
}
func (piAgentKind) RequiresProviderModel() bool { return true }
func (piAgentKind) AllowsCLIPath() bool         { return true }

func (piAgentKind) ValidateExtra(ctx context.Context, b *AgentBackend) error {
	// LLMProviderKey 非空 → 放行（本功能核心：piagent 可绑定自定义供应商）。
	// ModelRoutes / Sandbox / Approval / DefaultPermissionMode / DefaultModel 对 piagent 无意义，
	// 非默认值即报错（沿用 InvalidParameter 风格）。
	if !isEmptyJSONObject(b.ModelRoutes) ||
		strings.TrimSpace(b.Sandbox) != "" ||
		strings.TrimSpace(b.Approval) != "" ||
		strings.TrimSpace(b.DefaultPermissionMode) != "" ||
		strings.TrimSpace(b.DefaultModel) != "" {
		return i18n.NewError(ctx, code.InvalidParameter)
	}
	return nil
}

// openClawKind 仅保存 Gateway-native 的非敏感配置。token/device private key
// 由专用 keychain 管理，不进入 entity 或通用 env_json。
type openClawKind struct{}

func (openClawKind) Type() BackendType      { return TypeOpenClaw }
func (openClawKind) KnownAliases() []string { return nil }
func (openClawKind) ProviderTypeMatch(llm_provider_entity.ProviderType) bool {
	return false
}
func (openClawKind) RequiresProviderModel() bool { return false }
func (openClawKind) AllowsCLIPath() bool         { return false }

func (openClawKind) ValidateExtra(ctx context.Context, b *AgentBackend) error {
	if strings.TrimSpace(b.LLMProviderKey) != "" ||
		strings.TrimSpace(b.LLMModelKey) != "" ||
		strings.TrimSpace(b.CLIPath) != "" ||
		!isEmptyJSONObject(b.ModelRoutes) ||
		strings.TrimSpace(b.Sandbox) != "" ||
		strings.TrimSpace(b.Approval) != "" ||
		!isEmptyJSONObject(b.EnvJSON) ||
		strings.TrimSpace(b.ReasoningEffort) != "" ||
		strings.TrimSpace(b.DefaultPermissionMode) != "" ||
		strings.TrimSpace(b.DefaultModel) != "" {
		return i18n.NewError(ctx, code.InvalidParameter)
	}
	if _, err := NormalizeOpenClawGatewayURL(b.OpenClawGatewayURL); err != nil {
		return i18n.NewError(ctx, code.InvalidParameter)
	}
	if strings.TrimSpace(b.OpenClawSessionMode) != OpenClawSessionPerAgentRESession {
		return i18n.NewError(ctx, code.InvalidParameter)
	}
	return nil
}

// validateSandbox 校验 codex sandbox 枚举；空字符串表示走 CLI 默认。
func validateSandbox(ctx context.Context, v string) error {
	switch strings.TrimSpace(v) {
	case "", "read-only", "workspace-write", "danger-full-access":
		return nil
	default:
		return i18n.NewError(ctx, code.AgentBackendInvalidSandbox)
	}
}

// validateApproval 校验 codex approval policy 枚举；空字符串表示 never。
func validateApproval(ctx context.Context, v string) error {
	switch strings.TrimSpace(v) {
	case "", "untrusted", "on-request", "never":
		return nil
	default:
		return i18n.NewError(ctx, code.AgentBackendInvalidApproval)
	}
}

// isEmptyJSONObject 判断字符串是否表示空 JSON 对象（"" / "{}" / 含空白的 "{}"）。
func isEmptyJSONObject(s string) bool {
	t := strings.TrimSpace(s)
	return t == "" || t == "{}"
}

// ModelRouteTarget 是 Claude Tier 路由的结构化目标（spec 决策 14）。
// 同一 TEXT 列存 JSON `{"OPUS":{"providerKey":"..","modelKey":".."}}`；
// alias 缺失表示 inherit-main（不写进 JSON）。ModelKey 空 = provider-default。
// 生产 parser 只接受这个对象形状（旧字符串已由任务 1 的 patch migration 转换）。
type ModelRouteTarget struct {
	ProviderKey string `json:"providerKey"`
	ModelKey    string `json:"modelKey"`
}

// ParseModelRoutes 把 model_routes 字段解析成 map[alias]ModelRouteTarget。
// JSON 格式：`{"OPUS":{"providerKey":"<uuid>","modelKey":"<uuid>"}}`。
// alias 键统一转 ToUpper；解析失败或 providerKey 为空串均返回 error（统一交 service 报）。
// 调用方负责再用 BackendKind.KnownAliases() 把 alias 集合限定到本类型。
func ParseModelRoutes(s string) (map[string]ModelRouteTarget, error) {
	s = strings.TrimSpace(s)
	if s == "" || s == "{}" {
		return map[string]ModelRouteTarget{}, nil
	}
	var raw map[string]ModelRouteTarget
	if err := json.Unmarshal([]byte(s), &raw); err != nil {
		return nil, fmt.Errorf("parse model_routes: %w", err)
	}
	out := make(map[string]ModelRouteTarget, len(raw))
	for k, v := range raw {
		v.ProviderKey = strings.TrimSpace(v.ProviderKey)
		v.ModelKey = strings.TrimSpace(v.ModelKey)
		if v.ProviderKey == "" {
			return nil, fmt.Errorf("model_routes alias %q has empty providerKey", k)
		}
		out[strings.ToUpper(strings.TrimSpace(k))] = v
	}
	return out, nil
}

// MarshalModelRoutes 把结构化 route 序列化回持久化字符串。空 map → "{}"。
// alias 统一 ToUpper；空 providerKey 的条目被跳过（与 Check 的拒绝语义一致）。
func MarshalModelRoutes(routes map[string]ModelRouteTarget) (string, error) {
	if len(routes) == 0 {
		return "{}", nil
	}
	out := make(map[string]ModelRouteTarget, len(routes))
	for k, v := range routes {
		alias := strings.ToUpper(strings.TrimSpace(k))
		v.ProviderKey = strings.TrimSpace(v.ProviderKey)
		v.ModelKey = strings.TrimSpace(v.ModelKey)
		if alias == "" || v.ProviderKey == "" {
			continue
		}
		out[alias] = v
	}
	if len(out) == 0 {
		return "{}", nil
	}
	b, err := json.Marshal(out)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// ParseEnvJSON 把 env_json 字段解析成 map[string]string。空 / "{}" 视作空 map。
func ParseEnvJSON(s string) (map[string]string, error) {
	t := strings.TrimSpace(s)
	if t == "" || t == "{}" {
		return map[string]string{}, nil
	}
	out := make(map[string]string)
	if err := json.Unmarshal([]byte(t), &out); err != nil {
		return nil, err
	}
	return out, nil
}
