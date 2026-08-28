package syncqueue_repo

import (
	"context"

	"github.com/cago-frame/cago/database/db"

	"github.com/agentre-hub/agentre/internal/model/entity/syncqueue_entity"
)

//go:generate mockgen -source outbound_queue.go -destination mock_syncqueue_repo/mock_outbound_queue.go

// OutboundQueueRepo 出站方向同步队列的持久化访问（R7）：本地待上行的改动。
type OutboundQueueRepo interface {
	Create(ctx context.Context, row *syncqueue_entity.OutboundQueueItem) error
	ListByAccount(ctx context.Context, accountID int64) ([]*syncqueue_entity.OutboundQueueItem, error)
	Delete(ctx context.Context, id int64) error
	// DeleteMany 一条语句删一批(超过 DeleteManyChunkSize 自动分批)。
	// 刷队列必须走它而不是 for + Delete:后者每行一个 autocommit 事务,一次刷 871 行
	// 就要取 871 次 SQLite 写锁,而这把锁与流式落库是同一把。
	DeleteMany(ctx context.Context, ids []int64) error
}

// DeleteManyChunkSize 是单条 IN (...) 里的最大占位符数。SQLite 的
// SQLITE_MAX_VARIABLE_NUMBER 在老版本上低至 999,取 500 留足余量。
const DeleteManyChunkSize = 500

var defaultOutboundQueue OutboundQueueRepo

// OutboundQueue 取默认仓储单例。
func OutboundQueue() OutboundQueueRepo { return defaultOutboundQueue }

// RegisterOutboundQueue 注入仓储实现，由 bootstrap 调用一次。
func RegisterOutboundQueue(impl OutboundQueueRepo) { defaultOutboundQueue = impl }

// NewOutboundQueue 构造默认 GORM 实现。
func NewOutboundQueue() OutboundQueueRepo { return &outboundQueueRepo{} }

type outboundQueueRepo struct{}

func (r *outboundQueueRepo) Create(ctx context.Context, row *syncqueue_entity.OutboundQueueItem) error {
	return db.Ctx(ctx).Create(row).Error
}

func (r *outboundQueueRepo) ListByAccount(ctx context.Context, accountID int64) ([]*syncqueue_entity.OutboundQueueItem, error) {
	var rows []*syncqueue_entity.OutboundQueueItem
	err := db.Ctx(ctx).
		Where("sync_account_id = ?", accountID).
		Order("queued_at ASC, id ASC").
		Find(&rows).Error
	return rows, err
}

func (r *outboundQueueRepo) Delete(ctx context.Context, id int64) error {
	return db.Ctx(ctx).Where("id = ?", id).Delete(&syncqueue_entity.OutboundQueueItem{}).Error
}

func (r *outboundQueueRepo) DeleteMany(ctx context.Context, ids []int64) error {
	// 空列表直接返回:交给 GORM 会生成一条没有 WHERE 的 DELETE,清空整张表。
	for len(ids) > 0 {
		n := min(len(ids), DeleteManyChunkSize)
		if err := db.Ctx(ctx).
			Where("id IN ?", ids[:n]).
			Delete(&syncqueue_entity.OutboundQueueItem{}).Error; err != nil {
			return err
		}
		ids = ids[n:]
	}
	return nil
}
