package update_svc

import (
	"context"
	"time"

	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"
)

// timeNow 由测试替换，让节流判定不依赖真实时钟。
var timeNow = time.Now

// CheckTrigger 是一次更新检查的触发来源。判定是否真的发起请求只看它与上次检查时间，
// 与 ticker、App 生命周期、网络请求全部无关 —— 这是为了让节流可以被单测覆盖。
type CheckTrigger string

const (
	// TriggerStartup 应用启动后的首次判定。
	TriggerStartup CheckTrigger = "startup"
	// TriggerTick 常驻定时器。
	TriggerTick CheckTrigger = "tick"
	// TriggerFocus 窗口重新获得焦点（用户回到应用的那一刻）。
	TriggerFocus CheckTrigger = "focus"
	// TriggerManual 用户点「检查更新」。切换更新通道后的重查也走这一条：
	// 两者都是用户此刻在等结果，节流对它们没有意义。
	TriggerManual CheckTrigger = "manual"
)

// AutoCheckInterval 自动检查的最短间隔：距上次检查不足这么久就不再发请求。
// 用户主动触发（TriggerManual）不受它约束。
const AutoCheckInterval = 24 * time.Hour

// ShouldCheck 判定此刻是否应该真的发起一次更新检查。
//
// lastCheck 是持久化的上次检查时间（从未检查过时传零值或 Unix(0,0)）。
// 未知触发源按自动处理，不绕过节流 —— 新增触发源时宁可少查一次，也不要静默变成高频请求。
func ShouldCheck(trigger CheckTrigger, lastCheck, now time.Time) bool {
	if trigger == TriggerManual {
		// 用户此刻在等结果。
		return true
	}

	if lastCheck.IsZero() || lastCheck.Unix() <= 0 {
		return true
	}
	// 时钟回拨或存储被外部改坏会留下未来时间戳；按 now.Sub(lastCheck) 判定会得到负数，
	// 自动检查将一直被挡到系统时间追上为止。把它当作「上次检查不可信」，直接放行。
	if lastCheck.After(now) {
		return true
	}
	return now.Sub(lastCheck) >= AutoCheckInterval
}

// CheckOutcome 是一次后台发起的检查的结果，通过 "update:checked" 事件推给前端。
// 前端据此把状态栏胶囊置为「有更新 / 已是最新 / 检查失败」——ticker 与启动检查
// 没有调用方可以接收返回值，只能走事件。
type CheckOutcome struct {
	// Trigger 触发源，取 CheckTrigger 的字符串值。
	Trigger string `json:"trigger"`
	// Info 检查结果；Error 非空时为 nil。
	Info *UpdateInfo `json:"info"`
	// Error 失败原因原文；成功时为空串。
	Error string `json:"error"`
}

// RunCheck 按触发源判定是否该查，放行时执行一次检查并写回上次检查时间。
//
// 被节流跳过时返回 (nil, nil)：调用方据此什么都不做，而不是把「没查」误当成「已是最新」。
// 检查失败时不推进节流窗口，否则一次网络抖动会让接下来 AutoCheckInterval 内都不再尝试。
func RunCheck(ctx context.Context, trigger CheckTrigger) (*UpdateInfo, error) {
	svc := Update()

	last, err := svc.GetLastUpdateCheck(ctx)
	if err != nil {
		return nil, err
	}
	now := timeNow()
	if !ShouldCheck(trigger, time.UnixMilli(last), now) {
		logger.Ctx(ctx).Debug("update_svc.RunCheck: throttled",
			zap.String("trigger", string(trigger)), zap.Int64("lastCheck", last))
		return nil, nil
	}

	channel, err := svc.GetChannel(ctx)
	if err != nil {
		return nil, err
	}
	mirror, err := svc.GetMirror(ctx)
	if err != nil {
		return nil, err
	}

	info, err := svc.CheckForUpdate(channel, mirror)
	if err != nil {
		return nil, err
	}

	if err := svc.SetLastUpdateCheck(ctx, now.UnixMilli()); err != nil {
		// 落库失败只影响下一次节流判定，不该让本次已经拿到的结果作废。
		logger.Ctx(ctx).Warn("update_svc.RunCheck: persist last check",
			zap.String("trigger", string(trigger)), zap.Error(err))
	}
	logger.Ctx(ctx).Info("update_svc.RunCheck: checked",
		zap.String("trigger", string(trigger)), zap.String("channel", channel),
		zap.Bool("hasUpdate", info.HasUpdate), zap.String("latestVersion", info.LatestVersion))
	return info, nil
}
