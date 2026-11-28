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

// LLMUpsertParams is the payload of a local provider add (`agentred llm add`,
// over the local IPC socket).
type LLMUpsertParams struct {
	ProviderKey string `json:"providerKey"`
	Name        string `json:"name"`
	Type        string `json:"type"`
	BaseURL     string `json:"baseURL"`
	Model       string `json:"model"`
	// DefaultModelKey / Models 是 Provider 默认模型稳定 key + 模型目录。APIKey 是用户在
	// 这台机器上亲手给的 key,只存进本机添加的那一节。
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

// LLMProviderRow is one entry in LLMListResult. The raw API key never appears
// here; MaskedTail is empty for account providers, whose key is never on disk.
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

// Upsert stores a locally added provider (metadata and its API key) in the
// state section snapshot pulls never overwrite.
func (h *LLMHandlers) Upsert(ctx context.Context, p LLMUpsertParams) (OK, error) {
	if p.ProviderKey == "" {
		return OK{}, errors.New("providerKey required")
	}
	h.st.Mutate(func(s *state.State) {
		if s.LocalLLMProviders == nil {
			s.LocalLLMProviders = map[string]state.LocalLLMProvider{}
		}
		s.LocalLLMProviders[p.ProviderKey] = state.LocalLLMProvider{
			LLMProviderMeta: state.LLMProviderMeta{
				Name:            p.Name,
				Type:            p.Type,
				BaseURL:         p.BaseURL,
				Model:           p.Model,
				DefaultModelKey: p.DefaultModelKey,
				Models:          p.Models,
				ModelRoutes:     p.ModelRoutes,
				UpdatedAt:       p.UpdatedAt,
			},
			APIKey: p.APIKey,
		}
	})
	if err := h.st.Save(); err != nil {
		return OK{}, err
	}
	return OK{OK: true}, nil
}

// Delete removes a locally added provider and its API key. Account providers
// belong to the engine snapshot and are left alone.
func (h *LLMHandlers) Delete(_ context.Context, p LLMDeleteParams) (OK, error) {
	h.st.Mutate(func(s *state.State) { delete(s.LocalLLMProviders, p.ProviderKey) })
	return OK{OK: true}, h.st.Save()
}

// List returns every provider this daemon can run: locally added ones with a
// masked key tail, account ones with metadata only. Raw keys are never returned.
func (h *LLMHandlers) List(_ context.Context) (LLMListResult, error) {
	snap := h.st.Snapshot()
	out := make([]LLMProviderRow, 0, len(snap.LLMProviders)+len(snap.LocalLLMProviders))
	for key, m := range snap.LLMProviders {
		if _, local := snap.LocalLLMProviders[key]; local {
			continue
		}
		out = append(out, providerRow(key, m, ""))
	}
	for key, m := range snap.LocalLLMProviders {
		out = append(out, providerRow(key, m.LLMProviderMeta, maskedTail(m.APIKey)))
	}
	return LLMListResult{Providers: out}, nil
}

func providerRow(key string, m state.LLMProviderMeta, tail string) LLMProviderRow {
	return LLMProviderRow{
		ProviderKey:     key,
		Name:            m.Name,
		Type:            m.Type,
		BaseURL:         m.BaseURL,
		Model:           m.Model,
		DefaultModelKey: m.DefaultModelKey,
		Models:          m.Models,
		MaskedTail:      tail,
		UpdatedAt:       m.UpdatedAt,
		ModelRoutes:     m.ModelRoutes,
	}
}
