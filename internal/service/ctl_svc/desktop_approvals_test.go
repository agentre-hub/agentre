package ctl_svc

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/agentre-hub/agentre/internal/pkg/transcript/blocks"
)

// fakeDesktopEmitter 记下每次快照推送（生产实现把它接到 wails EventsEmit，测试只看参数）。
type fakeDesktopEmitter struct {
	snapshots [][]DesktopApprovalItem
}

func (f *fakeDesktopEmitter) EmitQueue(items []DesktopApprovalItem) {
	// 复制一份，防止调用方后续原地改 slice 污染历史快照。
	cp := make([]DesktopApprovalItem, len(items))
	copy(cp, items)
	f.snapshots = append(f.snapshots, cp)
}

func (f *fakeDesktopEmitter) last() []DesktopApprovalItem {
	if len(f.snapshots) == 0 {
		return nil
	}
	return f.snapshots[len(f.snapshots)-1]
}

func testApproval(id, command string) ExternalApproval {
	return ExternalApproval{
		RequestID: id,
		Input:     blocks.CtlApprovalInput{Command: command, Changes: []blocks.CtlApprovalChange{{Op: "update", Kind: "provider", Name: "openrouter"}}},
	}
}

// TestDesktopApprovalQueue_Enqueue 钉死：入队即推一次含新请求的快照，多条按入队顺序排列
// （前端拿它算「第 N 个，共 M 个」——N 恒为 1，M 是快照长度，见 desktop dialog 的实现）。
func TestDesktopApprovalQueue_Enqueue(t *testing.T) {
	emitter := &fakeDesktopEmitter{}
	q := NewDesktopApprovalQueue(emitter)
	var _ ExternalApprovals = q

	_, err := q.Enqueue(context.Background(), testApproval("r1", "agrctl update provider openrouter"))
	require.NoError(t, err)
	require.Len(t, emitter.last(), 1)
	assert.Equal(t, "r1", emitter.last()[0].RequestID)
	assert.Equal(t, "agrctl update provider openrouter", emitter.last()[0].Command)
	assert.Nil(t, emitter.last()[0].Caller, "agrctl 尚未上报调用方进程信息，见 approval.go 的 CallerInfo 注释")

	_, err = q.Enqueue(context.Background(), testApproval("r2", "agrctl delete department 临时小组 --cascade"))
	require.NoError(t, err)
	require.Len(t, emitter.last(), 2)
	assert.Equal(t, []string{"r1", "r2"}, []string{emitter.last()[0].RequestID, emitter.last()[1].RequestID})
}

// TestDesktopApprovalQueue_Answer 钉死：Answer 把 true/false 送进 Enqueue 返回的 channel，
// 从队列摘除并再推一次不含它的快照；未知 requestId 报错、不推快照。
func TestDesktopApprovalQueue_Answer(t *testing.T) {
	emitter := &fakeDesktopEmitter{}
	q := NewDesktopApprovalQueue(emitter)
	ch, err := q.Enqueue(context.Background(), testApproval("r1", "cmd"))
	require.NoError(t, err)

	require.NoError(t, q.Answer("r1", true))
	select {
	case allow := <-ch:
		assert.True(t, allow)
	default:
		t.Fatal("Answer 没有把结果送进 channel")
	}
	assert.Empty(t, emitter.last(), "批准后应推一次摘除该请求的快照")

	before := len(emitter.snapshots)
	assert.Error(t, q.Answer("r1", true), "已答过的 requestId 再答一次应该报错")
	assert.Len(t, emitter.snapshots, before, "重复 Answer 不应该再推快照")

	assert.Error(t, q.Answer("does-not-exist", false))
}

// TestDesktopApprovalQueue_Withdraw 钉死：Withdraw（超时/调用方断开）只摘除、不往 channel
// 送值——执行者的 await 早就用 select 的另一支走掉了，此后 Answer 对它必须失效。
func TestDesktopApprovalQueue_Withdraw(t *testing.T) {
	emitter := &fakeDesktopEmitter{}
	q := NewDesktopApprovalQueue(emitter)
	ch, err := q.Enqueue(context.Background(), testApproval("r1", "cmd"))
	require.NoError(t, err)

	q.Withdraw("r1")
	select {
	case v := <-ch:
		t.Fatalf("Withdraw 不应该往 channel 送值，收到了 %v", v)
	default:
	}
	assert.Empty(t, emitter.last())

	assert.Error(t, q.Answer("r1", true), "Withdraw 之后这条请求的 Answer 必须失效")

	// 撤下一个不存在的 id 是 no-op，不 panic、不推快照。
	before := len(emitter.snapshots)
	q.Withdraw("never-existed")
	assert.Len(t, emitter.snapshots, before)
}

// TestDesktopApprovalQueue_Caller 钉死：Caller 信息（有值时）原样透传进快照，供弹窗渲染
// 调用方那一行；agrctl 目前不上报，所以生产路径里恒为 nil（见 approval.go）。
func TestDesktopApprovalQueue_Caller(t *testing.T) {
	emitter := &fakeDesktopEmitter{}
	q := NewDesktopApprovalQueue(emitter)
	a := testApproval("r1", "cmd")
	a.Caller = &CallerInfo{ParentProcess: "codex", Pid: 48213, WorkingDir: "~/Code/agentre"}

	_, err := q.Enqueue(context.Background(), a)
	require.NoError(t, err)
	require.Len(t, emitter.last(), 1)
	require.NotNil(t, emitter.last()[0].Caller)
	assert.Equal(t, CallerInfo{ParentProcess: "codex", Pid: 48213, WorkingDir: "~/Code/agentre"}, *emitter.last()[0].Caller)
}
