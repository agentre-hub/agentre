package chat_svc_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/agentre-hub/agentre/internal/service/chat_svc"
)

func expectDeleteCleanup(m *chatMocks, id int64) {
	m.session.EXPECT().SoftDelete(m.ctx, id).Return(nil)
	m.frameSeq.EXPECT().DeleteBySession(m.ctx, id).Return(int64(0), nil)
	m.message.EXPECT().DeleteFromSeq(m.ctx, id, 0).Return(int64(0), nil)
	m.replacementRecovery.EXPECT().DeleteReplacementRecovery(m.ctx, id).Return(int64(0), nil)
	m.workRoots.EXPECT().DeleteBySession(m.ctx, id).Return(nil)
	m.dispatches.EXPECT().DeleteBySession(m.ctx, id).Return(nil)
	m.issueLinks.EXPECT().DeleteBySession(m.ctx, id).Return(nil)
}

// Given 一条会话认领过工作根, When 删除它, Then 它持久化的认领集合一并删除
// (spec 2026-11-24「会话删除时关联清理」),不留指向已删会话的行。
func TestDelete_RemovesClaimedWorkRoots(t *testing.T) {
	m := setupChatTest(t)
	m.session.EXPECT().SoftDelete(m.ctx, int64(7)).Return(nil)
	m.frameSeq.EXPECT().DeleteBySession(m.ctx, int64(7)).Return(int64(0), nil)
	m.message.EXPECT().DeleteFromSeq(m.ctx, int64(7), 0).Return(int64(0), nil)
	m.replacementRecovery.EXPECT().DeleteReplacementRecovery(m.ctx, int64(7)).Return(int64(0), nil)
	m.workRoots.EXPECT().DeleteBySession(m.ctx, int64(7)).Return(nil)
	m.dispatches.EXPECT().DeleteBySession(m.ctx, int64(7)).Return(nil)
	m.issueLinks.EXPECT().DeleteBySession(m.ctx, int64(7)).Return(nil)

	_, err := m.svc.Delete(m.ctx, &chat_svc.DeleteRequest{SessionID: 7})
	require.NoError(t, err)
}

// Given 一条会话既派发过别的会话、也被别的会话派发过, When 删除它, Then 派发关系两个方向
// 一并清理(决策 21:不留指向已删会话的行);清理失败与其它步骤一样直接报错。
func TestDelete_RemovesDispatchRelationsBothDirections(t *testing.T) {
	m := setupChatTest(t)
	m.session.EXPECT().SoftDelete(m.ctx, int64(7)).Return(nil)
	m.frameSeq.EXPECT().DeleteBySession(m.ctx, int64(7)).Return(int64(0), nil)
	m.message.EXPECT().DeleteFromSeq(m.ctx, int64(7), 0).Return(int64(0), nil)
	m.replacementRecovery.EXPECT().DeleteReplacementRecovery(m.ctx, int64(7)).Return(int64(0), nil)
	m.workRoots.EXPECT().DeleteBySession(m.ctx, int64(7)).Return(nil)
	m.dispatches.EXPECT().DeleteBySession(m.ctx, int64(7)).Return(nil) // 仓储一次删两个方向(见仓储测试)
	m.issueLinks.EXPECT().DeleteBySession(m.ctx, int64(7)).Return(nil)

	_, err := m.svc.Delete(m.ctx, &chat_svc.DeleteRequest{SessionID: 7})
	require.NoError(t, err)
}

func TestDelete_DispatchCleanupFailureReturnsError(t *testing.T) {
	m := setupChatTest(t)
	m.session.EXPECT().SoftDelete(m.ctx, int64(7)).Return(nil)
	m.frameSeq.EXPECT().DeleteBySession(m.ctx, int64(7)).Return(int64(0), nil)
	m.message.EXPECT().DeleteFromSeq(m.ctx, int64(7), 0).Return(int64(0), nil)
	m.replacementRecovery.EXPECT().DeleteReplacementRecovery(m.ctx, int64(7)).Return(int64(0), nil)
	m.workRoots.EXPECT().DeleteBySession(m.ctx, int64(7)).Return(nil)
	m.dispatches.EXPECT().DeleteBySession(m.ctx, int64(7)).Return(errors.New("dispatch cleanup boom"))

	_, err := m.svc.Delete(m.ctx, &chat_svc.DeleteRequest{SessionID: 7})
	require.Error(t, err)
}

// 认领集合清理失败与其它清理步骤一样直接报错(调用方据此重放删除),不吞掉。
func TestDelete_WorkRootCleanupFailureReturnsError(t *testing.T) {
	m := setupChatTest(t)
	m.session.EXPECT().SoftDelete(m.ctx, int64(7)).Return(nil)
	m.frameSeq.EXPECT().DeleteBySession(m.ctx, int64(7)).Return(int64(0), nil)
	m.message.EXPECT().DeleteFromSeq(m.ctx, int64(7), 0).Return(int64(0), nil)
	m.replacementRecovery.EXPECT().DeleteReplacementRecovery(m.ctx, int64(7)).Return(int64(0), nil)
	m.workRoots.EXPECT().DeleteBySession(m.ctx, int64(7)).Return(errors.New("work root cleanup boom"))

	_, err := m.svc.Delete(m.ctx, &chat_svc.DeleteRequest{SessionID: 7})
	require.Error(t, err)
}

