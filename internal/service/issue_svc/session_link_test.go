package issue_svc_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/chat_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/issue_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/project_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/session_issue_link_entity"
	"github.com/agentre-hub/agentre/internal/repository/agent_repo"
	"github.com/agentre-hub/agentre/internal/repository/agent_repo/mock_agent_repo"
	"github.com/agentre-hub/agentre/internal/repository/chat_repo"
	"github.com/agentre-hub/agentre/internal/repository/chat_repo/mock_chat_repo"
	"github.com/agentre-hub/agentre/internal/repository/issue_repo"
	"github.com/agentre-hub/agentre/internal/repository/issue_repo/mock_issue_repo"
	"github.com/agentre-hub/agentre/internal/repository/project_repo"
	"github.com/agentre-hub/agentre/internal/repository/project_repo/mock_project_repo"
	"github.com/agentre-hub/agentre/internal/repository/session_issue_link_repo"
	"github.com/agentre-hub/agentre/internal/repository/session_issue_link_repo/mock_session_issue_link_repo"
	"github.com/agentre-hub/agentre/internal/service/issue_svc"
)

type linkRig struct {
	ctx      context.Context
	svc      issue_svc.IssueSvc
	issues   *mock_issue_repo.MockIssueRepo
	labels   *mock_issue_repo.MockIssueLabelRepo
	links    *mock_session_issue_link_repo.MockSessionIssueLinkRepo
	sessions *mock_chat_repo.MockSessionRepo
	projects *mock_project_repo.MockProjectRepo
	agents   *mock_agent_repo.MockAgentRepo
}

func newLinkRig(t *testing.T) *linkRig {
	t.Helper()
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)
	r := &linkRig{
		ctx:      context.Background(),
		issues:   mock_issue_repo.NewMockIssueRepo(ctrl),
		labels:   mock_issue_repo.NewMockIssueLabelRepo(ctrl),
		links:    mock_session_issue_link_repo.NewMockSessionIssueLinkRepo(ctrl),
		sessions: mock_chat_repo.NewMockSessionRepo(ctrl),
		projects: mock_project_repo.NewMockProjectRepo(ctrl),
		agents:   mock_agent_repo.NewMockAgentRepo(ctrl),
	}
	issue_repo.RegisterIssue(r.issues)
	issue_repo.RegisterIssueLabel(r.labels)
	session_issue_link_repo.RegisterSessionIssueLink(r.links)
	chat_repo.RegisterSession(r.sessions)
	project_repo.RegisterProject(r.projects)
	agent_repo.RegisterAgent(r.agents)
	r.svc = issue_svc.New()
	return r
}

// registerLinkCleanup 给只关心任务删除本身的既有用例挂上「清理该任务的会话关联」这一步。
func registerLinkCleanup(t *testing.T, ctx context.Context, issueID int64) {
	t.Helper()
	ctrl := gomock.NewController(t)
	links := mock_session_issue_link_repo.NewMockSessionIssueLinkRepo(ctrl)
	links.EXPECT().DeleteByIssue(ctx, issueID).Return(nil)
	session_issue_link_repo.RegisterSessionIssueLink(links)
}

func link(sessionID, issueID int64) *session_issue_link_entity.SessionIssueLink {
	return &session_issue_link_entity.SessionIssueLink{SessionID: sessionID, IssueID: issueID}
}

// Given 阶段在「进行中」的任务, When 关联会话, Then 只写关联行:不读写 issues(任何
// Update / Move 都会因 mock 无期望而失败),阶段仍是进行中。
func TestLinkSession_DoesNotChangeTheIssueStage(t *testing.T) {
	r := newLinkRig(t)
	doing := &issue_entity.Issue{ID: 8, Stage: issue_entity.StageDoing}
	r.sessions.EXPECT().Find(r.ctx, int64(3)).Return(&chat_entity.Session{ID: 3}, nil)
	r.issues.EXPECT().Find(r.ctx, int64(8)).Return(doing, nil)
	r.links.EXPECT().Link(r.ctx, int64(3), int64(8)).Return(nil)

	require.NoError(t, r.svc.LinkSession(r.ctx, 3, 8))
	assert.Equal(t, issue_entity.StageDoing, doing.Stage)
}

