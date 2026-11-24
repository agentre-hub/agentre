package app

import (
	"errors"
	"fmt"
	"net/url"
	"strconv"

	"github.com/cago-frame/cago/pkg/i18n"

	"github.com/agentre-hub/agentre/internal/pkg/code"
	"github.com/agentre-hub/agentre/internal/service/chat_svc"
	"github.com/agentre-hub/agentre/internal/service/port_forward_svc"
)

// ListSessionProcesses 列出会话留下的存活进程及其监听端口（后台任务弹层「进程与端口」一节）。
// 扫描失败时返回空列表而不是错误。
func (a *App) ListSessionProcesses(sessionID int64) (*chat_svc.ListSessionProcessesResponse, error) {
	svc := chat_svc.SessionProcesses()
	if svc == nil {
		return nil, errSessionProcessUnavailable
	}
	return svc.ListSessionProcesses(a.ctx, &chat_svc.ListSessionProcessesRequest{SessionID: sessionID})
}

// KillSessionProcess 结束会话留下的一个进程。startedAt 取自列表那一行，用来核对 PID 没被复用。
func (a *App) KillSessionProcess(sessionID int64, pid int, startedAt int64) error {
	svc := chat_svc.SessionProcesses()
	if svc == nil {
		return errSessionProcessUnavailable
	}
	_, err := svc.KillSessionProcess(a.ctx, &chat_svc.KillSessionProcessRequest{
		SessionID: sessionID, PID: pid, StartedAt: startedAt,
	})
	return err
}

// OpenSessionProcessPort 交回「打开这个端口」该访问的地址，前端用 BrowserOpenURL 打开。
// 本机会话直接是 http://localhost:<port>；远程会话经执行设备上的端口转发，交回本机专属监听的地址。
func (a *App) OpenSessionProcessPort(sessionID int64, port int) (string, error) {
	if port < 1 || port > 65535 {
		return "", i18n.NewError(a.ctx, code.PortForwardInvalidPort)
	}
	svc := chat_svc.SessionProcesses()
	if svc == nil {
		return "", errSessionProcessUnavailable
	}
	execDeviceID, err := svc.SessionExecDevice(a.ctx, sessionID)
	if err != nil {
		return "", err
	}
	if execDeviceID == 0 {
		return fmt.Sprintf("http://localhost:%d", port), nil
	}
	return a.openRemoteSessionPort(execDeviceID, "http", port)
}

// openRemoteSessionPort 确保执行设备上有一条指向该端口的转发,再为它绑本机监听。
func (a *App) openRemoteSessionPort(execDeviceID int64, scheme string, port int) (string, error) {
	deviceID := strconv.FormatInt(execDeviceID, 10)
	view, err := port_forward_svc.Default().EnsureForPort(a.ctx, deviceID, scheme, port)
	if err != nil {
		return "", codedError(err)
	}
	return a.PortForwardOpen(deviceID, view.ID)
}

var errSessionProcessUnavailable = errors.New("session process service is not available")

// ResolveSessionURL 交回「在这个会话里打开这条链接」该访问的地址。
// 本机会话、非 http(s) / 非 localhost 的链接原样交回;远端会话里指向执行设备环回的链接
// 经端口转发,换成本机专属监听的地址,路径、查询与片段原样保留。
func (a *App) ResolveSessionURL(sessionID int64, rawURL string) (string, error) {
	u, port, ok := parseLoopbackURL(rawURL)
	if !ok {
		return rawURL, nil
	}
	svc := chat_svc.SessionProcesses()
	if svc == nil {
		return "", errSessionProcessUnavailable
	}
	execDeviceID, err := svc.SessionExecDevice(a.ctx, sessionID)
	if err != nil {
		return "", err
	}
	if execDeviceID == 0 {
		return rawURL, nil
	}
	forward, err := a.openRemoteSessionPort(execDeviceID, u.Scheme, port)
	if err != nil {
		return "", err
	}
	base, err := url.Parse(forward)
	if err != nil {
		return "", err
	}
	u.Host = base.Host
	u.Scheme = base.Scheme
	return u.String(), nil
}

// parseLoopbackURL 只认指向 localhost / 127.0.0.1 / ::1 的 http(s) 链接,并交回它实际
// 访问的端口(没写就是协议默认端口)。其余一律 ok=false,调用方原样放行。
func parseLoopbackURL(rawURL string) (u *url.URL, port int, ok bool) {
	u, err := url.Parse(rawURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return nil, 0, false
	}
	switch u.Hostname() {
	case "localhost", "127.0.0.1", "::1":
	default:
		return nil, 0, false
	}
	if p := u.Port(); p != "" {
		n, err := strconv.Atoi(p)
		if err != nil || n < 1 || n > 65535 {
			return nil, 0, false
		}
		return u, n, true
	}
	if u.Scheme == "https" {
		return u, 443, true
	}
	return u, 80, true
}
