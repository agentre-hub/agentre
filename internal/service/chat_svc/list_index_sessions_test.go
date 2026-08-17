package chat_svc

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/agentre-ai/agentre/internal/model/entity/chat_entity"
	"github.com/agentre-ai/agentre/internal/repository/chat_repo"
	"github.com/agentre-ai/agentre/internal/repository/chat_repo/mock_chat_repo"
)

// ListIndexSessions 是单一会话索引的分页查询（见
// docs/specs/2026-08-16-unified-chat-index.md）。三个 scope 走同一条查询、返回**同一种
// 载荷**，索引三个轴因此只需要一处投影：
//   - recent  ：跨 agent、跨项目的全局最近活动（「按时间」档）—— 此前根本拿不到
//   - free    ：project_id = 0 的会话（「随手对话」组）—— ListSessions 挡在 projectID > 0
//   - project ：某个项目下的会话 —— 取代 ProjectListSessions 那个缺 bgRunning 的形状

func withMockSessionRepo(t *testing.T) *mock_chat_repo.MockSessionRepo {
	t.Helper()
	ctrl := gomock.NewController(t)
	repo := mock_chat_repo.NewMockSessionRepo(ctrl)
	prev := chat_repo.Session()
	chat_repo.RegisterSession(repo)
	t.Cleanup(func() { chat_repo.RegisterSession(prev) })
	return repo
}

func TestListIndexSessions_RecentScope(t *testing.T) {
	repo := withMockSessionRepo(t)
	ctx := context.Background()

	repo.EXPECT().ListRecentPaged(ctx, 40, 20).Return([]*chat_entity.Session{
		{ID: 9, AgentID: 1, ProjectID: 3, Title: "in-project", LastMessageAt: 90},
		{ID: 8, AgentID: 2, ProjectID: 0, Title: "free one", LastMessageAt: 80},
	}, nil)
	repo.EXPECT().CountAll(ctx).Return(int64(100), nil)

	svc := &chatSvc{}
	got, err := svc.ListIndexSessions(ctx, &ListIndexSessionsRequest{
		Scope: SessionScopeRecent, Offset: 40, Limit: 20,
	})

	require.NoError(t, err)
	require.Len(t, got.Sessions, 2)
	assert.Equal(t, int64(100), got.Total)
	assert.True(t, got.HasMore, "40+2 < 100 时还有下一页")
	// 索引的行首要放「分组没说的那一维」（决策 4/5），所以每行必须自带 agent 与项目。
	assert.Equal(t, int64(1), got.Sessions[0].AgentID)
	assert.Equal(t, int64(3), got.Sessions[0].ProjectID)
	assert.Equal(t, int64(2), got.Sessions[1].AgentID)
	assert.Equal(t, int64(0), got.Sessions[1].ProjectID, "自由会话如实报 0，不猜一个项目")
}

func TestListIndexSessions_FreeScope(t *testing.T) {
	repo := withMockSessionRepo(t)
	ctx := context.Background()

	repo.EXPECT().ListFreePaged(ctx, 0, 20).Return([]*chat_entity.Session{
		{ID: 5, AgentID: 4, ProjectID: 0, Title: "随手问一句"},
	}, nil)
	repo.EXPECT().CountFree(ctx).Return(int64(1), nil)

	svc := &chatSvc{}
	got, err := svc.ListIndexSessions(ctx, &ListIndexSessionsRequest{
		Scope: SessionScopeFree, Offset: 0, Limit: 20,
	})

	require.NoError(t, err)
	require.Len(t, got.Sessions, 1)
	assert.Equal(t, int64(1), got.Total)
	assert.False(t, got.HasMore)
}

