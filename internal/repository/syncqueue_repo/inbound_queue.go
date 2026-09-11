package syncqueue_repo

import (
	"context"
	"database/sql"

	"github.com/cago-frame/cago/database/db"
	"gorm.io/gorm"

	"github.com/agentre-hub/agentre/internal/model/entity/syncqueue_entity"
)

//go:generate mockgen -source inbound_queue.go -destination mock_syncqueue_repo/mock_inbound_queue.go

// InboundQueueRepo 入站方向同步队列的持久化访问（R2a）：已收到但因引用目标未
// 到达而暂缓落地的行。
type InboundQueueRepo interface {
	Create(ctx context.Context, row *syncqueue_entity.InboundQueueItem) error
	ListByAccount(ctx context.Context, accountID int64) ([]*syncqueue_entity.InboundQueueItem, error)
	// ListExpired 只取收到时间不晚于 cutoff 的行（30 天回收）：未到期的一行都不读回来。
	ListExpired(ctx context.Context, accountID, cutoff int64) ([]*syncqueue_entity.InboundQueueItem, error)
	// ReplaceForEntity 在一个事务里把同一个同步标识的旧行换成 row，并把 row.ReceivedAt
	// 改成旧行里最早的那个收到时间：30 天窗口从「第一次等不到引用」开始算。
	ReplaceForEntity(ctx context.Context, row *syncqueue_entity.InboundQueueItem) error
	Delete(ctx context.Context, id int64) error
	// DeleteByEntity 删掉某个同步标识在队列里的全部行。
	DeleteByEntity(ctx context.Context, accountID int64, kind, syncID string) error
	// DeleteMany 一条语句删一批（超过 DeleteManyChunkSize 自动分批）：每条写语句在
	// 桌面端都是一次 BEGIN IMMEDIATE，与流式落库抢同一把写锁。
	DeleteMany(ctx context.Context, ids []int64) error
}

var defaultInboundQueue InboundQueueRepo

// InboundQueue 取默认仓储单例。
func InboundQueue() InboundQueueRepo { return defaultInboundQueue }

// RegisterInboundQueue 注入仓储实现，由 bootstrap 调用一次。
func RegisterInboundQueue(impl InboundQueueRepo) { defaultInboundQueue = impl }

// NewInboundQueue 构造默认 GORM 实现。
func NewInboundQueue() InboundQueueRepo { return &inboundQueueRepo{} }

type inboundQueueRepo struct{}

func (r *inboundQueueRepo) Create(ctx context.Context, row *syncqueue_entity.InboundQueueItem) error {
	return db.Ctx(ctx).Create(row).Error
}

func (r *inboundQueueRepo) ListByAccount(ctx context.Context, accountID int64) ([]*syncqueue_entity.InboundQueueItem, error) {
	var rows []*syncqueue_entity.InboundQueueItem
	err := db.Ctx(ctx).
		Where("sync_account_id = ?", accountID).
		Order("received_at ASC, id ASC").
		Find(&rows).Error
	return rows, err
}

func (r *inboundQueueRepo) ListExpired(ctx context.Context, accountID, cutoff int64) ([]*syncqueue_entity.InboundQueueItem, error) {
	var rows []*syncqueue_entity.InboundQueueItem
	err := db.Ctx(ctx).
		Where("sync_account_id = ?", accountID).
		Where("received_at <= ?", cutoff).
		Order("received_at ASC, id ASC").
		Find(&rows).Error
	return rows, err
}

func (r *inboundQueueRepo) ReplaceForEntity(ctx context.Context, row *syncqueue_entity.InboundQueueItem) error {
	return db.Ctx(ctx).Transaction(func(tx *gorm.DB) error {
		var earliest sql.NullInt64
		if err := sameEntity(tx.Model(&syncqueue_entity.InboundQueueItem{}), row.SyncAccountID, row.EntityType, row.EntitySyncID).
			Where("received_at > 0").
			Select("MIN(received_at)").
			Row().Scan(&earliest); err != nil {
			return err
		}
		if earliest.Valid && (row.ReceivedAt == 0 || earliest.Int64 < row.ReceivedAt) {
			row.ReceivedAt = earliest.Int64
		}
		if err := sameEntity(tx, row.SyncAccountID, row.EntityType, row.EntitySyncID).
			Delete(&syncqueue_entity.InboundQueueItem{}).Error; err != nil {
			return err
		}
		return tx.Create(row).Error
	})
}

func (r *inboundQueueRepo) Delete(ctx context.Context, id int64) error {
	return db.Ctx(ctx).Where("id = ?", id).Delete(&syncqueue_entity.InboundQueueItem{}).Error
}

func (r *inboundQueueRepo) DeleteByEntity(ctx context.Context, accountID int64, kind, syncID string) error {
	return sameEntity(db.Ctx(ctx), accountID, kind, syncID).Delete(&syncqueue_entity.InboundQueueItem{}).Error
}

func (r *inboundQueueRepo) DeleteMany(ctx context.Context, ids []int64) error {
	return deleteByIDs(ctx, &syncqueue_entity.InboundQueueItem{}, ids)
}

// sameEntity 限定到某个账号下某个同步标识的那些行（idx_sync_inbound_queue_account 的前缀）。
func sameEntity(q *gorm.DB, accountID int64, kind, syncID string) *gorm.DB {
	return q.Where("sync_account_id = ?", accountID).
		Where("entity_type = ?", kind).
		Where("entity_sync_id = ?", syncID)
}

// deleteByIDs 按主键分批删除，每批至多 DeleteManyChunkSize 个占位符。空列表直接返回：
// 交给 GORM 会生成一条没有 WHERE 的 DELETE。
func deleteByIDs(ctx context.Context, model any, ids []int64) error {
	for len(ids) > 0 {
		n := min(len(ids), DeleteManyChunkSize)
		if err := db.Ctx(ctx).Where("id IN ?", ids[:n]).Delete(model).Error; err != nil {
			return err
		}
		ids = ids[n:]
	}
	return nil
}
