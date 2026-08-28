package app

import (
	"errors"

	"github.com/agentre-hub/agentre/internal/service/chat_svc"
	"github.com/agentre-hub/agentre/internal/service/project_svc"
	"github.com/agentre-hub/agentre/internal/service/terminal_svc"
)

var errTerminalSvcNotInitialized = errors.New("terminal service not initialized")

// TerminalOpen opens a PTY for the given project/device combination. cols and
// rows set the initial terminal dimensions. The frontend should call
// TerminalClose when the panel is dismissed.
func (a *App) TerminalOpen(terminalID string, projectID int64, deviceID string, cols, rows uint16) error {
	if a.terminalSvc == nil {
		return errTerminalSvcNotInitialized
	}
	cwd, err := project_svc.Default().ResolveProjectCwd(a.ctx, projectID, deviceID)
	if err != nil {
		return err
	}
	return a.terminalSvc.Open(a.ctx, terminalID, deviceID, cwd, cols, rows)
}

// ResolveLocalCommandScope 只读解析已有会话或预会话目标的命令执行设备/cwd。
func (a *App) ResolveLocalCommandScope(
	req *chat_svc.ResolveLocalCommandScopeRequest,
) (*chat_svc.LocalCommandScope, error) {
	return resolveLocalCommandScope(a.ctx, req)
}

// TerminalRunCommand 在会话工作目录下,以 `$SHELL -l -c command` 跑一条本地命令(绕开 AI agent)。
// terminalID 由前端生成,与普通终端一致;输出走相同的 terminal:<id>:data/exit 事件。
func (a *App) TerminalRunCommand(
	terminalID string,
	sessionID int64,
	command string,
	cols, rows uint16,
) (*terminal_svc.RunCommandResponse, error) {
	if a.terminalSvc == nil {
		return nil, errTerminalSvcNotInitialized
	}
	return a.terminalSvc.RunCommand(a.ctx, terminal_svc.RunCommandRequest{
		TerminalID: terminalID,
		SessionID:  sessionID,
		Command:    command,
		Cols:       cols,
		Rows:       rows,
	})
}

// TerminalWrite sends input bytes (typically keystrokes) to the running PTY.
func (a *App) TerminalWrite(terminalID string, data string) error {
	if a.terminalSvc == nil {
		return errTerminalSvcNotInitialized
	}
	return a.terminalSvc.Write(a.ctx, terminalID, data)
}

// TerminalResize updates the PTY window dimensions (e.g. after the panel is
// resized by the user).
func (a *App) TerminalResize(terminalID string, cols, rows uint16) error {
	if a.terminalSvc == nil {
		return errTerminalSvcNotInitialized
	}
	return a.terminalSvc.Resize(a.ctx, terminalID, cols, rows)
}

// TerminalClose terminates the PTY process and releases resources.
func (a *App) TerminalClose(terminalID string) error {
	if a.terminalSvc == nil {
		return errTerminalSvcNotInitialized
	}
	return a.terminalSvc.Close(a.ctx, terminalID)
}
