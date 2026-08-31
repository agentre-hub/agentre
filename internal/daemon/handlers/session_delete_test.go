package handlers_test

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/agentre-hub/agentre/internal/daemon/handlers"
	"github.com/agentre-hub/agentre/internal/daemon/handlers/mock_handlers"
	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/runtimes/remote/wire"
	"github.com/agentre-hub/agentre/internal/pkg/rpcerror"
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
	sessions.EXPECT().Delete(gomock.Any(), "", convID(7)).Return(int64(1), nil)
	journal.EXPECT().DeleteAll(gomock.Any(), "", convID(7)).Return(int64(42), nil)

	got, err := h.Delete(ctx, wire.SessionDeleteParams{ConversationID: convID(7)})
	require.NoError(t, err)
	assert.True(t, got.Deleted, "删除返回时这一端必须已经没有这条会话")
}

// Given 同一条会话被删了第二次(server 记的待办重放、或用户重试), When 会话行早就
// 不在了, Then 仍然是成功且日志照样清 —— 报错会让 server 那条待办永远重放下去,而
// 「会话行已经没了、日志还剩着」正是上一次删到一半留下的样子,重试必须能收敛。
func TestSessionDelete_GivenAlreadyDeletedSession_ThenSucceedsAndStillPurgesTheJournal(t *testing.T) {
	ctx, sessions, journal, h := setupSessionDeleteTest(t, nil)
	sessions.EXPECT().Delete(gomock.Any(), "", convID(7)).Return(int64(0), nil)
	journal.EXPECT().DeleteAll(gomock.Any(), "", convID(7)).Return(int64(0), nil)

	got, err := h.Delete(ctx, wire.SessionDeleteParams{ConversationID: convID(7)})
	require.NoError(t, err)
	assert.True(t, got.Deleted, "重复删除同样以「这一端没有这条会话了」收尾")
}

// Given 一个只完成了配对的调用方点名了**别人的**对端, When 它请求删除, Then 请求被
// 拒且一行都没动 —— 点名对端是账号级能力(ResolveSessionPeer),配对身份点名任何人
// 都等于替别人删对话。
func TestSessionDelete_GivenNamedOriginWithoutAccount_ThenRejectsWithoutTouchingAnything(t *testing.T) {
	ctx, _, _, h := setupSessionDeleteTest(t, nil)

	_, err := h.Delete(ctx, wire.SessionDeleteParams{ConversationID: convID(7), PeerFingerprint: "sha256:someone-else"})
	require.ErrorIs(t, err, rpcerror.ErrUnauthorized)
}

// Given 会话行删掉了, When 清日志这一步失败, Then 整个调用报错 —— 交出成功会让
// server 把待办勾掉,那段转录就永远留在这台机器上了。
func TestSessionDelete_GivenJournalPurgeFails_ThenReportsFailure(t *testing.T) {
	ctx, sessions, journal, h := setupSessionDeleteTest(t, nil)
	sessions.EXPECT().Delete(gomock.Any(), "", convID(7)).Return(int64(1), nil)
	journal.EXPECT().DeleteAll(gomock.Any(), "", convID(7)).Return(int64(0), errors.New("disk is gone"))

	_, err := h.Delete(ctx, wire.SessionDeleteParams{ConversationID: convID(7)})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "disk is gone")
}

// Given 一个不成其为对话身份的取值(空、旧的裸数字会话号、畸形 uuid),When 它被拿来
// 删除,Then 在打库之前就以「参数不合法」拒绝 —— 这些取值删不到任何一行,却会让调用方
// 收到「删好了」。判据从"正整数"换成 uuid 格式,是本轮换身份的直接后果。
func TestSessionDelete_GivenSomethingThatIsNotAConversationID_ThenRejects(t *testing.T) {
	for _, bad := range []string{"", "0", "7", "-1", "not-a-uuid", "00000000-0000-0000-0000-000000000000"} {
		t.Run(bad, func(t *testing.T) {
			ctx, _, _, h := setupSessionDeleteTest(t, nil)

			_, err := h.Delete(ctx, wire.SessionDeleteParams{ConversationID: bad})
			var rpcErr *rpcerror.Error
			require.ErrorAs(t, err, &rpcErr)
			assert.Equal(t, rpcerror.CodeInvalidParams, rpcErr.Code,
				"非法对话身份要给一个能与「这条对话不在本机」分开的错误码")
		})
	}
}

// sessionReleaseRecorder 是一个认领了会话释放口的 runtime 替身。
type sessionReleaseRecorder struct {
	agentruntime.Runtime
	mu       sync.Mutex
	released []int64
}

func (r *sessionReleaseRecorder) CloseSession(_ context.Context, sessionID int64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.released = append(r.released, sessionID)
}

func (r *sessionReleaseRecorder) Released() []int64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]int64(nil), r.released...)
}

// Given 一条会话在本机还留着跨轮常驻的 CLI 子进程, When 这条会话被删除, Then 那个
// 子进程要跟着放掉。
//
// 删除从前只动库(会话行 + 通知日志),子进程留在 CLISessionPool 里:它只能等 8 条
// idle 上限把自己挤出去,否则一直活到 daemon 退出 —— 而会话已经不存在了,再也没有
// 任何一轮会用到它。桌面端删会话时是放的(chat_svc.Delete),daemon 这一侧缺了。
func TestSessionDelete_GivenPooledCLISession_WhenDeleted_ThenTheSubprocessIsReleased(t *testing.T) {
	recorder := &sessionReleaseRecorder{}
	defer agentruntime.SwapRuntimeForTest(agent_backend_entity.TypeClaudeCode, recorder)()

	ctx, sessions, journal, h := setupSessionDeleteTest(t, nil)
	sessions.EXPECT().Delete(gomock.Any(), "", convID(7)).Return(int64(1), nil)
	journal.EXPECT().DeleteAll(gomock.Any(), "", convID(7)).Return(int64(1), nil)

	_, err := h.Delete(ctx, wire.SessionDeleteParams{ConversationID: convID(7)})
	require.NoError(t, err)

	// 释放用的会话键与 runtime.run 交给 backend 的是同一个 —— 由对话身份折算出来的
	// 那个进程内键,而不是随手编的数字。
	assert.Equal(t, []int64{handlers.RuntimeSessionKey(convID(7))}, recorder.Released())
}
