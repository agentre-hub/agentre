// Package device_pref_repo 提供设备偏好（标签、账号设备顺序）与按（运行时类型, 设备）
// 存的 CLI 路径覆盖的持久化访问。
package device_pref_repo

import (
	"context"
	"errors"
	"time"

	"github.com/cago-frame/cago/database/db"
	"github.com/cago-frame/cago/pkg/consts"
	"gorm.io/gorm"

	"github.com/agentre-hub/agentre/internal/model/entity/device_pref_entity"

	"github.com/agentre-hub/agentre/pkg/wire/devicefp"
)

//go:generate mockgen -source device_pref.go -destination mock_device_pref_repo/mock_device_pref.go

// DevicePrefRepo 设备偏好仓储。
type DevicePrefRepo interface {
	// List 活的偏好行，按账号设备顺序（sort_order，再按 id 稳定）。
	List(ctx context.Context) ([]*device_pref_entity.DevicePref, error)
	Create(ctx context.Context, p *device_pref_entity.DevicePref) error
	// SetSortOrder 只写位置：标签与同步元数据不被一份读得早的旧实体拍回。
	SetSortOrder(ctx context.Context, fingerprint devicefp.Carrier, sortOrder int) error
	// SetLabels 只写标签（已归一的 JSON 数组）。
	SetLabels(ctx context.Context, fingerprint devicefp.Carrier, labelsJSON string) error
	// Delete 软删：一条入站墓碑的落地口。
	Delete(ctx context.Context, id int64) error
}

var defaultDevicePref DevicePrefRepo

// DevicePref 取默认仓储单例。
func DevicePref() DevicePrefRepo { return defaultDevicePref }

// RegisterDevicePref 注入仓储实现，由 bootstrap 调用一次。
func RegisterDevicePref(impl DevicePrefRepo) { defaultDevicePref = impl }

type devicePrefRepo struct{}

// NewDevicePref 构造默认 GORM 实现。
func NewDevicePref() DevicePrefRepo { return &devicePrefRepo{} }

func (r *devicePrefRepo) List(ctx context.Context) ([]*device_pref_entity.DevicePref, error) {
	var rows []*device_pref_entity.DevicePref
	err := db.Ctx(ctx).Where("status = ?", consts.ACTIVE).Order("sort_order ASC, id ASC").Find(&rows).Error
	return rows, err
}

func (r *devicePrefRepo) Create(ctx context.Context, p *device_pref_entity.DevicePref) error {
	now := time.Now().UnixMilli()
	if p.Createtime == 0 {
		p.Createtime = now
	}
	p.Updatetime = now
	if p.Status == 0 {
		p.Status = consts.ACTIVE
	}
	if p.LabelsJSON == "" {
		p.LabelsJSON = "[]"
	}
	p.EnsureSyncID()
	return db.Ctx(ctx).Create(p).Error
}

func (r *devicePrefRepo) SetSortOrder(ctx context.Context, fingerprint devicefp.Carrier, sortOrder int) error {
	return db.Ctx(ctx).Model(&device_pref_entity.DevicePref{}).
		Where("device_fingerprint = ?", fingerprint).
		Updates(map[string]any{"sort_order": sortOrder, "updatetime": time.Now().UnixMilli()}).Error
}

func (r *devicePrefRepo) SetLabels(ctx context.Context, fingerprint devicefp.Carrier, labelsJSON string) error {
	return db.Ctx(ctx).Model(&device_pref_entity.DevicePref{}).
		Where("device_fingerprint = ?", fingerprint).
		Updates(map[string]any{"labels_json": labelsJSON, "updatetime": time.Now().UnixMilli()}).Error
}

func (r *devicePrefRepo) Delete(ctx context.Context, id int64) error {
	return db.Ctx(ctx).Model(&device_pref_entity.DevicePref{}).Where("id = ?", id).
		Updates(map[string]any{"status": consts.DELETE, "updatetime": time.Now().UnixMilli()}).Error
}

// CLIPathRepo 按（运行时类型, 设备）存的 CLI 路径覆盖仓储。
type CLIPathRepo interface {
	// Find 未命中返回 (nil, nil)。
	Find(ctx context.Context, runtimeType string, fingerprint devicefp.Carrier) (*device_pref_entity.CLIPathOverride, error)
	List(ctx context.Context) ([]*device_pref_entity.CLIPathOverride, error)
	Create(ctx context.Context, o *device_pref_entity.CLIPathOverride) error
	// UpdatePath 只写路径。
	UpdatePath(ctx context.Context, id int64, path string) error
	// Delete 软删：「空 = 自动探测」就是没有这一行。
	Delete(ctx context.Context, id int64) error
}

var defaultCLIPath CLIPathRepo

// CLIPath 取默认仓储单例。
func CLIPath() CLIPathRepo { return defaultCLIPath }

// RegisterCLIPath 注入仓储实现，由 bootstrap 调用一次。
func RegisterCLIPath(impl CLIPathRepo) { defaultCLIPath = impl }

type cliPathRepo struct{}

// NewCLIPath 构造默认 GORM 实现。
func NewCLIPath() CLIPathRepo { return &cliPathRepo{} }

func (r *cliPathRepo) Find(ctx context.Context, runtimeType string, fingerprint devicefp.Carrier) (*device_pref_entity.CLIPathOverride, error) {
	out := &device_pref_entity.CLIPathOverride{}
	err := db.Ctx(ctx).Where("runtime_type = ? AND device_fingerprint = ? AND status = ?", runtimeType, fingerprint, consts.ACTIVE).First(out).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (r *cliPathRepo) List(ctx context.Context) ([]*device_pref_entity.CLIPathOverride, error) {
	var rows []*device_pref_entity.CLIPathOverride
	err := db.Ctx(ctx).Where("status = ?", consts.ACTIVE).Order("id ASC").Find(&rows).Error
	return rows, err
}

func (r *cliPathRepo) Create(ctx context.Context, o *device_pref_entity.CLIPathOverride) error {
	now := time.Now().UnixMilli()
	if o.Createtime == 0 {
		o.Createtime = now
	}
	o.Updatetime = now
	if o.Status == 0 {
		o.Status = consts.ACTIVE
	}
	o.EnsureSyncID()
	return db.Ctx(ctx).Create(o).Error
}

func (r *cliPathRepo) UpdatePath(ctx context.Context, id int64, path string) error {
	return db.Ctx(ctx).Model(&device_pref_entity.CLIPathOverride{}).Where("id = ?", id).
		Updates(map[string]any{"cli_path": path, "updatetime": time.Now().UnixMilli()}).Error
}

func (r *cliPathRepo) Delete(ctx context.Context, id int64) error {
	return db.Ctx(ctx).Model(&device_pref_entity.CLIPathOverride{}).Where("id = ?", id).
		Updates(map[string]any{"status": consts.DELETE, "updatetime": time.Now().UnixMilli()}).Error
}
