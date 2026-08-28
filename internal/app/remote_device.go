package app

import (
	"errors"

	"github.com/agentre-hub/agentre/internal/service/remote_device_svc"
)

// RemoteDeviceList 返回当前已配对的全部 agentred（不含 keychain 秘密）。
func (a *App) RemoteDeviceList() ([]*remote_device_svc.DeviceView, error) {
	return remote_device_svc.Default().List(a.ctx)
}

// RemoteDeviceAdd 走完整 pair 握手并落库 + 写 keychain。
func (a *App) RemoteDeviceAdd(req remote_device_svc.AddRequest) (*remote_device_svc.DeviceView, error) {
	return remote_device_svc.Default().Add(a.ctx, req)
}

// RemoteDeviceRemove 软删行 + 清 keychain；不调远端 auth.revoke（见 spec §3.7）。
func (a *App) RemoteDeviceRemove(id int64) error {
	return remote_device_svc.Default().Remove(a.ctx, id)
}

// RemoteDeviceUpdateTLS 更新 TLS 信任配置并立即 Refresh 一次。
func (a *App) RemoteDeviceUpdateTLS(id int64, mode, pem string) (*remote_device_svc.DeviceView, error) {
	return remote_device_svc.Default().UpdateTLS(a.ctx, id, mode, pem)
}

// RemoteDeviceRefresh 走 auth.connect 探活，更新 last_seen_at / last_error。
func (a *App) RemoteDeviceRefresh(id int64) (*remote_device_svc.DeviceView, error) {
	return remote_device_svc.Default().Refresh(a.ctx, id)
}

// RemoteDeviceRename 仅改 name 字段。
func (a *App) RemoteDeviceRename(id int64, name string) error {
	return remote_device_svc.Default().Rename(a.ctx, id, name)
}

// RemoteDeviceFingerprint 返回本机设备指纹(与 LAN 配对 / 账号登录共用,见 R5)。
// 前端用它判定一条用户消息是不是本机发出的(R17:本机不带来源标识)。
func (a *App) RemoteDeviceFingerprint() (string, error) {
	if svc := remote_device_svc.Default(); svc != nil {
		return svc.DeviceFingerprint()
	}
	return "", errors.New("remote device service unavailable")
}

// RemoteDeviceListProviders 返回该 device 上 daemon 已配置的 LLM provider key 列表
// (来源:最近一次 health.ping)。前端用来渲染 sync 状态。
func (a *App) RemoteDeviceListProviders(id int64) []remote_device_svc.ProviderSummary {
	if svc := remote_device_svc.Default(); svc != nil {
		return svc.ListDeviceProviders(id)
	}
	return nil
}

// RemoteDeviceSyncProvider copies one local LLM provider, including its API key,
// to the selected remote agentred daemon after the user confirms the operation.
func (a *App) RemoteDeviceSyncProvider(id int64, providerKey string) error {
	if svc := remote_device_svc.Default(); svc != nil {
		return svc.SyncProvider(a.ctx, id, providerKey)
	}
	return errors.New("remote device service unavailable")
}
