package app

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"

	"github.com/agentre-hub/agentre/internal/daemon/protorpc"
	"github.com/agentre-hub/agentre/internal/pkg/ccoauth"
	"github.com/agentre-hub/agentre/internal/service/cc_usage_svc"
	"github.com/agentre-hub/agentre/internal/service/remote_device_svc"
	"github.com/agentre-hub/agentre/pkg/wire/agentrewire"
)

// GetCCUsage 给前端 hook 主动拉指定 device 的缓存状态。
// 没拉过(还没首次 probe)→ 返回 zero-value(reason=""),前端按 reason="" 视为 loading。
func (a *App) GetCCUsage(deviceKey string) cc_usage_svc.UsageState {
	st, _ := cc_usage_svc.CCUsage().Get(cc_usage_svc.DeviceKey(deviceKey))
	return st
}

// RefreshCCUsage 强制触发一次 probe(供前端"手动刷新"按钮)。
// 429 backoff 仍然有效 —— 短时间内连续调用不会真打 endpoint。
func (a *App) RefreshCCUsage(deviceKey string) {
	cc_usage_svc.CCUsage().Probe(a.ctx, cc_usage_svc.DeviceKey(deviceKey))
}

// buildCCUsageResolver 给 cc_usage_svc 提供 deviceKey → Fetcher 的解析逻辑。
// 把"读凭证 / 远端 RPC"细节关在这一层,cc_usage_svc 本身只关心调度 + 状态。
func (a *App) buildCCUsageResolver() cc_usage_svc.FetcherResolver {
	localFetch := ccoauth.NewLocalFetcher()
	return func(key cc_usage_svc.DeviceKey) (cc_usage_svc.Fetcher, error) {
		if key == cc_usage_svc.LocalKey {
			return localFetch, nil
		}
		s := string(key)
		if !strings.HasPrefix(s, "remote:") {
			return nil, cc_usage_svc.ErrDeviceOffline
		}
		deviceID, err := strconv.ParseInt(s[len("remote:"):], 10, 64)
		if err != nil {
			return nil, cc_usage_svc.ErrDeviceOffline
		}
		rd := remote_device_svc.Default()
		if rd == nil || rd.Pool() == nil {
			return nil, cc_usage_svc.ErrDeviceOffline
		}
		pool := rd.Pool()
		return func(ctx context.Context) (*ccoauth.RateLimits, error) {
			lease, lerr := pool.Borrow(ctx, deviceID)
			if lerr != nil {
				return nil, errors.Join(cc_usage_svc.ErrDeviceOffline, lerr)
			}
			defer lease.Release()
			res, cerr := protorpc.CallMethod(ctx, lease.Client().Conn(), uint32(agentrewire.RpcMethod_RPC_METHOD_CLAUDE_CODE_USAGE),
				&agentrewire.ClaudeCodeUsageRequest{}, func() *agentrewire.ClaudeCodeUsageResponse { return &agentrewire.ClaudeCodeUsageResponse{} })
			if cerr != nil {
				return nil, errors.Join(ccoauth.ErrNetwork, cerr)
			}
			switch res.Reason {
			case "ok":
				return protobufRateLimits(res.GetData()), nil
			case "no_credentials":
				return nil, ccoauth.ErrNoCredentials
			case "auth_expired":
				return nil, ccoauth.ErrAuthExpired
			case "rate_limited":
				return nil, ccoauth.ErrRateLimited
			default:
				return nil, ccoauth.ErrNetwork
			}
		}, nil
	}
}

func protobufRateLimits(value *agentrewire.ClaudeCodeRateLimits) *ccoauth.RateLimits {
	if value == nil {
		return nil
	}
	result := &ccoauth.RateLimits{FiveHourPercent: value.GetFiveHourPercent(), WeeklyPercent: value.GetWeeklyPercent(), SonnetWeeklyPercent: value.SonnetWeeklyPercent, OpusWeeklyPercent: value.OpusWeeklyPercent}
	if value.FiveHourResetsAtMs != nil {
		reset := time.UnixMilli(value.GetFiveHourResetsAtMs())
		result.FiveHourResetsAt = &reset
	}
	if value.WeeklyResetsAtMs != nil {
		reset := time.UnixMilli(value.GetWeeklyResetsAtMs())
		result.WeeklyResetsAt = &reset
	}
	if value.SonnetWeeklyResetsAtMs != nil {
		reset := time.UnixMilli(value.GetSonnetWeeklyResetsAtMs())
		result.SonnetWeeklyResetsAt = &reset
	}
	if value.OpusWeeklyResetsAtMs != nil {
		reset := time.UnixMilli(value.GetOpusWeeklyResetsAtMs())
		result.OpusWeeklyResetsAt = &reset
	}
	return result
}

// ccUsagePollInterval 是 cc_usage_svc 后台轮询配额(本地 + 每个在线远端 device)
// 的间隔。桌面端驱动,agentred 只响应 claudecode.usage RPC 不主动拉。
const ccUsagePollInterval = 5 * time.Minute

// startCCUsage 在 App.Startup 末尾调用。挂 emitter / resolver / 本地 ticker,
// 5 秒后异步首探本地 + 所有已配对在线的远端 device。
// 返回 cancel 让 Shutdown 停所有 ticker。
func (a *App) startCCUsage() func() {
	mgr := cc_usage_svc.CCUsage()
	mgr.SetEmitter(func(p cc_usage_svc.EmitPayload) {
		wailsruntime.EventsEmit(a.ctx, "cc_usage:update", p)
	})
	mgr.SetFetcherResolver(a.buildCCUsageResolver())
	mgr.StartTicker(a.ctx, cc_usage_svc.LocalKey, ccUsagePollInterval)

	go func() {
		select {
		case <-a.ctx.Done():
			return
		case <-time.After(5 * time.Second):
		}
		mgr.Probe(a.ctx, cc_usage_svc.LocalKey)

		// 已配对设备:在线的起 ticker + 首探;离线的不起,等 watcher 事件回调上来。
		if rd := remote_device_svc.Default(); rd != nil {
			views, err := rd.List(a.ctx)
			if err == nil {
				for _, v := range views {
					if v.Online {
						key := cc_usage_svc.DeviceKey(fmt.Sprintf("remote:%d", v.ID))
						mgr.StartTicker(a.ctx, key, ccUsagePollInterval)
						mgr.Probe(a.ctx, key)
					}
				}
			}
		}
	}()

	return func() { mgr.StopAllTickers() }
}

// onRemoteDeviceState 由 Startup 在每条 remote.device.state 事件上调用。
// device online → 起 ticker + 立即 probe;offline → 停 ticker。
func (a *App) onRemoteDeviceState(id int64, online bool) {
	key := cc_usage_svc.DeviceKey(fmt.Sprintf("remote:%d", id))
	mgr := cc_usage_svc.CCUsage()
	if online {
		mgr.StartTicker(a.ctx, key, ccUsagePollInterval)
		go mgr.Probe(a.ctx, key)
		return
	}
	mgr.StopTicker(key)
}
