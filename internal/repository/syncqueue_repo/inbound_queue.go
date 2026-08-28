package syncqueue_repo

import (
	"context"

	"github.com/cago-frame/cago/database/db"

	"github.com/agentre-hub/agentre/internal/model/entity/syncqueue_entity"
)

//go:generate mockgen -source inbound_queue.go -destination mock_syncqueue_repo/mock_inbound_queue.go

// InboundQueueRepo 入站方向同步队列的持久化访问（R2a）：已收到但因引用目标未
// 到达而暂缓落地的行。
type InboundQueueRepo interface {
	Create(ctx context.Context, row *syncqueue_entity.InboundQueueItem) error
	ListByAccount(ctx context.Context, accountID int64) ([]*syncqueue_entity.InboundQueueItem, error)
	Delete(ctx context.Context, id int64) error
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

func (r *inboundQueueRepo) Delete(ctx context.Context, id int64) error {
	return db.Ctx(ctx).Where("id = ?", id).Delete(&syncqueue_entity.InboundQueueItem{}).Error
}
