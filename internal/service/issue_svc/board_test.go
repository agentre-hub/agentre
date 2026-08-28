package issue_svc_test

import (
	"context"
	"testing"
	"time"

	"github.com/cago-frame/cago/pkg/utils/httputils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/agentre-hub/agentre/internal/model/entity/issue_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/project_entity"
	"github.com/agentre-hub/agentre/internal/pkg/code"
	"github.com/agentre-hub/agentre/internal/repository/issue_repo"
	"github.com/agentre-hub/agentre/internal/repository/issue_repo/mock_issue_repo"
	"github.com/agentre-hub/agentre/internal/repository/project_repo"
	"github.com/agentre-hub/agentre/internal/repository/project_repo/mock_project_repo"
	"github.com/agentre-hub/agentre/internal/service/issue_svc"
)

// setupBoard 在 setupIssueSvc 之上再挂一个项目仓储 mock：项目范围要展开成子树 id，
// 项目选择器的计数也要按项目树汇总。
func setupBoard(t *testing.T) (
	context.Context,
	*mock_issue_repo.MockIssueRepo,
	*mock_issue_repo.MockLabelRepo,
	*mock_issue_repo.MockIssueLabelRepo,
	*mock_project_repo.MockProjectRepo,
	issue_svc.IssueSvc,
) {
	t.Helper()
	ctx, mi, ml, mil, svc := setupIssueSvc(t)
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)
	mp := mock_project_repo.NewMockProjectRepo(ctrl)
	project_repo.RegisterProject(mp)
	return ctx, mi, ml, mil, mp, svc
}

// projectTree 是一棵三层树：1 → 2 → 4，1 → 3，另有独立的 9。
func projectTree() []*project_entity.Project {
	return []*project_entity.Project{
		{ID: 1, ParentID: 0, Name: "root"},
		{ID: 2, ParentID: 1, Name: "child"},
		{ID: 3, ParentID: 1, Name: "sibling"},
		{ID: 4, ParentID: 2, Name: "grandchild"},
		{ID: 9, ParentID: 0, Name: "other"},
	}
}

// TestIssueSvcList_ProjectScopeCollectsTheWholeSubtree 选中父项目时看到的不止这一个
// 项目：整棵子树（含第三层）都要装进查询范围。
func TestIssueSvcList_ProjectScopeCollectsTheWholeSubtree(t *testing.T) {
	ctx, mi, ml, mil, mp, svc := setupBoard(t)
	mp.EXPECT().List(ctx).Return(projectTree(), nil).AnyTimes()
	mi.EXPECT().List(ctx, gomock.Any()).DoAndReturn(
		func(_ context.Context, f issue_repo.ListFilter) ([]*issue_entity.Issue, error) {
			assert.Equal(t, []int64{1, 2, 4, 3}, f.ProjectIDs)
			return nil, nil
		})
	mil.EXPECT().ListByIssues(ctx, gomock.Any()).Return(map[int64][]int64{}, nil)
	ml.EXPECT().List(ctx).Return(nil, nil)
	mi.EXPECT().StageCounts(ctx, gomock.Any()).Return(map[string]int64{}, nil).Times(2)
	mi.EXPECT().CountUnfinishedByProject(ctx).Return(map[int64]int64{}, nil)

	_, err := svc.List(ctx, &issue_svc.ListIssuesRequest{
		Scope: issue_svc.ScopeProject, ProjectID: 1,
	})
	require.NoError(t, err)
}

// TestIssueSvcList_UnassignedScope 「未归属」是 project_id = 0 那一档，和「全部项目」
// 分得开：后者不加任何项目条件。
func TestIssueSvcList_UnassignedScope(t *testing.T) {
	ctx, mi, ml, mil, mp, svc := setupBoard(t)
	mp.EXPECT().List(ctx).Return(projectTree(), nil).AnyTimes()
	mi.EXPECT().List(ctx, gomock.Any()).DoAndReturn(
		func(_ context.Context, f issue_repo.ListFilter) ([]*issue_entity.Issue, error) {
			assert.Equal(t, []int64{0}, f.ProjectIDs)
			return nil, nil
		})
	mil.EXPECT().ListByIssues(ctx, gomock.Any()).Return(map[int64][]int64{}, nil)
	ml.EXPECT().List(ctx).Return(nil, nil)
	mi.EXPECT().StageCounts(ctx, gomock.Any()).Return(map[string]int64{}, nil).Times(2)
	mi.EXPECT().CountUnfinishedByProject(ctx).Return(map[int64]int64{}, nil)

	_, err := svc.List(ctx, &issue_svc.ListIssuesRequest{Scope: issue_svc.ScopeUnassigned})
	require.NoError(t, err)
}

