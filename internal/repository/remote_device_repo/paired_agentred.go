// Package remote_device_repo 提供 paired_agentreds 表的访问。
package remote_device_repo

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/cago-frame/cago/database/db"
	"github.com/cago-frame/cago/pkg/consts"
	"gorm.io/gorm"

	"github.com/agentre-hub/agentre/internal/model/entity/paired_agentred_entity"
)

//go:generate mockgen -source paired_agentred.go -destination mock_remote_device_repo/mock_paired_agentred.go

// PairedAgentredRepo 桌面端 paired_agentreds 表的访问接口。
type PairedAgentredRepo interface {
	Create(ctx context.Context, p *paired_agentred_entity.PairedAgentred) error
	Get(ctx context.Context, id int64) (*paired_agentred_entity.PairedAgentred, error)
	FindByURL(ctx context.Context, url string) (*paired_agentred_entity.PairedAgentred, error)
	// FindByFingerprint 按 daemon 指纹取存活行。指纹是一台机器跨「LAN 配对」与
	// 「账号收编」两个来源的同一性依据（R5 硬不变量），收编的幂等与「配对一台已收编
	// 的机器要升级而不是再建一行」都靠它。
	FindByFingerprint(ctx context.Context, fingerprint string) (*paired_agentred_entity.PairedAgentred, error)
	List(ctx context.Context) ([]*paired_agentred_entity.PairedAgentred, error)
	// ListDeleted 返回软删（status=DELETE）的行。Delete 是软删，所以「用户解除过
	// 这台机器的配对」这件事只在这些行里留有记录 —— List 只给存活行，看不见它，
	// 而账号收编正需要它来分辨「本机还没有这台机器」与「本机不要这台机器」。
	ListDeleted(ctx context.Context) ([]*paired_agentred_entity.PairedAgentred, error)
	// Purge 硬删一行，且只删已经软删的行（status=DELETE）：存活行的删除永远走
	// Delete，本方法只负责回收已经逻辑消失的那些。
	Purge(ctx context.Context, id int64) error
	UpdateTLS(ctx context.Context, id int64, mode, pem string) error
	UpdateEndpoint(ctx context.Context, id int64, url, daemonFingerprint string) error
	UpdateLastSeen(ctx context.Context, id, ts int64, lastError string) error
	Rename(ctx context.Context, id int64, name string) error
	Delete(ctx context.Context, id int64) error
}

var defaultRepo PairedAgentredRepo

// PairedAgentred 取默认仓储单例。
func PairedAgentred() PairedAgentredRepo { return defaultRepo }

// RegisterPairedAgentred 由 bootstrap 注入默认实现。
func RegisterPairedAgentred(impl PairedAgentredRepo) { defaultRepo = impl }

type pairedAgentredRepo struct{}

// NewPairedAgentred 构造 GORM 实现。
func NewPairedAgentred() PairedAgentredRepo { return &pairedAgentredRepo{} }

func nowMs() int64 { return time.Now().UnixMilli() }

func (r *pairedAgentredRepo) Create(ctx context.Context, p *paired_agentred_entity.PairedAgentred) error {
	t := nowMs()
	if p.Createtime == 0 {
		p.Createtime = t
	}
	p.Updatetime = t
	return db.Ctx(ctx).Create(p).Error
}

func (r *pairedAgentredRepo) Get(ctx context.Context, id int64) (*paired_agentred_entity.PairedAgentred, error) {
	out := &paired_agentred_entity.PairedAgentred{}
	err := db.Ctx(ctx).First(out, id).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (r *pairedAgentredRepo) FindByURL(ctx context.Context, url string) (*paired_agentred_entity.PairedAgentred, error) {
	out := &paired_agentred_entity.PairedAgentred{}
	err := db.Ctx(ctx).Where("url = ? AND status = ?", url, consts.ACTIVE).First(out).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (r *pairedAgentredRepo) FindByFingerprint(
	ctx context.Context, fingerprint string,
) (*paired_agentred_entity.PairedAgentred, error) {
	// 空指纹不是「匹配所有空指纹行」而是「无从判断」：旧配对行可能还没握过手。
	// 拿空串去查会把它们混成一台机器。
	if strings.TrimSpace(fingerprint) == "" {
		return nil, nil
	}
	out := &paired_agentred_entity.PairedAgentred{}
	err := db.Ctx(ctx).
		Where("daemon_fingerprint = ? AND status = ?", fingerprint, consts.ACTIVE).First(out).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (r *pairedAgentredRepo) List(ctx context.Context) ([]*paired_agentred_entity.PairedAgentred, error) {
	var out []*paired_agentred_entity.PairedAgentred
	err := db.Ctx(ctx).Where("status = ?", consts.ACTIVE).Order("id DESC").Find(&out).Error
	return out, err
}

func (r *pairedAgentredRepo) ListDeleted(ctx context.Context) ([]*paired_agentred_entity.PairedAgentred, error) {
	var out []*paired_agentred_entity.PairedAgentred
	err := db.Ctx(ctx).Where("status = ?", consts.DELETE).Order("id DESC").Find(&out).Error
	return out, err
}

func (r *pairedAgentredRepo) Purge(ctx context.Context, id int64) error {
	// status 条件不是冗余的：它是「只回收已经软删的行」这条约束的执行点，
	// 拿掉之后一次传错的 id 就能把一台还在用的机器直接抹掉。
	return db.Ctx(ctx).
		Where("id = ? AND status = ?", id, consts.DELETE).
		Delete(&paired_agentred_entity.PairedAgentred{}).Error
}

func (r *pairedAgentredRepo) UpdateTLS(ctx context.Context, id int64, mode, pem string) error {
	return db.Ctx(ctx).Model(&paired_agentred_entity.PairedAgentred{}).
		Where("id = ?", id).
		Updates(map[string]interface{}{
			"tls_mode":     mode,
			"tls_cert_pem": pem,
			"updatetime":   nowMs(),
		}).Error
}

func (r *pairedAgentredRepo) UpdateEndpoint(ctx context.Context, id int64, url, daemonFingerprint string) error {
	return db.Ctx(ctx).Model(&paired_agentred_entity.PairedAgentred{}).
		Where("id = ?", id).
		Updates(map[string]interface{}{
			"url":                url,
			"daemon_fingerprint": daemonFingerprint,
			"updatetime":         nowMs(),
		}).Error
}

func (r *pairedAgentredRepo) UpdateLastSeen(ctx context.Context, id, ts int64, lastError string) error {
	updates := map[string]interface{}{
		"last_error": lastError,
		"updatetime": nowMs(),
	}
	if ts > 0 {
		updates["last_seen_at"] = ts
	}
	return db.Ctx(ctx).Model(&paired_agentred_entity.PairedAgentred{}).
		Where("id = ?", id).
		Updates(updates).Error
}

func (r *pairedAgentredRepo) Rename(ctx context.Context, id int64, name string) error {
	return db.Ctx(ctx).Model(&paired_agentred_entity.PairedAgentred{}).
		Where("id = ?", id).
		Updates(map[string]interface{}{
			"name":       name,
			"updatetime": nowMs(),
		}).Error
}

func (r *pairedAgentredRepo) Delete(ctx context.Context, id int64) error {
	return db.Ctx(ctx).Model(&paired_agentred_entity.PairedAgentred{}).
		Where("id = ?", id).
		Updates(map[string]interface{}{
			"status":     consts.DELETE,
			"updatetime": nowMs(),
		}).Error
}