func TestListIndexSessions_LimitBounds(t *testing.T) {
	ctx := context.Background()

	t.Run("Given limit 0, When listing, Then the default page size is used", func(t *testing.T) {
		repo := withMockSessionRepo(t)
		repo.EXPECT().ListRecentPaged(ctx, 0, listAgentSessionsDefaultLimit).Return(nil, nil)
		repo.EXPECT().CountAll(ctx).Return(int64(0), nil)

		svc := &chatSvc{}
		_, err := svc.ListIndexSessions(ctx, &ListIndexSessionsRequest{Scope: SessionScopeRecent})
		require.NoError(t, err)
	})

	t.Run("Given a limit above the cap, When listing, Then it is clamped instead of letting one call pull everything", func(t *testing.T) {
		repo := withMockSessionRepo(t)
		repo.EXPECT().ListRecentPaged(ctx, 0, listAgentSessionsMaxLimit).Return(nil, nil)
		repo.EXPECT().CountAll(ctx).Return(int64(0), nil)

		svc := &chatSvc{}
		_, err := svc.ListIndexSessions(ctx, &ListIndexSessionsRequest{
			Scope: SessionScopeRecent, Limit: listAgentSessionsMaxLimit + 500,
		})
		require.NoError(t, err)
	})
}

func TestListIndexSessions_RejectsBadInput(t *testing.T) {
	ctx := context.Background()

	t.Run("Given a nil request, When listing, Then it is an invalid parameter", func(t *testing.T) {
		withMockSessionRepo(t) // 不 EXPECT 任何调用：校验必须在碰 repo 之前。
		svc := &chatSvc{}
		_, err := svc.ListIndexSessions(ctx, nil)
		assert.Error(t, err)
	})

	t.Run("Given an unknown scope, When listing, Then it is rejected rather than silently treated as recent", func(t *testing.T) {
		withMockSessionRepo(t)
		svc := &chatSvc{}
		_, err := svc.ListIndexSessions(ctx, &ListIndexSessionsRequest{Scope: "byVibes"})
		assert.Error(t, err)
	})

	t.Run("Given a negative offset, When listing, Then it is rejected", func(t *testing.T) {
		withMockSessionRepo(t)
		svc := &chatSvc{}
		_, err := svc.ListIndexSessions(ctx, &ListIndexSessionsRequest{
			Scope: SessionScopeRecent, Offset: -1,
		})
		assert.Error(t, err)
	})
}

func TestListIndexSessions_PropagatesRepoError(t *testing.T) {
	repo := withMockSessionRepo(t)
	ctx := context.Background()
	boom := errors.New("boom")

	repo.EXPECT().ListRecentPaged(ctx, 0, listAgentSessionsDefaultLimit).Return(nil, boom)

	svc := &chatSvc{}
	_, err := svc.ListIndexSessions(ctx, &ListIndexSessionsRequest{Scope: SessionScopeRecent})
	assert.Error(t, err)
}

func TestListIndexSessions_ProjectScope(t *testing.T) {
	repo := withMockSessionRepo(t)
	ctx := context.Background()

	repo.EXPECT().ListByProjectPaged(ctx, int64(7), 0, 5).Return([]*chat_entity.Session{
		{ID: 3, AgentID: 9, ProjectID: 7, Title: "重构索引"},
	}, nil)
	repo.EXPECT().CountByProject(ctx, int64(7)).Return(int64(9), nil)

	svc := &chatSvc{}
	got, err := svc.ListIndexSessions(ctx, &ListIndexSessionsRequest{
		Scope: SessionScopeProject, ProjectID: 7, Limit: 5,
	})

	require.NoError(t, err)
	require.Len(t, got.Sessions, 1)
	assert.Equal(t, int64(9), got.Total)
	assert.True(t, got.HasMore, "项目组默认只展开前几条，其余走「查看全部 N」")
	assert.Equal(t, int64(7), got.Sessions[0].ProjectID)
	assert.Equal(t, int64(9), got.Sessions[0].AgentID)
}

func TestListIndexSessions_ProjectScopeRequiresPositiveID(t *testing.T) {
	ctx := context.Background()

	// projectID 0 有专门的 scope（free）。从 project 这条路传 0 必是调用方漏传了 ——
	// 若在这里当成 free 放行，用户会以为自己在看某个项目，实际看到的是全部自由会话。
	for _, id := range []int64{0, -1} {
		withMockSessionRepo(t) // 不 EXPECT 任何调用
		svc := &chatSvc{}
		_, err := svc.ListIndexSessions(ctx, &ListIndexSessionsRequest{
			Scope: SessionScopeProject, ProjectID: id,
		})
		assert.Error(t, err, "projectID=%d 应被拒绝", id)
	}
}
