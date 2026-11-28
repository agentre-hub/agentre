package chat_svc

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/agentre-hub/agentre/internal/model/entity/chat_entity"
)

// ListSessionsByIDs 给恢复出来、还没激活过的会话标签补元数据（规格
// docs/specs/2026-11-27-toolwindow-polish.md 决策 9）。它返回与 ListIndexSessions
// **同一种行**（ChatSessionLite），前端因此沿用同一处投影；不拉消息窗口，所以启动时
// 每个标签各调一次 LoadSession 的代价它都没有。

func TestListSessionsByIDs_ReturnsIndexRowsForKnownIDs(t *testing.T) {
	repo := withMockSessionRepo(t)
	ctx := context.Background()

	// 99 已删或从未存在：repo 不返回它。
	repo.EXPECT().FindByIDs(ctx, []int64{6, 99}).Return([]*chat_entity.Session{
		{ID: 6, AgentID: 3, ProjectID: 7, Title: "真实标题", LastMessageAt: 50, LastReadAt: 40},
	}, nil)

	svc := &chatSvc{}
	got, err := svc.ListSessionsByIDs(ctx, &ListSessionsByIDsRequest{IDs: []int64{6, 99}})

	require.NoError(t, err)
	require.Len(t, got.Sessions, 1, "不认识的 id 只是不出现，不算错")
	assert.Equal(t, ChatSessionLite{
		ID: 6, AgentID: 3, ProjectID: 7, Title: "真实标题", LastMessageAt: 50, LastReadAt: 40,
	}, got.Sessions[0])
}

func TestListSessionsByIDs_NormalisesIDsBeforeQuerying(t *testing.T) {
	ctx := context.Background()

	t.Run("Given duplicate and non-positive ids, When listing, Then only distinct positive ids are queried", func(t *testing.T) {
		repo := withMockSessionRepo(t)
		repo.EXPECT().FindByIDs(ctx, []int64{6, 8}).Return(nil, nil)

		svc := &chatSvc{}
		got, err := svc.ListSessionsByIDs(ctx, &ListSessionsByIDsRequest{IDs: []int64{6, 0, 8, 6, -1}})
		require.NoError(t, err)
		assert.Empty(t, got.Sessions)
	})

	t.Run("Given no usable ids, When listing, Then it returns an empty list without touching the repo", func(t *testing.T) {
		withMockSessionRepo(t) // 不 EXPECT 任何调用。

		svc := &chatSvc{}
		got, err := svc.ListSessionsByIDs(ctx, &ListSessionsByIDsRequest{IDs: []int64{0}})
		require.NoError(t, err)
		assert.NotNil(t, got.Sessions, "空列表序列化成 []，前端不必判 null")
		assert.Empty(t, got.Sessions)
	})
}

func TestListSessionsByIDs_RejectsNilRequest(t *testing.T) {
	withMockSessionRepo(t)
	svc := &chatSvc{}
	_, err := svc.ListSessionsByIDs(context.Background(), nil)
	assert.Error(t, err)
}

func TestListSessionsByIDs_PropagatesRepoError(t *testing.T) {
	repo := withMockSessionRepo(t)
	ctx := context.Background()
	repo.EXPECT().FindByIDs(ctx, []int64{6}).Return(nil, errors.New("boom"))

	svc := &chatSvc{}
	_, err := svc.ListSessionsByIDs(ctx, &ListSessionsByIDsRequest{IDs: []int64{6}})
	assert.Error(t, err)
}
