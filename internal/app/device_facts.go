package app

import (
	"github.com/agentre-hub/agentre/internal/service/device_facts_svc"

	"github.com/agentre-hub/agentre/pkg/wire/devicefp"
)

// 设备情况(spec 2026-11-25「设备与设备情况上报」):设备自己探测、自己上报的只读事实。

// ListDeviceFacts 列出参与派发的设备(本机与已配对的 agentred)各自已知的最后一份情况
// 与它的采集时间;读不到的设备标为够不着。
func (a *App) ListDeviceFacts() ([]*device_facts_svc.DeviceFactsView, error) {
	res, err := device_facts_svc.Default().List(a.ctx)
	return res, codedError(err)
}

// RefreshDeviceFacts 让一台设备立即重探并交回新情况;指纹为空即本机。
func (a *App) RefreshDeviceFacts(fingerprint devicefp.Carrier) (*device_facts_svc.DeviceFactsView, error) {
	res, err := device_facts_svc.Default().Refresh(a.ctx, fingerprint)
	return res, codedError(err)
}
