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

// 本文件覆盖「浏览器改得了这条会话用哪个模型」在 agentred 这一侧的落库。
//
// 为什么承载执行的这一端也要存:同一条对话可以在桌面端与 agentred 上各有一份,
// 而承载连接的那台未必是发起它的那台(mirror_entity 包注释)。用户在浏览器里换
// 模型时两台都写,在哪一台打开都看到自己刚选的那个。

func setupModelTargetTest(t *testing.T) (
	context.Context, *mock_handlers.MockSessionModelTargetPort, *handlers.SessionModelTargetHandlers,
) {
	t.Helper()
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)
	sessions := mock_handlers.NewMockSessionModelTargetPort(ctrl)
	h := handlers.NewSessionModelTargetHandlers(handlers.SessionModelTargetDeps{
		Sessions: sessions,
	})
	return context.Background(), sessions, h
}

// 三态各自落库:固定模型 / 供应商默认 / 跟随 Agent 绑定。第三态是**两格都写空**,
// 不是「不写」——用户从固定模型改回跟随绑定时,不清空就等于这次改动没发生。
func TestSessionModelTarget_Set_PersistsAllThreeStates(t *testing.T) {
	for _, tc := range []struct {
		name            string
		provider, model string
	}{
		{"fixed-model", "prov-anthropic", "sonnet-4-6"},
		{"provider-default", "prov-anthropic", ""},
		{"inherit-agent", "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, sessions, h := setupModelTargetTest(t)
			sessions.EXPECT().
				SetModelTarget(gomock.Any(), "", convID(41), tc.provider, tc.model).
				Return(int64(1), nil)

			// wire.OK 是个空结构:成功与否全在 error 上,断言点是「正好这两格、
			// 正好这条会话被写下去了」——由上面的 EXPECT 精确入参承担。
			_, err := h.SetModelTarget(ctx, wire.SetModelTargetParams{
				ConversationID: convID(41), ProviderKey: tc.provider, ModelKey: tc.model,
			})
			require.NoError(t, err)
		})
	}
}

// 会话不存在 → 报错。折成成功会让浏览器以为下一轮会用新模型,而实际上什么都没写。
// (与删除那条路径**刻意不同**:删一条已经不存在的会话是幂等成功,而「改一条不存在
// 的会话的模型」没有任何东西可以幂等。)
func TestSessionModelTarget_Set_UnknownSessionIsAnError(t *testing.T) {
	ctx, sessions, h := setupModelTargetTest(t)
	sessions.EXPECT().SetModelTarget(gomock.Any(), "", convID(999), "p", "m").Return(int64(0), nil)

	_, err := h.SetModelTarget(ctx, wire.SetModelTargetParams{
		ConversationID: convID(999), ProviderKey: "p", ModelKey: "m",
	})
	require.Error(t, err)
	assert.True(t, errors.Is(err, rpcerror.ErrSessionNotFound),
		"改不存在的会话必须可分辨地报错,不能静默成功")
}

// 不成其为对话身份的取值在打库之前就挡下:它们写不到任何行,却会让调用方收到「改好了」。
func TestSessionModelTarget_Set_RejectsSomethingThatIsNotAConversationID(t *testing.T) {
	for _, bad := range []string{"", "0", "41", "not-a-uuid", "00000000-0000-0000-0000-000000000000"} {
		t.Run(bad, func(t *testing.T) {
			ctx, _, h := setupModelTargetTest(t)
			_, err := h.SetModelTarget(ctx, wire.SetModelTargetParams{ConversationID: bad})
			var rpcErr *rpcerror.Error
			require.ErrorAs(t, err, &rpcErr)
			assert.Equal(t, rpcerror.CodeInvalidParams, rpcErr.Code,
				"非法对话身份要给一个能与「这条对话不在本机」分开的错误码")
		})
	}
}
