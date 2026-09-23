package ctl_svc

import (
	"context"
	"fmt"
	"sync"

	"github.com/agentre-hub/agentre/internal/pkg/transcript/blocks"
)

// DesktopApprovalItem 是推给桌面端全局审批弹窗的一条待审批请求；桌面弹窗用整份快照
// （按入队顺序）渲染队列，自己算「第 N 个，共 M 个」——N 恒为当前展示的那条（快照第一
// 条）的位置 1，M 是快照长度。
type DesktopApprovalItem struct {
	RequestID string                     `json:"requestId"`
	Command   string                     `json:"command"`
	Changes   []blocks.CtlApprovalChange `json:"changes"`
	Caller    *CallerInfo                `json:"caller,omitempty"`
}

// DesktopApprovalEmitter 把队列快照送到桌面端；internal/app 用 wails EventsEmit 实现它。
type DesktopApprovalEmitter interface {
	EmitQueue(items []DesktopApprovalItem)
}

// DesktopApprovalEmitterFunc 让一个普通函数满足 DesktopApprovalEmitter。
type DesktopApprovalEmitterFunc func(items []DesktopApprovalItem)

func (f DesktopApprovalEmitterFunc) EmitQueue(items []DesktopApprovalItem) { f(items) }

// desktopPending 是队列里一条挂起的请求。
type desktopPending struct {
	item DesktopApprovalItem
	ch   chan bool
}

// DesktopApprovalQueue 是 ExternalApprovals 的生产实现：内存里维护一份按入队顺序的
// 待审批列表，每次变化（入队/作答/撤下）都把整份快照推给桌面弹窗（见 DesktopApprovalEmitter）。
// 应答走 internal/app 的 Wails binding，最终落到这里的 Answer。
type DesktopApprovalQueue struct {
	mu    sync.Mutex
	order []string
	items map[string]desktopPending
	emit  DesktopApprovalEmitter
}

// NewDesktopApprovalQueue 装配一个空队列；emit 在每次快照变化时同步调用（内部已加锁，
// 调用方自己的实现不必再加锁）。
func NewDesktopApprovalQueue(emit DesktopApprovalEmitter) *DesktopApprovalQueue {
	return &DesktopApprovalQueue{items: map[string]desktopPending{}, emit: emit}
}

var _ ExternalApprovals = (*DesktopApprovalQueue)(nil)

// Enqueue 见 ExternalApprovals：入队并推一次含新请求的快照。
func (q *DesktopApprovalQueue) Enqueue(_ context.Context, a ExternalApproval) (<-chan bool, error) {
	ch := make(chan bool, 1)
	q.mu.Lock()
	q.items[a.RequestID] = desktopPending{
		item: DesktopApprovalItem{
			RequestID: a.RequestID,
			Command:   a.Input.Command,
			Changes:   a.Input.Changes,
			Caller:    a.Caller,
		},
		ch: ch,
	}
	q.order = append(q.order, a.RequestID)
	snapshot := q.snapshotLocked()
	q.mu.Unlock()
	q.emit.EmitQueue(snapshot)
	return ch, nil
}

// Answer 见 ExternalApprovals：由桌面弹窗（经 internal/app 的 Wails binding）调用。
func (q *DesktopApprovalQueue) Answer(requestID string, allow bool) error {
	q.mu.Lock()
	p, ok := q.items[requestID]
	if !ok {
		q.mu.Unlock()
		return fmt.Errorf("ctl_svc: no pending desktop approval %q", requestID)
	}
	q.removeLocked(requestID)
	snapshot := q.snapshotLocked()
	q.mu.Unlock()

	p.ch <- allow
	close(p.ch)
	q.emit.EmitQueue(snapshot)
	return nil
}

// Withdraw 见 ExternalApprovals：执行者在超时或调用方断开时调用，只摘除、不往 channel
// 送值——执行者的 await 早已经从 select 的另一支返回。之后这条请求的 Answer 一律失效。
func (q *DesktopApprovalQueue) Withdraw(requestID string) {
	q.mu.Lock()
	if _, ok := q.items[requestID]; !ok {
		q.mu.Unlock()
		return
	}
	q.removeLocked(requestID)
	snapshot := q.snapshotLocked()
	q.mu.Unlock()
	q.emit.EmitQueue(snapshot)
}

// removeLocked 必须持锁调用。
func (q *DesktopApprovalQueue) removeLocked(requestID string) {
	delete(q.items, requestID)
	for i, id := range q.order {
		if id == requestID {
			q.order = append(q.order[:i], q.order[i+1:]...)
			break
		}
	}
}

// snapshotLocked 必须持锁调用；返回值是独立副本，调用方在锁外安全使用。
func (q *DesktopApprovalQueue) snapshotLocked() []DesktopApprovalItem {
	out := make([]DesktopApprovalItem, 0, len(q.order))
	for _, id := range q.order {
		out = append(out, q.items[id].item)
	}
	return out
}
