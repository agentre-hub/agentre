// Package session_dispatch_repo 提供会话派发关系(session_dispatches)的持久化访问。
package session_dispatch_repo

import (
	"context"
	"time"

	"github.com/cago-frame/cago/database/db"

	"github.com/agentre-hub/agentre/internal/model/entity/session_dispatch_entity"
)

//go:generate mockgen -source session_dispatch.go -destination mock_session_dispatch_repo/mock_session_dispatch.go

// StateChange 是一次状态推进。Finishtime / ReplyMessageID 为 0 时不写(转入运行中只带 Status)。
type StateChange struct {
	Status         string
	Finishtime     int64
	ReplyMessageID int64
}

// SessionDispatchRepo 是会话派发关系的读写口。
type SessionDispatchRepo interface {
	// Create 记下一次派发,回填 ID;Createtime 为 0 时取当前时间。
	Create(ctx context.Context, row *session_dispatch_entity.SessionDispatch) error
	// UpdateState 推进一条还没进入终态的记录;已进入终态(finishtime>0)的行不改,没有这一行不算错。
	UpdateState(ctx context.Context, id int64, change StateChange) error
	// ListByDispatcher 交回本会话派发出去的记录,先派发的在前。
	ListByDispatcher(ctx context.Context, sessionID int64) ([]*session_dispatch_entity.SessionDispatch, error)
	// ListByDispatched 交回派发了本会话的记录,先派发的在前。
	ListByDispatched(ctx context.Context, sessionID int64) ([]*session_dispatch_entity.SessionDispatch, error)
	// DeleteBySession 删掉与本会话有关的全部记录(两个方向),会话被删除时的清理。
	DeleteBySession(ctx context.Context, sessionID int64) error
}

var defaultSessionDispatch SessionDispatchRepo

func SessionDispatch() SessionDispatchRepo             { return defaultSessionDispatch }
func RegisterSessionDispatch(impl SessionDispatchRepo) { defaultSessionDispatch = impl }
func NewSessionDispatch() SessionDispatchRepo          { return &sessionDispatchRepo{} }

type sessionDispatchRepo struct{}

func (r *sessionDispatchRepo) Create(ctx context.Context, row *session_dispatch_entity.SessionDispatch) error {
	if row.Createtime == 0 {
		row.Createtime = time.Now().UnixMilli()
	}
	return db.Ctx(ctx).Create(row).Error
}

func (r *sessionDispatchRepo) UpdateState(ctx context.Context, id int64, change StateChange) error {
	updates := map[string]any{"status": change.Status}
	if change.Finishtime > 0 {
		updates["finishtime"] = change.Finishtime
	}
	if change.ReplyMessageID > 0 {
		updates["reply_message_id"] = change.ReplyMessageID
	}
	return db.Ctx(ctx).Model(&session_dispatch_entity.SessionDispatch{}).
		Where("id = ? AND finishtime = 0", id).
		Updates(updates).Error
}

func (r *sessionDispatchRepo) ListByDispatcher(ctx context.Context, sessionID int64) ([]*session_dispatch_entity.SessionDispatch, error) {
	var rows []*session_dispatch_entity.SessionDispatch
	if err := db.Ctx(ctx).Where("dispatcher_session_id = ?", sessionID).Order("id ASC").Find(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}

func (r *sessionDispatchRepo) ListByDispatched(ctx context.Context, sessionID int64) ([]*session_dispatch_entity.SessionDispatch, error) {
	var rows []*session_dispatch_entity.SessionDispatch
	if err := db.Ctx(ctx).Where("dispatched_session_id = ?", sessionID).Order("id ASC").Find(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}

func (r *sessionDispatchRepo) DeleteBySession(ctx context.Context, sessionID int64) error {
	return db.Ctx(ctx).
		Where("dispatcher_session_id = ? OR dispatched_session_id = ?", sessionID, sessionID).
		Delete(&session_dispatch_entity.SessionDispatch{}).Error
}
