package handlers_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/agentre-hub/agentre/internal/daemon/handlers"
	"github.com/agentre-hub/agentre/internal/daemon/handlers/mock_handlers"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/runtimes/remote/wire"
	"github.com/agentre-hub/agentre/internal/pkg/rpcerror"
)

// 本文件覆盖「浏览器改得了这条会话的思考力度」在 agentred 这一侧的落库,与
// session_model_target_test.go 逐条同构:同一条对话可以在桌面端与 agentred 上各有
// 一份,承载连接的那台未必是发起它的那台,用户在浏览器里换档时两台都写。
//
// 这一列与 ModelTarget 那两列一样只供显示,执行路径不读它(规格「agentred 侧的
// 会话行」)——本轮真正用哪一档由 runtime.run 的 run 参数决定。

func setupReasoningEffortTest(t *testing.T) (
	context.Context, *mock_handlers.MockSessionReasoningEffortPort, *handlers.SessionReasoningEffortHandlers,
) {
	t.Helper()
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)
	sessions := mock_handlers.NewMockSessionReasoningEffortPort(ctrl)
	h := handlers.NewSessionReasoningEffortHandlers(handlers.SessionReasoningEffortDeps{
		Sessions: sessions,
	})
	return context.Background(), sessions, h
}

// 两态各自落库:钉住某一档 / 改回跟随后端配置。后者是**空串写下去**,不是「不写」
// ——用户从固定档改回跟随配置时不清空,就等于这次改动没发生。
func TestSessionReasoningEffort_Set_PersistsBothStates(t *testing.T) {
	for _, tc := range []struct {
		name, effort string
	}{
		{"pinned", "xhigh"},
		{"follow-backend-config", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, sessions, h := setupReasoningEffortTest(t)
			sessions.EXPECT().
				SetReasoningEffort(gomock.Any(), "", convID(41), tc.effort).
				Return(int64(1), nil)

			// wire.OK 是个空结构:断言点是「正好这一格、正好这条会话被写下去了」——
			// 由上面的 EXPECT 精确入参承担。
			_, err := h.SetReasoningEffort(ctx, wire.SetSessionReasoningEffortParams{
				ConversationID: convID(41), ReasoningEffort: tc.effort,
			})
			require.NoError(t, err)
		})
	}
}

// 会话不存在 → 报错。折成成功会让浏览器以为下一轮会用新档位,而实际上什么都没写
// (与删除那条幂等路径刻意不同)。
func TestSessionReasoningEffort_Set_UnknownSessionIsAnError(t *testing.T) {
	ctx, sessions, h := setupReasoningEffortTest(t)
	sessions.EXPECT().SetReasoningEffort(gomock.Any(), "", convID(999), "high").Return(int64(0), nil)

	_, err := h.SetReasoningEffort(ctx, wire.SetSessionReasoningEffortParams{
		ConversationID: convID(999), ReasoningEffort: "high",
	})
	require.Error(t, err)
	assert.True(t, errors.Is(err, rpcerror.ErrSessionNotFound),
		"改不存在的会话必须可分辨地报错,不能静默成功")
}

// 落库出错必须原样上报:静默成功同样会让调用方以为下一轮会用新档位。
func TestSessionReasoningEffort_Set_PropagatesWriteFailure(t *testing.T) {
	ctx, sessions, h := setupReasoningEffortTest(t)
	sessions.EXPECT().SetReasoningEffort(gomock.Any(), "", convID(41), "max").
		Return(int64(0), errors.New("database is locked"))

	_, err := h.SetReasoningEffort(ctx, wire.SetSessionReasoningEffortParams{
		ConversationID: convID(41), ReasoningEffort: "max",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "database is locked")
}

// 不成其为对话身份的取值在打库之前就挡下:它们写不到任何行,却会让调用方收到「改好了」。
func TestSessionReasoningEffort_Set_RejectsSomethingThatIsNotAConversationID(t *testing.T) {
	for _, bad := range []string{"", "0", "41", "not-a-uuid", "00000000-0000-0000-0000-000000000000"} {
		t.Run(bad, func(t *testing.T) {
			ctx, _, h := setupReasoningEffortTest(t)
			_, err := h.SetReasoningEffort(ctx, wire.SetSessionReasoningEffortParams{ConversationID: bad})
			var rpcErr *rpcerror.Error
			require.ErrorAs(t, err, &rpcErr)
			assert.Equal(t, rpcerror.CodeInvalidParams, rpcErr.Code,
				"非法对话身份要给一个能与「这条对话不在本机」分开的错误码")
		})
	}
}
