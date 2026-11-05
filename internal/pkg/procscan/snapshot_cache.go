package procscan

import (
	"context"
	"time"
)

// snapshotTTL 是共享快照的有效期：多个面板 / 会话的轮询在此期间共用一次进程快照。
const snapshotTTL = 5 * time.Second

// snapshot 返回共享快照；过期、被作废或从未拍过时才重新向 OS 取。失败不缓存。
func (s *Scanner) snapshot(ctx context.Context) ([]Proc, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.hasSnap && s.now().Sub(s.snapAt) < snapshotTTL {
		return s.snap, nil
	}
	snap, err := s.os.Snapshot(ctx)
	if err != nil {
		return nil, err
	}
	// ctx 在拍快照期间结束时,OS 层的辅助查询(darwin 的 lsof)会被一起杀掉,交回的是
	// 缺端口 / cwd 却不报错的残缺快照:只给这一个调用方,不拿去共享。
	if ctx.Err() == nil {
		s.snap, s.snapAt, s.hasSnap = snap, s.now(), true
	}
	return snap, nil
}

// invalidateSnapshot 丢弃共享快照，下一次 List 重拍。
func (s *Scanner) invalidateSnapshot() {
	s.mu.Lock()
	s.snap, s.hasSnap = nil, false
	s.mu.Unlock()
}