func TestIssueSvcList_AllProjectsScopeAddsNoProjectCondition(t *testing.T) {
	ctx, mi, ml, mil, mp, svc := setupBoard(t)
	mp.EXPECT().List(ctx).Return(projectTree(), nil).AnyTimes()
	mi.EXPECT().List(ctx, gomock.Any()).DoAndReturn(
		func(_ context.Context, f issue_repo.ListFilter) ([]*issue_entity.Issue, error) {
			assert.Empty(t, f.ProjectIDs)
			return nil, nil
		})
	mil.EXPECT().ListByIssues(ctx, gomock.Any()).Return(map[int64][]int64{}, nil)
	ml.EXPECT().List(ctx).Return(nil, nil)
	mi.EXPECT().StageCounts(ctx, gomock.Any()).Return(map[string]int64{}, nil).Times(2)
	mi.EXPECT().CountUnfinishedByProject(ctx).Return(map[int64]int64{}, nil)

	_, err := svc.List(ctx, &issue_svc.ListIssuesRequest{})
	require.NoError(t, err)
}

// TestIssueSvcList_PassesEveryFilterCondition 六个条件里除「项目」外的五个逐条落到
// 仓储过滤条件上；「已完成保留多久」在 service 里按天换算成绝对下界。
func TestIssueSvcList_PassesEveryFilterCondition(t *testing.T) {
	ctx, mi, ml, mil, mp, svc := setupBoard(t)
	mp.EXPECT().List(ctx).Return(nil, nil).AnyTimes()
	before := time.Now().UnixMilli()
	mi.EXPECT().List(ctx, gomock.Any()).DoAndReturn(
		func(_ context.Context, f issue_repo.ListFilter) ([]*issue_entity.Issue, error) {
			assert.Equal(t, "#179", f.Keyword)
			assert.Equal(t, []int64{1, 2}, f.LabelIDs)
			assert.True(t, f.LabelMatchAll)
			assert.True(t, f.NoLabel)
			assert.Equal(t, int64(10), f.UpdatedFrom)
			assert.Equal(t, int64(20), f.UpdatedTo)
			assert.Equal(t, int64(30), f.CreatedFrom)
			assert.Equal(t, int64(40), f.CreatedTo)
			// 30 天窗口：下界落在 now - 30d 附近（用例执行耗时之内）。
			want := before - 30*24*int64(time.Hour/time.Millisecond)
			assert.InDelta(t, want, f.DoneAfter, 5000)
			return nil, nil
		})
	mil.EXPECT().ListByIssues(ctx, gomock.Any()).Return(map[int64][]int64{}, nil)
	ml.EXPECT().List(ctx).Return(nil, nil)
	mi.EXPECT().StageCounts(ctx, gomock.Any()).Return(map[string]int64{}, nil).Times(2)
	mi.EXPECT().CountUnfinishedByProject(ctx).Return(map[int64]int64{}, nil)

	_, err := svc.List(ctx, &issue_svc.ListIssuesRequest{
		Keyword: "#179", LabelIDs: []int64{1, 2, 2}, LabelMatchAll: true, NoLabel: true,
		UpdatedFrom: 10, UpdatedTo: 20, CreatedFrom: 30, CreatedTo: 40, DoneWithinDays: 30,
	})
	require.NoError(t, err)
}

