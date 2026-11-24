package app

import (
	"context"
	"errors"
	"strconv"
	"strings"

	"github.com/cago-frame/cago/pkg/i18n"

	"github.com/agentre-hub/agentre/internal/pkg/code"
	"github.com/agentre-hub/agentre/internal/service/exec_target_svc"
	"github.com/agentre-hub/agentre/internal/service/project_svc"
	"github.com/agentre-hub/agentre/internal/service/terminal_svc"
	"github.com/agentre-hub/agentre/internal/service/workspace_fs_svc"
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

// TerminalOpenForSession 在会话的某个工作根里开一条 PTY(会话终端工具窗用)。
//
// root 为空串 = 会话 cwd(主根);非空时必须**精确等于**本会话已认领的工作根之一
// (workspace_fs_svc.WorkRoots 给出的路径,与 workspace_fs 的 rootFor 同一道闸门),
// 否则拒绝且不起进程 —— 终端是任意命令执行,不能让前端传一个任意目录进来。
// 远端会话的 PTY 开在执行设备上,cwd 就是那台设备上的路径。
func (a *App) TerminalOpenForSession(terminalID string, sessionID int64, root string, cols, rows uint16) error {
	if a.terminalSvc == nil {
		return errTerminalSvcNotInitialized
	}
	targets := exec_target_svc.ExecTarget()
	if targets == nil {
		return terminal_svc.ErrCommandScopeResolverNotInitialized
	}
	deviceID, cwd, err := targets.ResolveSessionWorkspace(a.ctx, sessionID)
	if err != nil {
		return err
	}
	if cwd == "" {
		return i18n.NewError(a.ctx, code.WorkspaceFsNoCwd)
	}
	start, err := a.claimedTerminalRoot(a.ctx, sessionID, cwd, root)
	if err != nil {
		return err
	}
	device := ""
	if deviceID != 0 {
		device = strconv.FormatInt(deviceID, 10)
	}
	return a.terminalSvc.Open(a.ctx, terminalID, device, start, cols, rows)
}

// claimedTerminalRoot 把前端传来的 root 解析成起始目录:空串或等于会话 cwd 即 cwd,
// 其余必须是已认领工作根(精确相等,只容忍末尾分隔符)。
func (a *App) claimedTerminalRoot(ctx context.Context, sessionID int64, cwd, root string) (string, error) {
	if root == "" || trimRootSep(root) == trimRootSep(cwd) {
		return cwd, nil
	}
	roots, err := workspace_fs_svc.Default().WorkRoots(ctx, sessionID)
	if err != nil {
		return "", err
	}
	for _, r := range roots {
		if trimRootSep(r.Path) == trimRootSep(root) {
			return r.Path, nil
		}
	}
	return "", i18n.NewError(ctx, code.WorkspaceFsPathRefused)
}

func trimRootSep(p string) string {
	if t := strings.TrimRight(p, `/\`); t != "" {
		return t
	}
	return p
}

// ResolveLocalCommandScope 只读解析已有会话或预会话目标的命令执行设备/cwd。
func (a *App) ResolveLocalCommandScope(
	req *exec_target_svc.ResolveLocalCommandScopeRequest,
) (*exec_target_svc.LocalCommandScope, error) {
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
