// Package hook_repo 提供脚本 Hook 与产出事件的持久化访问。
package hook_repo

import (
	"context"
	"errors"

	"github.com/cago-frame/cago/database/db"
	"github.com/cago-frame/cago/pkg/consts"
	"gorm.io/gorm"

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
	FindByDedupeKey(ctx context.Context, hookID int64, key string) (*hook_entity.HookEvent, error)
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

func (r *hookEventRepo) FindByDedupeKey(ctx context.Context, hookID int64, key string) (*hook_entity.HookEvent, error) {
	out := &hook_entity.HookEvent{}
	err := db.Ctx(ctx).Where("hook_id = ? AND dedupe_key = ? AND status = ?", hookID, key, consts.ACTIVE).First(out).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return out, nil
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
