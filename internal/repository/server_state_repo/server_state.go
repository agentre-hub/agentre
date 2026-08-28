// Package server_state_repo 提供桌面端 server_state 单行表的访问。
package server_state_repo

import (
	"context"
	"errors"
	"time"

	"github.com/cago-frame/cago/database/db"
	"gorm.io/gorm"

	"github.com/agentre-hub/agentre/internal/model/entity/server_state_entity"
)

//go:generate mockgen -source server_state.go -destination mock_server_state_repo/mock_server_state.go

// ServerStateRepo 单行 server_state 表的访问接口。
type ServerStateRepo interface {
	// Get 返回 id=1 的状态行；表为空时返回 (nil, nil)。
	Get(ctx context.Context) (*server_state_entity.ServerState, error)
	// Save 覆盖 id=1 的全部字段（内部 Touch 更新 updatetime）。
	Save(ctx context.Context, e *server_state_entity.ServerState) error
	// ClearLoginFields 清空与「已登录」相关的字段（device_id / server_user_id / keychain_account），
	// 但保留 server_url 和 device_fingerprint，让 Settings → 联机页能保留 URL，让重新登录复用同一指纹。
	ClearLoginFields(ctx context.Context) error
}

var defaultServerState ServerStateRepo

// ServerState 取默认仓储单例。
func ServerState() ServerStateRepo { return defaultServerState }

// RegisterServerState 由 bootstrap 注入默认实现。
func RegisterServerState(impl ServerStateRepo) { defaultServerState = impl }

type serverStateRepo struct{}

// NewServerState 构造 GORM 实现。
func NewServerState() ServerStateRepo { return &serverStateRepo{} }

func (r *serverStateRepo) Get(ctx context.Context) (*server_state_entity.ServerState, error) {
	out := &server_state_entity.ServerState{}
	err := db.Ctx(ctx).First(out, int64(1)).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (r *serverStateRepo) Save(ctx context.Context, e *server_state_entity.ServerState) error {
	e.ID = 1
	e.Touch()
	return db.Ctx(ctx).Save(e).Error
}

func (r *serverStateRepo) ClearLoginFields(ctx context.Context) error {
	return db.Ctx(ctx).Model(&server_state_entity.ServerState{}).
		Where("id = ?", int64(1)).
		Updates(map[string]interface{}{
			"device_id":        int64(0),
			"server_user_id":   int64(0),
			"keychain_account": "",
			"updatetime":       time.Now().UnixMilli(),
		}).Error
}
