package chat_svc

import (
	"context"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-hub/agentre/internal/service/agent_backend_svc"
)

// withDeviceCLIOverlay 把执行这一轮的那台设备（后端 DeviceFingerprint）的 cli_path 覆盖
// （agent_backend_cli_overlays）写进交给 runtime 的后端**副本**，所有 CLI 类型一视同仁；
// 库里的后端行不动（创建/更新从不把覆盖写回行上，覆盖只活在按设备的覆盖表里）。
//
// 本机执行读本机那份；发往远端的一轮读**目标设备**那份随线过去 —— 局域网直连的
// agentred 没有账号快照，只能靠这份路径启动 CLI；有账号快照时 agentred 侧的权威覆盖
// 照旧整份替换它（daemon/handlers/runtime.go）。按目标指纹读，本机路径不会漏给远端。
// 没有覆盖（或覆盖为空）时原样返回，执行端照旧回落到 PATH。读取方式与 ListCLIModels
// 一致：后端 SyncID + 后端所在设备指纹。
func withDeviceCLIOverlay(ctx context.Context, be *agent_backend_entity.AgentBackend) (*agent_backend_entity.AgentBackend, error) {
	if be == nil || be.SyncID == "" {
		return be, nil
	}
	overlay, err := agent_backend_svc.AgentBackend().GetCLIOverlay(ctx, &agent_backend_svc.GetCLIOverlayRequest{
		BackendSyncID: be.SyncID,
		DeviceID:      string(be.DeviceFingerprint),
	})
	if err != nil {
		return nil, err
	}
	if overlay.CLIPath == "" {
		return be, nil
	}
	cp := *be
	cp.CLIPath = overlay.CLIPath
	return &cp, nil
}
