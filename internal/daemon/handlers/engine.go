package handlers

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/agentre-hub/agentre/internal/pkg/cliprober"
	"github.com/agentre-hub/agentre/internal/pkg/llmurl"
)

// EngineDeps supplies the daemon-local capabilities used by engine RPCs.
type EngineDeps struct {
	State       StatePort
	HTTPClient  llmurl.HTTPDoer
	ScanAllCLIs func() []cliprober.CLIProbeResult
}

// EngineHandlers groups the engine.* RPC methods. It only reads credentials
// from daemon state; none of its response types contain a provider credential.
type EngineHandlers struct {
	state       StatePort
	probes      *llmurl.Client
	scanAllCLIs func() []cliprober.CLIProbeResult
}

// NewEngineHandlers constructs the engine RPC handlers.
func NewEngineHandlers(deps EngineDeps) *EngineHandlers {
	scan := deps.ScanAllCLIs
	if scan == nil {
		scan = cliprober.ScanAllCLIs
	}
	return &EngineHandlers{
		state:       deps.State,
		probes:      llmurl.NewClient(deps.HTTPClient),
		scanAllCLIs: scan,
	}
}

// EngineTestParams is the engine.test request payload.
type EngineTestParams struct {
	ProviderKey string `json:"providerKey"`
	ModelKey    string `json:"modelKey,omitempty"`
}

// EngineTestResult is the privacy-safe engine.test response.
type EngineTestResult struct {
	OK        bool   `json:"ok"`
	Message   string `json:"message"`
	LatencyMs *int64 `json:"latencyMs,omitempty"`
}

// EngineDiscoverParams is the engine.discover request payload.
type EngineDiscoverParams struct {
	ProviderKey string `json:"providerKey"`
}

// EngineModel is one privacy-safe discovered model.
type EngineModel struct {
	ModelID string `json:"modelId"`
	Name    string `json:"name"`
}

// EngineDiscoverResult is the engine.discover response.
type EngineDiscoverResult struct {
	Models []EngineModel `json:"models"`
}

// EngineScanItem is one privacy-safe CLI scan result.
type EngineScanItem struct {
	BackendType string `json:"backendType"`
	Status      string `json:"status"`
}

// EngineScanResult is the engine.scan response.
type EngineScanResult struct {
	Items []EngineScanItem `json:"items"`
}

// Test runs one minimal upstream request using the provider held in daemon
// state. Provider and model keys are inputs only and never occur in the result.
func (h *EngineHandlers) Test(ctx context.Context, params EngineTestParams) (EngineTestResult, error) {
	provider, modelID, ok := h.providerAndModel(params.ProviderKey, params.ModelKey)
	if !ok {
		return EngineTestResult{OK: false, Message: "provider or model is not configured"}, nil
	}
	started := time.Now()
	err := h.probes.Test(ctx, provider, modelID)
	latencyMs := time.Since(started).Milliseconds()
	result := EngineTestResult{LatencyMs: &latencyMs}
	if err != nil {
		result.Message = err.Error()
		return result, nil //nolint:nilerr // upstream failure is returned as the caller-visible OK:false probe result
	}
	result.OK = true
	result.Message = "connection succeeded"
	return result, nil
}

// Discover lists the upstream models for a provider held in daemon state.
func (h *EngineHandlers) Discover(ctx context.Context, params EngineDiscoverParams) (EngineDiscoverResult, error) {
	provider, ok := h.provider(params.ProviderKey)
	if !ok {
		return EngineDiscoverResult{}, errors.New("provider is not configured")
	}
	models, err := h.probes.Discover(ctx, provider)
	if err != nil {
		return EngineDiscoverResult{}, err
	}
	result := EngineDiscoverResult{Models: make([]EngineModel, 0, len(models))}
	for _, model := range models {
		result.Models = append(result.Models, EngineModel{ModelID: model.ModelID, Name: model.Name})
	}
	return result, nil
}

// Scan checks local CLI availability. Paths are deliberately discarded: the
// browser only receives recognized or unchecked status.
func (h *EngineHandlers) Scan(context.Context) (EngineScanResult, error) {
	probes := h.scanAllCLIs()
	items := make([]EngineScanItem, 0, len(probes))
	for _, probe := range probes {
		status := "unchecked"
		if probe.Found {
			status = "recognized"
		}
		items = append(items, EngineScanItem{BackendType: probe.BackendType, Status: status})
	}
	return EngineScanResult{Items: items}, nil
}

func (h *EngineHandlers) provider(key string) (llmurl.Provider, bool) {
	key = strings.TrimSpace(key)
	if key == "" || h.state == nil {
		return llmurl.Provider{}, false
	}
	meta, ok := h.state.Snapshot().LLMProviders[key]
	if !ok || strings.TrimSpace(meta.APIKey) == "" {
		return llmurl.Provider{}, false
	}
	return llmurl.Provider{Type: meta.Type, BaseURL: meta.BaseURL, APIKey: meta.APIKey}, true
}

func (h *EngineHandlers) providerAndModel(providerKey, modelKey string) (llmurl.Provider, string, bool) {
	provider, ok := h.provider(providerKey)
	if !ok {
		return llmurl.Provider{}, "", false
	}
	meta := h.state.Snapshot().LLMProviders[strings.TrimSpace(providerKey)]
	modelKey = strings.TrimSpace(modelKey)
	if modelKey == "" {
		modelKey = meta.DefaultModelKey
	}
	for _, model := range meta.Models {
		if model.ModelKey == modelKey && model.Enabled && strings.TrimSpace(model.ModelID) != "" {
			return provider, model.ModelID, true
		}
	}
	// Before multi-model state reached an existing daemon, llm.upsert persisted
	// one Model string. Preserve its executable configuration until a snapshot
	// supplies the model catalog.
	if modelKey == "" && strings.TrimSpace(meta.Model) != "" {
		return provider, meta.Model, true
	}
	return llmurl.Provider{}, "", false
}
