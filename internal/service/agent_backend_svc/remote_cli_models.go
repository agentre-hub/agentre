package agent_backend_svc

import (
	"context"
	"errors"

	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
	"github.com/agentre-hub/agentre/internal/service/remote_device_svc"
	"github.com/agentre-hub/agentre/pkg/wire/agentrewire"
	"github.com/agentre-hub/agentre/pkg/wire/protorpc"
	"github.com/agentre-hub/agentre/pkg/wire/wirecall"
)

// remoteCLIModelQuerier 只借目标设备连接，拨号或 RPC 失败绝不回退本机。
type remoteCLIModelQuerier struct{}

func (remoteCLIModelQuerier) QueryCLIModels(ctx context.Context, req agentruntime.CLIModelQueryRequest) (agentruntime.CLIModelCatalog, error) {
	deviceID, _, err := localPairedDeviceID(ctx, req.Backend.DeviceFingerprint)
	if err != nil {
		return agentruntime.CLIModelCatalog{}, err
	}
	rds := remote_device_svc.Default()
	if rds == nil || rds.Pool() == nil {
		return agentruntime.CLIModelCatalog{}, errors.New("remote device service unavailable")
	}
	lease, err := rds.Pool().Borrow(ctx, deviceID)
	if err != nil {
		return agentruntime.CLIModelCatalog{}, err
	}
	defer lease.Release()
	return listRemoteCLIModels(ctx, lease.Client(), &agentrewire.CLIModelsListRequest{BackendType: req.Backend.Type, CliPath: req.Backend.CLIPath, EnvJson: req.Backend.EnvJSON,
		DefaultPermissionMode: req.Backend.DefaultPermissionMode, ReasoningEffort: req.Backend.ReasoningEffort})
}

func listRemoteCLIModels(ctx context.Context, conn wirecall.Caller, req *agentrewire.CLIModelsListRequest) (agentruntime.CLIModelCatalog, error) {
	response, err := wirecall.CLIModelsList(ctx, conn, req)
	if err != nil {
		// protorpc.Error.Error 含传输层前缀；界面应显示 CLI 原文而不是 RPC 编号。
		var rpcErr *protorpc.Error
		if errors.As(err, &rpcErr) {
			err = errors.New(rpcErr.Message)
		}
		return agentruntime.CLIModelCatalog{}, err
	}
	catalog := agentruntime.CLIModelCatalog{Models: make([]agentruntime.CLIModel, 0, len(response.Models)), EffectiveValue: response.EffectiveValue, EffectiveModelID: response.EffectiveModelId}
	for _, m := range response.Models {
		catalog.Models = append(catalog.Models, agentruntime.CLIModel{Value: m.Value, DisplayName: m.DisplayName, ModelID: m.ModelId, Group: m.Group, Legacy: m.Legacy})
	}
	return catalog, nil
}
