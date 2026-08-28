package app

import (
	"strconv"

	"github.com/agentre-hub/agentre/internal/service/notification_svc"

	"github.com/cago-frame/cago/pkg/logger"
	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"
	"go.uber.org/zap"
)

// ShowNotification 弹一条系统通知；文案已由前端按 i18n 生成。
func (a *App) ShowNotification(req *notification_svc.ShowRequest) error {
	return notification_svc.Notification().Show(a.ctx, req)
}

// RegisterNotificationHandlers 在 Startup 调用：初始化 Wails 通知 + 注册点击回调。
// 非 bundle / 旧系统下 InitializeNotifications 报错 → 仅告警降级。
func (a *App) RegisterNotificationHandlers() {
	if err := wailsruntime.InitializeNotifications(a.ctx); err != nil {
		logger.Ctx(a.ctx).Warn("app.RegisterNotificationHandlers: init notifications", zap.Error(err))
	}
	wailsruntime.OnNotificationResponse(a.ctx, func(res wailsruntime.NotificationResult) {
		if res.Error != nil {
			return
		}
		sid := sessionIDFromUserInfo(res.Response.UserInfo)
		if sid <= 0 {
			return
		}
		wailsruntime.WindowUnminimise(a.ctx)
		wailsruntime.WindowShow(a.ctx)
		wailsruntime.EventsEmit(a.ctx, "notification:click", sid)
	})
}

// sessionIDFromUserInfo 从通知 userInfo 取 sessionID，兼容 JSON 往返后的 float64、int64、数字字符串；
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
	case string:
		n, err := strconv.ParseInt(x, 10, 64)
		if err != nil {
			return 0
		}
		return n
	default:
		return 0
	}
}
