package app

import "github.com/agentre-hub/agentre/internal/service/sync_svc"

// SyncStatus 返回设置里同步区要展示的状态：上次成功同步时间、待同步条目数与
// 不可达原因（R12：未登录时 Enabled 为 false，界面据此让「同步」这一项整个
// 不存在，而不是显示灰态）。
func (a *App) SyncStatus() (*sync_svc.Status, error) {
	return sync_svc.Default().Status(a.ctx)
}

// SyncListLostChanges 返回「没能同步的改动」列表（R5）：被覆盖 / 引用丢失 /
// 超时未上传三种失效原因共用一份，每条可展开查看内容并恢复。
func (a *App) SyncListLostChanges() ([]*sync_svc.LostChangeView, error) {
	return sync_svc.Default().ListLostChanges(a.ctx)
}

// SyncRestoreLostChange 把某一版恢复成当前值，按 R3 正常上行；目标已在别处
// 被删除时明确失败，RestoreOutcome.TargetDeleted 据此驱动前端切换到「按这份
// 内容新建」（R5a）。
func (a *App) SyncRestoreLostChange(id int64) (*sync_svc.RestoreOutcome, error) {
	return sync_svc.Default().RestoreLostChange(a.ctx, id)
}

// SyncRecreateFromLostChange 按这份内容新建一个对象：分配新的同步标识，作为
// 一个新对象正常上行——原来那个仍然是墓碑，删除不会被复活（R5a、R6）。
func (a *App) SyncRecreateFromLostChange(id int64) error {
	return sync_svc.Default().RecreateFromLostChange(a.ctx, id)
}

// SyncDiscardLostChange 丢掉一条「没能同步的改动」记录（用户看过了、不想要了）。
func (a *App) SyncDiscardLostChange(id int64) error {
	return sync_svc.Default().DiscardLostChange(a.ctx, id)
}

// SyncNow 立即跑一次同步来回，供设置页「立即重试」按钮使用。后台轮询仍按
// 30 秒周期跑（R3）；这只是提前触发一次，结果反映在下一次 SyncStatus 里。
func (a *App) SyncNow() error {
	return sync_svc.Default().SyncOnce(a.ctx)
}

// SyncAcknowledgeBoardJoinNotice 销掉「看板首次并入同步组」那条一次性说明：
// 两台机器各自积累的历史任务在首次同步后会合并到同一个账号下，且不可逆，所以
// 说明要给在前面（Status.BoardJoinNoticePending 报出它）。界面把它展示过一次
// 之后调这个绑定，此后永不再出现。
func (a *App) SyncAcknowledgeBoardJoinNotice() error {
	return sync_svc.Default().AcknowledgeBoardJoinNotice(a.ctx)
}
