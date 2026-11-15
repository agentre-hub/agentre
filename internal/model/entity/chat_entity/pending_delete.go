package chat_entity

// PendingDeleteTarget 是一条待删除要补发给谁。桌面端删除一条对话后，其它副本的删除
// 只走一条路（spec 2026-11-12 决策 7/8）：已登录交给 server，没登录直连配对的 agentred。
type PendingDeleteTarget string

const (
	// PendingDeleteTargetServer 补发 POST /v1/saved-sessions/delete；MachineFingerprint
	// 是当初要告诉 server 的执行端（本机执行的对话为空）。
	PendingDeleteTargetServer PendingDeleteTarget = "server"
	// PendingDeleteTargetAgentred 补发 SESSION_DELETE 给 DeviceID 那台配对的 agentred；
	// MachineFingerprint 是它当时的实例指纹。
	PendingDeleteTargetAgentred PendingDeleteTarget = "agentred"
)

// PendingDelete 是本机删掉一条对话之后、另一处副本还没删成的那一笔（chat_pending_deletes）。
// 按 (conversation_id, target) 唯一：同一条对话对同一个对象只记一笔，补发成功或对方
// 答「已经没有了」就删掉这一行。
type PendingDelete struct {
	ID                 int64               `gorm:"column:id;primaryKey;autoIncrement"`
	ConversationID     string              `gorm:"column:conversation_id;type:text;not null"`
	Target             PendingDeleteTarget `gorm:"column:target;type:text;not null"`
	MachineFingerprint string              `gorm:"column:machine_fingerprint;type:text;not null;default:''"`
	DeviceID           int64               `gorm:"column:device_id;type:bigint;not null;default:0"`
	Createtime         int64               `gorm:"column:createtime;type:bigint;not null;default:0"`
}

func (PendingDelete) TableName() string { return "chat_pending_deletes" }