// TestIssueSvcList_TotalsIgnoreTheFilters 列头是「命中 / 全部」：命中数吃全部条件，
// 全部数只吃项目范围 —— 否则筛选一开两个数一起缩水，分母就没意义了。
func TestIssueSvcList_TotalsIgnoreTheFilters(t *testing.T) {
	ctx, mi, ml, mil, mp, svc := setupBoard(t)
	mp.EXPECT().List(ctx).Return(projectTree(), nil).AnyTimes()
	mi.EXPECT().List(ctx, gomock.Any()).Return(nil, nil)
	mil.EXPECT().ListByIssues(ctx, gomock.Any()).Return(map[int64][]int64{}, nil)
	ml.EXPECT().List(ctx).Return(nil, nil)
	mi.EXPECT().StageCounts(ctx, gomock.Any()).DoAndReturn(
		func(_ context.Context, f issue_repo.ListFilter) (map[string]int64, error) {
			if f.Keyword != "" {
				assert.Equal(t, []int64{9}, f.ProjectIDs)
				return map[string]int64{issue_entity.StageTodo: 1}, nil
			}
			// 「全部」那一次：只剩项目范围，别的条件一个都不许带。
			assert.Equal(t, []int64{9}, f.ProjectIDs)
			assert.Empty(t, f.LabelIDs)
			assert.False(t, f.NoLabel)
			assert.Zero(t, f.DoneAfter)
			assert.Zero(t, f.UpdatedFrom)
			return map[string]int64{issue_entity.StageTodo: 9}, nil
		}).Times(2)
	mi.EXPECT().CountUnfinishedByProject(ctx).Return(map[int64]int64{}, nil)

	got, err := svc.List(ctx, &issue_svc.ListIssuesRequest{
		Scope: issue_svc.ScopeProject, ProjectID: 9,
		Keyword: "x", LabelIDs: []int64{1}, NoLabel: true, DoneWithinDays: 30, UpdatedFrom: 5,
	})
	require.NoError(t, err)
	assert.Equal(t, int64(1), got.StageCounts[issue_entity.StageTodo])
	assert.Equal(t, int64(9), got.StageTotals[issue_entity.StageTodo])
}

// TestIssueSvcList_ProjectCountsRollUpTheSubtree 选择器每项右侧的计数是「该项目及其
// 子树里未完成的任务数」，且不随筛选变化。
func TestIssueSvcList_ProjectCountsRollUpTheSubtree(t *testing.T) {
	ctx, mi, ml, mil, mp, svc := setupBoard(t)
	mp.EXPECT().List(ctx).Return(projectTree(), nil).AnyTimes()
	mi.EXPECT().List(ctx, gomock.Any()).Return(nil, nil)
	mil.EXPECT().ListByIssues(ctx, gomock.Any()).Return(map[int64][]int64{}, nil)
	ml.EXPECT().List(ctx).Return(nil, nil)
	mi.EXPECT().StageCounts(ctx, gomock.Any()).Return(map[string]int64{}, nil).Times(2)
	mi.EXPECT().CountUnfinishedByProject(ctx).Return(map[int64]int64{
		0: 3, 1: 1, 2: 2, 4: 5, 9: 7,
	}, nil)

	got, err := svc.List(ctx, &issue_svc.ListIssuesRequest{Keyword: "narrow"})
	require.NoError(t, err)
	assert.Equal(t, int64(8), got.ProjectCounts[1], "1 自己 1 + 2 的 2 + 4 的 5")
	assert.Equal(t, int64(7), got.ProjectCounts[2], "2 自己 2 + 4 的 5")
	assert.Equal(t, int64(5), got.ProjectCounts[4])
	assert.Equal(t, int64(0), got.ProjectCounts[3], "没有任务的项目照常报 0")
	assert.Equal(t, int64(3), got.ProjectCounts[0], "未归属自成一档，不挂在任何项目下")
}

// TestIssueSvcCreate_RoundTripsExecutionAssignment 三个执行字段建任务时要落进实体，
// 本轮没有任何路径读它们，写丢了不会有别的用例变红。
func TestIssueSvcCreate_RoundTripsExecutionAssignment(t *testing.T) {
	ctx, mi, _, mil, _, svc := setupBoard(t)
	mi.EXPECT().List(ctx, issue_repo.ListFilter{Stage: issue_entity.StageTodo, Sort: "position"}).
		Return(nil, nil)
	mi.EXPECT().Create(ctx, gomock.Any()).DoAndReturn(func(_ context.Context, i *issue_entity.Issue) error {
		i.ID = 9
		assert.Equal(t, int64(3), i.AssigneeAgentID)
		assert.Equal(t, int64(11), i.AgentBackendID)
		assert.Equal(t, "openai", i.LLMProviderKey)
		assert.Equal(t, "gpt-5", i.LLMModelKey)
		return nil
	})
	mil.EXPECT().SetLabels(ctx, int64(9), nil).Return(nil)

	got, err := svc.Create(ctx, &issue_svc.CreateIssueRequest{
		Title: "demo",
		Execution: issue_svc.ExecutionAssignment{
			AssigneeAgentID: 3, AgentBackendID: 11,
			LLMProviderKey: "openai", LLMModelKey: "gpt-5",
		},
	})
	require.NoError(t, err)
	assert.Equal(t, "gpt-5", got.Issue.LLMModelKey)
}

