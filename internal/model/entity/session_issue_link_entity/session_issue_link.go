// Package session_issue_link_entity 存放「会话 ↔ 看板任务」关联的持久化实体。
package session_issue_link_entity

// SessionIssueLink 是一条「会话 S 关联了任务 I」。多对多:一个任务可以关联多个会话,
// 一个会话也可以关联多个任务;(SessionID, IssueID) 唯一。
//
// 只存本机、不同步(spec 2026-11-24 决策 23):会话本身不跨设备,关联随之只在本机有意义。
// 它独立于 issues.session_id(那一列从未被写入,保持原样,不复用)。
type SessionIssueLink struct {
	ID         int64 `gorm:"column:id;primaryKey;autoIncrement"`
	SessionID  int64 `gorm:"column:session_id;type:integer;not null"`
	IssueID    int64 `gorm:"column:issue_id;type:integer;not null"`
	Createtime int64 `gorm:"column:createtime;type:bigint;not null;default:0"`
}

func (SessionIssueLink) TableName() string { return "session_issue_links" }