// 会话或任务不存在时不写关联。
func TestLinkSession_RejectsMissingSessionOrIssue(t *testing.T) {
	t.Run("session missing", func(t *testing.T) {
		r := newLinkRig(t)
		r.sessions.EXPECT().Find(r.ctx, int64(3)).Return(nil, nil)
		assert.Error(t, r.svc.LinkSession(r.ctx, 3, 8))
	})
	t.Run("issue missing", func(t *testing.T) {
		r := newLinkRig(t)
		r.sessions.EXPECT().Find(r.ctx, int64(3)).Return(&chat_entity.Session{ID: 3}, nil)
		r.issues.EXPECT().Find(r.ctx, int64(8)).Return(nil, nil)
		assert.Error(t, r.svc.LinkSession(r.ctx, 3, 8))
	})
	t.Run("repo error is returned", func(t *testing.T) {
		r := newLinkRig(t)
		r.sessions.EXPECT().Find(r.ctx, int64(3)).Return(&chat_entity.Session{ID: 3}, nil)
		r.issues.EXPECT().Find(r.ctx, int64(8)).Return(&issue_entity.Issue{ID: 8}, nil)
		r.links.EXPECT().Link(r.ctx, int64(3), int64(8)).Return(errors.New("locked"))
		assert.Error(t, r.svc.LinkSession(r.ctx, 3, 8))
	})
}

func TestUnlinkSession_RemovesOnlyThePairAndKeepsTheStage(t *testing.T) {
	r := newLinkRig(t)
	r.links.EXPECT().Unlink(r.ctx, int64(3), int64(8)).Return(nil)
	require.NoError(t, r.svc.UnlinkSession(r.ctx, 3, 8))
}

func TestUnlinkSession_ReturnsRepoError(t *testing.T) {
	r := newLinkRig(t)
	r.links.EXPECT().Unlink(r.ctx, int64(3), int64(8)).Return(errors.New("locked"))
	assert.Error(t, r.svc.UnlinkSession(r.ctx, 3, 8))
}

// 多对多:会话 3 关联任务 8、9,会话 5 也关联任务 8。会话 3 的关联窗里任务 8「另有 1 个
// 会话」,任务 9 另有 0 个;任务 8 的卡片上列出两个会话。
func TestManyToMany_OtherSessionCountAndBothDirections(t *testing.T) {
	r := newLinkRig(t)

	r.links.EXPECT().ListBySession(r.ctx, int64(3)).Return([]*session_issue_link_entity.SessionIssueLink{link(3, 8), link(3, 9)}, nil)
	r.issues.EXPECT().Find(r.ctx, int64(8)).Return(&issue_entity.Issue{ID: 8, Title: "登录", Stage: issue_entity.StageDoing}, nil)
	r.issues.EXPECT().Find(r.ctx, int64(9)).Return(&issue_entity.Issue{ID: 9, Title: "注册", Stage: issue_entity.StageTodo}, nil)
	r.links.EXPECT().CountByIssues(r.ctx, []int64{8, 9}).Return(map[int64]int64{8: 2, 9: 1}, nil)

	got, err := r.svc.ListBySession(r.ctx, 3)
	require.NoError(t, err)
	require.Len(t, got, 2)
	assert.EqualValues(t, 8, got[0].Issue.ID)
	assert.Equal(t, "登录", got[0].Issue.Title)
	assert.Equal(t, issue_entity.StageDoing, got[0].Issue.Stage)
	assert.EqualValues(t, 1, got[0].OtherSessionCount)
	assert.EqualValues(t, 9, got[1].Issue.ID)
	assert.EqualValues(t, 0, got[1].OtherSessionCount)

	r.links.EXPECT().ListByIssue(r.ctx, int64(8)).Return([]*session_issue_link_entity.SessionIssueLink{link(3, 8), link(5, 8)}, nil)
	r.sessions.EXPECT().Find(r.ctx, int64(3)).Return(&chat_entity.Session{ID: 3, Title: "A", AgentID: 1, AgentStatus: "running"}, nil)
	r.sessions.EXPECT().Find(r.ctx, int64(5)).Return(&chat_entity.Session{ID: 5, Title: "B", AgentID: 2, AgentStatus: "idle"}, nil)
	r.agents.EXPECT().Find(r.ctx, int64(1)).Return(&agent_entity.Agent{ID: 1, Name: "Alice"}, nil)
	r.agents.EXPECT().Find(r.ctx, int64(2)).Return(&agent_entity.Agent{ID: 2, Name: "Bob"}, nil)

	sessions, err := r.svc.SessionsOf(r.ctx, 8)
	require.NoError(t, err)
	require.Len(t, sessions, 2)
	assert.EqualValues(t, 3, sessions[0].SessionID)
	assert.Equal(t, "A", sessions[0].Title)
	assert.Equal(t, "Alice", sessions[0].AgentName)
	assert.EqualValues(t, 1, sessions[0].AgentID)
	assert.EqualValues(t, 5, sessions[1].SessionID)
	assert.Equal(t, "Bob", sessions[1].AgentName)
}

