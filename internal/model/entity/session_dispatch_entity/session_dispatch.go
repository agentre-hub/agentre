// Package session_dispatch_entity 存放会话派发关系的持久化实体。
package session_dispatch_entity

// 派发方式(session_dispatches.method)。
const (
	MethodAgrctlSend = "agrctl_send"
	MethodAgentCall  = "agent_call"
)

// 派发状态(session_dispatches.status);终态取值与子代理卡 SubagentStateBlock.Status 的词表一致,
// queued 是已登记、起轮尚未成功的「等待」。
const (
	StatusQueued    = "queued"
	StatusRunning   = "running"
	StatusCompleted = "completed"
	StatusFailed    = "failed"
	StatusCanceled  = "canceled"
)

// SessionDispatch 是一次「会话 A 把任务派给会话 B」的记录(agrctl send 带不带 --wait、
// 隔离与否,以及 agent 派发)。自引入起记录,不回填历史。
//
// Finishtime 为 0 表示尚未进入终态;耗时 = Finishtime - Createtime(未终态时按现在算)。
// ReplyMessageID 是被派发方终轮最后那条 assistant 消息,隔离子会话的「最后回复」据它读出。
type SessionDispatch struct {
	ID                  int64  `gorm:"column:id;primaryKey;autoIncrement"`
	DispatcherSessionID int64  `gorm:"column:dispatcher_session_id;type:integer;not null"`
	DispatchedSessionID int64  `gorm:"column:dispatched_session_id;type:integer;not null"`
	Method              string `gorm:"column:method;type:text;not null"`
	Task                string `gorm:"column:task;type:text;not null;default:''"`
	Isolated            bool   `gorm:"column:isolated;type:integer;not null;default:0"`
	Status              string `gorm:"column:status;type:text;not null;default:'queued'"`
	ReplyMessageID      int64  `gorm:"column:reply_message_id;type:integer;not null;default:0"`
	Createtime          int64  `gorm:"column:createtime;type:bigint;not null;default:0"`
	Finishtime          int64  `gorm:"column:finishtime;type:bigint;not null;default:0"`
}

func (SessionDispatch) TableName() string { return "session_dispatches" }
