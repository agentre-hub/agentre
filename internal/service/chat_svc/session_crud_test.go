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
