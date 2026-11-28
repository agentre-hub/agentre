package device_facts_svc

import (
	"context"

	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/devicefacts"
	"github.com/agentre-hub/agentre/internal/service/remote_device_svc"
	"github.com/agentre-hub/agentre/pkg/syncwire"
	"github.com/agentre-hub/agentre/pkg/wire/agentrewire"
	"github.com/agentre-hub/agentre/pkg/wire/devicefp"
	"github.com/agentre-hub/agentre/pkg/wire/wirecall"
)

// remoteDevices 是 Devices 的生产实现:本机指纹与已配对设备来自 remote_device_svc,
// 情况经它的连接池借一条到那台 agentred 的连接调 device.facts。
type remoteDevices struct{}

func (remoteDevices) Self() (devicefp.Carrier, error) {
	return remote_device_svc.Default().DeviceFingerprint()
}

func (remoteDevices) Paired(ctx context.Context) ([]PairedDevice, error) {
	rows, err := remote_device_svc.Default().List(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]PairedDevice, 0, len(rows))
	for _, r := range rows {
		if r != nil {
			out = append(out, PairedDevice{ID: r.ID, Fingerprint: r.DaemonFingerprint, Name: r.Name})
		}
	}
	return out, nil
}

func (remoteDevices) ReadFacts(ctx context.Context, deviceID int64, refresh bool) (syncwire.DeviceFactsReport, error) {
	lease, err := remote_device_svc.Default().Pool().Borrow(ctx, deviceID)
	if err != nil {
		return syncwire.DeviceFactsReport{}, err
	}
	defer lease.Release()
	resp, err := wirecall.DeviceFacts(ctx, lease.Client(), &agentrewire.DeviceFactsRequest{Refresh: refresh})
	if err != nil {
		return syncwire.DeviceFactsReport{}, err
	}
	return devicefacts.FromWire(resp), nil
}
