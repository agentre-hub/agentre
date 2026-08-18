package handlers_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/agentre-ai/agentre/internal/daemon/handlers"
	"github.com/agentre-ai/agentre/internal/daemon/handlers/mock_handlers"
	"github.com/agentre-ai/agentre/internal/daemon/rpc"
	"github.com/agentre-ai/agentre/internal/pkg/agentruntime/runtimes/remote/wire"
)

// setupSessionDeleteTest 组装删除 handler:会话行与通知日志两个写端口都用 mockgen
// 注入,不碰数据库(样例同 session_catchup_test.go)。
func setupSessionDeleteTest(t *testing.T, claimedAccountID func() string) (
	context.Context,
	*mock_handlers.MockSessionDeletePort,
	*mock_handlers.MockJournalPurgePort,
	*handlers.SessionDeleteHandlers,
) {
	t.Helper()
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)
	sessions := mock_handlers.NewMockSessionDeletePort(ctrl)
	journal := mock_handlers.NewMockJournalPurgePort(ctrl)
	h := handlers.NewSessionDeleteHandlers(handlers.SessionDeleteDeps{
		Sessions:         sessions,
		Journal:          journal,
		ClaimedAccountID: claimedAccountID,
	})
	return context.Background(), sessions, journal, h
}

// Given 一条属于调用方的会话, When 它被删除, Then 会话行与它的**全部**通知日志一起
// 消失 —— 只删会话行会留下一段没有主人的转录,而这台机器上的会话 id 是调用方本地自增
// 的、会被复用,那段旧日志下一次就会被当成新会话的历史拉走。
func TestSessionDelete_GivenOwnSession_ThenRemovesTheRowAndItsWholeJournal(t *testing.T) {
	ctx, sessions, journal, h := setupSessionDeleteTest(t, nil)
	sessions.EXPECT().Delete(gomock.Any(), "", "7").Return(int64(1), nil)
	journal.EXPECT().DeleteAll(gomock.Any(), "", "7").Return(int64(42), nil)

	got, err := h.Delete(ctx, wire.SessionDeleteParams{SessionID: 7})
	require.NoError(t, err)
	assert.True(t, got.Deleted, "删除返回时这一端必须已经没有这条会话")
}

// Given 同一条会话被删了第二次(server 记的待办重放、或用户重试), When 会话行早就
// 不在了, Then 仍然是成功且日志照样清 —— 报错会让 server 那条待办永远重放下去,而
// 「会话行已经没了、日志还剩着」正是上一次删到一半留下的样子,重试必须能收敛。
func TestSessionDelete_GivenAlreadyDeletedSession_ThenSucceedsAndStillPurgesTheJournal(t *testing.T) {
	ctx, sessions, journal, h := setupSessionDeleteTest(t, nil)
	sessions.EXPECT().Delete(gomock.Any(), "", "7").Return(int64(0), nil)
	journal.EXPECT().DeleteAll(gomock.Any(), "", "7").Return(int64(0), nil)

	got, err := h.Delete(ctx, wire.SessionDeleteParams{SessionID: 7})
	require.NoError(t, err)
	assert.True(t, got.Deleted, "重复删除同样以「这一端没有这条会话了」收尾")
}

// Given 一个只完成了配对的调用方点名了**别人的**对端, When 它请求删除, Then 请求被
// 拒且一行都没动 —— 点名对端是账号级能力(ResolveSessionPeer),配对身份点名任何人
// 都等于替别人删对话。
func TestSessionDelete_GivenNamedOriginWithoutAccount_ThenRejectsWithoutTouchingAnything(t *testing.T) {
	ctx, _, _, h := setupSessionDeleteTest(t, nil)

	_, err := h.Delete(ctx, wire.SessionDeleteParams{SessionID: 7, PeerFingerprint: "sha256:someone-else"})
	require.ErrorIs(t, err, rpc.ErrUnauthorized)
}

// Given 会话行删掉了, When 清日志这一步失败, Then 整个调用报错 —— 交出成功会让
// server 把待办勾掉,那段转录就永远留在这台机器上了。
func TestSessionDelete_GivenJournalPurgeFails_ThenReportsFailure(t *testing.T) {
	ctx, sessions, journal, h := setupSessionDeleteTest(t, nil)
	sessions.EXPECT().Delete(gomock.Any(), "", "7").Return(int64(1), nil)
	journal.EXPECT().DeleteAll(gomock.Any(), "", "7").Return(int64(0), errors.New("disk is gone"))

	_, err := h.Delete(ctx, wire.SessionDeleteParams{SessionID: 7})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "disk is gone")
}

// Given 一个不成其为会话标识的 id, When 它被拿来删除, Then 直接拒绝 —— 会话 id 是
// 正整数主键,0 / 负数只会静默删掉零行并报「删好了」。
func TestSessionDelete_GivenNonPositiveSessionID_ThenRejects(t *testing.T) {
	ctx, _, _, h := setupSessionDeleteTest(t, nil)

	_, err := h.Delete(ctx, wire.SessionDeleteParams{SessionID: 0})
	require.ErrorIs(t, err, rpc.ErrInvalidParams)
}