// 卡片上的运行状态直接取会话自己的 agent_status(与会话列表同一来源):运行中 / 空闲 / 出错
// 原样透出,不被合并或改写。
func TestSessionsOf_CarriesTheSessionRunStatus(t *testing.T) {
	r := newLinkRig(t)
	r.links.EXPECT().ListByIssue(r.ctx, int64(8)).Return([]*session_issue_link_entity.SessionIssueLink{link(1, 8), link(2, 8), link(3, 8), link(4, 8)}, nil)
	for id, st := range map[int64]string{1: "running", 2: "idle", 3: "error", 4: "waiting"} {
		r.sessions.EXPECT().Find(r.ctx, id).Return(&chat_entity.Session{ID: id, AgentStatus: st}, nil)
	}

	got, err := r.svc.SessionsOf(r.ctx, 8)
	require.NoError(t, err)
	require.Len(t, got, 4)
	want := []string{"running", "idle", "error", "waiting"}
	for i, w := range want {
		assert.Equal(t, w, got[i].AgentStatus)
	}
}

// 已不存在(软删)的会话不出现在卡片上;其余照常。
func TestSessionsOf_SkipsSessionsThatNoLongerExist(t *testing.T) {
	r := newLinkRig(t)
	r.links.EXPECT().ListByIssue(r.ctx, int64(8)).Return([]*session_issue_link_entity.SessionIssueLink{link(3, 8), link(5, 8)}, nil)
	r.sessions.EXPECT().Find(r.ctx, int64(3)).Return(nil, nil)
	r.sessions.EXPECT().Find(r.ctx, int64(5)).Return(&chat_entity.Session{ID: 5, AgentStatus: "idle"}, nil)

	got, err := r.svc.SessionsOf(r.ctx, 8)
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.EqualValues(t, 5, got[0].SessionID)
}

func TestSessionsOf_NoLinksReturnsEmptyList(t *testing.T) {
	r := newLinkRig(t)
	r.links.EXPECT().ListByIssue(r.ctx, int64(8)).Return(nil, nil)

	got, err := r.svc.SessionsOf(r.ctx, 8)
	require.NoError(t, err)
	assert.NotNil(t, got)
	assert.Empty(t, got)
}

func TestSessionsOf_ReturnsRepoError(t *testing.T) {
	r := newLinkRig(t)
	r.links.EXPECT().ListByIssue(r.ctx, int64(8)).Return(nil, errors.New("boom"))

	_, err := r.svc.SessionsOf(r.ctx, 8)
	assert.Error(t, err)
}

func TestListBySession_SkipsDeletedIssuesAndEmptyIsNotNil(t *testing.T) {
	r := newLinkRig(t)
	r.links.EXPECT().ListBySession(r.ctx, int64(3)).Return([]*session_issue_link_entity.SessionIssueLink{link(3, 8)}, nil)
	r.issues.EXPECT().Find(r.ctx, int64(8)).Return(nil, nil)

	got, err := r.svc.ListBySession(r.ctx, 3)
	require.NoError(t, err)
	assert.NotNil(t, got)
	assert.Empty(t, got)
}

