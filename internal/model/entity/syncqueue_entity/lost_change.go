// Package syncqueue_entity 维护本地同步骨架的三张表（387-390 行）：没能同步的
// 改动，以及分出站 / 入站两个方向的同步队列。三张表都按账号分命名空间，换账号
// 后旧账号的记录留在本地，但不参与新账号下的展示 / 上行（R13a）。
//
// 本包目前只提供数据落地；真正的入队 / 出队 / 30 天回收是后续任务，这里只给
// 它们留位置。
package syncqueue_entity

// 三类失效事件共用 LostChange 这一张表（决策 12：用户的心智是「我的改动没了
// 去哪找」，一个入口即可，每条标明原因）。
const (
	// ReasonOverwritten R5：本端的修改被他端更新覆盖（基版本不符）。
	ReasonOverwritten = "overwritten"
	// ReasonDiscarded R2a：入站暂缓行等待的引用目标超过 30 天未到达，丢弃。
	ReasonDiscarded = "discarded"
	// ReasonRejected R6a：设备离线超过墓碑保留窗口，重同步后该条改动被拒。
	ReasonRejected = "rejected"
)

// LostChange 是「没能同步的改动」列表的一行（R5、R5a）：存被覆盖 / 被丢弃 / 被拒
// 的版本正文与原因，保留 30 天，供用户在设置里追回。
type LostChange struct {
	ID int64 `gorm:"column:id;primaryKey;autoIncrement"`
	// SyncAccountID 这条记录属于哪个账号；换账号后旧账号的记录留在本地，不随
	// 新账号展示（R13a）。
	SyncAccountID int64 `gorm:"column:sync_account_id;type:bigint;not null;default:0"`
	// EntityType 指向账号级七张表中的哪一张（如 "project" / "agent"）。
	EntityType string `gorm:"column:entity_type;type:text;not null"`
	// EntitySyncID 被覆盖 / 丢弃 / 拒绝的那一版对象的同步标识，供 R5a 判断目标
	// 是否已被删除。
	EntitySyncID string `gorm:"column:entity_sync_id;type:text;not null"`
	// BaseVersion 这一版当时对应的基版本（R4a）。
	BaseVersion int64 `gorm:"column:base_version;type:bigint;not null;default:0"`
	// Reason 三选一：ReasonOverwritten / ReasonDiscarded / ReasonRejected。
	Reason string `gorm:"column:reason;type:text;not null"`
	// PayloadJSON 被覆盖 / 丢弃 / 拒绝那一版的**内容正文**（不是上行 / 下行的信封），
	// 供「可展开查看内容并恢复」。
	PayloadJSON string `gorm:"column:payload_json;type:text;not null;default:''"`
	// ScopeSyncID / AgentredFingerprint 是跨机自然键里不在正文中的那两项（决策 26）：
	// 路径记录属于哪个项目、哪台 agentred，backend 指向哪台 agentred。恢复要把这一版
	// 重新落地（R5a），少了它们路径记录解析不出归属、远端 backend 会被当成本机的。
	//
	// 叫 Scope 而不是 Project：路径记录这一段装项目，backend 那一段装的是后端
	// sync id，与 server 的 sync_objects.scope_sync_id 同义。
	ScopeSyncID         string `gorm:"column:scope_sync_id;type:text;not null;default:''"`
	AgentredFingerprint string `gorm:"column:agentred_fingerprint;type:text;not null;default:''"`
	// OriginDevice 覆盖发生的来源设备；仅「被覆盖」一类有意义。
	OriginDevice string `gorm:"column:origin_device;type:text;not null;default:''"`
	// OccurredAt 发生时间（毫秒 epoch）。
	OccurredAt int64 `gorm:"column:occurred_at;type:bigint;not null;default:0"`
	// Createtime 落库时间，供 30 天留存窗口计算。
	Createtime int64 `gorm:"column:createtime;type:bigint;not null;default:0"`
}

func (*LostChange) TableName() string { return "sync_lost_changes" }
