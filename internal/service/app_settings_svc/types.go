// Package app_settings_svc 暴露 App 全局设置项 + 本地 HTTP 代理生命周期的应用服务。
//
// 类型定义直接被 Wails 绑定层引用,会被 wails dev / wails build 提取为 TypeScript
// 类型暴露给前端,因此字段名要稳定、json tag 要明确。
package app_settings_svc

import (
	"github.com/agentre-hub/agentre/internal/pkg/httpgateway"
)

// GetRequest 按 key 读单条设置项。
type GetRequest struct {
	Key string `json:"key"`
}

// GetResponse 单条设置项。
type GetResponse struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

// SettingEntry 一条键值对，UpdateRequest.Entries 元素。
type SettingEntry struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

// UpdateRequest 批量更新设置项。同时改 proxy.listen_host + proxy.listen_port 时只触发一次 Restart。
type UpdateRequest struct {
	Entries []SettingEntry `json:"entries"`
}

// UpdateResponse 占位返回；前端拿到后通常会刷一次 GatewayStatus。
type UpdateResponse struct{}

// GatewayStatusResponse 直接复用 httpgateway.GatewayStatus，json tag 已经对齐前端。
type GatewayStatusResponse = httpgateway.GatewayStatus

// RestartGatewayResponse RestartGateway 返回当前 status，便于前端立即刷新。
type RestartGatewayResponse struct {
	Status httpgateway.GatewayStatus `json:"status"`
}