// 删除成功后触发「会话已删」钩子,且钩子在独立 goroutine 里跑:钩子阻塞时 Delete 照样返回。
func TestDelete_FiresSessionDeletedHookWithoutBlocking(t *testing.T) {
	m := setupChatTest(t)
	expectDeleteCleanup(m, 5)

	called := make(chan struct{}, 1)
	release := make(chan struct{})
	chat_svc.RegisterSessionDeletedHook(func(context.Context) error {
		called <- struct{}{}
		<-release // 模拟回收迟迟不返回
		return nil
	})
	t.Cleanup(func() { chat_svc.RegisterSessionDeletedHook(nil) })

	done := make(chan error, 1)
	go func() {
		_, err := m.svc.Delete(m.ctx, &chat_svc.DeleteRequest{SessionID: 5})
		done <- err
	}()
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(2 * time.Second):
		t.Fatal("Delete blocked on the session-deleted hook")
	}
	select {
	case <-called:
	case <-time.After(2 * time.Second):
		t.Fatal("hook was not invoked")
	}
	close(release)
}

// Given 宿主登记了「会话删除后的善后」(如关掉这条会话开的终端), When 删除成功, Then 它收到
// 被删会话的 ID(任何删除入口 —— 用户删除、批量清理、入站删除 —— 都走 Delete)。
func TestDelete_RunsRegisteredSessionCleanupWithTheSessionID(t *testing.T) {
	m := setupChatTest(t)
	expectDeleteCleanup(m, 9)

	got := make(chan int64, 1)
	unregister := chat_svc.RegisterSessionDeletedCleanup(func(_ context.Context, sessionID int64) { got <- sessionID })
	t.Cleanup(unregister)

	_, err := m.svc.Delete(m.ctx, &chat_svc.DeleteRequest{SessionID: 9})
	require.NoError(t, err)
	select {
	case id := <-got:
		assert.Equal(t, int64(9), id)
	case <-time.After(2 * time.Second):
		t.Fatal("cleanup was not invoked")
	}
}

// 钩子报错不得让删除失败。
func TestDelete_HookFailureDoesNotFailDelete(t *testing.T) {
	m := setupChatTest(t)
	expectDeleteCleanup(m, 6)

	called := make(chan struct{}, 1)
	chat_svc.RegisterSessionDeletedHook(func(context.Context) error {
		defer func() { called <- struct{}{} }()
		return errors.New("reclaim boom")
	})
	t.Cleanup(func() { chat_svc.RegisterSessionDeletedHook(nil) })

	_, err := m.svc.Delete(m.ctx, &chat_svc.DeleteRequest{SessionID: 6})
	assert.NoError(t, err)
	select {
	case <-called:
	case <-time.After(2 * time.Second):
		t.Fatal("hook was not invoked")
	}
}

// 删除失败时不触发钩子(没有东西可回收)。
func TestDelete_FailedDeleteDoesNotFireHook(t *testing.T) {
	m := setupChatTest(t)
	m.session.EXPECT().SoftDelete(m.ctx, int64(7)).Return(errors.New("boom"))

	fired := make(chan struct{}, 1)
	chat_svc.RegisterSessionDeletedHook(func(context.Context) error { fired <- struct{}{}; return nil })
	t.Cleanup(func() { chat_svc.RegisterSessionDeletedHook(nil) })

	_, err := m.svc.Delete(m.ctx, &chat_svc.DeleteRequest{SessionID: 7})
	assert.Error(t, err)
	select {
	case <-fired:
		t.Fatal("hook fired for failed delete")
	case <-time.After(100 * time.Millisecond):
	}
}

// Given 一条会话关联了看板任务, When 删除它, Then 它的全部任务关联一并清理
// (spec 2026-11-24 决策 23:不留指向已删会话的关联行)。
func TestDelete_RemovesSessionIssueLinks(t *testing.T) {
	m := setupChatTest(t)
	expectDeleteCleanup(m, 7)

	_, err := m.svc.Delete(m.ctx, &chat_svc.DeleteRequest{SessionID: 7})
	require.NoError(t, err)
}

// 任务关联清理失败与其它清理步骤一样直接报错(调用方据此重放删除),不吞掉。
func TestDelete_SessionIssueLinkCleanupFailureReturnsError(t *testing.T) {
	m := setupChatTest(t)
	m.session.EXPECT().SoftDelete(m.ctx, int64(7)).Return(nil)
	m.frameSeq.EXPECT().DeleteBySession(m.ctx, int64(7)).Return(int64(0), nil)
	m.message.EXPECT().DeleteFromSeq(m.ctx, int64(7), 0).Return(int64(0), nil)
	m.replacementRecovery.EXPECT().DeleteReplacementRecovery(m.ctx, int64(7)).Return(int64(0), nil)
	m.workRoots.EXPECT().DeleteBySession(m.ctx, int64(7)).Return(nil)
	m.dispatches.EXPECT().DeleteBySession(m.ctx, int64(7)).Return(nil)
	m.issueLinks.EXPECT().DeleteBySession(m.ctx, int64(7)).Return(errors.New("issue link cleanup boom"))

	_, err := m.svc.Delete(m.ctx, &chat_svc.DeleteRequest{SessionID: 7})
	require.Error(t, err)
}