// 搜索范围:会话属于项目 2 时,是项目 2 与它的全部子孙项目(不含兄弟 / 祖先);
// 已关联的打勾(Linked);关键词去首尾空白后交给仓储。
func TestSearchForSession_ScopesToTheSessionsProjectSubtreeAndFlagsLinked(t *testing.T) {
	r := newLinkRig(t)
	r.sessions.EXPECT().Find(r.ctx, int64(3)).Return(&chat_entity.Session{ID: 3, ProjectID: 2}, nil)
	r.projects.EXPECT().List(r.ctx).Return([]*project_entity.Project{
		{ID: 1, ParentID: 0}, {ID: 2, ParentID: 1}, {ID: 3, ParentID: 2}, {ID: 4, ParentID: 1},
	}, nil)
	r.issues.EXPECT().List(r.ctx, gomock.Any()).DoAndReturn(func(_ context.Context, f issue_repo.ListFilter) ([]*issue_entity.Issue, error) {
		assert.ElementsMatch(t, []int64{2, 3}, f.ProjectIDs)
		assert.Equal(t, "登录", f.Keyword)
		return []*issue_entity.Issue{
			{ID: 8, Title: "登录页", Stage: issue_entity.StageDoing},
			{ID: 9, Title: "登录接口", Stage: issue_entity.StageTodo},
		}, nil
	})
	r.links.EXPECT().ListBySession(r.ctx, int64(3)).Return([]*session_issue_link_entity.SessionIssueLink{link(3, 9)}, nil)

	got, err := r.svc.SearchForSession(r.ctx, 3, "  登录 ")
	require.NoError(t, err)
	require.Len(t, got, 2)
	assert.EqualValues(t, 8, got[0].Issue.ID)
	assert.False(t, got[0].Linked)
	assert.EqualValues(t, 9, got[1].Issue.ID)
	assert.True(t, got[1].Linked)
}

// 会话不属于任何项目:范围是全部任务(不加项目条件,也不读项目树)。
func TestSearchForSession_SessionWithoutProjectSearchesEverything(t *testing.T) {
	r := newLinkRig(t)
	r.sessions.EXPECT().Find(r.ctx, int64(3)).Return(&chat_entity.Session{ID: 3, ProjectID: 0}, nil)
	r.issues.EXPECT().List(r.ctx, gomock.Any()).DoAndReturn(func(_ context.Context, f issue_repo.ListFilter) ([]*issue_entity.Issue, error) {
		assert.Empty(t, f.ProjectIDs, "空集合 = 全部项目")
		return []*issue_entity.Issue{{ID: 8, Title: "x"}}, nil
	})
	r.links.EXPECT().ListBySession(r.ctx, int64(3)).Return(nil, nil)

	got, err := r.svc.SearchForSession(r.ctx, 3, "")
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.False(t, got[0].Linked)
}

func TestSearchForSession_MissingSessionIsAnError(t *testing.T) {
	r := newLinkRig(t)
	r.sessions.EXPECT().Find(r.ctx, int64(3)).Return(nil, nil)

	_, err := r.svc.SearchForSession(r.ctx, 3, "x")
	assert.Error(t, err)
}

// 新建并关联:标题缺省取会话标题,项目取会话所属项目,阶段「待办」,创建后立即关联。
func TestCreateLinked_DefaultsToSessionTitleProjectAndTodoThenLinks(t *testing.T) {
	r := newLinkRig(t)
	r.sessions.EXPECT().Find(r.ctx, int64(3)).Return(&chat_entity.Session{ID: 3, Title: "排查登录", ProjectID: 5}, nil)
	r.issues.EXPECT().MaxPosition(r.ctx, issue_entity.StageTodo).Return(0.0, nil)
	r.issues.EXPECT().Create(r.ctx, gomock.Any()).DoAndReturn(func(_ context.Context, i *issue_entity.Issue) error {
		assert.Equal(t, "排查登录", i.Title)
		assert.EqualValues(t, 5, i.ProjectID)
		assert.Equal(t, issue_entity.StageTodo, i.Stage)
		i.ID = 21
		return nil
	})
	r.labels.EXPECT().SetLabels(r.ctx, int64(21), gomock.Any()).Return(nil)
	r.links.EXPECT().Link(r.ctx, int64(3), int64(21)).Return(nil)

	got, err := r.svc.CreateLinked(r.ctx, &issue_svc.CreateLinkedRequest{SessionID: 3})
	require.NoError(t, err)
	assert.EqualValues(t, 21, got.Issue.ID)
}

