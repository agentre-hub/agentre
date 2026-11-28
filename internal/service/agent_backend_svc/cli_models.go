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
	"github.com/agentre-hub/agentre/internal/repository/device_pref_repo"
	"github.com/agentre-hub/agentre/internal/service/remote_device_svc"
	"github.com/agentre-hub/agentre/pkg/wire/devicefp"
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
	backend := *saved
	if backend.SyncID != "" {
		overlay, err := s.GetCLIOverlay(ctx, &GetCLIOverlayRequest{BackendSyncID: backend.SyncID, DeviceID: string(backend.DeviceFingerprint)})
		if err != nil {
			return nil, err
		}
		backend.CLIPath = overlay.CLIPath
	}
	return s.queryCLIModels(ctx, backend, req.Refresh), nil
}

// ListEngineProfileCLIModelsRequest 查询引擎配置在某台设备上的 CLI 目录。Device 空 = 本机。
type ListEngineProfileCLIModelsRequest struct {
	EngineProfileID int64            `json:"engineProfileId"`
	Device          devicefp.Carrier `json:"device"`
	Refresh         bool             `json:"refresh"`
}

// ListEngineProfileCLIModels 是 ListCLIModels 的引擎配置形态（决策 10）：配置不是
// agent_backends 的一行、没有设备，目录按「配置摊成的视图 × 执行它的设备上这种运行时的
// CLI 路径（device_cli_paths）」查，设备在远端时走远端查询。会话钉住的配置可能已被软删，
// 仍按它最后的设置作答。
func (s *agentBackendSvc) ListEngineProfileCLIModels(ctx context.Context, req *ListEngineProfileCLIModelsRequest) (*ListCLIModelsResponse, error) {
	if req == nil || req.EngineProfileID <= 0 {
		return nil, i18n.NewError(ctx, code.InvalidParameter)
	}
	profile, err := agent_backend_repo.EngineProfile().FindAnyStatus(ctx, req.EngineProfileID)
	if err != nil {
		return nil, err
	}
	if profile == nil {
		return nil, i18n.NewError(ctx, code.EngineProfileNotFound)
	}
	backend := *profile.AsBackend()
	backend.ID = profile.ID
	backend.DeviceFingerprint = req.Device
	pathDevice := req.Device
	if pathDevice == "" {
		if rds := remote_device_svc.Default(); rds != nil {
			if fp, err := rds.DeviceFingerprint(); err == nil {
				pathDevice = fp
			}
		}
	}
	if pathDevice != "" {
		row, err := device_pref_repo.CLIPath().Find(ctx, backend.Type, pathDevice)
		if err != nil {
			return nil, err
		}
		if row != nil {
			backend.CLIPath = row.CLIPath
		}
	}
	return s.queryCLIModels(ctx, backend, req.Refresh), nil
}

// queryCLIModels 是两种入口共用的查询与缓存：backend 已带上执行设备与该设备上的 CLI 路径。
// 缓存键含设备与「后端 / 引擎配置」两个命名空间，配置指纹变化时重新查询。
func (s *agentBackendSvc) queryCLIModels(ctx context.Context, backend agent_backend_entity.AgentBackend, refresh bool) *ListCLIModelsResponse {
	response := &ListCLIModelsResponse{Models: []agentruntime.CLIModel{}}
	remote := remote_device_svc.TargetsAnotherMachine(backend.DeviceFingerprint)
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
		return response
	}
	key := string(backend.DeviceFingerprint) + "/" + strconv.FormatInt(backend.ID, 10)
	raw, _ := json.Marshal(backend)
	config := sha256.Sum256(raw)
	value, _ := s.cliModels.LoadOrStore(key, &cliModelCacheEntry{})
	entry := value.(*cliModelCacheEntry)
	entry.mu.Lock()
	defer entry.mu.Unlock()
	if refresh || entry.config != config {
		entry.catalog = nil
	}
	if entry.catalog == nil {
		queryCtx, cancel := context.WithTimeout(ctx, agentruntime.CLIModelQueryTimeout)
		defer cancel()
		catalog, err := querier.QueryCLIModels(queryCtx, agentruntime.CLIModelQueryRequest{Backend: &backend})
		if err != nil {
			logger.Ctx(ctx).Warn("agent_backend_svc.queryCLIModels: CLI query failed", zap.Int64("backendId", backend.ID), zap.Error(err))
			response.Error = err.Error()
			return response
		}
		catalog.Models = slices.Clone(catalog.Models)
		entry.catalog = &catalog
		entry.config = config
	}
	response.Models = append(response.Models, entry.catalog.Models...)
	response.EffectiveValue = entry.catalog.EffectiveValue
	response.EffectiveModelID = entry.catalog.EffectiveModelID
	return response
}
