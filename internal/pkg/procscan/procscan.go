// Package procscan 找出某个会话留下的进程与监听端口，并安全地结束其中一个。
//
// 认领靠 CLI 环境里的 AGENTRE_SESSION=<conversation_id> 标记；平台相关的
// 枚举 / 读环境 / 发信号由各平台的 OS 实现提供，本文件只放平台无关的骨架。
package procscan

import (
	"context"
	"errors"
	"sync"
	"time"
)

// MarkerEnv 是注入 CLI 环境、用来认领会话进程的变量名，值为 conversation_id。
const MarkerEnv = "AGENTRE_SESSION"

// defaultGrace 是 SIGTERM 之后等进程自行退出的时间，超过再强杀。
const defaultGrace = 3 * time.Second

var (
	// ErrNotSessionProcess 表示目标 PID 已不存在或已不属于该会话，此时不发任何信号。
	ErrNotSessionProcess = errors.New("procscan: process does not belong to this session")
	// ErrUnsupported 表示当前平台没有扫描实现。
	ErrUnsupported = errors.New("procscan: unsupported platform")
)

// Proc 是一次快照里的一个进程。
type Proc struct {
	PID, PPID int
	StartMs   int64
	Argv      []string
	Cwd       string
	// Marker 是环境里 AGENTRE_SESSION 的值，读不到或没有时为空。
	Marker string
	// Ports 是该进程监听的 TCP 端口。
	Ports []uint16
}

// Listed 是认领后要展示给用户的一行。
type Listed struct {
	PID         int
	Command     string
	Ports       []uint16
	StartedAtMs int64
	Cwd         string
}

// OS 是平台差异的接缝，测试里用假实现注入。
type OS interface {
	// Snapshot 返回同一操作系统用户的全部进程；读不到环境的进程应被跳过。
	Snapshot(ctx context.Context) ([]Proc, error)
	// Inspect 只为身份核对返回单个进程（PID/PPID/StartMs/Marker/Argv），不取端口与 cwd；
	// Kill 在宽限期内按 pollInterval 反复调用它，必须便宜。不存在时第二个返回值为 false。
	Inspect(ctx context.Context, pid int) (Proc, bool, error)
	// Terminate 只对单个 pid 发信号（force 为 SIGKILL / 直接终止），绝不碰进程组。
	Terminate(pid int, force bool) error
}

// platformOS 由各平台文件在 init 里设置；为 nil 表示该平台没有实现。
var platformOS func() OS

// Scanner 把 OS 与登记的 CLI 进程拼成对外的 List / Kill。
type Scanner struct {
	os    OS
	grace time.Duration
	now   func() time.Time

	// mu 串行化快照：并发的 List 等同一次快照，而不是各跑一遍。
	mu      sync.Mutex
	snap    []Proc
	snapAt  time.Time
	hasSnap bool
}

// New 返回当前平台的 Scanner；平台没有实现时所有方法返回 ErrUnsupported。
func New() *Scanner {
	if platformOS == nil {
		return NewWith(unsupportedOS{}, defaultGrace)
	}
	return NewWith(platformOS(), defaultGrace)
}

// NewWith 用给定 OS 与 SIGTERM 宽限期构造 Scanner。
func NewWith(os OS, grace time.Duration) *Scanner {
	return &Scanner{os: os, grace: grace, now: time.Now}
}

// List 列出 convID 会话当前留下的进程；快照失败时返回错误（调用方据此什么都不画）。
func (s *Scanner) List(ctx context.Context, convID string) ([]Listed, error) {
	snap, err := s.snapshot(ctx)
	if err != nil {
		return nil, err
	}
	return Claim(snap, convID, trackedCLIs()), nil
}

// unsupportedOS 是没有平台实现时的占位，全部返回 ErrUnsupported。
type unsupportedOS struct{}

func (unsupportedOS) Snapshot(context.Context) ([]Proc, error) { return nil, ErrUnsupported }
func (unsupportedOS) Inspect(context.Context, int) (Proc, bool, error) {
	return Proc{}, false, ErrUnsupported
}
func (unsupportedOS) Terminate(int, bool) error { return ErrUnsupported }
