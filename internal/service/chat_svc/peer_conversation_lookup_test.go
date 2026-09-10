package chat_svc

import (
	"context"
	"errors"
	"testing"

	"github.com/cago-frame/cago/pkg/consts"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/agentre-hub/agentre/internal/model/entity/chat_entity"
	"github.com/agentre-hub/agentre/internal/repository/chat_repo"
	"github.com/agentre-hub/agentre/internal/repository/chat_repo/mock_chat_repo"
)

// mintedConversationID 是一个 UUIDv7 —— 也就是**铸**出来的号,不是从
// (指纹, 本地会话 id) 派生出来的。用它当被测输入是有意的:落列之前那层反查靠
// 枚举本机会话逐条重铸身份再比对,而铸号在那条路上**永远**对不上。
const mintedConversationID = "0198f4c1-a000-7c0d-8b21-0000000000ff"

// registerSessionRepo 只换掉会话仓储,不碰这个包里其它进程级装配。
func registerSessionRepo(t *testing.T) *mock_chat_repo.MockSessionRepo {
	t.Helper()
	ctrl := gomock.NewController(t)
	sessions := mock_chat_repo.NewMockSessionRepo(ctrl)
	prev := chat_repo.Session()
	chat_repo.RegisterSession(sessions)
	t.Cleanup(func() {
		chat_repo.RegisterSession(prev)
		ctrl.Finish()
	})
	return sessions
}

// Given 一条身份是铸出来(而非派生出来)的对话,When 把它翻回本机主键,Then 走
// conversation_id 那一列上的**一次查询**就得到,并且一行本机会话都没有被枚举。
func TestResolvePeerConversation_GivenAMintedConversationID_ThenResolvesThroughTheStoredColumn(t *testing.T) {
	sessions := registerSessionRepo(t)
	ctx := context.Background()

	sessions.EXPECT().FindByConversationID(ctx, mintedConversationID).
		Return(&chat_entity.Session{ID: 77, ConversationID: mintedConversationID, Status: consts.ACTIVE}, nil)

	got, err := ResolvePeerConversation(ctx, mintedConversationID)
	require.NoError(t, err)
	assert.Equal(t, int64(77), got)
}

// Given 本机没有这条对话,When 反查,Then 是「不在本机」而不是「参数非法」——
// 两者在 RPC 边界上是不同的错误码。
func TestResolvePeerConversation_GivenNoLocalRow_ThenReportsNotFound(t *testing.T) {
	sessions := registerSessionRepo(t)
	ctx := context.Background()

	sessions.EXPECT().FindByConversationID(ctx, mintedConversationID).Return(nil, nil)

	_, err := ResolvePeerConversation(ctx, mintedConversationID)
	assert.True(t, errors.Is(err, ErrPeerSessionNotFound), "got %v", err)
	assert.False(t, errors.Is(err, ErrPeerSessionInvalidID))
}

// Given 一个不是对话身份的取值(旧的裸数字会话号),When 反查,Then 在碰库之前
// 就被挡下 —— 没有任何仓储调用发生(mock 未登记期望,发生了就红)。
func TestResolvePeerConversation_GivenSomethingThatIsNotAConversationID_ThenRejectsBeforeTouchingStorage(t *testing.T) {
	registerSessionRepo(t)

	_, err := ResolvePeerConversation(context.Background(), "41")
	assert.True(t, errors.Is(err, ErrPeerSessionInvalidID), "got %v", err)
}