// TestIssueSvcUpdate_RoundTripsExecutionAssignmentAndStage 编辑态里阶段仍可改，
// 三个执行字段照常往返。
func TestIssueSvcUpdate_RoundTripsExecutionAssignmentAndStage(t *testing.T) {
	ctx, mi, ml, mil, _, svc := setupBoard(t)
	mi.EXPECT().Find(ctx, int64(3)).Return(&issue_entity.Issue{
		ID: 3, Title: "old", State: issue_entity.StateOpen, Stage: issue_entity.StageTodo,
	}, nil)
	mi.EXPECT().Update(ctx, gomock.Any()).DoAndReturn(func(_ context.Context, i *issue_entity.Issue) error {
		assert.Equal(t, issue_entity.StageDone, i.Stage)
		assert.Equal(t, issue_entity.StateClosed, i.State, "落进已完成列的任务同时关闭")
		assert.NotZero(t, i.ClosedAt)
		assert.Equal(t, int64(11), i.AgentBackendID)
		assert.Equal(t, "openai", i.LLMProviderKey)
		assert.Equal(t, "gpt-5", i.LLMModelKey)
		return nil
	})
	mil.EXPECT().SetLabels(ctx, int64(3), nil).Return(nil)
	_ = ml

	got, err := svc.Update(ctx, &issue_svc.UpdateIssueRequest{
		ID: 3, Title: "new", Stage: issue_entity.StageDone,
		Execution: issue_svc.ExecutionAssignment{
			AgentBackendID: 11, LLMProviderKey: "openai", LLMModelKey: "gpt-5",
		},
	})
	require.NoError(t, err)
	assert.Equal(t, issue_entity.StageDone, got.Issue.Stage)
}

// TestIssueSvcListLabels_ReportsUsage 标签管理列表要报出「被 N 个任务使用」。
func TestIssueSvcListLabels_ReportsUsage(t *testing.T) {
	ctx, _, ml, mil, _, svc := setupBoard(t)
	ml.EXPECT().List(ctx).Return([]*issue_entity.Label{
		{ID: 2, Name: "bug", Tone: issue_entity.ToneRed},
		{ID: 3, Name: "docs", Tone: issue_entity.ToneGray},
	}, nil)
	mil.EXPECT().CountByLabel(ctx).Return(map[int64]int64{2: 4}, nil)

	got, err := svc.ListLabels(ctx)
	require.NoError(t, err)
	require.Len(t, got, 2)
	assert.Equal(t, int64(4), got[0].UsageCount)
	assert.Equal(t, int64(0), got[1].UsageCount, "没被用过的标签报 0")
}

func TestIssueSvcCreateLabel(t *testing.T) {
	ctx, _, ml, mil, _, svc := setupBoard(t)
	ml.EXPECT().FindByName(ctx, "wire").Return(nil, nil)
	ml.EXPECT().Create(ctx, gomock.Any()).DoAndReturn(func(_ context.Context, l *issue_entity.Label) error {
		l.ID = 9
		assert.Equal(t, "wire", l.Name)
		assert.Equal(t, issue_entity.ToneBlue, l.Tone)
		return nil
	})
	mil.EXPECT().CountByLabel(ctx).Return(map[int64]int64{}, nil)

	got, err := svc.CreateLabel(ctx, &issue_svc.LabelRequest{Name: "  wire  ", Tone: issue_entity.ToneBlue})
	require.NoError(t, err)
	assert.Equal(t, int64(9), got.Label.ID)
	assert.Equal(t, int64(0), got.UsageCount)
}

// TestIssueSvcCreateLabel_RejectsToneOutsideThePalette 色调固定为设计系统的 8 档，
// 不开放自由取色。
func TestIssueSvcCreateLabel_RejectsToneOutsideThePalette(t *testing.T) {
	ctx, _, _, _, _, svc := setupBoard(t)
	_, err := svc.CreateLabel(ctx, &issue_svc.LabelRequest{Name: "wire", Tone: "rainbow"})
	assert.Error(t, err) // 校验在触达仓储之前拦下，无 mock 调用
}

func TestIssueSvcCreateLabel_RejectsEmptyName(t *testing.T) {
	ctx, _, _, _, _, svc := setupBoard(t)
	_, err := svc.CreateLabel(ctx, &issue_svc.LabelRequest{Name: "   ", Tone: issue_entity.ToneBlue})
	assert.Error(t, err)
}

