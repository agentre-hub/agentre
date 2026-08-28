package agentruntime

import "strings"

// EffectiveLLMConfigMode 是解析目标模式（ModelTarget 契约的派生形态）。
//
// 只持久化 providerKey / modelKey 的组合决定 mode：
//   - 两个 key 都为空 → native（CLI 自身登录态）；
//   - ProviderKey 非空且 ModelKey 为空 → provider-default（每轮解析当前默认模型）；
//   - 两个 key 都非空 → fixed-model（解析指定 Model 记录）。
//
// v1 阶段三种 mode 都会从 chat_svc 产出：native（无供应商）、provider-default
// （ProviderKey 非空 + ModelKey 空）与 fixed-model（Backend 或 Session 钉了固定
// ModelKey）。
type EffectiveLLMConfigMode string

const (
	// EffectiveModeNative 无 Agentre 供应商：CLI 自身登录态。
	EffectiveModeNative EffectiveLLMConfigMode = "native"
	// EffectiveModeProviderDefault ProviderKey 非空、ModelKey 空：每轮解析 Provider 当前默认模型。
	EffectiveModeProviderDefault EffectiveLLMConfigMode = "provider-default"
	// EffectiveModeFixedModel ProviderKey + ModelKey 都非空：解析指定 Model 记录。
	EffectiveModeFixedModel EffectiveLLMConfigMode = "fixed-model"
)

// EffectiveLLMConfig 是执行侧唯一解析结果（EffectiveLLMConfig v1 seam）。
//
// chat_svc 通过 llm_provider_svc.ResolveTarget 解析产生，随 RunRequest / GoalRequest
// 下发；runtime 与 gateway 只消费它决定实际 Provider 与 Model，不得各自重新拼装
// Backend / Session / Provider 的优先级。
//
// 秘密边界：APIKey 只存在于执行侧契约里（与 llm_provider_svc.ResolvedModel 同口径）；
// 展示侧永远走脱敏 DTO，不进入本结构体所在的日志 / IPC 路径。
type EffectiveLLMConfig struct {
	// Mode 目标模式（见 EffectiveLLMConfigMode）。
	Mode EffectiveLLMConfigMode
	// ProviderKey / ModelKey 是持久化目标的稳定身份（来自 ModelTarget）。
	ProviderKey string
	ModelKey    string
	// ProviderType 是 Provider 的类型（anthropic / openai-chat / openai-response）。
	ProviderType string
	// ProviderName 是供应商展示名（仅日志/展示，不含凭证）。
	ProviderName string
	// ModelID 是实际发给上游的模型 id（provider-default 每轮解析当前默认）。
	ModelID string
	// ContextWindow / MaxOutput 是解析出模型的元数据（供上下文窗口展示 / provider 扩展）。
	ContextWindow int
	MaxOutput     int
	// BaseURL / APIKey / HasAPIKey 是执行侧连接信息。
	BaseURL   string
	APIKey    string
	HasAPIKey bool
}

// EffectiveModelID 返回解析出的实际模型 id；native（无供应商）时为空串。
func (c *EffectiveLLMConfig) EffectiveModelID() string {
	if c == nil {
		return ""
	}
	return c.ModelID
}

// EffectiveProviderKey 返回供应商稳定 key；native 时为空串。
func (c *EffectiveLLMConfig) EffectiveProviderKey() string {
	if c == nil {
		return ""
	}
	return c.ProviderKey
}

// EffectiveModeFor 是 mode 的**唯一**计算口（契约见 EffectiveLLMConfigMode）：由持久化
// 目标的两个 key 决定，桌面、daemon 与 prober 三条装配路径都经它，不得各自再写一遍
// `mode := provider-default; if modelKey != "" { mode = fixed-model }`。
//
// modelKey 必须是**本轮请求的**持久化 ModelKey（ModelTarget.ModelKey），不是解析结果的
// ModelKey：provider-default 解析出来的默认模型同样带 key，拿它算 mode 会把每一轮
// provider-default 误判成 fixed-model。
func EffectiveModeFor(providerKey, modelKey string) EffectiveLLMConfigMode {
	switch {
	case strings.TrimSpace(providerKey) == "":
		return EffectiveModeNative
	case strings.TrimSpace(modelKey) == "":
		return EffectiveModeProviderDefault
	default:
		return EffectiveModeFixedModel
	}
}

// EffectiveLLMConfigInput 是 NewEffectiveLLMConfig 的入参：一半来自本轮请求的持久化
// 目标（ProviderKey / TargetModelKey），一半来自解析层的结果（Resolved* 与模型元数据、
// 连接信息）。两侧解析层不同（桌面走 llm_provider_svc.ResolveTarget，daemon 走自家
// state 目录），但装配规则只有这一份。
type EffectiveLLMConfigInput struct {
	// ProviderKey 是本轮生效供应商的稳定 key。
	ProviderKey string
	// ProviderType / ProviderName 是供应商类型与展示名。
	ProviderType string
	ProviderName string
	// TargetModelKey 是本轮请求的持久化 ModelKey（空 = provider-default）。只用于定 Mode。
	TargetModelKey string
	// ResolvedModelKey / ResolvedModelID 是解析层交出的模型身份与实际上游模型 id。
	ResolvedModelKey string
	ResolvedModelID  string
	// ContextWindow / MaxOutput 是解析出模型的元数据。
	ContextWindow int
	MaxOutput     int
	// BaseURL / APIKey / HasAPIKey 是执行侧连接信息。
	BaseURL   string
	APIKey    string
	HasAPIKey bool
}

// NewEffectiveLLMConfig 装配执行侧 EffectiveLLMConfig（EffectiveLLMConfig v1 seam 的
// 唯一构造口）。桌面与 daemon 给同一组入参必须得到逐字段相同的配置 —— daemon 曾因自己
// 手写这段装配而漏填 ContextWindow / MaxOutput。
func NewEffectiveLLMConfig(in EffectiveLLMConfigInput) *EffectiveLLMConfig {
	return &EffectiveLLMConfig{
		Mode:          EffectiveModeFor(in.ProviderKey, in.TargetModelKey),
		ProviderKey:   in.ProviderKey,
		ModelKey:      in.ResolvedModelKey,
		ProviderType:  in.ProviderType,
		ProviderName:  in.ProviderName,
		ModelID:       in.ResolvedModelID,
		ContextWindow: in.ContextWindow,
		MaxOutput:     in.MaxOutput,
		BaseURL:       in.BaseURL,
		APIKey:        in.APIKey,
		HasAPIKey:     in.HasAPIKey,
	}
}

// NewKeysOnlyEffectiveLLMConfig 装配 keys-only 配置（远端执行目标，决策 11）：只带两个
// key 与由它们定出的 Mode，不含任何解析结果或凭证 —— 远端由 daemon 按 key 从自家目录
// 自解，桌面不透传解析结果。ProviderKey 为空即 native（CLI 自身登录态），此时 ModelKey
// 无处可解，不落进配置。
func NewKeysOnlyEffectiveLLMConfig(providerKey, modelKey string) *EffectiveLLMConfig {
	mode := EffectiveModeFor(providerKey, modelKey)
	if mode == EffectiveModeNative {
		return &EffectiveLLMConfig{Mode: mode}
	}
	if mode == EffectiveModeProviderDefault {
		return &EffectiveLLMConfig{Mode: mode, ProviderKey: providerKey}
	}
	return &EffectiveLLMConfig{Mode: mode, ProviderKey: providerKey, ModelKey: modelKey}
}
