package agent_backend_repo

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/cago-frame/cago/database/db"
	"github.com/cago-frame/cago/pkg/consts"
	"gorm.io/gorm"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
)

//go:generate mockgen -source engine_profile.go -destination mock_agent_backend_repo/mock_engine_profile.go

// EngineProfileRepo 引擎配置仓储（spec 2026-11-25 决策 2）。
type EngineProfileRepo interface {
	Create(ctx context.Context, p *agent_backend_entity.EngineProfile) error
	// Update 整行回写配置，不碰账号默认标记（只由 SetAccountDefault 改）。
	Update(ctx context.Context, p *agent_backend_entity.EngineProfile) error
	// Find 只看活配置：成员、部门与新会话只能选它们。
	Find(ctx context.Context, id int64) (*agent_backend_entity.EngineProfile, error)
	// FindAnyStatus 不看状态：已钉在被删配置上的会话按它最后的设置继续。
	FindAnyStatus(ctx context.Context, id int64) (*agent_backend_entity.EngineProfile, error)
	List(ctx context.Context) ([]*agent_backend_entity.EngineProfile, error)
	// FindAccountDefault 取账号默认那一份活配置；没有时为 nil。
	FindAccountDefault(ctx context.Context) (*agent_backend_entity.EngineProfile, error)
	// SetAccountDefault 把 id 设为唯一的账号默认：先清掉别的标记再写上它，同一事务；
	// id 不是活配置时整体回滚。
	SetAccountDefault(ctx context.Context, id int64) error
	// Delete 软删（带 updatetime）。
	Delete(ctx context.Context, id int64) error
}

var defaultEngineProfile EngineProfileRepo

// EngineProfile 取默认仓储单例。
func EngineProfile() EngineProfileRepo { return defaultEngineProfile }

// RegisterEngineProfile 注入仓储实现，由 bootstrap 调用一次。
func RegisterEngineProfile(impl EngineProfileRepo) { defaultEngineProfile = impl }

type engineProfileRepo struct{}

// NewEngineProfile 构造默认 GORM 实现。
func NewEngineProfile() EngineProfileRepo { return &engineProfileRepo{} }

func (r *engineProfileRepo) Create(ctx context.Context, p *agent_backend_entity.EngineProfile) error {
	p.EnsureSyncID()
	if err := p.MarshalConfig(); err != nil {
		return err
	}
	return db.Ctx(ctx).Create(p).Error
}

func (r *engineProfileRepo) Update(ctx context.Context, p *agent_backend_entity.EngineProfile) error {
	p.EnsureSyncID()
	if err := p.MarshalConfig(); err != nil {
		return err
	}
	// 账号默认只由 SetAccountDefault 改：整行回写一份读得早的旧实体会把标记拍回旧值。
	return db.Ctx(ctx).Omit("is_account_default").Save(p).Error
}

func (r *engineProfileRepo) Find(ctx context.Context, id int64) (*agent_backend_entity.EngineProfile, error) {
	return firstEngineProfile(db.Ctx(ctx).Where("id = ? AND status = ?", id, consts.ACTIVE))
}

func (r *engineProfileRepo) FindAnyStatus(ctx context.Context, id int64) (*agent_backend_entity.EngineProfile, error) {
	return firstEngineProfile(db.Ctx(ctx).Where("id = ?", id))
}

func (r *engineProfileRepo) FindAccountDefault(ctx context.Context) (*agent_backend_entity.EngineProfile, error) {
	return firstEngineProfile(db.Ctx(ctx).Where("is_account_default = ? AND status = ?", true, consts.ACTIVE))
}

func (r *engineProfileRepo) List(ctx context.Context) ([]*agent_backend_entity.EngineProfile, error) {
	var rows []*agent_backend_entity.EngineProfile
	if err := db.Ctx(ctx).Where("status = ?", consts.ACTIVE).Order("id ASC").Find(&rows).Error; err != nil {
		return nil, err
	}
	for _, p := range rows {
		if err := p.UnmarshalConfig(); err != nil {
			return nil, err
		}
	}
	return rows, nil
}

func (r *engineProfileRepo) SetAccountDefault(ctx context.Context, id int64) error {
	return db.Ctx(ctx).Transaction(func(tx *gorm.DB) error {
		// 先清后写：账号默认上有部分唯一索引，反过来会在两行同时带标记时撞索引。
		if err := tx.Model(&agent_backend_entity.EngineProfile{}).
			Where("is_account_default = ? AND id <> ?", true, id).
			Update("is_account_default", false).Error; err != nil {
			return err
		}
		res := tx.Model(&agent_backend_entity.EngineProfile{}).
			Where("id = ? AND status = ?", id, consts.ACTIVE).
			Update("is_account_default", true)
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected != 1 {
			return fmt.Errorf("engine profile %d is not active", id)
		}
		return nil
	})
}

func (r *engineProfileRepo) Delete(ctx context.Context, id int64) error {
	return db.Ctx(ctx).Model(&agent_backend_entity.EngineProfile{}).
		Where("id = ?", id).
		Updates(map[string]any{
			"status":     consts.DELETE,
			"updatetime": time.Now().UnixMilli(),
		}).Error
}

func firstEngineProfile(q *gorm.DB) (*agent_backend_entity.EngineProfile, error) {
	out := &agent_backend_entity.EngineProfile{}
	err := q.First(out).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return out, out.UnmarshalConfig()
}
