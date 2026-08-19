package app

import (
	"time"

	"github.com/cago-frame/cago/pkg/logger"
	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"
	"go.uber.org/zap"

	"github.com/agentre-ai/agentre/internal/service/update_svc"
)

// 启动后延迟若干秒再触发首次判定，避开前端冷启动 / DB 迁移高峰。
const autoUpdateCheckDelay = 5 * time.Second

// 常驻定时器的 tick 间隔。是否真的发请求由 update_svc.ShouldCheck 的
// AutoCheckInterval 节流决定；tick 比节流窗口密只是为了让「应用连开不重启」
// 也能在窗口到期后尽快查一次，而不是等下次启动。
const autoUpdateCheckTick = 4 * time.Hour

// CheckForUpdate 用户主动检查最新版本，绕过节流。前端切换更新通道后也调它 ——
// 用户此刻在等结果。channel / mirror 从持久化设置读取。
func (a *App) CheckForUpdate() (*update_svc.UpdateInfo, error) {
	return update_svc.RunCheck(a.ctx, update_svc.TriggerManual)
}

// MaybeCheckForUpdate 受节流约束的检查入口，供前端在窗口重新获得焦点时调用。
// 距上次检查不足 update_svc.AutoCheckInterval 时不发请求，返回 (nil, nil)。
func (a *App) MaybeCheckForUpdate() (*update_svc.UpdateInfo, error) {
	return update_svc.RunCheck(a.ctx, update_svc.TriggerFocus)
}

// DownloadAndInstallUpdate 下载并安装最新版本；进度通过 "update:progress" 事件推送。
// skipChecksum=true 用于 SHA256SUMS.txt 获取失败但用户选择继续的场景。
func (a *App) DownloadAndInstallUpdate(skipChecksum bool) error {
	channel, err := update_svc.Update().GetChannel(a.ctx)
	if err != nil {
		return err
	}
	mirror, err := update_svc.Update().GetMirror(a.ctx)
	if err != nil {
		return err
	}
	ctx := a.ctx
	onProgress := func(downloaded, total int64) {
		wailsruntime.EventsEmit(ctx, "update:progress", map[string]int64{
			"downloaded": downloaded,
			"total":      total,
		})
	}
	return update_svc.Update().DownloadAndUpdate(channel, mirror, skipChecksum, onProgress)
}

// GetAvailableMirrors 返回内置可用下载镜像列表。
func (a *App) GetAvailableMirrors() []update_svc.MirrorInfo {
	return update_svc.Update().GetAvailableMirrors()
}

// GetUpdateChannel 返回当前更新通道。
func (a *App) GetUpdateChannel() (string, error) {
	return update_svc.Update().GetChannel(a.ctx)
}

// SetUpdateChannel 更新通道（stable / beta / nightly）。
func (a *App) SetUpdateChannel(channel string) error {
	return update_svc.Update().SetChannel(a.ctx, channel)
}

// GetDownloadMirror 返回当前下载镜像前缀；空串表示直连 GitHub。
func (a *App) GetDownloadMirror() (string, error) {
	return update_svc.Update().GetMirror(a.ctx)
}

// SetDownloadMirror 更新下载镜像前缀；空串恢复直连。
func (a *App) SetDownloadMirror(mirror string) error {
	return update_svc.Update().SetMirror(a.ctx, mirror)
}

// RestartApp 的实现见 restart.go（跨平台逻辑）与 restart_{darwin,unix,windows}.go。

// startAutoUpdateCheck 启动 5s 后做首次判定，随后常驻 tick 直到应用退出。
// 每次判定是否真的发请求由 update_svc 的节流决定。
//
// 仅在 Startup 中由 goroutine 调用，不阻塞主流程。
func (a *App) startAutoUpdateCheck() {
	select {
	case <-a.ctx.Done():
		return
	case <-time.After(autoUpdateCheckDelay):
	}
	a.emitUpdateCheck(update_svc.TriggerStartup)

	ticker := time.NewTicker(autoUpdateCheckTick)
	defer ticker.Stop()
	for {
		select {
		case <-a.ctx.Done():
			return
		case <-ticker.C:
			a.emitUpdateCheck(update_svc.TriggerTick)
		}
	}
}

// emitUpdateCheck 跑一次后台检查并把结果广播成 "update:checked"。
//
// 启动检查与 ticker 没有调用方可以接收返回值，状态栏胶囊只能靠这个事件知道
// 「有新版本 / 已是最新 / 检查失败」。被节流跳过时什么都不广播 —— 「没查」
// 不等于「已是最新」。
func (a *App) emitUpdateCheck(trigger update_svc.CheckTrigger) {
	info, err := update_svc.RunCheck(a.ctx, trigger)
	if info == nil && err == nil {
		return
	}

	outcome := update_svc.CheckOutcome{Trigger: string(trigger), Info: info}
	if err != nil {
		outcome.Error = err.Error()
		// 自动检查失败是背景事件：不弹提示、不亮红点，只让胶囊显示失败并留下日志。
		logger.Ctx(a.ctx).Info("app.emitUpdateCheck: check failed",
			zap.String("trigger", string(trigger)), zap.Error(err))
	}
	wailsruntime.EventsEmit(a.ctx, "update:checked", outcome)
}
