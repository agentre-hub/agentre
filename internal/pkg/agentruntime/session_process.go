package agentruntime

import (
	"context"
	"errors"
)

//go:generate mockgen -source session_process.go -destination mock_agentruntime/mock_session_process.go

// ErrSessionProcessNotOwned 表示要结束的进程不属于该会话（已退出、PID 被复用或不是它留下的）。
var ErrSessionProcessNotOwned = errors.New("agentruntime: process does not belong to the session")

// SessionProcess 是会话留下的一个存活进程。StartedAtMs 供结束时核对 PID 没有被复用。
type SessionProcess struct {
	PID         int
	Command     string
	Ports       []int
	StartedAtMs int64
	Cwd         string
}

// SessionProcessInspector 由能在别的机器上扫描/结束会话进程的 runtime（远程）实现。
// 按 conversationID 工作，不要求会话此刻挂着 CLI。
type SessionProcessInspector interface {
	ListSessionProcesses(ctx context.Context, conversationID string) ([]SessionProcess, error)
	KillSessionProcess(ctx context.Context, conversationID string, pid int, startedAtMs int64) error
}
