package project_entity

import "github.com/agentre-hub/agentre/internal/model/entity/syncmeta_entity"

// ProjectAgent 是 Project ↔ Agent 多对多成员关系的关联行。
//
// 仅存「直接成员」；父项目成员**只读继承**到子项目，继承在查询时按 parent_id 链
// 上溯聚合（spec §3.3 决议 3），不入库以避免父改后子副本不同步。
type ProjectAgent struct {
	ID int64 `gorm:"column:id;primaryKey;autoIncrement"`
	// (project_id, agent_id) 是自然键，落在唯一索引上：同一个 Agent 不会在同一个
	// 项目里出现两次，行身份则由 ID 承担。
	ProjectID int64 `gorm:"column:project_id"`
	AgentID   int64 `gorm:"column:agent_id"`
	JoinedAt  int64 `gorm:"column:joined_at;type:bigint;not null;default:0"`
	// SyncMeta 账号级同步元数据（R1，366 行）。
	syncmeta_entity.SyncMeta `gorm:"embedded"`
}

func (*ProjectAgent) TableName() string { return "project_agents" }
