package agentprovider_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/cago-frame/agents/provider"
	"github.com/stretchr/testify/assert"

	"github.com/agentre-hub/agentre/internal/model/entity/llm_provider_entity"
	"github.com/agentre-hub/agentre/internal/pkg/agentprovider"
)

func TestBuild(t *testing.T) {
	cases := []struct {
		name    string
		entity  *llm_provider_entity.LLMProvider
		wantErr error
		wantNil bool
	}{
		{
			name:    "nil entity returns ErrNilProvider",
			entity:  nil,
			wantErr: agentprovider.ErrNilProvider,
			wantNil: true,
		},
		{
			name: "anthropic entity returns non-nil provider",
			entity: &llm_provider_entity.LLMProvider{
				Type:    string(llm_provider_entity.TypeAnthropic),
				APIKey:  "sk-test",
				BaseURL: "",
			},
		},
		{
			name: "openai-chat entity returns non-nil provider",
			entity: &llm_provider_entity.LLMProvider{
				Type:    string(llm_provider_entity.TypeOpenAIChat),
				APIKey:  "sk-test",
				BaseURL: "https://api.openai.com/v1",
			},
		},
		{
			name: "openai-response entity returns non-nil provider",
			entity: &llm_provider_entity.LLMProvider{
				Type:    string(llm_provider_entity.TypeOpenAIResponse),
				APIKey:  "sk-test",
				BaseURL: "https://api.openai.com/v1",
			},
		},
		{
			name: "unknown type returns ErrUnsupportedType",
			entity: &llm_provider_entity.LLMProvider{
				Type:   "bogus",
				APIKey: "sk-test",
			},
			wantErr: agentprovider.ErrUnsupportedType,
			wantNil: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := agentprovider.Build(tc.entity)
			if tc.wantErr != nil {
				assert.True(t, errors.Is(err, tc.wantErr), "want %v, got %v", tc.wantErr, err)
			} else {
				assert.NoError(t, err)
			}
			if tc.wantNil {
				assert.Nil(t, got)
			} else {
				assert.NotNil(t, got)
			}
		})
	}
}

// TestBuild_AnthropicUses1hCacheTTL 验证 Build 出来的 anthropic provider 在发请求时
// 把 cache_control 的 ttl 升到 1h。
//
// Given 一个 anthropic LLMProvider entity 指向 mock anthropic 端,
// When 通过 Build 拿到 provider 并触发 ChatCompletion,
// Then 出站 HTTP body 里至少有一个 cache_control 块带 "ttl":"1h"。
func TestBuild_AnthropicUses1hCacheTTL(t *testing.T) {
	var captured string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		captured = string(body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"id": "msg_1",
			"type": "message",
			"role": "assistant",
			"model": "claude-sonnet-4-6",
			"content": [{"type":"text","text":"ok"}],
			"stop_reason": "end_turn",
			"usage": {"input_tokens": 1, "output_tokens": 1}
		}`))
	}))
	defer srv.Close()

	prov, err := agentprovider.Build(&llm_provider_entity.LLMProvider{
		Type:    string(llm_provider_entity.TypeAnthropic),
		APIKey:  "sk-test",
		BaseURL: srv.URL,
	})
	assert.NoError(t, err)

	_, err = prov.ChatCompletion(context.Background(), &provider.CompletionRequest{
		Model: "claude-sonnet-4-6",
		Messages: []provider.Message{
			{Role: provider.RoleSystem, Content: "you are helpful"},
			{Role: provider.RoleUser, Content: "hi"},
		},
	})
	assert.NoError(t, err)
	assert.Contains(t, captured, `"cache_control":{"ttl":"1h","type":"ephemeral"}`,
		"want cache_control with ttl=1h in outgoing body, got: %s", captured)
	// Sanity: 不应出现裸的 ttl 缺失版本(防止退化回默认 5m)。
	assert.NotContains(t, captured, `"cache_control":{"type":"ephemeral"}`,
		"cache_control should not be emitted without ttl when 1h is configured")
	// 防止字段顺序变化时给出可读 diff。
	if !strings.Contains(captured, `"ttl":"1h"`) {
		t.Logf("captured body: %s", captured)
	}
}
