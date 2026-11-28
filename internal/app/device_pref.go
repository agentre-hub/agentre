package app

import (
	"github.com/agentre-hub/agentre/internal/service/device_pref_svc"
	"github.com/agentre-hub/agentre/internal/service/llm_provider_svc"
	"github.com/agentre-hub/agentre/internal/service/project_svc"

	"github.com/agentre-hub/agentre/pkg/wire/devicefp"
)

// 设备偏好与设备相关配置（spec 2026-11-25「设备页」可编辑项、「项目」默认设备、「LLM 供应商」
// 谁能连到）。账号设备顺序里的设备只有本机与已配对的 agentred。

// UpdateDeviceLabelsRequest 整体替换一台设备的标签。
type UpdateDeviceLabelsRequest struct {
	DeviceFingerprint devicefp.Carrier `json:"deviceFingerprint"`
	Labels            []string         `json:"labels"`
}

// DeviceCLIPathRequest 读一台设备上某运行时的 CLI 路径覆盖；设备为空 = 本机。
type DeviceCLIPathRequest struct {
	RuntimeType       string           `json:"runtimeType"`
	DeviceFingerprint devicefp.Carrier `json:"deviceFingerprint"`
}

// SetDeviceCLIPathRequest 写覆盖；路径为空 = 清除（回到自动探测）。
type SetDeviceCLIPathRequest struct {
	RuntimeType       string           `json:"runtimeType"`
	DeviceFingerprint devicefp.Carrier `json:"deviceFingerprint"`
	CLIPath           string           `json:"cliPath"`
}

// ListDevicePrefs 列出参与派发的设备（有效顺序：本机优先开着时本机第一），带标签与本地开关。
func (a *App) ListDevicePrefs() (*device_pref_svc.ListDevicesResponse, error) {
	res, err := device_pref_svc.Default().ListDevices(a.ctx)
	return res, codedError(err)
}

// ReorderDevices 按给出的顺序排前，没列出的设备保持原相对顺序。
func (a *App) ReorderDevices(order []devicefp.Carrier) error {
	return codedError(device_pref_svc.Default().Reorder(a.ctx, order))
}

// UpdateDeviceLabels 整体替换一台设备的标签。
func (a *App) UpdateDeviceLabels(req *UpdateDeviceLabelsRequest) error {
	return codedError(device_pref_svc.Default().UpdateLabels(a.ctx, req.DeviceFingerprint, req.Labels))
}

// GetDeviceLocalFirst 「在这台电脑上，本机始终排第一」；只存本机，默认开。
func (a *App) GetDeviceLocalFirst() (bool, error) {
	on, err := device_pref_svc.Default().GetLocalFirst(a.ctx)
	return on, codedError(err)
}

// SetDeviceLocalFirst 写上面的开关。
func (a *App) SetDeviceLocalFirst(on bool) error {
	return codedError(device_pref_svc.Default().SetLocalFirst(a.ctx, on))
}

// GetDeviceCLIPath 一台设备上某运行时的 CLI 路径覆盖；空串 = 自动探测。
func (a *App) GetDeviceCLIPath(req *DeviceCLIPathRequest) (string, error) {
	path, err := device_pref_svc.Default().GetCLIPath(a.ctx, req.RuntimeType, req.DeviceFingerprint)
	return path, codedError(err)
}

// SetDeviceCLIPath 写按（运行时类型, 设备）存的 CLI 路径覆盖。
func (a *App) SetDeviceCLIPath(req *SetDeviceCLIPathRequest) error {
	return codedError(device_pref_svc.Default().SetCLIPath(a.ctx, req.RuntimeType, req.DeviceFingerprint, req.CLIPath))
}

// GetProjectDefaultDevice 项目默认设备；空串 = 没设。
func (a *App) GetProjectDefaultDevice(projectID int64) (devicefp.Carrier, error) {
	fp, err := project_svc.Default().GetDefaultDevice(a.ctx, projectID)
	return fp, codedError(err)
}

// SetProjectDefaultDevice 设项目默认设备（只能是在这个项目里有路径的设备）；空串清除。
func (a *App) SetProjectDefaultDevice(projectID int64, fingerprint devicefp.Carrier) error {
	return codedError(project_svc.Default().SetDefaultDevice(a.ctx, projectID, fingerprint))
}

// GetLLMProviderReach 供应商「谁能连到」。
func (a *App) GetLLMProviderReach(req *llm_provider_svc.GetProviderReachRequest) (*llm_provider_svc.ProviderReachResponse, error) {
	res, err := llm_provider_svc.LLMProvider().GetProviderReach(a.ctx, req)
	return res, codedError(err)
}

// SetLLMProviderReach 写供应商「谁能连到」：任何设备 / 只有这些设备。
func (a *App) SetLLMProviderReach(req *llm_provider_svc.SetProviderReachRequest) (*llm_provider_svc.ProviderReachResponse, error) {
	res, err := llm_provider_svc.LLMProvider().SetProviderReach(a.ctx, req)
	return res, codedError(err)
}
