package app

import (
	"github.com/agentre-hub/agentre/internal/service/chat_svc"
)

// ListSessionDispatches 返回某会话两个方向的派发关系:dispatchedBy 是派发了它的会话
// (派发我的:标题 / Agent / 派发方式 / 时间),dispatched 是它派发出去的会话(我派发的:
// 任务 / Agent / 方式 / 状态 / 耗时 / 是否隔离,隔离子会话带最后一条回复)。
// 派发关系自 spec 2026-11-24 起记录,此前的会话两组都为空。
func (a *App) ListSessionDispatches(sessionID int64) (*chat_svc.SessionDispatchesView, error) {
	return chat_svc.Dispatch().ListSessionDispatches(a.ctx, sessionID)
}
