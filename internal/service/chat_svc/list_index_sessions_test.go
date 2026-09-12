package chat_svc

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/agentre-hub/agentre/internal/model/entity/chat_entity"
	"github.com/agentre-hub/agentre/internal/repository/chat_repo"
	"github.com/agentre-hub/agentre/internal/repository/chat_repo/mock_chat_repo"
)

// ListIndexSessions 是单一会话索引的分页查询（见
// docs/specs/2026-08-16-unified-chat-index.md）。三个 scope 走同一条查询、返回**同一种
// 载荷**，索引三个轴因此只需要一处投影：
//   - recent  ：跨 agent、跨项目的全局最近活动（「按时间」档）—— 此前根本拿不到
//   - free    ：project_id = 0 的会话（「随手对话」组）—— ListSessions 挡在 projectID > 0
//   - project ：某个项目下的会话 —— 取代 ProjectListSessions 那个缺 bgRunning 的形状

// sessionFilterID 把一个 id 变成 filter 上的指针维。0 在每一维上都是有意义的取值
// （随手对话 / 本机），所以这些维是指针而不是哨兵。
func sessionFilterID(v int64) *int64 { return &v }

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

	repo.EXPECT().ListIndexPaged(ctx, chat_repo.SessionIndexFilter{}, 40, 20).Return([]*chat_entity.Session{
		{ID: 9, AgentID: 1, ProjectID: 3, Title: "in-project", LastMessageAt: 90},
		{ID: 8, AgentID: 2, ProjectID: 0, Title: "free one", LastMessageAt: 80},
	}, nil)
	repo.EXPECT().CountIndex(ctx, chat_repo.SessionIndexFilter{}).Return(int64(100), nil)

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

	free := chat_repo.SessionIndexFilter{ProjectID: sessionFilterID(0)}
	repo.EXPECT().ListIndexPaged(ctx, free, 0, 20).Return([]*chat_entity.Session{
		{ID: 5, AgentID: 4, ProjectID: 0, Title: "随手问一句"},
	}, nil)
	repo.EXPECT().CountIndex(ctx, free).Return(int64(1), nil)

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
		repo.EXPECT().ListIndexPaged(ctx, chat_repo.SessionIndexFilter{}, 0, listAgentSessionsDefaultLimit).Return(nil, nil)
		repo.EXPECT().CountIndex(ctx, chat_repo.SessionIndexFilter{}).Return(int64(0), nil)

		svc := &chatSvc{}
		_, err := svc.ListIndexSessions(ctx, &ListIndexSessionsRequest{Scope: SessionScopeRecent})
		require.NoError(t, err)
	})

	t.Run("Given a limit above the cap, When listing, Then it is clamped instead of letting one call pull everything", func(t *testing.T) {
		repo := withMockSessionRepo(t)
		repo.EXPECT().ListIndexPaged(ctx, chat_repo.SessionIndexFilter{}, 0, listAgentSessionsMaxLimit).Return(nil, nil)
		repo.EXPECT().CountIndex(ctx, chat_repo.SessionIndexFilter{}).Return(int64(0), nil)

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

	repo.EXPECT().ListIndexPaged(ctx, chat_repo.SessionIndexFilter{}, 0, listAgentSessionsDefaultLimit).Return(nil, boom)

	svc := &chatSvc{}
	_, err := svc.ListIndexSessions(ctx, &ListIndexSessionsRequest{Scope: SessionScopeRecent})
	assert.Error(t, err)
}

func TestListIndexSessions_ProjectScope(t *testing.T) {
	repo := withMockSessionRepo(t)
	ctx := context.Background()

	inProject := chat_repo.SessionIndexFilter{ProjectID: sessionFilterID(7)}
	repo.EXPECT().ListIndexPaged(ctx, inProject, 0, 5).Return([]*chat_entity.Session{
		{ID: 3, AgentID: 9, ProjectID: 7, Title: "重构索引"},
	}, nil)
	repo.EXPECT().CountIndex(ctx, inProject).Return(int64(9), nil)

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

// ── machine scope ────────────────────────────────────────────────────────────
//
// 第四个 scope，给桌面端的「按机器」轴（docs/specs/2026-08-21-index-glyph-and-machine-axis.md）。
// 分组这一维是 chat_entity.Session.ExecDeviceID —— 会话表上的一列，**0 = 本机执行**。
// 与项目 scope 的判据差一格正是因为这个 0：项目那边 0 有专门的 free scope，这边 0 是
// 一台正当的机器（本机），拒绝它就等于本机那一组永远取不到数。

func TestListIndexSessions_MachineScope(t *testing.T) {
	repo := withMockSessionRepo(t)
	ctx := context.Background()

	onDevice := chat_repo.SessionIndexFilter{DeviceID: sessionFilterID(7)}
	repo.EXPECT().ListIndexPaged(ctx, onDevice, 0, 5).Return([]*chat_entity.Session{
		{ID: 3, AgentID: 9, ProjectID: 7, ExecDeviceID: 7, Title: "跑在 7 号机上"},
	}, nil)
	repo.EXPECT().CountIndex(ctx, onDevice).Return(int64(9), nil)

	svc := &chatSvc{}
	got, err := svc.ListIndexSessions(ctx, &ListIndexSessionsRequest{
		Scope: SessionScopeMachine, DeviceID: 7, Limit: 5,
	})

	require.NoError(t, err)
	require.Len(t, got.Sessions, 1)
	assert.Equal(t, int64(9), got.Total)
	assert.True(t, got.HasMore, "机器组同样分页，其余走「查看全部 N」")
	assert.Equal(t, int64(7), got.Sessions[0].ProjectID)
	assert.Equal(t, int64(9), got.Sessions[0].AgentID)
}

func TestListIndexSessions_MachineScopeAcceptsLocalDevice(t *testing.T) {
	repo := withMockSessionRepo(t)
	ctx := context.Background()

	// ExecDeviceID = 0 是**本机**（chat_entity.Session 的约定），不是「没有机器」。
	// 把它当非法值拒掉，本机那一组就永远空着 —— 而绝大多数会话都在本机。
	local := chat_repo.SessionIndexFilter{DeviceID: sessionFilterID(0)}
	repo.EXPECT().ListIndexPaged(ctx, local, 0, 20).Return([]*chat_entity.Session{
		{ID: 1, AgentID: 2, ExecDeviceID: 0, Title: "本机的一条"},
	}, nil)
	repo.EXPECT().CountIndex(ctx, local).Return(int64(1), nil)

	svc := &chatSvc{}
	got, err := svc.ListIndexSessions(ctx, &ListIndexSessionsRequest{
		Scope: SessionScopeMachine, DeviceID: 0,
	})

	require.NoError(t, err)
	require.Len(t, got.Sessions, 1)
	assert.False(t, got.HasMore)
}

func TestListIndexSessions_MachineScopeRejectsNegativeDevice(t *testing.T) {
	ctx := context.Background()

	withMockSessionRepo(t) // 不 EXPECT 任何调用
	svc := &chatSvc{}
	_, err := svc.ListIndexSessions(ctx, &ListIndexSessionsRequest{
		Scope: SessionScopeMachine, DeviceID: -1,
	})

	assert.Error(t, err, "负的设备号不是任何一台机器")
}

// ── 搜索：关键词是 scope 的一部分 ────────────────────────────────────────────
//
// 索引的搜索框此前只在**前端已加载的那一页**上做子串匹配，命中范围等于首屏窗口
// （项目组 5 条 / 时间轴 30 条），库里更早的会话搜不出来。修法是把关键词并进取数
// 本身：每条轴本来就各有一条查询，加上 Keyword 之后列表、总数、翻页全都自动是
// 「过滤后」的口径，前端不再需要第二套过滤。
//
// 关键词与「按哪一维分组」是正交的两件事，所以它必须能叠在每一个 scope 上 ——
// 尤其是机器轴：那一维的会话此前完全没有搜索可言。

func TestListIndexSessions_KeywordRidesOnEveryScope(t *testing.T) {
	ctx := context.Background()

	cases := []struct {
		name string
		req  ListIndexSessionsRequest
		want chat_repo.SessionIndexFilter
	}{
		{
			name: "recent",
			req:  ListIndexSessionsRequest{Scope: SessionScopeRecent, Keyword: "happy"},
			want: chat_repo.SessionIndexFilter{Keyword: "happy"},
		},
		{
			name: "free",
			req:  ListIndexSessionsRequest{Scope: SessionScopeFree, Keyword: "happy"},
			want: chat_repo.SessionIndexFilter{ProjectID: sessionFilterID(0), Keyword: "happy"},
		},
		{
			name: "project",
			req:  ListIndexSessionsRequest{Scope: SessionScopeProject, ProjectID: 1, Keyword: "happy"},
			want: chat_repo.SessionIndexFilter{ProjectID: sessionFilterID(1), Keyword: "happy"},
		},
		{
			name: "machine",
			req:  ListIndexSessionsRequest{Scope: SessionScopeMachine, DeviceID: 0, Keyword: "happy"},
			want: chat_repo.SessionIndexFilter{DeviceID: sessionFilterID(0), Keyword: "happy"},
		},
		{
			name: "agent",
			req:  ListIndexSessionsRequest{Scope: SessionScopeAgent, AgentID: 2, Keyword: "happy"},
			want: chat_repo.SessionIndexFilter{AgentID: sessionFilterID(2), Keyword: "happy"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			repo := withMockSessionRepo(t)
			repo.EXPECT().ListIndexPaged(ctx, tc.want, 0, listAgentSessionsDefaultLimit).
				Return([]*chat_entity.Session{{ID: 3495, Title: "看看happy是怎么实现中继的"}}, nil)
			// 总数必须收同一个 filter：组头的「会话 N」与「查看全部 N」写的就是它，
			// 拿未过滤的总数去配过滤后的列表，两行字会自相矛盾。
			repo.EXPECT().CountIndex(ctx, tc.want).Return(int64(17), nil)

			svc := &chatSvc{}
			req := tc.req
			got, err := svc.ListIndexSessions(ctx, &req)

			require.NoError(t, err)
			require.Len(t, got.Sessions, 1)
			assert.Equal(t, int64(3495), got.Sessions[0].ID)
			assert.Equal(t, int64(17), got.Total)
		})
	}
}

// agent scope 是搜索给 Agent 轴补的取数口。不搜索时那条轴的会话仍由 ListAgents 的
// 每 agent 前 5 条给出（不为了摆一屏多发 N 个 RPC）；一旦开搜，前 5 条那个窗口就不够
// 用了，得按 agent 各查一遍全量。
func TestListIndexSessions_AgentScope(t *testing.T) {
	ctx := context.Background()

	t.Run("Given an agent scope, When listing, Then it narrows to that agent", func(t *testing.T) {
		repo := withMockSessionRepo(t)
		byAgent := chat_repo.SessionIndexFilter{AgentID: sessionFilterID(2)}
		repo.EXPECT().ListIndexPaged(ctx, byAgent, 0, 5).Return([]*chat_entity.Session{
			{ID: 3, AgentID: 2, ProjectID: 7, Title: "某一条"},
		}, nil)
		repo.EXPECT().CountIndex(ctx, byAgent).Return(int64(9), nil)

		svc := &chatSvc{}
		got, err := svc.ListIndexSessions(ctx, &ListIndexSessionsRequest{
			Scope: SessionScopeAgent, AgentID: 2, Limit: 5,
		})

		require.NoError(t, err)
		require.Len(t, got.Sessions, 1)
		assert.Equal(t, int64(9), got.Total)
		assert.True(t, got.HasMore)
	})

	// agentID 0 不是「不限 agent」—— recent scope 才是。放行它会让调用方以为自己在看
	// 某个 agent，实际拿到的是全部会话。
	t.Run("Given a non-positive agent id, When listing, Then it is rejected", func(t *testing.T) {
		for _, id := range []int64{0, -1} {
			withMockSessionRepo(t) // 不 EXPECT 任何调用
			svc := &chatSvc{}
			_, err := svc.ListIndexSessions(ctx, &ListIndexSessionsRequest{
				Scope: SessionScopeAgent, AgentID: id,
			})
			assert.Error(t, err, "agentID=%d 应被拒绝", id)
		}
	})
}
