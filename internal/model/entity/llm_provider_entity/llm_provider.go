// Package llm_provider_entity 维护 LLM 供应商的充血实体。
//
// 一个 LLMProvider = 一次"我可以调谁家的 API"的凭证 + 配置：
//   - Type        决定走哪个 cago agents/provider 实现（anthropic / openai-chat / openai-response）；
//   - APIKey  / BaseURL 是请求 LLM 实际需要的凭证；BaseURL 留空表示用该 Type 的默认端点，
//     原样保存，由各上游出口经 llmurl.EffectiveBaseURL（或 SDK 自身默认值）解析。
//
// 模型从 Provider 行拆到独立的 llm_provider_model_entity（1 → N）。Provider 只保存
// 连接配置、独立可运行状态 enabled 与指向启用默认模型的 default_model_key。
package llm_provider_entity

import (
	"context"
	"encoding/json"
	"slices"
	"strings"

	"github.com/cago-frame/cago/pkg/consts"
	"github.com/cago-frame/cago/pkg/i18n"

	"github.com/agentre-hub/agentre/internal/model/entity/syncmeta_entity"
	"github.com/agentre-hub/agentre/internal/pkg/code"

	"github.com/agentre-hub/agentre/pkg/wire/devicefp"
)

// ProviderType 供应商实现类型。值与 cago agents/provider.Provider.Name() 对齐。
type ProviderType string

const (
	// TypeAnthropic 走 anthropic-sdk-go；Base URL 留空走 https://api.anthropic.com。
	TypeAnthropic ProviderType = "anthropic"
	// TypeOpenAIChat 走 cago provider/openai（基于 sashabaranov/go-openai）打 /v1/chat/completions。
	// OpenAI 兼容端（Ollama / vLLM / Azure）目前都走这条，BaseURL 留空时使用 https://api.openai.com/v1。
	TypeOpenAIChat ProviderType = "openai-chat"
	// TypeOpenAIResponse 走 cago provider/openai_response（基于官方 openai/openai-go）打 /v1/responses。
	// 适用于 o-series、gpt-5-codex 等仅支持 Responses API 的 OpenAI 模型；
	// 多数 OpenAI 兼容端尚未实现此协议，请优先选 TypeOpenAIChat。
	TypeOpenAIResponse ProviderType = "openai-response"
	// TypeGemini 是 Gemini 原生协议（/v1beta/...）的网关或官方端点；Base URL 填根地址，
	// 留空走 https://generativelanguage.googleapis.com。只供 antigravity 后端使用。
	TypeGemini ProviderType = "gemini"
)

const (
	// EnabledOff / EnabledOn 是 Provider 独立的可运行状态（与 status 软删除解耦）。
	// enabled=0 的 Provider 仍可见、可编辑、可重新启用，但不能被新选择或用于执行。
	EnabledOff = 0
	EnabledOn  = 1
)

// LLMProvider 一条供应商配置记录。
//
// Enabled 独立于 Status（软删除）表示可运行状态；DefaultModelKey 指向属于本 Provider
// 的一个启用 Model（llm_provider_model_entity.LLMProviderModel.ModelKey）。
type LLMProvider struct {
	ID              int64  `gorm:"column:id;primaryKey;autoIncrement"`
	ProviderKey     string `gorm:"column:provider_key;type:text;not null;uniqueIndex:uniq_llm_providers_provider_key;default:''" json:"providerKey"`
	Type            string `gorm:"column:type;type:text;not null"`
	Name            string `gorm:"column:name;type:text;not null"`
	APIKey          string `gorm:"column:api_key;type:text;not null;default:''"`
	BaseURL         string `gorm:"column:base_url;type:text;not null;default:''"`
	Enabled         int    `gorm:"column:enabled;type:int;not null;default:1"`
	DefaultModelKey string `gorm:"column:default_model_key;type:text;not null;default:''"`
	// ReachMode / ReachDevicesJSON 是「谁能连到」（spec 2026-11-25 决策 14）：任何设备
	// （默认）或只有这些设备（本地端点只在特定机器上可达）。这两列只由 SetReach 经窄写口
	// 改，整行回写不碰它们。
	ReachMode        string `gorm:"column:reach_mode;type:text;not null;default:'any'"`
	ReachDevicesJSON string `gorm:"column:reach_devices_json;type:text;not null;default:'[]'"`
	Status           int    `gorm:"column:status;type:int;not null;default:1"`
	Createtime       int64  `gorm:"column:createtime;type:bigint;not null;default:0"`
	Updatetime       int64  `gorm:"column:updatetime;type:bigint;not null;default:0"`
	// SyncMeta uses ProviderKey as the account-sync identity. Models travel nested
	// in the provider payload, so their local rows deliberately have no separate
	// sync identity.
	syncmeta_entity.SyncMeta `gorm:"embedded"`
}

