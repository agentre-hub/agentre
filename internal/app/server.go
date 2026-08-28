package app

import (
	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"

	"github.com/agentre-hub/agentre/internal/model/entity/server_state_entity"
	"github.com/agentre-hub/agentre/internal/service/remote_device_svc"
	"github.com/agentre-hub/agentre/internal/service/server_svc"

	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

// ServerGetState reads the persisted server_state row. UI uses this on mount to decide
// whether to show the Disconnected hero or the Connected panel.
func (a *App) ServerGetState() (*server_state_entity.ServerState, error) {
	return server_svc.Server().GetState(a.ctx)
}

// ServerCheckURL is the URL-validation probe used by the LoginDialog. It returns
// the server-reported version on success; the UI uses the absence of an error as
// the "healthy" signal.
func (a *App) ServerCheckURL(serverURL string) (string, error) {
	return server_svc.Server().CheckURL(a.ctx, serverURL)
}

// ServerStartLogin kicks off the device flow. On success it opens the user's
// system browser at the verification URL so they don't have to copy/paste.
func (a *App) ServerStartLogin(serverURL string) (*server_svc.StartLoginResult, error) {
	res, err := server_svc.Server().StartLogin(a.ctx, serverURL)
	if err != nil {
		return nil, err
	}
	if res != nil && res.VerificationURIComplete != "" {
		wailsruntime.BrowserOpenURL(a.ctx, res.VerificationURIComplete)
	}
	return res, nil
}

// ServerPollLoginToken is called by the frontend on a fixed interval until it
// returns (true, nil) (success) or a non-nil error (terminal failure).
func (a *App) ServerPollLoginToken(deviceCode string) (bool, error) {
	return server_svc.Server().PollLoginToken(a.ctx, deviceCode)
}

// ServerCancelLogin aborts an in-flight login so the user can retry with a
// different Server URL without restarting the app.
func (a *App) ServerCancelLogin() error {
	return server_svc.Server().CancelLogin(a.ctx)
}

// ServerOffline reports whether the Server is currently out of reach (a boot
// refresh is backing off). The UI reads it on mount because the server.state
// event may have fired before the panel existed.
func (a *App) ServerOffline() bool {
	return server_svc.Server().Offline()
}

// ServerListDevices returns the user's devices known to the Server.
//
// 拿到清单之后顺手收编（决策 1）：账号里已有、本机却没有本地记录的 agentred 补一行
// 「只有中转路径」的记录，否则它选不进「运行设备」，同步下来的后端在本机也跑不起来
// （见 remote_device_svc.AdoptAccountDevices）。这里是桌面端唯一一处「刚刚得知账号
// 有哪些设备」的地方，设备面板与两个执行设备选择器都经它刷新，所以补齐挂在这里。
// 收编失败只记日志：它是补齐，不该让设备清单读不出来。
func (a *App) ServerListDevices() ([]server_svc.Device, error) {
	devices, err := server_svc.Server().ListDevices(a.ctx)
	if err != nil {
		return nil, err
	}
	if svc := remote_device_svc.Default(); svc != nil {
		adopting := make([]remote_device_svc.AccountDevice, 0, len(devices))
		for _, d := range devices {
			adopting = append(adopting, remote_device_svc.AccountDevice{
				Fingerprint: d.Fingerprint, Name: d.Name, Kind: d.Kind,
			})
		}
		if _, aerr := svc.AdoptAccountDevices(a.ctx, adopting); aerr != nil {
			logger.Ctx(a.ctx).Warn("app.ServerListDevices: adopting account agentreds failed", zap.Error(aerr))
		}
	}
	return devices, nil
}

// ServerLogout best-effort revokes server-side and clears the local state.
func (a *App) ServerLogout() error {
	return server_svc.Server().Logout(a.ctx)
}
