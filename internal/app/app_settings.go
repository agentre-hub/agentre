package app

import (
	"github.com/agentre-hub/agentre/internal/service/app_settings_svc"
)

// GetAppSetting 按 key 读单条 App 全局设置。
func (a *App) GetAppSetting(req *app_settings_svc.GetRequest) (*app_settings_svc.GetResponse, error) {
	return app_settings_svc.AppSettings().Get(a.ctx, req)
}

// UpdateAppSettings 批量写入 App 全局设置；含 proxy.* 时触发一次本地 HTTP 代理 Restart。
func (a *App) UpdateAppSettings(req *app_settings_svc.UpdateRequest) (*app_settings_svc.UpdateResponse, error) {
	return app_settings_svc.AppSettings().Update(a.ctx, req)
}

// GetGatewayStatus 返回本地 HTTP 代理的当前状态（运行中 / 已停 + URL / 已挂路由）。
func (a *App) GetGatewayStatus() (*app_settings_svc.GatewayStatusResponse, error) {
	return app_settings_svc.AppSettings().GetGatewayStatus(a.ctx)
}

// RestartGateway 主动重启本地 HTTP 代理，复用当前 host / port 设置。
func (a *App) RestartGateway() (*app_settings_svc.RestartGatewayResponse, error) {
	return app_settings_svc.AppSettings().RestartGateway(a.ctx)
}