// TableName 绑定表名。
func (*LLMProvider) TableName() string { return "llm_providers" }

// IsActive 是否处于启用态（未被软删除）。
func (p *LLMProvider) IsActive() bool { return p != nil && p.Status == consts.ACTIVE }

// IsEnabled 是否可被新选择 / 用于执行（独立于软删除状态）。
func (p *LLMProvider) IsEnabled() bool { return p != nil && p.Enabled == EnabledOn }

// HasDefaultModel 是否已指定默认模型（default_model_key 非空）。
func (p *LLMProvider) HasDefaultModel() bool { return p != nil && p.DefaultModelKey != "" }

// Check 校验关键字段。空 Name / 不支持的 Type 直接返回业务错误。
func (p *LLMProvider) Check(ctx context.Context) error {
	if p == nil {
		return i18n.NewError(ctx, code.LLMProviderNotFound)
	}
	if strings.TrimSpace(p.Name) == "" {
		return i18n.NewError(ctx, code.InvalidParameter)
	}
	switch ProviderType(p.Type) {
	case TypeAnthropic, TypeOpenAIChat, TypeOpenAIResponse, TypeGemini:
		return nil
	default:
		return i18n.NewError(ctx, code.LLMProviderInvalidType)
	}
}

// IsAnthropic 是否走 Anthropic provider。
func (p *LLMProvider) IsAnthropic() bool {
	return ProviderType(p.Type) == TypeAnthropic
}

// IsOpenAIChat 是否走 OpenAI Chat Completions（/v1/chat/completions）。
func (p *LLMProvider) IsOpenAIChat() bool {
	return ProviderType(p.Type) == TypeOpenAIChat
}

// IsOpenAIResponse 是否走 OpenAI Responses API（/v1/responses）。
func (p *LLMProvider) IsOpenAIResponse() bool {
	return ProviderType(p.Type) == TypeOpenAIResponse
}

// IsOpenAICompatible 是否属于 OpenAI 系列（chat 或 responses）。
// service 层判断「能复用 /v1/models 列模型 / 用 OpenAI vendor 富化元数据」时用。
func (p *LLMProvider) IsOpenAICompatible() bool {
	return p.IsOpenAIChat() || p.IsOpenAIResponse()
}

// MaskedAPIKey 用于前端只读展示，不暴露原始 key。
func (p *LLMProvider) MaskedAPIKey() string {
	key := p.APIKey
	if key == "" {
		return ""
	}
	if len(key) <= 8 {
		return strings.Repeat("•", len(key))
	}
	return key[:4] + strings.Repeat("•", 6) + key[len(key)-4:]
}

// 「谁能连到」的取值。
const (
	ReachAny     = "any"     // 任何设备（默认）
	ReachDevices = "devices" // 只有 ReachDevicesJSON 里列出的设备
)

// Reach 解出「谁能连到」；没设过（空串）与读不出列表都按任何设备。
func (p *LLMProvider) Reach() (mode string, devices []devicefp.Carrier) {
	if p == nil || p.ReachMode != ReachDevices {
		return ReachAny, nil
	}
	if err := json.Unmarshal([]byte(p.ReachDevicesJSON), &devices); err != nil {
		return ReachAny, nil
	}
	return ReachDevices, devices
}

// SetReach 写入「谁能连到」：设备列表去空白、去重、保持首次出现顺序；任何设备时清空列表；
// 只有这些设备却没有任何设备是不合法的（等于谁都连不到）。
func (p *LLMProvider) SetReach(ctx context.Context, mode string, devices []devicefp.Carrier) error {
	switch mode {
	case ReachAny:
		p.ReachMode, p.ReachDevicesJSON = ReachAny, "[]"
		return nil
	case ReachDevices:
		clean := make([]devicefp.Carrier, 0, len(devices))
		for _, d := range devices {
			d = devicefp.Carrier(strings.TrimSpace(string(d)))
			if d != "" && !slices.Contains(clean, d) {
				clean = append(clean, d)
			}
		}
		if len(clean) == 0 {
			return i18n.NewError(ctx, code.LLMProviderReachInvalid)
		}
		raw, _ := json.Marshal(clean)
		p.ReachMode, p.ReachDevicesJSON = ReachDevices, string(raw)
		return nil
	default:
		return i18n.NewError(ctx, code.LLMProviderReachInvalid)
	}
}

// ReachableFrom 报告供应商从这台设备上连不连得到。
func (p *LLMProvider) ReachableFrom(device devicefp.Carrier) bool {
	mode, devices := p.Reach()
	return mode == ReachAny || slices.Contains(devices, device)
}
