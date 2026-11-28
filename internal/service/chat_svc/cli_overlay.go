package chat_svc

import (
	"context"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-hub/agentre/internal/repository/device_pref_repo"
	"github.com/agentre-hub/agentre/internal/service/remote_device_svc"
	"github.com/agentre-hub/agentre/pkg/wire/devicefp"
)

// withDeviceCLIOverlay 把执行这一轮的那台设备上、这种运行时的 CLI 路径覆盖（spec 2026-11-25
// 决策 13：覆盖按「运行时类型 × 设备」存在 device_cli_paths）写进交给 runtime 的后端
// **副本**；库里解析出的那份后端不动。
//
// 设备取后端视图上的设备指纹；为空（引擎配置视图在本机执行时不带设备）即本机，按本机指纹
// 读。发往远端的一轮读**目标设备**那份随线过去 —— 局域网直连的 agentred 没有账号快照，
// 只能靠这份路径启动 CLI；有账号快照时 agentred 侧的权威覆盖照旧整份替换它
// （daemon/handlers/runtime.go）。按目标指纹读，本机路径不会漏给远端。
//
// 没有 CLI 的运行时不查；认不出设备（本机指纹读不出）、没有覆盖或覆盖为空时原样返回，
// 执行端回落到 PATH。
func withDeviceCLIOverlay(ctx context.Context, be *agent_backend_entity.AgentBackend) (*agent_backend_entity.AgentBackend, error) {
	if be == nil {
		return be, nil
	}
	if kind := be.Kind(); kind == nil || !kind.AllowsCLIPath() {
		return be, nil
	}
	device := be.DeviceFingerprint
	if device == "" {
		device = selfFingerprint()
	}
	if device == "" {
		return be, nil
	}
	row, err := device_pref_repo.CLIPath().Find(ctx, be.Type, device)
	if err != nil {
		return nil, err
	}
	if row == nil || row.CLIPath == "" {
		return be, nil
	}
	cp := *be
	cp.CLIPath = row.CLIPath
	return &cp, nil
}

// selfFingerprint 本机的设备指纹；读不出（服务未装配、钥匙串缺失）时为空。
func selfFingerprint() devicefp.Carrier {
	rds := remote_device_svc.Default()
	if rds == nil {
		return ""
	}
	fp, err := rds.DeviceFingerprint()
	if err != nil {
		return ""
	}
	return fp
}
