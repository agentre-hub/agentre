// Package project_repo 提供 Project 的持久化访问。
package project_repo

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/cago-frame/cago/database/db"
	"github.com/cago-frame/cago/pkg/consts"
	"gorm.io/gorm"

	"github.com/agentre-hub/agentre/internal/model/entity/project_entity"
)

//go:generate mockgen -source project.go -destination mock_project_repo/mock_project.go

// ProjectRepo Project 仓储接口（消费方约束模式）。
type ProjectRepo interface {
	Create(ctx context.Context, p *project_entity.Project) error
	Update(ctx context.Context, p *project_entity.Project) error
	Find(ctx context.Context, id int64) (*project_entity.Project, error)
	FindByName(ctx context.Context, parentID int64, name string) (*project_entity.Project, error)
	List(ctx context.Context) ([]*project_entity.Project, error)
	ListByParent(ctx context.Context, parentID int64) ([]*project_entity.Project, error)
	NextSortOrder(ctx context.Context, parentID int64) (int, error)
	ReorderSiblings(ctx context.Context, parentID int64, orderedIDs []int64) error
	// ReassignParent 把 parent_id 从 fromParentID 整批改挂到 toParentID（R11a 的
	// 合并）。WHERE 里**只有 parent_id**，刻意不带 status —— 合并后不允许留下任何
	// 指向已消失项目的引用，软删的子项目也算。
	ReassignParent(ctx context.Context, fromParentID, toParentID int64) error
	HasActiveChildren(ctx context.Context, id int64) (bool, error)
	Delete(ctx context.Context, id int64) error
}

var defaultProject ProjectRepo

// Project 取默认仓储单例。
func Project() ProjectRepo { return defaultProject }

// RegisterProject 注入仓储实现，由 bootstrap 调用一次。
func RegisterProject(impl ProjectRepo) { defaultProject = impl }

// NewProject 构造默认 GORM 实现。
func NewProject() ProjectRepo { return &projectRepo{} }

type projectRepo struct{}

func (r *projectRepo) Create(ctx context.Context, p *project_entity.Project) error {
	now := time.Now().UnixMilli()
	if p.Createtime == 0 {
		p.Createtime = now
	}
	p.Updatetime = now
	// 同步标识在行创建时就地生成，未登录期间也照常写入（R1/R12a）。
	p.EnsureSyncID()
	return db.Ctx(ctx).Create(p).Error
}

func (r *projectRepo) Update(ctx context.Context, p *project_entity.Project) error {
	p.Updatetime = time.Now().UnixMilli()
	// 迁移前已存在、还没有标识的历史行在下一次落库时补齐（JIT），已有标识的行
	// 原样保留（R1：终身不变）。
	p.EnsureSyncID()
	return db.Ctx(ctx).Save(p).Error
}

func (r *projectRepo) Find(ctx context.Context, id int64) (*project_entity.Project, error) {
	out := &project_entity.Project{}
	err := db.Ctx(ctx).Where("id = ? AND status = ?", id, consts.ACTIVE).First(out).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return out, nil
}

// FindByName 同父项目下的项目名唯一。parentID = 0 表示顶级。
func (r *projectRepo) FindByName(ctx context.Context, parentID int64, name string) (*project_entity.Project, error) {
	out := &project_entity.Project{}
	err := db.Ctx(ctx).
		Where("parent_id = ? AND name = ? AND status = ?", parentID, name, consts.ACTIVE).
		First(out).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return out, nil
}

// List 返回全部 active 项目，order 保证父先于子（id 升序即可，业务层组装树）。
func (r *projectRepo) List(ctx context.Context) ([]*project_entity.Project, error) {
	var rows []*project_entity.Project
	err := db.Ctx(ctx).
		Where("status = ?", consts.ACTIVE).
		Order("parent_id ASC, sort_order ASC, id ASC").
		Find(&rows).Error
	return rows, err
}

func (r *projectRepo) ListByParent(ctx context.Context, parentID int64) ([]*project_entity.Project, error) {
	var rows []*project_entity.Project
	err := db.Ctx(ctx).
		Where("parent_id = ? AND status = ?", parentID, consts.ACTIVE).
		Order("sort_order ASC, id ASC").
		Find(&rows).Error
	return rows, err
}

// ReassignParent 见接口注释：WHERE 里只有 parent_id，没有 status。
func (r *projectRepo) ReassignParent(ctx context.Context, fromParentID, toParentID int64) error {
	return db.Ctx(ctx).Model(&project_entity.Project{}).
		Where("parent_id = ?", fromParentID).
		Updates(map[string]any{
			"parent_id":  toParentID,
			"updatetime": time.Now().UnixMilli(),
		}).Error
}

func (r *projectRepo) NextSortOrder(ctx context.Context, parentID int64) (int, error) {
	var maxOrder int
	err := db.Ctx(ctx).Table("projects").
		Where("parent_id = ? AND status = ?", parentID, consts.ACTIVE).
		Select("COALESCE(MAX(sort_order), 0)").Row().Scan(&maxOrder)
	if err != nil {
		return 0, err
	}
	return maxOrder + 1, nil
}

func (r *projectRepo) ReorderSiblings(ctx context.Context, parentID int64, orderedIDs []int64) error {
	now := time.Now().UnixMilli()
	return db.Ctx(ctx).Transaction(func(tx *gorm.DB) error {
		for idx, id := range orderedIDs {
			sortOrder := idx + 1
			result := tx.Exec(
				"UPDATE projects SET sort_order = ?, updatetime = ? WHERE id = ? AND parent_id = ? AND status = ?",
				sortOrder, now, id, parentID, consts.ACTIVE,
			)
			if result.Error != nil {
				return result.Error
			}
			if result.RowsAffected != 1 {
				return fmt.Errorf("project reorder affected %d rows for id %d", result.RowsAffected, id)
			}
		}
		return nil
	})
}

// HasActiveChildren 删除项目前的预检 —— 有 active 子项目时拒绝。
func (r *projectRepo) HasActiveChildren(ctx context.Context, id int64) (bool, error) {
	var n int64
	err := db.Ctx(ctx).
		Model(&project_entity.Project{}).
		Where("parent_id = ? AND status = ?", id, consts.ACTIVE).
		Count(&n).Error
	if err != nil {
		return false, err
	}
	return n > 0, nil
}

// Delete 软删 —— 把 status 置为 DELETE，文件系统不动。
func (r *projectRepo) Delete(ctx context.Context, id int64) error {
	return db.Ctx(ctx).Model(&project_entity.Project{}).
		Where("id = ?", id).
		Updates(map[string]any{
			"status":     consts.DELETE,
			"updatetime": time.Now().UnixMilli(),
		}).Error
}