// TestIssueSvcCreateLabel_RejectsDuplicateName 目录里同名只能有一个（唯一索引也是
// 这么定的），重名要在触达仓储之前说清楚。
func TestIssueSvcCreateLabel_RejectsDuplicateName(t *testing.T) {
	ctx, _, ml, _, _, svc := setupBoard(t)
	ml.EXPECT().FindByName(ctx, "bug").Return(&issue_entity.Label{ID: 2, Name: "bug"}, nil)

	_, err := svc.CreateLabel(ctx, &issue_svc.LabelRequest{Name: "bug", Tone: issue_entity.ToneRed})
	var httpErr *httputils.Error
	require.ErrorAs(t, err, &httpErr)
	assert.Equal(t, code.IssueLabelNameDuplicated, httpErr.Code,
		"重名要说「这个名字已经有了」，不能退回一句通用的「参数错误」")
}

// TestIssueSvcUpdateLabel 改名与换色走同一条路；改成别人的名字要被拦下，改回自己
// 原来的名字不算重名。
func TestIssueSvcUpdateLabel(t *testing.T) {
	ctx, _, ml, mil, _, svc := setupBoard(t)
	ml.EXPECT().Find(ctx, int64(9)).Return(&issue_entity.Label{
		ID: 9, Name: "wire", Tone: issue_entity.ToneBlue,
	}, nil)
	ml.EXPECT().FindByName(ctx, "wire").Return(&issue_entity.Label{ID: 9, Name: "wire"}, nil)
	ml.EXPECT().Update(ctx, gomock.Any()).DoAndReturn(func(_ context.Context, l *issue_entity.Label) error {
		assert.Equal(t, issue_entity.ToneViolet, l.Tone)
		return nil
	})
	mil.EXPECT().CountByLabel(ctx).Return(map[int64]int64{9: 2}, nil)

	got, err := svc.UpdateLabel(ctx, &issue_svc.LabelRequest{
		ID: 9, Name: "wire", Tone: issue_entity.ToneViolet,
	})
	require.NoError(t, err)
	assert.Equal(t, int64(2), got.UsageCount)
}

func TestIssueSvcUpdateLabel_RejectsNameTakenByAnotherLabel(t *testing.T) {
	ctx, _, ml, _, _, svc := setupBoard(t)
	ml.EXPECT().Find(ctx, int64(9)).Return(&issue_entity.Label{ID: 9, Name: "wire"}, nil)
	ml.EXPECT().FindByName(ctx, "bug").Return(&issue_entity.Label{ID: 2, Name: "bug"}, nil)

	_, err := svc.UpdateLabel(ctx, &issue_svc.LabelRequest{
		ID: 9, Name: "bug", Tone: issue_entity.ToneRed,
	})
	var httpErr *httputils.Error
	require.ErrorAs(t, err, &httpErr)
	assert.Equal(t, code.IssueLabelNameDuplicated, httpErr.Code)
}

func TestIssueSvcUpdateLabel_NotFound(t *testing.T) {
	ctx, _, ml, _, _, svc := setupBoard(t)
	ml.EXPECT().Find(ctx, int64(404)).Return(nil, nil)

	_, err := svc.UpdateLabel(ctx, &issue_svc.LabelRequest{
		ID: 404, Name: "x", Tone: issue_entity.ToneRed,
	})
	assert.Error(t, err)
}

// TestIssueSvcDeleteLabel 软删标签：先把它从任务上摘掉，再把目录行置为已删除 ——
// 顺序反了会留下指向一个已消失标签的关联行。
func TestIssueSvcDeleteLabel(t *testing.T) {
	ctx, _, ml, mil, _, svc := setupBoard(t)
	ml.EXPECT().Find(ctx, int64(9)).Return(&issue_entity.Label{ID: 9, Name: "wire"}, nil)
	gomock.InOrder(
		mil.EXPECT().DeleteByLabel(ctx, int64(9)).Return(nil),
		ml.EXPECT().Delete(ctx, int64(9)).Return(nil),
	)

	require.NoError(t, svc.DeleteLabel(ctx, 9))
}

func TestIssueSvcDeleteLabel_NotFound(t *testing.T) {
	ctx, _, ml, _, _, svc := setupBoard(t)
	ml.EXPECT().Find(ctx, int64(404)).Return(nil, nil)

	assert.Error(t, svc.DeleteLabel(ctx, 404))
}
