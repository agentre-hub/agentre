package terminal_svc_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/agentre-hub/agentre/internal/pkg/pty"
	"github.com/agentre-hub/agentre/internal/service/terminal_svc"
	"github.com/agentre-hub/agentre/internal/service/terminal_svc/mocks"
)

// Given 两条会话各开了会话终端、另有一个项目终端, When 其中一条会话被删除(CloseChatSession),
// Then 只关掉那条会话开的终端:别的会话与项目终端不受影响,用户已自己关掉的终端不再关一次。
func TestService_CloseChatSession_ClosesOnlyThatSessionsTerminals(t *testing.T) {
	ctrl := gomock.NewController(t)
	mockBE := mocks.NewMockPTYBackend(ctrl)
	handles := map[string]*mocks.MockHandle{}
	for _, id := range []string{"s7-a", "s7-b", "s7-closed", "s8", "project"} {
		h := mocks.NewMockHandle(ctrl)
		h.EXPECT().Data().AnyTimes().Return(make(chan []byte))
		h.EXPECT().Exit().AnyTimes().Return(make(chan pty.ExitInfo))
		handles[id] = h
	}
	mockBE.EXPECT().Open(gomock.Any(), gomock.Any()).AnyTimes().DoAndReturn(
		func(_ context.Context, spec pty.Spec) (pty.Handle, error) { return handles[spec.TerminalID], nil })
	handles["s7-a"].EXPECT().Close().Return(nil)
	handles["s7-b"].EXPECT().Close().Return(nil)
	handles["s7-closed"].EXPECT().Close().Return(nil).Times(1)

	svc := terminal_svc.NewService(terminal_svc.NewBackendSelector(mockBE, nil), terminal_svc.NoopEmitter{})
	ctx := context.Background()
	require.NoError(t, svc.OpenForChatSession(ctx, 7, "s7-a", "", "/repo", 80, 24))
	require.NoError(t, svc.OpenForChatSession(ctx, 7, "s7-b", "", "/repo/wt", 80, 24))
	require.NoError(t, svc.OpenForChatSession(ctx, 7, "s7-closed", "", "/repo", 80, 24))
	require.NoError(t, svc.OpenForChatSession(ctx, 8, "s8", "", "/other", 80, 24))
	require.NoError(t, svc.Open(ctx, "project", "", "/repo", 80, 24))
	require.NoError(t, svc.Close(ctx, "s7-closed"))

	svc.CloseChatSession(ctx, 7)

	require.ErrorIs(t, svc.Write(ctx, "s7-a", "x"), terminal_svc.ErrTerminalClosed)
	handles["s8"].EXPECT().Write([]byte("x")).Return(1, nil)
	handles["project"].EXPECT().Write([]byte("x")).Return(1, nil)
	require.NoError(t, svc.Write(ctx, "s8", "x"))
	require.NoError(t, svc.Write(ctx, "project", "x"))
}

// Given 会话终端开启失败, Then 不留下归属记录:之后删除这条会话什么都不关。
func TestService_OpenForChatSession_FailureRecordsNoOwnership(t *testing.T) {
	ctrl := gomock.NewController(t)
	mockBE := mocks.NewMockPTYBackend(ctrl)
	mockBE.EXPECT().Open(gomock.Any(), gomock.Any()).Return(nil, context.DeadlineExceeded)

	svc := terminal_svc.NewService(terminal_svc.NewBackendSelector(mockBE, nil), terminal_svc.NoopEmitter{})
	require.Error(t, svc.OpenForChatSession(context.Background(), 7, "s7", "", "/repo", 80, 24))
	svc.CloseChatSession(context.Background(), 7) // 严格 mock:不应再有任何调用
}
