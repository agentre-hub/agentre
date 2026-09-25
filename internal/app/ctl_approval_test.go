package app

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/agentre-hub/agentre/internal/service/ctl_svc"
)

// fakeExternalApprovals 只用来断言 App.AnswerCtlApproval 是薄转发，不重实现队列语义
// （队列本身的行为已经在 ctl_svc.DesktopApprovalQueue 的单测里钉死）。
type fakeExternalApprovals struct {
	answered  []string
	allow     []bool
	returnErr error
	pending   []ctl_svc.DesktopApprovalItem
}

func (f *fakeExternalApprovals) Enqueue(context.Context, ctl_svc.ExternalApproval) (<-chan bool, error) {
	return make(chan bool, 1), nil
}

func (f *fakeExternalApprovals) Answer(requestID string, allow bool) error {
	f.answered = append(f.answered, requestID)
	f.allow = append(f.allow, allow)
	return f.returnErr
}

func (f *fakeExternalApprovals) Withdraw(string) bool { return true }

func (f *fakeExternalApprovals) Pending() []ctl_svc.DesktopApprovalItem { return f.pending }

// TestAnswerCtlApproval_ForwardsToRegisteredQueue 钉死：App.AnswerCtlApproval 是薄转发，
// 把 (requestID, allow) 原样递给 ctl_svc.Default() 当前注册的 ExternalApprovals；队列
// 报错时原样冒泡（前端据此在弹窗底栏显示错误、保留可重试）。
func TestAnswerCtlApproval_ForwardsToRegisteredQueue(t *testing.T) {
	fake := &fakeExternalApprovals{}
	ctl_svc.Default().RegisterExternalApprovals(fake)
	t.Cleanup(func() { ctl_svc.Default().RegisterExternalApprovals(nil) })

	a := &App{}
	require.NoError(t, a.AnswerCtlApproval("r1", true))
	assert.Equal(t, []string{"r1"}, fake.answered)
	assert.Equal(t, []bool{true}, fake.allow)

	fake.returnErr = assert.AnError
	assert.ErrorIs(t, a.AnswerCtlApproval("r2", false), assert.AnError)
}

// TestPendingCtlApprovals_ReturnsRegisteredQueueSnapshot：弹窗挂载时经这个 binding 补齐
// 订阅之前已入队的请求；队列还没注册时是空数组（不是 null）。
func TestPendingCtlApprovals_ReturnsRegisteredQueueSnapshot(t *testing.T) {
	a := &App{}
	ctl_svc.Default().RegisterExternalApprovals(nil)
	assert.Equal(t, []ctl_svc.DesktopApprovalItem{}, a.PendingCtlApprovals())

	fake := &fakeExternalApprovals{pending: []ctl_svc.DesktopApprovalItem{{RequestID: "r1", Command: "agrctl delete agent x"}}}
	ctl_svc.Default().RegisterExternalApprovals(fake)
	t.Cleanup(func() { ctl_svc.Default().RegisterExternalApprovals(nil) })
	assert.Equal(t, fake.pending, a.PendingCtlApprovals())
}
