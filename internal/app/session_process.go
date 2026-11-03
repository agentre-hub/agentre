package app

import (
	"errors"
	"fmt"
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
	return a.openRemoteSessionPort(execDeviceID, port)
}

// openRemoteSessionPort 确保执行设备上有一条指向该端口的转发,再为它绑本机监听。
func (a *App) openRemoteSessionPort(execDeviceID int64, port int) (string, error) {
	deviceID := strconv.FormatInt(execDeviceID, 10)
	view, err := port_forward_svc.Default().EnsureForPort(a.ctx, deviceID, port)
	if err != nil {
		return "", codedError(err)
	}
	return a.PortForwardOpen(deviceID, view.ID)
}

var errSessionProcessUnavailable = errors.New("session process service is not available")
