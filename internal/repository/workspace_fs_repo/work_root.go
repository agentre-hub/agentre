// Package workspace_fs_repo 提供工作根认领集合(session_work_roots)的持久化访问。
package workspace_fs_repo

import (
	"context"
	"time"

	"github.com/cago-frame/cago/database/db"
	"gorm.io/gorm/clause"

	"github.com/agentre-hub/agentre/internal/model/entity/workspace_fs_entity"
)

//go:generate mockgen -source work_root.go -destination mock_workspace_fs_repo/mock_work_root.go

// WorkRootRepo 是会话已认领工作根的读写口。
type WorkRootRepo interface {
	// ListBySession 交回本会话已认领的工作根,先认领的在前。
	ListBySession(ctx context.Context, sessionID int64) ([]*workspace_fs_entity.WorkRoot, error)
	// Create 记下一个认领;同一 (session_id, path) 已有时不重复记、不报错。
	Create(ctx context.Context, row *workspace_fs_entity.WorkRoot) error
	// Delete 撤销本会话的这一个根;没有这一行不算错。
	Delete(ctx context.Context, sessionID int64, path string) error
	// DeleteBySession 删掉本会话的全部认领(会话被删除时的清理)。
	DeleteBySession(ctx context.Context, sessionID int64) error
}

var defaultWorkRoot WorkRootRepo

func WorkRoot() WorkRootRepo             { return defaultWorkRoot }
func RegisterWorkRoot(impl WorkRootRepo) { defaultWorkRoot = impl }
func NewWorkRoot() WorkRootRepo          { return &workRootRepo{} }

type workRootRepo struct{}

func (r *workRootRepo) ListBySession(ctx context.Context, sessionID int64) ([]*workspace_fs_entity.WorkRoot, error) {
	var rows []*workspace_fs_entity.WorkRoot
	if err := db.Ctx(ctx).Where("session_id = ?", sessionID).Order("id ASC").Find(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}

func (r *workRootRepo) Create(ctx context.Context, row *workspace_fs_entity.WorkRoot) error {
	if row.Createtime == 0 {
		row.Createtime = time.Now().UnixMilli()
	}
	return db.Ctx(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "session_id"}, {Name: "path"}},
		DoNothing: true,
	}).Create(row).Error
}

func (r *workRootRepo) Delete(ctx context.Context, sessionID int64, path string) error {
	return db.Ctx(ctx).
		Where("session_id = ? AND path = ?", sessionID, path).
		Delete(&workspace_fs_entity.WorkRoot{}).Error
}

func (r *workRootRepo) DeleteBySession(ctx context.Context, sessionID int64) error {
	return db.Ctx(ctx).Where("session_id = ?", sessionID).Delete(&workspace_fs_entity.WorkRoot{}).Error
}
