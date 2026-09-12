package syncqueue_repo

import (
	"context"

	"github.com/cago-frame/cago/database/db"

	"github.com/agentre-hub/agentre/internal/model/entity/syncqueue_entity"
)

//go:generate mockgen -source outbound_queue.go -destination mock_syncqueue_repo/mock_outbound_queue.go

// OutboundQueueRepo 出站方向同步队列的持久化访问（R7）：本地待上行的改动。
type OutboundQueueRepo interface {
	// CreateMany 一条语句写一批(超过 CreateManyBatchSize 自动分批,整体仍在一个
	// 事务里)。一次入队(改动本身 + 从属行)与一轮认领/补齐同一 kind 的多行都走它:
	// 逐行 INSERT 每行一次 BEGIN IMMEDIATE,与流式落库抢同一把 SQLite 写锁（要求 15）。
	CreateMany(ctx context.Context, rows []*syncqueue_entity.OutboundQueueItem) error
	ListByAccount(ctx context.Context, accountID int64) ([]*syncqueue_entity.OutboundQueueItem, error)
	// DeleteMany 一条语句删一批(超过 DeleteManyChunkSize 自动分批)。
	// 刷队列走它而不是逐行 DELETE:后者每行一个 autocommit 事务,一次刷 871 行
	// 就要取 871 次 SQLite 写锁,而这把锁与流式落库是同一把。
	DeleteMany(ctx context.Context, ids []int64) error
	// ReassignAccount 把账号 from 名下的存活行整体改记到 to 名下(一条 UPDATE)。
	// 用于匿名出站队列认领(account 0 → 当前账号):此前是先读整批行、再逐行
	// Create + Delete,M 行就是 1 次读 + 2M 次写事务且这一对不是原子的；改成集合
	// UPDATE 后原行的 id/EntitySyncID/Op/QueuedAt 都不变,只有归属换了（要求 15）。
	ReassignAccount(ctx context.Context, from, to int64) error
}

// DeleteManyChunkSize 是单条 IN (...) 里的最大占位符数。SQLite 的
// SQLITE_MAX_VARIABLE_NUMBER 在老版本上低至 999,取 500 留足余量。
const DeleteManyChunkSize = 500

// CreateManyBatchSize 是单批 INSERT 里的最大行数。OutboundQueueItem 除自增 id 外
// 每行绑定 7 个变量,100 行是 700 个,在老版本 SQLite SQLITE_MAX_VARIABLE_NUMBER=999
// 的上限之内。
const CreateManyBatchSize = 100

var defaultOutboundQueue OutboundQueueRepo

// OutboundQueue 取默认仓储单例。
func OutboundQueue() OutboundQueueRepo { return defaultOutboundQueue }

// RegisterOutboundQueue 注入仓储实现，由 bootstrap 调用一次。
func RegisterOutboundQueue(impl OutboundQueueRepo) { defaultOutboundQueue = impl }

// NewOutboundQueue 构造默认 GORM 实现。
func NewOutboundQueue() OutboundQueueRepo { return &outboundQueueRepo{} }

type outboundQueueRepo struct{}

func (r *outboundQueueRepo) CreateMany(ctx context.Context, rows []*syncqueue_entity.OutboundQueueItem) error {
	// 空切片直接返回:交给 GORM 的 CreateInBatches 在 reflectLen == 0 时仍会打开
	// 一段没有语句的事务。
	if len(rows) == 0 {
		return nil
	}
	return db.Ctx(ctx).CreateInBatches(rows, CreateManyBatchSize).Error
}

func (r *outboundQueueRepo) ListByAccount(ctx context.Context, accountID int64) ([]*syncqueue_entity.OutboundQueueItem, error) {
	var rows []*syncqueue_entity.OutboundQueueItem
	err := db.Ctx(ctx).
		Where("sync_account_id = ?", accountID).
		Order("queued_at ASC, id ASC").
		Find(&rows).Error
	return rows, err
}

func (r *outboundQueueRepo) DeleteMany(ctx context.Context, ids []int64) error {
	return deleteByIDs(ctx, &syncqueue_entity.OutboundQueueItem{}, ids)
}

func (r *outboundQueueRepo) ReassignAccount(ctx context.Context, from, to int64) error {
	return db.Ctx(ctx).Model(&syncqueue_entity.OutboundQueueItem{}).
		Where("sync_account_id = ?", from).
		Update("sync_account_id", to).Error
}
