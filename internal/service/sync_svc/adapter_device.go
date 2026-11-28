package sync_svc

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/cago-frame/cago/pkg/consts"

	"github.com/agentre-hub/agentre/internal/model/entity/device_pref_entity"
	"github.com/agentre-hub/agentre/internal/repository/device_pref_repo"
	"github.com/agentre-hub/agentre/internal/repository/syncstate_repo"
	"github.com/agentre-hub/agentre/pkg/syncwire"
)

var errDeviceKeyMissing = errors.New("sync: device-scoped payload carries no device fingerprint")

// devicePrefAdapter 同步一台设备的标签与账号设备顺序位次。设备指纹是账号内自然键，
// 在信封的 agentred_fingerprint 列上，不在载荷里。
type devicePrefAdapter struct{ baseAdapter }

func (devicePrefAdapter) kind() string { return syncwire.KindDevicePref }

func (devicePrefAdapter) load(ctx context.Context, syncID string) (*outbound, error) {
	row := &device_pref_entity.DevicePref{}
	found, err := syncstate_repo.SyncState().FindRow(ctx, syncwire.KindDevicePref, syncID, row)
	if err != nil || !found {
		return nil, err
	}
	payload, err := json.Marshal(syncwire.DevicePrefPayload{Labels: row.Labels(), SortOrder: row.SortOrder})
	if err != nil {
		return nil, err
	}
	return &outbound{
		SyncID: row.SyncID, UpdatedAt: row.Updatetime,
		AgentredFingerprint: row.DeviceFingerprint, Payload: payload,
	}, nil
}

// 设备不必在本机配对过：偏好按指纹存，不是阻塞引用。
func (devicePrefAdapter) refs(*inbound) []ref { return nil }

func (devicePrefAdapter) apply(ctx context.Context, in *inbound, _ map[string]int64) error {
	var p syncwire.DevicePrefPayload
	if err := json.Unmarshal(in.Payload, &p); err != nil {
		return err
	}
	if in.AgentredFingerprint == "" {
		return errDeviceKeyMissing
	}
	incoming := &device_pref_entity.DevicePref{}
	incoming.SetLabels(p.Labels)

	held, err := heldDevicePref(ctx, in)
	if err != nil {
		return err
	}
	if held == nil {
		row := &device_pref_entity.DevicePref{
			DeviceFingerprint: in.AgentredFingerprint, LabelsJSON: incoming.LabelsJSON,
			SortOrder: p.SortOrder, Status: consts.ACTIVE,
		}
		row.SyncID = in.SyncID
		return device_pref_repo.DevicePref().Create(ctx, row)
	}
	// 已有这台设备的偏好行：只经窄写口落两列，本机行的同步元数据不动。
	if err := device_pref_repo.DevicePref().SetLabels(ctx, held.DeviceFingerprint, incoming.LabelsJSON); err != nil {
		return err
	}
	return device_pref_repo.DevicePref().SetSortOrder(ctx, held.DeviceFingerprint, p.SortOrder)
}

// heldDevicePref 找本机已有的这台设备的偏好行：先按同步标识，再按设备指纹（自然键）。
func heldDevicePref(ctx context.Context, in *inbound) (*device_pref_entity.DevicePref, error) {
	rows, err := device_pref_repo.DevicePref().List(ctx)
	if err != nil {
		return nil, err
	}
	var byKey *device_pref_entity.DevicePref
	for _, row := range rows {
		if row == nil {
			continue
		}
		if row.SyncID == in.SyncID {
			return row, nil
		}
		if byKey == nil && row.DeviceFingerprint == in.AgentredFingerprint {
			byKey = row
		}
	}
	return byKey, nil
}

// remove：一条墓碑的含义是「这台设备不再有偏好」，落成软删。
func (devicePrefAdapter) remove(ctx context.Context, in *inbound) error {
	row := &device_pref_entity.DevicePref{}
	found, err := syncstate_repo.SyncState().FindRow(ctx, syncwire.KindDevicePref, in.SyncID, row)
	if err != nil || !found {
		return err
	}
	return device_pref_repo.DevicePref().Delete(ctx, row.ID)
}

// deviceCLIPathAdapter 同步按（运行时类型, 设备）存的 CLI 路径覆盖：运行时类型在
// ScopeSyncID 列、设备指纹在 agentred_fingerprint 列，载荷只带路径。
type deviceCLIPathAdapter struct{ baseAdapter }

func (deviceCLIPathAdapter) kind() string { return syncwire.KindDeviceCLIPath }

func (deviceCLIPathAdapter) load(ctx context.Context, syncID string) (*outbound, error) {
	row := &device_pref_entity.CLIPathOverride{}
	found, err := syncstate_repo.SyncState().FindRow(ctx, syncwire.KindDeviceCLIPath, syncID, row)
	if err != nil || !found {
		return nil, err
	}
	payload, err := json.Marshal(syncwire.DeviceCLIPathPayload{CLIPath: row.CLIPath})
	if err != nil {
		return nil, err
	}
	return &outbound{
		SyncID: row.SyncID, UpdatedAt: row.Updatetime, ScopeSyncID: row.RuntimeType,
		AgentredFingerprint: row.DeviceFingerprint, Payload: payload,
	}, nil
}

func (deviceCLIPathAdapter) refs(*inbound) []ref { return nil }

func (deviceCLIPathAdapter) apply(ctx context.Context, in *inbound, _ map[string]int64) error {
	var p syncwire.DeviceCLIPathPayload
	if err := json.Unmarshal(in.Payload, &p); err != nil {
		return err
	}
	row := &device_pref_entity.CLIPathOverride{
		RuntimeType: in.ScopeSyncID, DeviceFingerprint: in.AgentredFingerprint, CLIPath: p.CLIPath,
		Status: consts.ACTIVE,
	}
	// 运行时必须是带 CLI 的类型、设备不能为空：过不了的载荷不落库。
	if err := row.Check(ctx); err != nil {
		return err
	}
	held, err := device_pref_repo.CLIPath().Find(ctx, row.RuntimeType, row.DeviceFingerprint)
	if err != nil {
		return err
	}
	if held != nil {
		// 自然键已有本机行：只写路径，同步元数据留给本机行。
		return device_pref_repo.CLIPath().UpdatePath(ctx, held.ID, p.CLIPath)
	}
	row.SyncID = in.SyncID
	return device_pref_repo.CLIPath().Create(ctx, row)
}

func (deviceCLIPathAdapter) remove(ctx context.Context, in *inbound) error {
	row := &device_pref_entity.CLIPathOverride{}
	found, err := syncstate_repo.SyncState().FindRow(ctx, syncwire.KindDeviceCLIPath, in.SyncID, row)
	if err != nil || !found {
		return err
	}
	return device_pref_repo.CLIPath().Delete(ctx, row.ID)
}
