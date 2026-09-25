package app

import (
	"github.com/agentre-hub/agentre/internal/service/notification_svc"

	"github.com/cago-frame/cago/pkg/logger"
	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"
	"go.uber.org/zap"
)

// ShowNotification 弹一条系统通知；文案已由前端按 i18n 生成。
func (a *App) ShowNotification(req *notification_svc.ShowRequest) error {
	return notification_svc.Notification().Show(a.ctx, req)
}

// registerNotificationHandlers 在 Startup 调用：初始化 Wails 通知 + 注册点击回调。
// 非 bundle / 旧系统下 InitializeNotifications 报错 → 仅告警降级。
func (a *App) registerNotificationHandlers() {
	if err := wailsruntime.InitializeNotifications(a.ctx); err != nil {
		logger.Ctx(a.ctx).Warn("app.registerNotificationHandlers: init notifications", zap.Error(err))
	}
	wailsruntime.OnNotificationResponse(a.ctx, func(res wailsruntime.NotificationResult) {
		if res.Error != nil {
			return
		}
		show, sid := notificationClickAction(res.Response.UserInfo)
		if !show {
			return
		}
		wailsruntime.WindowUnminimise(a.ctx)
		wailsruntime.WindowShow(a.ctx)
		if sid > 0 {
			wailsruntime.EventsEmit(a.ctx, "notification:click", sid)
		}
	})
}

// notificationClickAction 决定点击通知后做什么：本应用发出的通知（userInfo 带 sessionID）
// 一律切回窗口；sessionID > 0 时再跳到那个会话。没有会话的通知（如外部 agrctl 审批）
// 切回窗口后由常驻的全局弹窗自己露出来。
func notificationClickAction(userInfo map[string]interface{}) (show bool, sessionID int64) {
	if _, ok := userInfo["sessionID"]; !ok {
		return false, 0
	}
	return true, sessionIDFromUserInfo(userInfo)
}

// sessionIDFromUserInfo 从通知 userInfo 取 sessionID，兼容 JSON 往返后的 float64、int64、int；
// 缺失或非法返回 0。
func sessionIDFromUserInfo(userInfo map[string]interface{}) int64 {
	v, ok := userInfo["sessionID"]
	if !ok {
		return 0
	}
	switch x := v.(type) {
	case float64:
		return int64(x)
	case int64:
		return x
	case int:
		return int64(x)
	default:
		return 0
	}
}
