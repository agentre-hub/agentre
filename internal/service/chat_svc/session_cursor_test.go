package chat_svc

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/agentre-hub/agentre/internal/model/entity/chat_entity"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
	"github.com/agentre-hub/agentre/internal/repository/chat_repo"
	"github.com/agentre-hub/agentre/internal/repository/chat_repo/mock_chat_repo"
)

// setupSessionCursorTest 注入 session repo mock 并返回 agentruntime 侧已注册的游标端口。
// 取的是 agentruntime.SessionCursor() 而不是本包的具体类型：远端 runtime 只看得到端口，
// 拿不到 chat_repo —— 这条注入链本身就是被测契约的一部分。
func setupSessionCursorTest(t *testing.T) (*mock_chat_repo.MockSessionRepo, agentruntime.SessionCursorPort) {
	t.Helper()
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)

	sessRepo := mock_chat_repo.NewMockSessionRepo(ctrl)
	prev := chat_repo.Session()
	chat_repo.RegisterSession(sessRepo)
	t.Cleanup(func() { chat_repo.RegisterSession(prev) })

	port := agentruntime.SessionCursor()
	require.NotNil(t, port, "chat_svc 必须把游标端口实现注册进 agentruntime")
	return sessRepo, port
}

// TestSessionCursorPort_LoadCursor —— R12：游标只在 daemon 实例标识一致时可用。
func TestSessionCursorPort_LoadCursor(t *testing.T) {
	t.Run("Given 跑在某台 daemon 上的会话, When 实例标识与记录一致, Then 交出已记录的游标", func(t *testing.T) {
		sessRepo, port := setupSessionCursorTest(t)
		sessRepo.EXPECT().Find(gomock.Any(), int64(42)).Return(&chat_entity.Session{
			ID: 42, ExecDeviceID: 3, ExecDeviceFingerprint: "sha256:beef", EventCursor: 17,
		}, nil)

		seq, ok, err := port.LoadCursor(context.Background(), 42, "sha256:beef")
		require.NoError(t, err)
		assert.True(t, ok)
		assert.Equal(t, int64(17), seq)
	})

	t.Run("Given 同一条会话, When daemon 实例标识变了(重装/换机/数据目录被清), Then 游标判失效", func(t *testing.T) {
		sessRepo, port := setupSessionCursorTest(t)
		sessRepo.EXPECT().Find(gomock.Any(), int64(42)).Return(&chat_entity.Session{
			ID: 42, ExecDeviceID: 3, ExecDeviceFingerprint: "sha256:beef", EventCursor: 17,
		}, nil)

		seq, ok, err := port.LoadCursor(context.Background(), 42, "sha256:cafe")
		require.NoError(t, err)
		assert.False(t, ok, "实例标识不匹配时游标指向另一条通知日志，不得据此增量拉取")
		assert.Equal(t, int64(0), seq)
	})

	t.Run("Given 本机执行的老会话, When 问它的游标, Then 无游标可用", func(t *testing.T) {
		sessRepo, port := setupSessionCursorTest(t)
		sessRepo.EXPECT().Find(gomock.Any(), int64(9)).Return(&chat_entity.Session{ID: 9}, nil)

		seq, ok, err := port.LoadCursor(context.Background(), 9, "sha256:beef")
		require.NoError(t, err)
		assert.False(t, ok)
		assert.Equal(t, int64(0), seq)
	})

	t.Run("Given 本机执行的老会话, When 当前 daemon 实例标识也是空的, Then 仍然无游标可用", func(t *testing.T) {
		sessRepo, port := setupSessionCursorTest(t)
		sessRepo.EXPECT().Find(gomock.Any(), int64(9)).Return(&chat_entity.Session{ID: 9}, nil)

		_, ok, err := port.LoadCursor(context.Background(), 9, "")
		require.NoError(t, err)
		assert.False(t, ok, "两边都是空串不等于「对上了」——身份未知时游标一律不可用")
	})

	t.Run("Given 会话不存在, Then 无游标可用且不报错", func(t *testing.T) {
		sessRepo, port := setupSessionCursorTest(t)
		sessRepo.EXPECT().Find(gomock.Any(), int64(404)).Return(nil, nil)

		_, ok, err := port.LoadCursor(context.Background(), 404, "sha256:beef")
		require.NoError(t, err)
		assert.False(t, ok)
	})
}

// TestSessionCursorPort_SaveCursorThenLoad —— 推进游标后再读回，拿到的是刚写下的值。
func TestSessionCursorPort_SaveCursorThenLoad(t *testing.T) {
	sessRepo, port := setupSessionCursorTest(t)

	stored := &chat_entity.Session{ID: 42, ExecDeviceID: 3, ExecDeviceFingerprint: "sha256:beef", EventCursor: 17}
	sessRepo.EXPECT().UpdateEventCursor(gomock.Any(), int64(42), "sha256:beef", int64(23)).
		DoAndReturn(func(_ context.Context, _ int64, _ string, seq int64) error {
			stored.EventCursor = seq
			return nil
		})
	sessRepo.EXPECT().Find(gomock.Any(), int64(42)).Return(stored, nil)

	require.NoError(t, port.SaveCursor(context.Background(), 42, "sha256:beef", 23))

	seq, ok, err := port.LoadCursor(context.Background(), 42, "sha256:beef")
	require.NoError(t, err)
	assert.True(t, ok)
	assert.Equal(t, int64(23), seq)
}

// TestSessionCursorPort_SaveCursorCarriesDaemonIdentity —— 写入侧同样带 daemon 身份。
// seq 是某一条通知日志里的位置,离开那条日志就是个错的数字:会话改绑到别的 daemon
// 后,老连接上迟到的一条通知若把 seq 无条件写进去,记录就变成「新 daemon 的标识 +
// 老 daemon 的 seq」,下次重连会从一个远超新日志长度的位置往后拉,整段 transcript
// 永久丢失。端口把身份一路带到仓储的 WHERE 守卫上,调用方不需要(也无法安全地)
// 自己先读后写。
func TestSessionCursorPort_SaveCursorCarriesDaemonIdentity(t *testing.T) {
	sessRepo, port := setupSessionCursorTest(t)

	sessRepo.EXPECT().UpdateEventCursor(gomock.Any(), int64(42), "sha256:beef", int64(900)).Return(nil)

	require.NoError(t, port.SaveCursor(context.Background(), 42, "sha256:beef", 900))
}
