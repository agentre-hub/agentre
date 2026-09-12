package agentruntime

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestEffectiveModeFor 钉死 mode 的唯一计算口：两个 key 的组合决定 mode，且判据取的是
// **本轮请求的持久化 ModelKey**，不是解析结果的 ModelKey（provider-default 解析出来的
// 默认模型也带 key，用它算 mode 会把 provider-default 全部误判成 fixed-model）。
func TestEffectiveModeFor(t *testing.T) {
	assert.Equal(t, EffectiveModeNative, EffectiveModeFor("", ""))
	assert.Equal(t, EffectiveModeNative, EffectiveModeFor("  ", "mk"),
		"没有供应商就是 CLI 登录态，带不带 model key 都一样")
	assert.Equal(t, EffectiveModeProviderDefault, EffectiveModeFor("pk", ""))
	assert.Equal(t, EffectiveModeProviderDefault, EffectiveModeFor("pk", "   "),
		"空白 model key 与空等价")
	assert.Equal(t, EffectiveModeFixedModel, EffectiveModeFor("pk", "mk"))
}

// TestNewEffectiveLLMConfig_ProviderDefault 钉死 provider-default 装配：mode 由请求的
// 空 ModelKey 决定，而 ModelKey 字段落解析出来的默认模型 key。
func TestNewEffectiveLLMConfig_ProviderDefault(t *testing.T) {
	got := NewEffectiveLLMConfig(EffectiveLLMConfigInput{
		ProviderKey:      "pk",
		ProviderType:     "anthropic",
		ProviderName:     "主号",
		TargetModelKey:   "",
		ResolvedModelKey: "model-default",
		ResolvedModelID:  "claude-sonnet-4-6",
		ContextWindow:    200000,
		MaxOutput:        8192,
		BaseURL:          "https://api.example.com",
		APIKey:           "sk-fixture",
		HasAPIKey:        true,
	})
	assert.Equal(t, &EffectiveLLMConfig{
		Mode:          EffectiveModeProviderDefault,
		ProviderKey:   "pk",
		ModelKey:      "model-default",
		ProviderType:  "anthropic",
		ProviderName:  "主号",
		ModelID:       "claude-sonnet-4-6",
		ContextWindow: 200000,
		MaxOutput:     8192,
		BaseURL:       "https://api.example.com",
		APIKey:        "sk-fixture",
		HasAPIKey:     true,
	}, got)
}

// TestNewEffectiveLLMConfig_FixedModel 钉死 fixed-model 装配：请求带 ModelKey → mode
// 为 fixed-model，其余字段原样落位。
func TestNewEffectiveLLMConfig_FixedModel(t *testing.T) {
	got := NewEffectiveLLMConfig(EffectiveLLMConfigInput{
		ProviderKey:      "pk",
		ProviderType:     "openai-chat",
		ProviderName:     "副号",
		TargetModelKey:   "model-opus",
		ResolvedModelKey: "model-opus",
		ResolvedModelID:  "claude-opus-4-5",
		ContextWindow:    100,
		MaxOutput:        20,
		BaseURL:          "https://api2.example.com",
		APIKey:           "",
		HasAPIKey:        false,
	})
	assert.Equal(t, EffectiveModeFixedModel, got.Mode)
	assert.Equal(t, "model-opus", got.ModelKey)
	assert.Equal(t, "claude-opus-4-5", got.ModelID)
	assert.Equal(t, 100, got.ContextWindow)
	assert.Equal(t, 20, got.MaxOutput)
	assert.False(t, got.HasAPIKey)
}

// TestNewKeysOnlyEffectiveLLMConfig 钉死 keys-only 变体（远端执行目标，决策 11）：
// 只填 Mode / ProviderKey / ModelKey，不带任何解析结果与凭证。
func TestNewKeysOnlyEffectiveLLMConfig(t *testing.T) {
	assert.Equal(t, &EffectiveLLMConfig{Mode: EffectiveModeNative},
		NewKeysOnlyEffectiveLLMConfig("", ""))
	assert.Equal(t, &EffectiveLLMConfig{Mode: EffectiveModeProviderDefault, ProviderKey: "pk"},
		NewKeysOnlyEffectiveLLMConfig("pk", ""))
	assert.Equal(t, &EffectiveLLMConfig{Mode: EffectiveModeFixedModel, ProviderKey: "pk", ModelKey: "mk"},
		NewKeysOnlyEffectiveLLMConfig("pk", "mk"))
	assert.Equal(t, &EffectiveLLMConfig{Mode: EffectiveModeNative},
		NewKeysOnlyEffectiveLLMConfig("", "mk"),
		"没有供应商时 model key 无处可解，不得漏进配置")
}
