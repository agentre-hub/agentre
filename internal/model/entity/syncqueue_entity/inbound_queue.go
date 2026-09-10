package syncqueue_entity

// InboundQueueItem 入站队列的一行（R2a）：已收到但因引用目标未到达而暂缓落地
// 的行，保留 30 天，超期丢弃并转入 LostChange（ReasonDiscarded）。
type InboundQueueItem struct {
	ID int64 `gorm:"column:id;primaryKey;autoIncrement"`
	// SyncAccountID 这条暂缓行属于哪个账号（R13a）。
	SyncAccountID int64 `gorm:"column:sync_account_id;type:bigint;not null;default:0"`
	// EntityType 指向账号级七张表中的哪一张。
	EntityType string `gorm:"column:entity_type;type:text;not null"`
	// EntitySyncID 这条待落地的记录自己的同步标识。
	EntitySyncID string `gorm:"column:entity_sync_id;type:text;not null"`
	// PayloadJSON 完整内容，等待落地。
	PayloadJSON string `gorm:"column:payload_json;type:text;not null;default:''"`
	// ReceivedAt 收到时间（毫秒 epoch），供 30 天留存窗口计算。
	ReceivedAt int64 `gorm:"column:received_at;type:bigint;not null;default:0"`
}

func (*InboundQueueItem) TableName() string { return "sync_inbound_queue" }
