package sync_svc

import (
	"context"
	"encoding/json"

	"github.com/cago-frame/cago/pkg/consts"

	"github.com/agentre-hub/agentre/internal/model/entity/llm_provider_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/llm_provider_model_entity"
	"github.com/agentre-hub/agentre/internal/pkg/syncwire"
	"github.com/agentre-hub/agentre/internal/repository/llm_provider_repo"
	"github.com/agentre-hub/agentre/internal/repository/syncstate_repo"
)

// llmProviderPayload is the one account object that carries an API key. Its
// model rows are nested so model_key remains stable without becoming a second
// sync kind.
type llmProviderPayload struct {
	Name            string             `json:"name"`
	Type            string             `json:"type"`
	BaseURL         string             `json:"base_url"`
	APIKey          string             `json:"api_key"`
	DefaultModelKey string             `json:"default_model_key"`
	Enabled         bool               `json:"enabled"`
	Models          []llmProviderModel `json:"models"`
}

type llmProviderModel struct {
	ModelKey      string `json:"model_key"`
	ModelID       string `json:"model_id"`
	Name          string `json:"name"`
	Enabled       bool   `json:"enabled"`
	ContextWindow int    `json:"context_window,omitempty"`
	MaxOutput     int    `json:"max_output,omitempty"`
}

type llmProviderAdapter struct{ baseAdapter }

func (llmProviderAdapter) kind() string { return syncwire.KindLLMProvider }

func (llmProviderAdapter) load(ctx context.Context, syncID string) (*outbound, error) {
	row := &llm_provider_entity.LLMProvider{}
	found, err := syncstate_repo.SyncState().FindRow(ctx, syncwire.KindLLMProvider, syncID, row)
	if err != nil || !found {
		return nil, err
	}
	models, err := llm_provider_repo.LLMProvider().ListModels(ctx, row.ID)
	if err != nil {
		return nil, err
	}
	payloadModels := make([]llmProviderModel, 0, len(models))
	for _, model := range models {
		if model == nil {
			continue
		}
		payloadModels = append(payloadModels, llmProviderModel{
			ModelKey: model.ModelKey, ModelID: model.ModelID, Name: model.Name,
			Enabled: model.IsEnabled(), ContextWindow: model.ContextWindow, MaxOutput: model.MaxOutput,
		})
	}
	payload, err := json.Marshal(llmProviderPayload{
		Name: row.Name, Type: row.Type, BaseURL: row.BaseURL, APIKey: row.APIKey,
		DefaultModelKey: row.DefaultModelKey, Enabled: row.IsEnabled(), Models: payloadModels,
	})
	if err != nil {
		return nil, err
	}
	return &outbound{SyncID: row.ProviderKey, UpdatedAt: row.Updatetime, Payload: payload}, nil
}

func (llmProviderAdapter) refs(*inbound) []ref { return nil }

func (llmProviderAdapter) apply(ctx context.Context, in *inbound, _ map[string]int64) error {
	var payload llmProviderPayload
	if err := json.Unmarshal(in.Payload, &payload); err != nil {
		return err
	}
	row := &llm_provider_entity.LLMProvider{
		ProviderKey: in.SyncID, Name: payload.Name, Type: payload.Type, APIKey: payload.APIKey,
		BaseURL: payload.BaseURL, DefaultModelKey: payload.DefaultModelKey, Status: consts.ACTIVE,
	}
	if payload.Enabled {
		row.Enabled = llm_provider_entity.EnabledOn
	} else {
		row.Enabled = llm_provider_entity.EnabledOff
	}
	models := make([]*llm_provider_model_entity.LLMProviderModel, 0, len(payload.Models))
	for _, model := range payload.Models {
		entry := &llm_provider_model_entity.LLMProviderModel{
			ModelKey: model.ModelKey, ModelID: model.ModelID, Name: model.Name,
			ContextWindow: model.ContextWindow, MaxOutput: model.MaxOutput, Status: consts.ACTIVE,
		}
		if model.Enabled {
			entry.Enabled = llm_provider_model_entity.EnabledOn
		} else {
			entry.Enabled = llm_provider_model_entity.EnabledOff
		}
		models = append(models, entry)
	}
	return llm_provider_repo.LLMProvider().UpsertFromSync(ctx, row, models)
}

func (llmProviderAdapter) remove(ctx context.Context, in *inbound) error {
	row, err := llm_provider_repo.LLMProvider().FindByKey(ctx, in.SyncID)
	if err != nil || row == nil {
		return err
	}
	return llm_provider_repo.LLMProvider().DeleteWithModels(ctx, row.ID)
}