// 从搜索列表进入时标题是搜索词(去空白)。
func TestCreateLinked_ExplicitTitleWins(t *testing.T) {
	r := newLinkRig(t)
	r.sessions.EXPECT().Find(r.ctx, int64(3)).Return(&chat_entity.Session{ID: 3, Title: "排查登录", ProjectID: 5}, nil)
	r.issues.EXPECT().MaxPosition(r.ctx, issue_entity.StageTodo).Return(0.0, nil)
	r.issues.EXPECT().Create(r.ctx, gomock.Any()).DoAndReturn(func(_ context.Context, i *issue_entity.Issue) error {
		assert.Equal(t, "搜索词", i.Title)
		i.ID = 22
		return nil
	})
	r.labels.EXPECT().SetLabels(r.ctx, int64(22), gomock.Any()).Return(nil)
	r.links.EXPECT().Link(r.ctx, int64(3), int64(22)).Return(nil)

	_, err := r.svc.CreateLinked(r.ctx, &issue_svc.CreateLinkedRequest{SessionID: 3, Title: " 搜索词 "})
	require.NoError(t, err)
}

// 会话不存在:不创建任务,也不关联。
func TestCreateLinked_MissingSessionCreatesNothing(t *testing.T) {
	r := newLinkRig(t)
	r.sessions.EXPECT().Find(r.ctx, int64(3)).Return(nil, nil)

	_, err := r.svc.CreateLinked(r.ctx, &issue_svc.CreateLinkedRequest{SessionID: 3})
	assert.Error(t, err)
}

// 创建成功但关联失败:把错误交给调用方(任务已创建,不假装成功)。
func TestCreateLinked_LinkFailureIsReturned(t *testing.T) {
	r := newLinkRig(t)
	r.sessions.EXPECT().Find(r.ctx, int64(3)).Return(&chat_entity.Session{ID: 3, Title: "t"}, nil)
	r.issues.EXPECT().MaxPosition(r.ctx, issue_entity.StageTodo).Return(0.0, nil)
	r.issues.EXPECT().Create(r.ctx, gomock.Any()).DoAndReturn(func(_ context.Context, i *issue_entity.Issue) error {
		i.ID = 23
		return nil
	})
	r.labels.EXPECT().SetLabels(r.ctx, int64(23), gomock.Any()).Return(nil)
	r.links.EXPECT().Link(r.ctx, int64(3), int64(23)).Return(errors.New("locked"))

	_, err := r.svc.CreateLinked(r.ctx, &issue_svc.CreateLinkedRequest{SessionID: 3})
	assert.Error(t, err)
}

// 任务在本机被删除:先清它的全部关联,再删任务;清理失败时任务不删(重试能走完整条路)。
func TestIssueDelete_CleansItsSessionLinksFirst(t *testing.T) {
	r := newLinkRig(t)
	r.issues.EXPECT().Find(r.ctx, int64(9)).Return(&issue_entity.Issue{ID: 9}, nil)
	gomock.InOrder(
		r.links.EXPECT().DeleteByIssue(r.ctx, int64(9)).Return(nil),
		r.issues.EXPECT().Delete(r.ctx, int64(9)).Return(nil),
	)
	require.NoError(t, r.svc.Delete(r.ctx, 9))
}

func TestIssueDelete_LinkCleanupFailureKeepsTheIssue(t *testing.T) {
	r := newLinkRig(t)
	r.issues.EXPECT().Find(r.ctx, int64(9)).Return(&issue_entity.Issue{ID: 9}, nil)
	r.links.EXPECT().DeleteByIssue(r.ctx, int64(9)).Return(errors.New("locked"))

	assert.Error(t, r.svc.Delete(r.ctx, 9))
}
