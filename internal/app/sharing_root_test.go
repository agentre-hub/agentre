package app

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/agentre-hub/agentre/internal/service/chat_svc"
)

// stubSharingRootChat 只实现「同一份代码上在跑」这一块;嵌入的 ChatSvc 留 nil,碰到别的方法当场 panic。
type stubSharingRootChat struct {
	chat_svc.ChatSvc

	items  []*chat_svc.SharingRootSession
	err    error
	gotSID int64
}

func (s *stubSharingRootChat) ListSessionsSharingRoot(_ context.Context, sessionID int64) ([]*chat_svc.SharingRootSession, error) {
	s.gotSID = sessionID
	return s.items, s.err
}

// Given chat 服务汇总出同根在跑的会话, When 前端经 ListSessionsSharingRoot 查询, Then 原样交回并带着会话 ID。
func TestListSessionsSharingRoot_PassesThrough(t *testing.T) {
	stub := &stubSharingRootChat{items: []*chat_svc.SharingRootSession{
		{SessionID: 8, Title: "登录页", AgentName: "Codex", RootName: "demo-app", OverlapFirst: "src/a.css", OverlapCount: 2},
	}}
	original := chat_svc.Chat()
	t.Cleanup(func() { chat_svc.RegisterChat(original) })
	chat_svc.RegisterChat(stub)
	a := &App{ctx: context.Background()}

	got, err := a.ListSessionsSharingRoot(7)

	require.NoError(t, err)
	assert.EqualValues(t, 7, stub.gotSID)
	require.Len(t, got, 1)
	assert.Equal(t, "src/a.css", got[0].OverlapFirst)
	assert.Equal(t, 2, got[0].OverlapCount)
}

// 服务失败时错误原样交给前端。
func TestListSessionsSharingRoot_PropagatesError(t *testing.T) {
	boom := errors.New("boom")
	original := chat_svc.Chat()
	t.Cleanup(func() { chat_svc.RegisterChat(original) })
	chat_svc.RegisterChat(&stubSharingRootChat{err: boom})
	a := &App{ctx: context.Background()}

	_, err := a.ListSessionsSharingRoot(7)

	assert.ErrorIs(t, err, boom)
}
