// Package agentprovider 把项目内的 LLMProvider entity 映射成 cago provider.Provider。
// 仅做装配，不发任何网络请求；空 API key / 空 model 由调用方校验。
package agentprovider

import (
	"errors"
	"strings"

	"github.com/cago-frame/agents/provider"
	"github.com/cago-frame/agents/provider/anthropics"
	"github.com/cago-frame/agents/provider/openai"
	"github.com/cago-frame/agents/provider/openai_response"
	goopenai "github.com/sashabaranov/go-openai"

	"github.com/agentre-hub/agentre/internal/model/entity/llm_provider_entity"
)

var (
	// ErrNilProvider 入参 entity 为 nil。
	ErrNilProvider = errors.New("agentprovider: nil provider entity")
	// ErrUnsupportedType entity.Type 不在已知集合内。
	ErrUnsupportedType = errors.New("agentprovider: unsupported provider type")
)

// Build 把项目内的 LLMProvider entity 映射成 cago provider.Provider。
func Build(p *llm_provider_entity.LLMProvider) (provider.Provider, error) {
	if p == nil {
		return nil, ErrNilProvider
	}
	switch llm_provider_entity.ProviderType(p.Type) {
	case llm_provider_entity.TypeAnthropic:
		// 显式 1h 走 Anthropic GA 的 extended cache;OpenAI 系无客户端 TTL 字段,服务端自动 5–10 分钟。
		return anthropics.NewProvider(anthropics.Config{
			BaseURL:  strings.TrimSpace(p.BaseURL),
			APIKey:   p.APIKey,
			CacheTTL: anthropics.CacheTTL1h,
		}), nil
	case llm_provider_entity.TypeOpenAIChat:
		cfg := goopenai.DefaultConfig(p.APIKey)
		if base := strings.TrimSpace(p.BaseURL); base != "" {
			cfg.BaseURL = base
		}
		return openai.NewProvider(cfg), nil
	case llm_provider_entity.TypeOpenAIResponse:
		return openai_response.NewProvider(openai_response.Config{
			BaseURL: strings.TrimSpace(p.BaseURL),
			APIKey:  p.APIKey,
		}), nil
	default:
		return nil, ErrUnsupportedType
	}
}
