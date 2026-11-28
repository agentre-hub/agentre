package handlers

import (
	"context"

	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/devicefacts"
	"github.com/agentre-hub/agentre/pkg/syncwire"
	"github.com/agentre-hub/agentre/pkg/wire/agentrewire"
)

// DeviceFactsSource 是这台 daemon 的设备情况(devicefacts.Reporter 满足它)。
type DeviceFactsSource interface {
	// Current 交回最近一次的情况,从没采过就当场采一次。
	Current(ctx context.Context) (syncwire.DeviceFactsReport, error)
	// Refresh 立即重探;登录了账号的 daemon 同时把新情况报给 server。
	Refresh(ctx context.Context) (syncwire.DeviceFactsReport, error)
}

// DeviceFactsHandlers 答 device.facts:未登录账号的桌面端经它直接读已配对 agentred 的
// 情况,用户在设备页点「刷新」时也经它让这台机器立即重探(规格「概念与归属」末段)。
type DeviceFactsHandlers struct {
	source DeviceFactsSource
}

// NewDeviceFactsHandlers 注入情况来源。
func NewDeviceFactsHandlers(source DeviceFactsSource) *DeviceFactsHandlers {
	return &DeviceFactsHandlers{source: source}
}

// Facts 见类型注释。探测失败时报错,不交回一份空情况冒充「什么都没装」。
func (h *DeviceFactsHandlers) Facts(ctx context.Context, request *agentrewire.DeviceFactsRequest) (*agentrewire.DeviceFactsResponse, error) {
	read := h.source.Current
	if request.GetRefresh() {
		read = h.source.Refresh
	}
	report, err := read(ctx)
	if err != nil {
		return nil, err
	}
	return devicefacts.ToWire(report), nil
}
