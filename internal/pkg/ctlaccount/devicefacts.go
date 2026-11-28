package ctlaccount

import (
	"context"
	"fmt"

	"github.com/agentre-hub/agentre/internal/pkg/cagoenvelope"
	"github.com/agentre-hub/agentre/pkg/syncwire"
)

// DeviceFacts 是 GET /v1/devices/{id}/facts 的应答（server 任务实现，这里是 agrctl 假定的
// 形状）：Reported=false 表示这台设备还没上报过；上报过时 Report 是它最后一份整份上报，
// 与设备 POST /v1/devices/facts 的请求体同一个类型。
type DeviceFacts struct {
	Reported bool                        `json:"reported"`
	Report   *syncwire.DeviceFactsReport `json:"report"`
}

// GetDeviceFacts 读一台设备上报的情况（运行时、服务型引擎的端点、本机项目路径…）。
func GetDeviceFacts(ctx context.Context, srv Server, deviceID int64) (DeviceFacts, error) {
	raw, err := srv.GetRaw(ctx, fmt.Sprintf("/v1/devices/%d/facts", deviceID))
	if err != nil {
		return DeviceFacts{}, ServerErr(err)
	}
	var out DeviceFacts
	if err := cagoenvelope.Decode(raw, &out); err != nil {
		return DeviceFacts{}, fmt.Errorf("decode the device facts: %w", err)
	}
	return out, nil
}
