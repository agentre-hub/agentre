package sync_svc

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/cago-frame/cago/pkg/consts"

	"github.com/agentre-hub/agentre/internal/model/entity/llm_provider_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/llm_provider_model_entity"
	"github.com/agentre-hub/agentre/internal/repository/llm_provider_repo"
	"github.com/agentre-hub/agentre/internal/repository/syncstate_repo"
	"github.com/agentre-hub/agentre/pkg/syncwire"
	"github.com/agentre-hub/agentre/pkg/wire/devicefp"
)

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
	payloadModels := make([]syncwire.LLMProviderModel, 0, len(models))
	for _, model := range models {
		if model == nil {
			continue
		}
		payloadModels = append(payloadModels, syncwire.LLMProviderModel{
			ModelKey: model.ModelKey, ModelID: model.ModelID, Name: model.Name,
			Enabled: model.IsEnabled(), ContextWindow: model.ContextWindow, MaxOutput: model.MaxOutput,
		})
	}
	reachMode, reachDevices := row.Reach()
	reach := &syncwire.LLMProviderReach{Mode: reachMode}
	for _, d := range reachDevices {
		reach.Devices = append(reach.Devices, string(d))
	}
	payload, err := json.Marshal(syncwire.LLMProviderPayload{
		Name: row.Name, Type: row.Type, BaseURL: row.BaseURL, APIKey: row.APIKey,
		DefaultModelKey: row.DefaultModelKey, Enabled: row.IsEnabled(), Models: payloadModels,
		Reach: reach,
	})
	if err != nil {
		return nil, err
	}
	return &outbound{SyncID: row.ProviderKey, UpdatedAt: row.Updatetime, Payload: payload}, nil
}

func (llmProviderAdapter) refs(*inbound) []ref { return nil }

func (llmProviderAdapter) apply(ctx context.Context, in *inbound, _ map[string]int64) error {
	var payload syncwire.LLMProviderPayload
	if err := json.Unmarshal(in.Payload, &payload); err != nil {
		return err
	}
	// 「谁能连到」先在内存里校验归一，过不了的载荷不落库；真正的写在 upsert 之后经
	// 窄写口（UpsertFromSync 的整行写口跳过这两列）。键不在 = 旧版本对端，保持本机现状。
	var reach *llm_provider_entity.LLMProvider
	if payload.Reach != nil {
		devices := make([]devicefp.Carrier, 0, len(payload.Reach.Devices))
		for _, d := range payload.Reach.Devices {
			devices = append(devices, devicefp.Carrier(d))
		}
		reach = &llm_provider_entity.LLMProvider{}
		if err := reach.SetReach(ctx, payload.Reach.Mode, devices); err != nil {
			return err
		}
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
	if err := llm_provider_repo.LLMProvider().UpsertFromSync(ctx, row, models); err != nil {
		return err
	}
	if reach == nil {
		return nil
	}
	saved, err := llm_provider_repo.LLMProvider().FindByKey(ctx, in.SyncID)
	if err != nil {
		return err
	}
	if saved == nil {
		return errProviderVanished
	}
	return llm_provider_repo.LLMProvider().UpdateReach(ctx, saved.ID, reach.ReachMode, reach.ReachDevicesJSON)
}

var errProviderVanished = errors.New("sync: provider row missing right after upsert")

func (llmProviderAdapter) remove(ctx context.Context, in *inbound) error {
	row, err := llm_provider_repo.LLMProvider().FindByKey(ctx, in.SyncID)
	if err != nil || row == nil {
		return err
	}
	return llm_provider_repo.LLMProvider().DeleteWithModels(ctx, row.ID)
}
