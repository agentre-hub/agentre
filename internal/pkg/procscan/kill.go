package procscan

import (
	"context"
	"time"
)

// pollInterval 是 SIGTERM 之后检查进程是否退出的间隔。
const pollInterval = 20 * time.Millisecond

// Kill 结束 convID 会话里的单个进程 pid（绝不碰进程组）。
//
// 发信号前核对：pid 仍存在、标记仍是 convID、启动时间等于列出时的 startedAtMs，
// 任一不符返回 ErrNotSessionProcess 且不发信号。先 TERM，宽限期内未退出则重新
// 核对身份（防 pid 被复用）后 KILL。
func (s *Scanner) Kill(ctx context.Context, convID string, pid int, startedAtMs int64) (err error) {
	// 成功结束后共享快照里还留着这个进程：作废它，下一次列出重拍。
	defer func() {
		if err == nil {
			s.invalidateSnapshot()
		}
	}()
	if err := s.verify(ctx, convID, pid, startedAtMs); err != nil {
		return err
	}
	if err := s.os.Terminate(pid, false); err != nil {
		return err
	}

	deadline := time.NewTimer(s.grace)
	defer deadline.Stop()
	tick := time.NewTicker(pollInterval)
	defer tick.Stop()
	for {
		_, alive, err := s.os.Inspect(ctx, pid)
		if err != nil {
			return err
		}
		if !alive {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			if err := s.verify(ctx, convID, pid, startedAtMs); err != nil {
				return err
			}
			return s.os.Terminate(pid, true)
		case <-tick.C:
		}
	}
}

// verify 确认 pid 仍是本会话列出的那个进程。
func (s *Scanner) verify(ctx context.Context, convID string, pid int, startedAtMs int64) error {
	p, ok, err := s.os.Inspect(ctx, pid)
	if err != nil {
		return err
	}
	if !ok || convID == "" || p.Marker != convID || p.StartMs != startedAtMs {
		return ErrNotSessionProcess
	}
	return nil
}
