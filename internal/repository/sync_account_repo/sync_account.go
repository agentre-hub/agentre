// Package sync_account_repo 提供「本机认得的账号」表的访问。
//
// 这张表把同步侧的归属判定从 server 的 user_id 上摘了下来：见
// sync_account_entity 的包注释。
package sync_account_repo

import (
	"context"
	"errors"
	"time"

	"github.com/cago-frame/cago/database/db"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/agentre-hub/agentre/internal/model/entity/sync_account_entity"
)

//go:generate mockgen -source sync_account.go -destination mock_sync_account_repo/mock_sync_account.go

// SyncAccountRepo 本地账号表的访问接口。
type SyncAccountRepo interface {
	// EnsureKey 交出 (serverURL, remoteUserID) 这一对在本机的账号键；本机还不认得
	// 这一对时分配一个新的。同一对永远拿到同一个键。
	EnsureKey(ctx context.Context, serverURL string, remoteUserID int64) (int64, error)
}

var defaultSyncAccount SyncAccountRepo

// SyncAccount 取默认仓储单例。
func SyncAccount() SyncAccountRepo { return defaultSyncAccount }

// RegisterSyncAccount 由 bootstrap 注入默认实现。
func RegisterSyncAccount(impl SyncAccountRepo) { defaultSyncAccount = impl }

type syncAccountRepo struct{}

// NewSyncAccount 构造 GORM 实现。
func NewSyncAccount() SyncAccountRepo { return &syncAccountRepo{} }

// EnsureKey 先查后插。插入带 ON CONFLICT DO NOTHING 并在冲突时回头再查一次：
// 「编辑当场上行」与「30 秒轮询」两条路径可能同时第一次问同一个账号，抢输的那一方
// 必须拿到赢家分配的那个键——各造一个键会把同一个账号劈成两半，一半的行从此再也
// 不参与同步。
func (r *syncAccountRepo) EnsureKey(ctx context.Context, serverURL string, remoteUserID int64) (int64, error) {
	if found, err := r.find(ctx, serverURL, remoteUserID); err != nil || found != 0 {
		return found, err
	}
	row := &sync_account_entity.SyncAccount{
		ServerURL: serverURL, RemoteUserID: remoteUserID, Createtime: time.Now().UnixMilli(),
	}
	if err := db.Ctx(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(row).Error; err != nil {
		return 0, err
	}
	if row.ID != 0 {
		return row.ID, nil
	}
	return r.find(ctx, serverURL, remoteUserID)
}

// find 返回这一对的键；本机还不认得它时返回 (0, nil)。
func (r *syncAccountRepo) find(ctx context.Context, serverURL string, remoteUserID int64) (int64, error) {
	out := &sync_account_entity.SyncAccount{}
	err := db.Ctx(ctx).
		Where("server_url = ? AND remote_user_id = ?", serverURL, remoteUserID).
		First(out).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	return out.ID, nil
}
