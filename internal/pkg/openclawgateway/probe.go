package openclawgateway

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
)

var (
	ErrSelectedAgentNotFound = errors.New("selected openclaw agent not found")
	ErrSelectedModelNotFound = errors.New("selected openclaw model not found")
	ErrRequiredMethodMissing = errors.New("required openclaw gateway method missing")
	ErrRequiredEventMissing  = errors.New("required openclaw gateway event missing")
)

var requiredRuntimeMethods = []string{
	"agent",
	"agent.wait",
	"chat.abort",
	"agents.list",
	"models.list",
	"exec.approval.list",
	"exec.approval.resolve",
}

var requiredRuntimeEvents = []string{
	"agent",
	"chat",
	"exec.approval.requested",
	"exec.approval.resolved",
}

type ProbeSelection struct {
	AgentID string
	Model   string
}

type AgentSummary struct {
	ID           string
	Name         string
	PrimaryModel string
	Fallbacks    []string
	Default      bool
}

type ModelSummary struct {
	ID        string
	Name      string
	Provider  string
	Available bool
}

type ProbeResult struct {
	GatewayVersion string
	Protocol       int
	GrantedScopes  []string
	Methods        []string
	Events         []string
	Agents         []AgentSummary
	Models         []ModelSummary
}

type agentsListResponse struct {
	DefaultID string `json:"defaultId"`
	MainKey   string `json:"mainKey"`
	Scope     string `json:"scope"`
	Agents    []struct {
		ID    string `json:"id"`
		Name  string `json:"name"`
		Model struct {
			Primary   string   `json:"primary"`
			Fallbacks []string `json:"fallbacks"`
		} `json:"model"`
	} `json:"agents"`
}

type modelsListResponse struct {
	Models []struct {
		ID        string `json:"id"`
		Name      string `json:"name"`
		Provider  string `json:"provider"`
		Available bool   `json:"available"`
	} `json:"models"`
}

// Probe performs the same Gateway-native handshake and discovery calls used by
// the runtime. It never starts an agent turn and never reads provider history.
func Probe(ctx context.Context, config Config, selection ProbeSelection) (*ProbeResult, error) {
	client, err := NewClient(config)
	if err != nil {
		return nil, err
	}
	defer client.Close()
	hello, err := client.Start(ctx)
	if err != nil {
		return nil, err
	}
	if err := ValidateRuntimeFeatures(hello); err != nil {
		return nil, err
	}

	var agentsPayload agentsListResponse
	if err := client.Call(ctx, "agents.list", map[string]any{}, &agentsPayload); err != nil {
		return nil, err
	}
	var modelsPayload modelsListResponse
	if err := client.Call(ctx, "models.list", map[string]any{"view": "configured"}, &modelsPayload); err != nil {
		return nil, err
	}

	result := &ProbeResult{
		GatewayVersion: hello.Server.Version,
		Protocol:       hello.Protocol,
		GrantedScopes:  slices.Clone(hello.Auth.Scopes),
		Methods:        slices.Clone(hello.Features.Methods),
		Events:         slices.Clone(hello.Features.Events),
		Agents:         make([]AgentSummary, 0, len(agentsPayload.Agents)),
		Models:         make([]ModelSummary, 0, len(modelsPayload.Models)),
	}
	selectedAgentFound := strings.TrimSpace(selection.AgentID) == ""
	for _, agent := range agentsPayload.Agents {
		summary := AgentSummary{
			ID:           agent.ID,
			Name:         agent.Name,
			PrimaryModel: agent.Model.Primary,
			Fallbacks:    slices.Clone(agent.Model.Fallbacks),
			Default:      agent.ID == agentsPayload.DefaultID,
		}
		result.Agents = append(result.Agents, summary)
		if agent.ID == selection.AgentID {
			selectedAgentFound = true
		}
	}
	if !selectedAgentFound {
		return nil, fmt.Errorf("%w: %s", ErrSelectedAgentNotFound, selection.AgentID)
	}

	selectedModel := strings.TrimSpace(selection.Model)
	selectedModelFound := selectedModel == ""
	for _, model := range modelsPayload.Models {
		stableID := model.ID
		if model.Provider != "" && !strings.Contains(stableID, "/") {
			stableID = model.Provider + "/" + stableID
		}
		name := model.Name
		if name == "" {
			name = stableID
		}
		result.Models = append(result.Models, ModelSummary{
			ID: stableID, Name: name, Provider: model.Provider, Available: model.Available,
		})
		if selectedModel == stableID || selectedModel == model.ID {
			selectedModelFound = true
		}
	}
	if !selectedModelFound {
		return nil, fmt.Errorf("%w: %s", ErrSelectedModelNotFound, selectedModel)
	}
	return result, nil
}

// ValidateRuntimeFeatures rejects a Gateway that negotiated protocol 4 but
// does not advertise the RPC/event surface required by the OpenClaw runtime.
func ValidateRuntimeFeatures(hello Hello) error {
	if err := requireAdvertised("method", hello.Features.Methods, requiredRuntimeMethods); err != nil {
		return err
	}
	return requireAdvertised("event", hello.Features.Events, requiredRuntimeEvents)
}

func requireAdvertised(kind string, advertised, required []string) error {
	for _, value := range required {
		if slices.Contains(advertised, value) {
			continue
		}
		if kind == "method" {
			return fmt.Errorf("%w: %s", ErrRequiredMethodMissing, value)
		}
		return fmt.Errorf("%w: %s", ErrRequiredEventMissing, value)
	}
	return nil
}
