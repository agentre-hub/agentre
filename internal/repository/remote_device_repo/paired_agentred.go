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

	"github.com/agentre-hub/agentre/pkg/wire/devicefp"
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
	FindByFingerprint(ctx context.Context, fingerprint devicefp.Carrier) (*paired_agentred_entity.PairedAgentred, error)
	List(ctx context.Context) ([]*paired_agentred_entity.PairedAgentred, error)
	// ListDeleted 返回软删（status=DELETE）的行。Delete 是软删，所以「用户解除过
	// 这台机器的配对」这件事只在这些行里留有记录 —— List 只给存活行，看不见它，
	// 而账号收编正需要它来分辨「本机还没有这台机器」与「本机不要这台机器」。
	ListDeleted(ctx context.Context) ([]*paired_agentred_entity.PairedAgentred, error)
	// Purge 硬删一行，且只删已经软删的行（status=DELETE）：存活行的删除永远走
	// Delete，本方法只负责回收已经逻辑消失的那些。
	Purge(ctx context.Context, id int64) error
	UpdateTLS(ctx context.Context, id int64, mode, pem string) error
	UpdateEndpoint(ctx context.Context, id int64, url string, daemonFingerprint devicefp.Carrier) error
	UpdateLastSeen(ctx context.Context, id, ts int64, lastError string) error
	Rename(ctx context.Context, id int64, name string) error
	Delete(ctx context.Context, id int64) error
	// UpsertAccountDirect 把一行既有记录写成「来自账号的直连」的内容（D6）：地址位
	// address（由 remote_device_svc.RecordAccountDirect 决定：仍在下发列表里的最近成功地址，否则第一个）、
	// 账号一次下发的全部地址 urlsJSON、pin-cert 证书 tlsCertPEM，来源标记为 account。
	// 只更新既有行——首次记录一台从未见过的机器走 Create，不走本方法。
	UpsertAccountDirect(ctx context.Context, id int64, address, urlsJSON, tlsCertPEM string) error
	// ClearAccountDirect 把一行「来自账号的直连」的痕迹清空，退回收编行的形状
	// （D10 桌面端登出 / D14 手动配对覆盖前先清）：地址、pin-cert 证书、下发的地址
	// 列表与来源标记一并清空；keychain 里的凭据不归这里管，由调用方另外删除。
	ClearAccountDirect(ctx context.Context, id int64) error
	// UpdateDirectAddress 只更新地址位（D6：设备面板显示最近一次直连成功的地址），
	// 不动下发的全部地址列表与证书。
	UpdateDirectAddress(ctx context.Context, id int64, address string) error
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
	// 空 URL 不是「匹配所有空 URL 行」而是「无从判断」，与 FindByFingerprint 的
	// 空指纹守卫同理；拿空串去查也注定用不上下面的部分索引。
	if strings.TrimSpace(url) == "" {
		return nil, nil
	}
	out := &paired_agentred_entity.PairedAgentred{}
	// `AND url != ''` 不是多余的守卫：迁移里的部分唯一索引是
	// `ON paired_agentreds(url) WHERE status = 1 AND url != ''`，SQLite 只在能
	// 证明查询蕴含索引谓词时才用得上它——绑定变量 `url = ?` 证不出 `? != ''`。
	// 少了这一句，EXPLAIN QUERY PLAN 退回 SCAN 全表。上面已经挡掉空 URL，它不
	// 改变结果集。
	err := db.Ctx(ctx).Where("url = ? AND url != '' AND status = ?", url, consts.ACTIVE).First(out).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (r *pairedAgentredRepo) FindByFingerprint(
	ctx context.Context, fingerprint devicefp.Carrier,
) (*paired_agentred_entity.PairedAgentred, error) {
	// 空指纹不是「匹配所有空指纹行」而是「无从判断」：旧配对行可能还没握过手。
	// 拿空串去查会把它们混成一台机器。
	if strings.TrimSpace(string(fingerprint)) == "" {
		return nil, nil
	}
	out := &paired_agentred_entity.PairedAgentred{}
	// `AND daemon_fingerprint != ''` 不是多余的守卫：迁移里的部分唯一索引是
	// `ON paired_agentreds(daemon_fingerprint) WHERE status = 1 AND
	// daemon_fingerprint != ''`，同 FindByURL 的道理——绑定变量证不出 `!= ''`。
	// 上面已经挡掉空指纹，它不改变结果集。
	err := db.Ctx(ctx).
		Where("daemon_fingerprint = ? AND daemon_fingerprint != '' AND status = ?", fingerprint, consts.ACTIVE).
		First(out).Error
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

func (r *pairedAgentredRepo) UpdateEndpoint(ctx context.Context, id int64, url string, daemonFingerprint devicefp.Carrier) error {
	return db.Ctx(ctx).Model(&paired_agentred_entity.PairedAgentred{}).
		Where("id = ?", id).
		Updates(map[string]interface{}{
			"url":                url,
			"daemon_fingerprint": daemonFingerprint,
			"updatetime":         nowMs(),
		}).Error
}

func (r *pairedAgentredRepo) UpsertAccountDirect(ctx context.Context, id int64, address, urlsJSON, tlsCertPEM string) error {
	return db.Ctx(ctx).Model(&paired_agentred_entity.PairedAgentred{}).
		Where("id = ?", id).
		Updates(map[string]interface{}{
			"url":              address,
			"direct_urls_json": urlsJSON,
			"tls_mode":         "pin-cert",
			"tls_cert_pem":     tlsCertPEM,
			"origin":           "account",
			"updatetime":       nowMs(),
		}).Error
}

func (r *pairedAgentredRepo) ClearAccountDirect(ctx context.Context, id int64) error {
	return db.Ctx(ctx).Model(&paired_agentred_entity.PairedAgentred{}).
		Where("id = ?", id).
		Updates(map[string]interface{}{
			"url":              "",
			"tls_mode":         "default",
			"tls_cert_pem":     "",
			"direct_urls_json": "[]",
			"origin":           "manual",
			"updatetime":       nowMs(),
		}).Error
}

func (r *pairedAgentredRepo) UpdateDirectAddress(ctx context.Context, id int64, address string) error {
	return db.Ctx(ctx).Model(&paired_agentred_entity.PairedAgentred{}).
		Where("id = ?", id).
		Updates(map[string]interface{}{
			"url":        address,
			"updatetime": nowMs(),
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
