package handlers

import (
	"context"
	"errors"

	"github.com/agentre-hub/agentre/internal/daemon/state"
)

// OK is the canonical "success ack" payload reused by several handlers.
type OK struct {
	OK bool `json:"ok"`
}

// LLMUpsertParams is the request payload for llm.upsert.
type LLMUpsertParams struct {
	ProviderKey string `json:"providerKey"`
	Name        string `json:"name"`
	Type        string `json:"type"`
	BaseURL     string `json:"baseURL"`
	Model       string `json:"model"`
	// DefaultModelKey / Models 是 task 6 多模型同步：Provider 默认模型稳定 key + 模型目录。
	// APIKey 只在这次显式同步里随行（规格：向远端复制凭证必须用户显式确认）。
	DefaultModelKey string               `json:"defaultModelKey,omitempty"`
	Models          []state.LLMModelMeta `json:"models,omitempty"`
	APIKey          string               `json:"apiKey"`
	ModelRoutes     map[string]string    `json:"modelRoutes"`
	UpdatedAt       int64                `json:"updatedAt"`
}

// LLMDeleteParams is the request payload for llm.delete.
type LLMDeleteParams struct {
	ProviderKey string `json:"providerKey"`
}

// LLMListResult is the response payload for llm.list.
type LLMListResult struct {
	Providers []LLMProviderRow `json:"providers"`
}

// LLMProviderRow is one entry in LLMListResult. The raw API key never appears here.
type LLMProviderRow struct {
	ProviderKey string `json:"providerKey"`
	Name        string `json:"name"`
	Type        string `json:"type"`
	BaseURL     string `json:"baseURL"`
	Model       string `json:"model"`
	// 非敏感多模型目录，供 desktop 渲染远端可选项。
	DefaultModelKey string               `json:"defaultModelKey,omitempty"`
	Models          []state.LLMModelMeta `json:"models,omitempty"`
	MaskedTail      string               `json:"maskedTail"`
	UpdatedAt       int64                `json:"updatedAt"`
	ModelRoutes     map[string]string    `json:"modelRoutes,omitempty"`
}

// LLMHandlers groups the llm.* RPC methods.
type LLMHandlers struct {
	st StatePort
}

// NewLLMHandlers constructs an LLMHandlers.
func NewLLMHandlers(st StatePort) *LLMHandlers {
	return &LLMHandlers{st: st}
}

// maskedTail returns a masked representation of the key showing the last 4 chars.
// Keys ≤ 4 chars get the whole string; longer keys show "...XXXX".
func maskedTail(key string) string {
	if len(key) <= 4 {
		return "..." + key
	}
	return "..." + key[len(key)-4:]
}

// Upsert stores provider metadata and the API key in agentred state.
func (h *LLMHandlers) Upsert(ctx context.Context, p LLMUpsertParams) (OK, error) {
	if p.ProviderKey == "" {
		return OK{}, errors.New("providerKey required")
	}
	h.st.Mutate(func(s *state.State) {
		s.LLMProviders[p.ProviderKey] = state.LLMProviderMeta{
			Name:            p.Name,
			Type:            p.Type,
			BaseURL:         p.BaseURL,
			APIKey:          p.APIKey,
			Model:           p.Model,
			DefaultModelKey: p.DefaultModelKey,
			Models:          p.Models,
			ModelRoutes:     p.ModelRoutes,
			UpdatedAt:       p.UpdatedAt,
		}
	})
	if err := h.st.Save(); err != nil {
		return OK{}, err
	}
	return OK{OK: true}, nil
}

// Delete removes the provider's state metadata and API key.
func (h *LLMHandlers) Delete(_ context.Context, p LLMDeleteParams) (OK, error) {
	h.st.Mutate(func(s *state.State) { delete(s.LLMProviders, p.ProviderKey) })
	return OK{OK: true}, h.st.Save()
}

// List returns all known providers with masked key tails. Raw keys are never returned.
func (h *LLMHandlers) List(_ context.Context) (LLMListResult, error) {
	snap := h.st.Snapshot()
	out := make([]LLMProviderRow, 0, len(snap.LLMProviders))
	for key, m := range snap.LLMProviders {
		out = append(out, LLMProviderRow{
			ProviderKey:     key,
			Name:            m.Name,
			Type:            m.Type,
			BaseURL:         m.BaseURL,
			Model:           m.Model,
			DefaultModelKey: m.DefaultModelKey,
			Models:          m.Models,
			MaskedTail:      maskedTail(m.APIKey),
			UpdatedAt:       m.UpdatedAt,
			ModelRoutes:     m.ModelRoutes,
		})
	}
	return LLMListResult{Providers: out}, nil
}
