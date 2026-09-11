// Package hook_repo 提供脚本 Hook 与产出事件的持久化访问。
package hook_repo

import (
	"context"
	"errors"

	"github.com/cago-frame/cago/database/db"
	"github.com/cago-frame/cago/pkg/consts"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/agentre-hub/agentre/internal/model/entity/hook_entity"
)

//go:generate mockgen -source hook.go -destination mock_hook_repo/mock_hook.go

type HookRepo interface {
	Create(ctx context.Context, h *hook_entity.Hook) error
	Update(ctx context.Context, h *hook_entity.Hook) error
	Find(ctx context.Context, id int64) (*hook_entity.Hook, error)
	FindByName(ctx context.Context, name string) (*hook_entity.Hook, error)
	List(ctx context.Context) ([]*hook_entity.Hook, error)
	ListDue(ctx context.Context, now int64) ([]*hook_entity.Hook, error)
	Delete(ctx context.Context, id int64) error
}

type HookEventRepo interface {
	Create(ctx context.Context, e *hook_entity.HookEvent) error
	// CreateIfAbsent 按 (hook_id, dedupe_key) 的部分唯一索引 ux_hook_events_dedupe 判重并插入；
	// created=false 表示这一 key 已存在（本次运行内或与另一次并发运行撞车），调用方应计入
	// 重复数、跳过这一条、继续处理其余事件——不能把冲突当错误让整次运行失败。
	CreateIfAbsent(ctx context.Context, e *hook_entity.HookEvent) (created bool, err error)
	ListByHook(ctx context.Context, hookID int64, limit int) ([]*hook_entity.HookEvent, error)
	ListRecent(ctx context.Context, limit int) ([]*hook_entity.HookEvent, error)
}

var (
	defaultHook  HookRepo
	defaultEvent HookEventRepo
)

func Hook() HookRepo           { return defaultHook }
func HookEvent() HookEventRepo { return defaultEvent }

func RegisterHook(impl HookRepo)           { defaultHook = impl }
func RegisterHookEvent(impl HookEventRepo) { defaultEvent = impl }

type hookRepo struct{}
type hookEventRepo struct{}

func NewHook() HookRepo           { return &hookRepo{} }
func NewHookEvent() HookEventRepo { return &hookEventRepo{} }

func (r *hookRepo) Create(ctx context.Context, h *hook_entity.Hook) error {
	return db.Ctx(ctx).Create(h).Error
}

func (r *hookRepo) Update(ctx context.Context, h *hook_entity.Hook) error {
	return db.Ctx(ctx).Save(h).Error
}

func (r *hookRepo) Find(ctx context.Context, id int64) (*hook_entity.Hook, error) {
	out := &hook_entity.Hook{}
	err := db.Ctx(ctx).Where("id = ? AND status = ?", id, consts.ACTIVE).First(out).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (r *hookRepo) FindByName(ctx context.Context, name string) (*hook_entity.Hook, error) {
	out := &hook_entity.Hook{}
	err := db.Ctx(ctx).Where("name = ? AND status = ?", name, consts.ACTIVE).First(out).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (r *hookRepo) List(ctx context.Context) ([]*hook_entity.Hook, error) {
	var rows []*hook_entity.Hook
	if err := db.Ctx(ctx).Where("status = ?", consts.ACTIVE).Order("id ASC").Find(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}

func (r *hookRepo) ListDue(ctx context.Context, now int64) ([]*hook_entity.Hook, error) {
	var rows []*hook_entity.Hook
	if err := db.Ctx(ctx).
		Where("enabled = 1 AND next_run_at <= ? AND status = ?", now, consts.ACTIVE).
		Order("next_run_at ASC").Find(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}

func (r *hookRepo) Delete(ctx context.Context, id int64) error {
	return db.Ctx(ctx).Model(&hook_entity.Hook{}).Where("id = ?", id).Update("status", consts.DELETE).Error
}

func (r *hookEventRepo) Create(ctx context.Context, e *hook_entity.HookEvent) error {
	return db.Ctx(ctx).Create(e).Error
}

func (r *hookEventRepo) CreateIfAbsent(ctx context.Context, e *hook_entity.HookEvent) (bool, error) {
	res := db.Ctx(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(e)
	if res.Error != nil {
		return false, res.Error
	}
	return res.RowsAffected > 0, nil
}

func (r *hookEventRepo) ListByHook(ctx context.Context, hookID int64, limit int) ([]*hook_entity.HookEvent, error) {
	var rows []*hook_entity.HookEvent
	q := db.Ctx(ctx).Where("hook_id = ? AND status = ?", hookID, consts.ACTIVE).Order("received_at DESC, id DESC")
	if limit > 0 {
		q = q.Limit(limit)
	}
	if err := q.Find(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}

func (r *hookEventRepo) ListRecent(ctx context.Context, limit int) ([]*hook_entity.HookEvent, error) {
	var rows []*hook_entity.HookEvent
	q := db.Ctx(ctx).Where("status = ?", consts.ACTIVE).Order("received_at DESC, id DESC")
	if limit > 0 {
		q = q.Limit(limit)
	}
	if err := q.Find(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}
