package piagent

import (
	"context"
	"errors"
	"sync"
)

// Session 是一条跨轮复用的 Pi RPC 会话:进程在 OpenSession 时起来一次,之后每一轮都在
// 同一个进程上开,直到 Close。
//
// 为什么要有它:pi 的启动参数(--session / --append-system-prompt / --model /
// --thinking / --extension)在 spawn 时就烤死了,而这些逐轮不变 —— 每轮重起一个进程,
// 付的是进程启动 + 扩展加载的钱,买的是一模一样的东西。Client 自己保持一次性语义
// (Stream / Text / Compact 各自起一个进程,轮末关掉),不受影响。
type Session struct {
	client *Client
	proc   *rpcProcess

	// turnMu 串行化本会话的轮:一个 RPC 进程同时只服务一轮,后一轮的帧会被前一轮的
	// 扫描器吃掉。
	turnMu sync.Mutex

	mu     sync.Mutex
	closed bool
}

// errSessionClosed 表示会话已经关掉,进程不在了。
var errSessionClosed = errors.New("piagent: session closed")

// OpenSession 起一个常驻的 Pi RPC 进程并把它交给一条会话。调用方负责 Close。
func (c *Client) OpenSession(ctx context.Context) (*Session, error) {
	proc, err := c.startRPC(ctx)
	if err != nil {
		return nil, err
	}
	return &Session{client: c, proc: proc}, nil
}

// Stream 在本会话的进程上开一轮,语义同 Client.Stream。
func (s *Session) Stream(ctx context.Context, prompt string, opts ...RunOption) (*Stream, error) {
	prepared, err := s.PrepareStream(ctx, prompt, opts...)
	if err != nil {
		return nil, err
	}
	return prepared.start(ctx, false)
}

// PrepareStream 在本会话的进程上准备一轮但不发 prompt,语义同 Client.PrepareStream。
func (s *Session) PrepareStream(ctx context.Context, prompt string, opts ...RunOption) (*PreparedStream, error) {
	return s.prepare(ctx, prompt, true, opts...)
}

func (s *Session) prepare(ctx context.Context, prompt string, requireExactBoundary bool, opts ...RunOption) (*PreparedStream, error) {
	s.mu.Lock()
	closed := s.closed
	s.mu.Unlock()
	if closed {
		return nil, errSessionClosed
	}
	s.turnMu.Lock()
	defer s.turnMu.Unlock()
	return s.client.prepareStreamOn(ctx, s.proc, false, prompt, requireExactBoundary, opts...)
}

// Close 终止本会话的 RPC 进程。重入安全。
func (s *Session) Close(ctx context.Context) error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	s.mu.Unlock()
	return s.proc.terminate(ctx, s.client.killGrace)
}
