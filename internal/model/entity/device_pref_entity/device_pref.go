// Package device_pref_entity 维护设备偏好的充血实体（spec 2026-11-25「组织成员与执行位置
// 解耦」设备页的可编辑项）：设备标签与它在账号设备顺序里的位置，以及仅此设备的覆盖
// ——各运行时的 CLI 路径（决策 13：按（运行时类型, 设备）存，不再按后端）。
//
// 设备身份只用 device_fingerprint（承载方角色，见 docs/architecture.md 指纹词汇）。
package device_pref_entity

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/cago-frame/cago/pkg/consts"
	"github.com/cago-frame/cago/pkg/i18n"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/syncmeta_entity"
	"github.com/agentre-hub/agentre/internal/pkg/code"

	"github.com/agentre-hub/agentre/pkg/wire/devicefp"
)

// DevicePref 一台设备的账号级偏好：标签与在账号设备顺序里的位置。
type DevicePref struct {
	ID                int64            `gorm:"column:id;primaryKey;autoIncrement"`
	DeviceFingerprint devicefp.Carrier `gorm:"column:device_fingerprint;type:text;not null;default:''"`
	LabelsJSON        string           `gorm:"column:labels_json;type:text;not null;default:'[]'"`
	// SortOrder 账号设备顺序里的位置（升序，0 起）；同值按 id 稳定排序。
	SortOrder                int   `gorm:"column:sort_order;type:int;not null;default:0"`
	Status                   int   `gorm:"column:status;type:int;not null;default:1"`
	Createtime               int64 `gorm:"column:createtime;type:bigint;not null;default:0"`
	Updatetime               int64 `gorm:"column:updatetime;type:bigint;not null;default:0"`
	syncmeta_entity.SyncMeta `gorm:"embedded"`
}

func (*DevicePref) TableName() string { return "device_prefs" }

func (p *DevicePref) IsActive() bool { return p != nil && p.Status == consts.ACTIVE }

// Labels 解出标签列表；从未设置或存的不是 JSON 数组时为空。
func (p *DevicePref) Labels() []string {
	var out []string
	if p == nil || json.Unmarshal([]byte(p.LabelsJSON), &out) != nil {
		return []string{}
	}
	return out
}

// SetLabels 写入标签：去空白、去空串、按首次出现去重，边界处归一一次。
func (p *DevicePref) SetLabels(labels []string) {
	seen := make(map[string]struct{}, len(labels))
	out := make([]string, 0, len(labels))
	for _, l := range labels {
		l = strings.TrimSpace(l)
		if l == "" {
			continue
		}
		if _, dup := seen[l]; dup {
			continue
		}
		seen[l] = struct{}{}
		out = append(out, l)
	}
	raw, _ := json.Marshal(out)
	p.LabelsJSON = string(raw)
}

// Check 字段校验；设备是否参与派发（桌面端 / agentred）是 service 的职责。
func (p *DevicePref) Check(ctx context.Context) error {
	if p == nil || strings.TrimSpace(string(p.DeviceFingerprint)) == "" {
		return i18n.NewError(ctx, code.DevicePrefInvalidDevice)
	}
	return nil
}

// CLIPathOverride 一台设备上某种运行时的 CLI 可执行文件路径覆盖；空路径 = 自动探测。
type CLIPathOverride struct {
	ID                       int64            `gorm:"column:id;primaryKey;autoIncrement"`
	RuntimeType              string           `gorm:"column:runtime_type;type:text;not null;default:''"`
	DeviceFingerprint        devicefp.Carrier `gorm:"column:device_fingerprint;type:text;not null;default:''"`
	CLIPath                  string           `gorm:"column:cli_path;type:text;not null;default:''"`
	Status                   int              `gorm:"column:status;type:int;not null;default:1"`
	Createtime               int64            `gorm:"column:createtime;type:bigint;not null;default:0"`
	Updatetime               int64            `gorm:"column:updatetime;type:bigint;not null;default:0"`
	syncmeta_entity.SyncMeta `gorm:"embedded"`
}

func (*CLIPathOverride) TableName() string { return "device_cli_paths" }

func (o *CLIPathOverride) IsActive() bool { return o != nil && o.Status == consts.ACTIVE }

// Check 只有带 CLI 可执行文件的运行时（沿用后端类型的 AllowsCLIPath）才能覆盖路径。
func (o *CLIPathOverride) Check(ctx context.Context) error {
	if o == nil {
		return i18n.NewError(ctx, code.InvalidParameter)
	}
	kind := agent_backend_entity.KindFor(agent_backend_entity.BackendType(strings.TrimSpace(o.RuntimeType)))
	if kind == nil || !kind.AllowsCLIPath() {
		return i18n.NewError(ctx, code.DeviceCLIRuntimeInvalid)
	}
	if strings.TrimSpace(string(o.DeviceFingerprint)) == "" {
		return i18n.NewError(ctx, code.DevicePrefInvalidDevice)
	}
	return nil
}
