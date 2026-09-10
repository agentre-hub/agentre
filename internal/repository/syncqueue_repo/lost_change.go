// Package syncqueue_repo 提供本地同步骨架三张表的持久化访问：没能同步的改动
// （LostChangeRepo），以及分出站 / 入站两个方向的同步队列（OutboundQueueRepo /
// InboundQueueRepo）。本包目前只做基础读写；真正的入队 / 出队 / 30 天回收是
// 三张表的 30 天回收由 sync_svc 在每一轮下行结束时执行（gcDeferred / gcLostChanges）。
package syncqueue_repo

import (
	"context"
	"time"

	"github.com/cago-frame/cago/database/db"

	"github.com/agentre-hub/agentre/internal/model/entity/syncqueue_entity"
)

//go:generate mockgen -source lost_change.go -destination mock_syncqueue_repo/mock_lost_change.go

// LostChangeRepo 「没能同步的改动」列表的持久化访问（R5）。
type LostChangeRepo interface {
	Create(ctx context.Context, row *syncqueue_entity.LostChange) error
	ListByAccount(ctx context.Context, accountID int64) ([]*syncqueue_entity.LostChange, error)
	Delete(ctx context.Context, id int64) error
}

var defaultLostChange LostChangeRepo

// LostChange 取默认仓储单例。
func LostChange() LostChangeRepo { return defaultLostChange }

// RegisterLostChange 注入仓储实现，由 bootstrap 调用一次。
func RegisterLostChange(impl LostChangeRepo) { defaultLostChange = impl }

// NewLostChange 构造默认 GORM 实现。
func NewLostChange() LostChangeRepo { return &lostChangeRepo{} }

type lostChangeRepo struct{}

func (r *lostChangeRepo) Create(ctx context.Context, row *syncqueue_entity.LostChange) error {
	// Createtime 是 30 天留存窗口的判据（R5）：留 0 会让这一条在下一次回收时立刻
	// 被当成早已过期而删掉。写入方忘了填就在这里补上，别让「可追回」悄悄落空。
	now := time.Now().UnixMilli()
	if row.Createtime == 0 {
		row.Createtime = now
	}
	if row.OccurredAt == 0 {
		row.OccurredAt = now
	}
	return db.Ctx(ctx).Create(row).Error
}

func (r *lostChangeRepo) ListByAccount(ctx context.Context, accountID int64) ([]*syncqueue_entity.LostChange, error) {
	var rows []*syncqueue_entity.LostChange
	err := db.Ctx(ctx).
		Where("sync_account_id = ?", accountID).
		Order("occurred_at DESC, id DESC").
		Find(&rows).Error
	return rows, err
}

func (r *lostChangeRepo) Delete(ctx context.Context, id int64) error {
	return db.Ctx(ctx).Where("id = ?", id).Delete(&syncqueue_entity.LostChange{}).Error
}
