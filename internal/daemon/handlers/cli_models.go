package handlers

import (
	"context"
	"fmt"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
	"github.com/agentre-hub/agentre/pkg/wire/agentrewire"
)

// CLIModelsHandlers 只查询本机 runtime，不查会话进程，也不代理到其他设备。
// 桌面端入站与 agentred 共用它，查询的路径、环境始终属于被调设备。
type CLIModelsHandlers struct {
	querierFor func(agent_backend_entity.BackendType) agentruntime.CLIModelQuerier
}

func NewCLIModelsHandlers() *CLIModelsHandlers {
	return &CLIModelsHandlers{querierFor: func(bt agent_backend_entity.BackendType) agentruntime.CLIModelQuerier {
		q, _ := agentruntime.RuntimeFor(bt).(agentruntime.CLIModelQuerier)
		return q
	}}
}

// List 将设备级参数交给本地 CLI 查询，错误由宿主折成保留原文的 RPC error。
func (h *CLIModelsHandlers) List(ctx context.Context, req *agentrewire.CLIModelsListRequest) (*agentrewire.CLIModelsListResponse, error) {
	q := h.querierFor(agent_backend_entity.BackendType(req.GetBackendType()))
	if q == nil {
		return nil, fmt.Errorf("CLI model discovery is not supported for backend %q", req.GetBackendType())
	}
	catalog, err := q.QueryCLIModels(ctx, agentruntime.CLIModelQueryRequest{Backend: &agent_backend_entity.AgentBackend{
		Type: req.GetBackendType(), CLIPath: req.GetCliPath(), EnvJSON: req.GetEnvJson(),
		DefaultPermissionMode: req.GetDefaultPermissionMode(), ReasoningEffort: req.GetReasoningEffort(),
	}})
	if err != nil {
		return nil, err
	}
	response := &agentrewire.CLIModelsListResponse{Models: make([]*agentrewire.CLIModel, 0, len(catalog.Models)), EffectiveValue: catalog.EffectiveValue, EffectiveModelId: catalog.EffectiveModelID}
	for _, m := range catalog.Models {
		response.Models = append(response.Models, &agentrewire.CLIModel{Value: m.Value, DisplayName: m.DisplayName, ModelId: m.ModelID, Group: m.Group, Legacy: m.Legacy})
	}
	return response, nil
}
