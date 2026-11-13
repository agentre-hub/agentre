package chat_repo

import (
	"context"
	"time"

	"github.com/cago-frame/cago/database/db"
	"gorm.io/gorm/clause"

	"github.com/agentre-hub/agentre/internal/model/entity/chat_entity"
)

//go:generate mockgen -source pending_delete.go -destination mock_chat_repo/mock_pending_delete.go

// PendingDeleteRepo 是本机待删除表（chat_pending_deletes）的读写口：桌面端删了自己
// 那份之后，server / agentred 那边没删成的那一笔记在这里，等联系上再补发
// （spec 2026-11-12「删除传播」）。
type PendingDeleteRepo interface {
	// Create 记下一笔待删除；同一 (conversation_id, target) 已有时只刷新执行端信息。
	Create(ctx context.Context, row *chat_entity.PendingDelete) error
	// List 交回全部待删除，最早记下的在前。
	List(ctx context.Context) ([]*chat_entity.PendingDelete, error)
	// Remove 删掉这条对话对这个对象的那一笔；没有这一行不算错。
	Remove(ctx context.Context, conversationID string, target chat_entity.PendingDeleteTarget) error
}

var defaultPendingDelete PendingDeleteRepo

func PendingDelete() PendingDeleteRepo             { return defaultPendingDelete }
func RegisterPendingDelete(impl PendingDeleteRepo) { defaultPendingDelete = impl }
func NewPendingDelete() PendingDeleteRepo          { return &pendingDeleteRepo{} }

type pendingDeleteRepo struct{}

func (r *pendingDeleteRepo) Create(ctx context.Context, row *chat_entity.PendingDelete) error {
	if row.Createtime == 0 {
		row.Createtime = time.Now().UnixMilli()
	}
	return db.Ctx(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "conversation_id"}, {Name: "target"}},
		DoUpdates: clause.AssignmentColumns([]string{"machine_fingerprint", "device_id"}),
	}).Create(row).Error
}

func (r *pendingDeleteRepo) List(ctx context.Context) ([]*chat_entity.PendingDelete, error) {
	var rows []*chat_entity.PendingDelete
	if err := db.Ctx(ctx).Order("id ASC").Find(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}

func (r *pendingDeleteRepo) Remove(ctx context.Context, conversationID string, target chat_entity.PendingDeleteTarget) error {
	return db.Ctx(ctx).
		Where("conversation_id = ? AND target = ?", conversationID, target).
		Delete(&chat_entity.PendingDelete{}).Error
}
