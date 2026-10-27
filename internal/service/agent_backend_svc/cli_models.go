package agent_backend_svc

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"slices"
	"strconv"
	"sync"

	"github.com/cago-frame/cago/pkg/consts"
	"github.com/cago-frame/cago/pkg/i18n"
	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
	"github.com/agentre-hub/agentre/internal/pkg/code"
	"github.com/agentre-hub/agentre/internal/repository/agent_backend_repo"
	"github.com/agentre-hub/agentre/internal/service/remote_device_svc"
)

// ListCLIModelsRequest 查询保存后端所在设备的 CLI 目录；刷新会丢弃旧结果。
type ListCLIModelsRequest struct {
	BackendID int64 `json:"backendId"`
	Refresh   bool  `json:"refresh"`
}

// ListCLIModelsResponse 的 Error 保留 CLI 原始错误，供手输与重试状态展示。
type ListCLIModelsResponse struct {
	Models           []agentruntime.CLIModel `json:"models"`
	EffectiveValue   string                  `json:"effectiveValue"`
	EffectiveModelID string                  `json:"effectiveModelId"`
	Error            string                  `json:"error"`
}

type cliModelCacheEntry struct {
	mu      sync.Mutex
	config  [32]byte
	catalog *agentruntime.CLIModelCatalog
}

// ListCLIModels 每次先读配置，既覆盖本机写入也覆盖同步落地的配置变化。
// 同一设备后端的查询串行，其他后端不被慢 CLI 阻塞。
func (s *agentBackendSvc) ListCLIModels(ctx context.Context, req *ListCLIModelsRequest) (*ListCLIModelsResponse, error) {
	if req == nil || req.BackendID <= 0 {
		return nil, i18n.NewError(ctx, code.InvalidParameter)
	}
	saved, err := agent_backend_repo.AgentBackend().Find(ctx, req.BackendID)
	if err != nil {
		return nil, err
	}
	if saved == nil || saved.Status != consts.ACTIVE {
		return nil, i18n.NewError(ctx, code.AgentBackendNotFound)
	}
	response := &ListCLIModelsResponse{Models: []agentruntime.CLIModel{}}
	remote := remote_device_svc.TargetsAnotherMachine(saved.DeviceFingerprint)
	backend := *saved
	if backend.SyncID != "" {
		overlay, err := s.GetCLIOverlay(ctx, &GetCLIOverlayRequest{BackendSyncID: backend.SyncID, DeviceID: string(backend.DeviceFingerprint)})
		if err != nil {
			return nil, err
		}
		backend.CLIPath = overlay.CLIPath
	}
	querier := s.cliModelQuerier
	if remote {
		querier = s.remoteCLIModelQuerier
		if querier == nil {
			querier = remoteCLIModelQuerier{}
		}
	}
	if querier == nil {
		querier, _ = agentruntime.RuntimeFor(agent_backend_entity.BackendType(backend.Type)).(agentruntime.CLIModelQuerier)
	}
	if querier == nil {
		response.Error = "CLI model discovery is not supported for this backend yet"
		return response, nil
	}
	key := string(backend.DeviceFingerprint) + "/" + strconv.FormatInt(backend.ID, 10)
	raw, _ := json.Marshal(backend)
	config := sha256.Sum256(raw)
	value, _ := s.cliModels.LoadOrStore(key, &cliModelCacheEntry{})
	entry := value.(*cliModelCacheEntry)
	entry.mu.Lock()
	defer entry.mu.Unlock()
	if req.Refresh || entry.config != config {
		entry.catalog = nil
	}
	if entry.catalog == nil {
		queryCtx, cancel := context.WithTimeout(ctx, agentruntime.CLIModelQueryTimeout)
		defer cancel()
		catalog, err := querier.QueryCLIModels(queryCtx, agentruntime.CLIModelQueryRequest{Backend: &backend})
		if err != nil {
			logger.Ctx(ctx).Warn("agent_backend_svc.ListCLIModels: CLI query failed", zap.Int64("backendId", backend.ID), zap.Error(err))
			response.Error = err.Error()
			return response, nil
		}
		catalog.Models = slices.Clone(catalog.Models)
		entry.catalog = &catalog
		entry.config = config
	}
	response.Models = append(response.Models, entry.catalog.Models...)
	response.EffectiveValue = entry.catalog.EffectiveValue
	response.EffectiveModelID = entry.catalog.EffectiveModelID
	return response, nil
}
