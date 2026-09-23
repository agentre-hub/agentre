package app

import (
	"github.com/agentre-hub/agentre/internal/service/ctl_svc"

	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

// CtlExternalApprovalEvent 是桌面端全局审批弹窗队列变化时发的 Wails 事件名；payload 是
// 按入队顺序排列的待审批请求快照（[]ctl_svc.DesktopApprovalItem，JSON 数组）。前端拿它
// 渲染弹窗：第一条是当前展示的那条，数组长度是「共 M 个待审批」的 M
// （docs/specs/2026-09-22-agrctl-resource-management.md「外部调用的审批弹窗」）。
const CtlExternalApprovalEvent = "ctl:external-approval"

// registerCtlExternalApprovals 在 Startup 装上桌面端全局审批弹窗背后的队列：ctl_svc 执行者
// 挂起一条外部写请求时，队列把整份快照经这个事件推给前端；批准/拒绝走 AnswerCtlApproval。
// 没装之前外部调用的写操作一律 503（ctl_svc.RegisterExternalApprovals 的既有行为）。
func (a *App) registerCtlExternalApprovals() {
	ctl_svc.Default().RegisterExternalApprovals(ctl_svc.NewDesktopApprovalQueue(
		ctl_svc.DesktopApprovalEmitterFunc(func(items []ctl_svc.DesktopApprovalItem) {
			wailsruntime.EventsEmit(a.ctx, CtlExternalApprovalEvent, items)
		}),
	))
}

// AnswerCtlApproval 批准或拒绝一条挂在桌面端全局审批弹窗队列里的外部写请求；关闭弹窗
// （×/Esc）等同调用 allow=false（spec 决策 12）。requestID 不在队列里（已撤下、已答过、
// 或队列还没注册）时返回错误，前端据此在弹窗底栏显示错误并保留可重试。
func (a *App) AnswerCtlApproval(requestID string, allow bool) error {
	return ctl_svc.Default().AnswerExternalApproval(requestID, allow)
}
