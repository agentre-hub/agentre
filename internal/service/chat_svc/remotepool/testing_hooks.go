package remotepool

import (
	"time"

	"github.com/agentre-hub/agentre/internal/service/remote_device_svc"
)

// SnapshotForTest 在锁内交出某设备的 cache entry 与它此刻持有的 lease。
// 测试专用:entry 的字段是私有的(生命周期不变量靠这一点保住),但既有回归用例
// 需要断言「重连后还是同一个 entry / 它换到了新 lease」。
func (p *Pool) SnapshotForTest(deviceID int64) (*Entry, remote_device_svc.Lease) {
	p.mu.Lock()
	defer p.mu.Unlock()
	entry, ok := p.cache[deviceID]
	if !ok {
		return nil, nil
	}
	return entry, entry.lease
}

// HoldLockForTest 占住池锁 d,用来把它压进 sync.Mutex 的饥饿模式。
// 测试专用:并发冷路径回归用例靠它把 TOCTOU 窗口从「偶尔」压成「每轮都穿」。
func (p *Pool) HoldLockForTest(d time.Duration) {
	p.mu.Lock()
	time.Sleep(d)
	p.mu.Unlock()
}
