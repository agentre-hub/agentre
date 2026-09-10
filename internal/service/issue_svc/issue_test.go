package issue_svc_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/agentre-hub/agentre/internal/model/entity/issue_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/project_entity"
	"github.com/agentre-hub/agentre/internal/repository/issue_repo"
	"github.com/agentre-hub/agentre/internal/repository/issue_repo/mock_issue_repo"
	"github.com/agentre-hub/agentre/internal/service/issue_svc"
)

func setupIssueSvc(t *testing.T) (
	context.Context,
	*mock_issue_repo.MockIssueRepo,
	*mock_issue_repo.MockLabelRepo,
	*mock_issue_repo.MockIssueLabelRepo,
	issue_svc.IssueSvc,
) {
	t.Helper()
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)
	mi := mock_issue_repo.NewMockIssueRepo(ctrl)
	ml := mock_issue_repo.NewMockLabelRepo(ctrl)
	mil := mock_issue_repo.NewMockIssueLabelRepo(ctrl)
	issue_repo.RegisterIssue(mi)
	issue_repo.RegisterLabel(ml)
	issue_repo.RegisterIssueLabel(mil)
	return context.Background(), mi, ml, mil, issue_svc.New()
}

func TestIssueSvcCreate_Happy(t *testing.T) {
	ctx, mi, ml, mil, svc := setupIssueSvc(t)
	mi.EXPECT().List(ctx, issue_repo.ListFilter{Stage: issue_entity.StageTodo, Sort: "position"}).
		Return(nil, nil)
	ml.EXPECT().ListByIDs(ctx, []int64{2}).
		Return([]*issue_entity.Label{{ID: 2, Name: "bug", Tone: "bug"}}, nil)
	mi.EXPECT().Create(ctx, gomock.Any()).DoAndReturn(func(_ context.Context, i *issue_entity.Issue) error {
		i.ID = 9
		assert.Equal(t, issue_entity.StateOpen, i.State)
		return nil
	})
	mil.EXPECT().SetLabels(ctx, int64(9), []int64{2}).Return(nil)

	got, err := svc.Create(ctx, &issue_svc.CreateIssueRequest{Title: "demo", LabelIDs: []int64{2}})
	require.NoError(t, err)
	assert.Equal(t, int64(9), got.Issue.ID)
	require.Len(t, got.Labels, 1)
}

func TestIssueSvcCreate_EmptyTitleRejected(t *testing.T) {
	ctx, _, _, _, svc := setupIssueSvc(t)
	_, err := svc.Create(ctx, &issue_svc.CreateIssueRequest{Title: "   "})
	assert.Error(t, err) // 校验在 repo.Create 之前拦截，无 mock 调用
}

func TestIssueSvcCreate_LabelNotFound(t *testing.T) {
	ctx, mi, ml, _, svc := setupIssueSvc(t)
	// appendPosition 先于 resolveLabels 调用。
	mi.EXPECT().List(ctx, issue_repo.ListFilter{Stage: issue_entity.StageTodo, Sort: "position"}).
		Return(nil, nil)
	// 请求两个 label，仓储只返回一个 → resolveLabels 报错，且不触达 Create/SetLabels。
	ml.EXPECT().ListByIDs(ctx, []int64{2, 3}).
		Return([]*issue_entity.Label{{ID: 2, Name: "bug", Tone: "bug"}}, nil)

	_, err := svc.Create(ctx, &issue_svc.CreateIssueRequest{Title: "demo", LabelIDs: []int64{2, 3}})
	assert.Error(t, err)
}

func TestIssueSvcCreate_DeduplicatesLabelIDs(t *testing.T) {
	ctx, mi, ml, mil, svc := setupIssueSvc(t)
	mi.EXPECT().List(ctx, issue_repo.ListFilter{Stage: issue_entity.StageTodo, Sort: "position"}).
		Return(nil, nil)
	ml.EXPECT().ListByIDs(ctx, []int64{2, 3}).
		Return([]*issue_entity.Label{
			{ID: 2, Name: "bug", Tone: "bug"},
			{ID: 3, Name: "feature", Tone: "feature"},
		}, nil)
	mi.EXPECT().Create(ctx, gomock.Any()).DoAndReturn(func(_ context.Context, i *issue_entity.Issue) error {
		i.ID = 9
		return nil
	})
	mil.EXPECT().SetLabels(ctx, int64(9), []int64{2, 3}).Return(nil)

	got, err := svc.Create(ctx, &issue_svc.CreateIssueRequest{Title: "demo", LabelIDs: []int64{2, 2, 3}})
	require.NoError(t, err)
	require.Len(t, got.Labels, 2)
	assert.Equal(t, int64(2), got.Labels[0].ID)
	assert.Equal(t, int64(3), got.Labels[1].ID)
}

func TestIssueSvcUpdate_Happy(t *testing.T) {
	ctx, mi, ml, mil, svc := setupIssueSvc(t)
	mi.EXPECT().Find(ctx, int64(5)).
		Return(&issue_entity.Issue{ID: 5, State: issue_entity.StateOpen, Title: "old"}, nil)
	ml.EXPECT().ListByIDs(ctx, []int64{2}).
		Return([]*issue_entity.Label{{ID: 2, Name: "bug", Tone: "bug"}}, nil)
	mi.EXPECT().Update(ctx, gomock.Any()).DoAndReturn(func(_ context.Context, i *issue_entity.Issue) error {
		assert.Equal(t, int64(5), i.ID)
		assert.Equal(t, "new title", i.Title)
		assert.Equal(t, int64(7), i.ProjectID)
		return nil
	})
	mil.EXPECT().SetLabels(ctx, int64(5), []int64{2}).Return(nil)

	got, err := svc.Update(ctx, &issue_svc.UpdateIssueRequest{
		ID: 5, ProjectID: 7, Title: "  new title  ", Body: "b", LabelIDs: []int64{2},
	})
	require.NoError(t, err)
	assert.Equal(t, int64(5), got.Issue.ID)
	assert.Equal(t, "new title", got.Issue.Title)
	require.Len(t, got.Labels, 1)
}

func TestIssueSvcUpdate_NotFound(t *testing.T) {
	ctx, mi, _, _, svc := setupIssueSvc(t)
	mi.EXPECT().Find(ctx, int64(404)).Return(nil, nil)

	_, err := svc.Update(ctx, &issue_svc.UpdateIssueRequest{ID: 404, Title: "x"})
	assert.Error(t, err)
}

func TestIssueSvcDelete_NotFound(t *testing.T) {
	ctx, mi, _, _, svc := setupIssueSvc(t)
	mi.EXPECT().Find(ctx, int64(404)).Return(nil, nil)
	// Find 返回 nil → IssueNotFound，不应调用 Delete。

	err := svc.Delete(ctx, 404)
	assert.Error(t, err)
}

func TestIssueSvcList(t *testing.T) {
	ctx, mi, ml, mil, mp, svc := setupBoard(t)
	req := &issue_svc.ListIssuesRequest{Scope: issue_svc.ScopeProject, ProjectID: 7, Sort: "updated"}
	mp.EXPECT().List(ctx).Return([]*project_entity.Project{{ID: 7}}, nil)
	mi.EXPECT().List(ctx, issue_repo.ListFilter{
		ProjectIDs: []int64{7}, Sort: "updated",
	}).Return([]*issue_entity.Issue{
		{ID: 1, State: issue_entity.StateOpen},
		{ID: 2, State: issue_entity.StateOpen},
	}, nil)
	mil.EXPECT().ListByIssues(ctx, []int64{1, 2}).Return(map[int64][]int64{
		1: {10},
		2: {10, 20},
	}, nil)
	ml.EXPECT().List(ctx).Return([]*issue_entity.Label{
		{ID: 10, Name: "bug", Tone: issue_entity.ToneRed},
		{ID: 20, Name: "feature", Tone: issue_entity.ToneGreen},
	}, nil)
	// 命中数与全部数各量一次；这个用例没有别的筛选条件，两把尺子形状相同，按声明
	// 顺序先命中后全部。
	gomock.InOrder(
		mi.EXPECT().StageCounts(ctx, issue_repo.ListFilter{ProjectIDs: []int64{7}}).
			Return(map[string]int64{issue_entity.StageTodo: 2}, nil),
		mi.EXPECT().StageCounts(ctx, issue_repo.ListFilter{ProjectIDs: []int64{7}}).
			Return(map[string]int64{issue_entity.StageTodo: 9}, nil),
	)
	mi.EXPECT().CountUnfinishedByProject(ctx).Return(map[int64]int64{7: 4}, nil)

	got, err := svc.List(ctx, req)
	require.NoError(t, err)
	require.Len(t, got.Issues, 2)
	assert.Equal(t, int64(2), got.StageCounts[issue_entity.StageTodo])
	assert.Equal(t, int64(9), got.StageTotals[issue_entity.StageTodo])
	assert.Equal(t, int64(4), got.ProjectCounts[7])

	require.Len(t, got.Issues[0].Labels, 1)
	assert.Equal(t, int64(10), got.Issues[0].Labels[0].ID)
	require.Len(t, got.Issues[1].Labels, 2)
	assert.Equal(t, int64(10), got.Issues[1].Labels[0].ID)
	assert.Equal(t, int64(20), got.Issues[1].Labels[1].ID)
}

// 防御：确保 List 在底层仓储报错时把错误透传出来。
func TestIssueSvcList_RepoError(t *testing.T) {
	ctx, mi, _, _, mp, svc := setupBoard(t)
	mp.EXPECT().List(ctx).Return(nil, nil)
	mi.EXPECT().List(ctx, gomock.Any()).Return(nil, errors.New("boom"))

	_, err := svc.List(ctx, &issue_svc.ListIssuesRequest{})
	assert.Error(t, err)
}

// Create 已落库后 SetLabels 失败，错误必须透传给调用方。
func TestIssueSvcCreate_SetLabelsFail(t *testing.T) {
	ctx, mi, ml, mil, svc := setupIssueSvc(t)
	mi.EXPECT().List(ctx, issue_repo.ListFilter{Stage: issue_entity.StageTodo, Sort: "position"}).
		Return(nil, nil)
	ml.EXPECT().ListByIDs(ctx, []int64{2}).
		Return([]*issue_entity.Label{{ID: 2, Name: "bug", Tone: "bug"}}, nil)
	mi.EXPECT().Create(ctx, gomock.Any()).DoAndReturn(func(_ context.Context, i *issue_entity.Issue) error {
		i.ID = 9
		return nil
	})
	mil.EXPECT().SetLabels(ctx, int64(9), []int64{2}).Return(errors.New("db error"))

	_, err := svc.Create(ctx, &issue_svc.CreateIssueRequest{Title: "demo", LabelIDs: []int64{2}})
	assert.Error(t, err)
}

// Update 已落库后 SetLabels 失败，错误必须透传给调用方。
func TestIssueSvcUpdate_SetLabelsFail(t *testing.T) {
	ctx, mi, ml, mil, svc := setupIssueSvc(t)
	mi.EXPECT().Find(ctx, int64(5)).
		Return(&issue_entity.Issue{ID: 5, State: issue_entity.StateOpen, Title: "old"}, nil)
	ml.EXPECT().ListByIDs(ctx, []int64{2}).
		Return([]*issue_entity.Label{{ID: 2, Name: "bug", Tone: "bug"}}, nil)
	mi.EXPECT().Update(ctx, gomock.Any()).Return(nil)
	mil.EXPECT().SetLabels(ctx, int64(5), []int64{2}).Return(errors.New("db error"))

	_, err := svc.Update(ctx, &issue_svc.UpdateIssueRequest{ID: 5, Title: "new", LabelIDs: []int64{2}})
	assert.Error(t, err)
}

func TestIssueSvcGet_Happy(t *testing.T) {
	ctx, mi, ml, mil, svc := setupIssueSvc(t)
	mi.EXPECT().Find(ctx, int64(5)).Return(&issue_entity.Issue{ID: 5, State: issue_entity.StateOpen}, nil)
	mil.EXPECT().ListByIssue(ctx, int64(5)).Return([]int64{2}, nil)
	ml.EXPECT().ListByIDs(ctx, []int64{2}).
		Return([]*issue_entity.Label{{ID: 2, Name: "bug", Tone: "bug"}}, nil)

	got, err := svc.Get(ctx, 5)
	require.NoError(t, err)
	assert.Equal(t, int64(5), got.Issue.ID)
	require.Len(t, got.Labels, 1)
	assert.Equal(t, int64(2), got.Labels[0].ID)
}

func TestIssueSvcGet_NotFound(t *testing.T) {
	ctx, mi, _, _, svc := setupIssueSvc(t)
	mi.EXPECT().Find(ctx, int64(404)).Return(nil, nil)
	// Find 返回 nil → IssueNotFound，不应触发 hydrate（ListByIssue/ListByIDs）。

	_, err := svc.Get(ctx, 404)
	assert.Error(t, err)
}

func TestIssueSvcDelete_Happy(t *testing.T) {
	ctx, mi, _, _, svc := setupIssueSvc(t)
	mi.EXPECT().Find(ctx, int64(5)).Return(&issue_entity.Issue{ID: 5, State: issue_entity.StateOpen}, nil)
	mi.EXPECT().Delete(ctx, int64(5)).Return(nil)

	err := svc.Delete(ctx, 5)
	require.NoError(t, err)
}

func TestIssueSvcCreate_DefaultsStageAndAppendsPosition(t *testing.T) {
	ctx, mi, _, mil, svc := setupIssueSvc(t)
	// 该 stage 已有末位 position=100 → 新卡 position=100+step。
	mi.EXPECT().List(ctx, issue_repo.ListFilter{Stage: issue_entity.StageTodo, Sort: "position"}).
		Return([]*issue_entity.Issue{{ID: 1, Stage: issue_entity.StageTodo, Position: 100}}, nil)
	mi.EXPECT().Create(ctx, gomock.Any()).DoAndReturn(func(_ context.Context, i *issue_entity.Issue) error {
		assert.Equal(t, issue_entity.StageTodo, i.Stage)
		assert.Equal(t, float64(100+65536), i.Position)
		i.ID = 9
		return nil
	})
	mil.EXPECT().SetLabels(ctx, int64(9), []int64(nil)).Return(nil)

	got, err := svc.Create(ctx, &issue_svc.CreateIssueRequest{Title: "demo"})
	require.NoError(t, err)
	assert.Equal(t, int64(9), got.Issue.ID)
}

func TestIssueSvcMove_MidpointBetweenNeighbors(t *testing.T) {
	ctx, mi, ml, mil, svc := setupIssueSvc(t)
	moving := &issue_entity.Issue{ID: 5, Stage: issue_entity.StageTodo, Position: 5, State: issue_entity.StateOpen}
	mi.EXPECT().Find(ctx, int64(5)).Return(moving, nil)
	// 目标列 doing 顺序：[id=3 pos=10, id=4 pos=20]；AfterID=3 → 落在 3 与 4 之间 → 15。
	mi.EXPECT().List(ctx, issue_repo.ListFilter{Stage: issue_entity.StageDoing, Sort: "position"}).
		Return([]*issue_entity.Issue{
			{ID: 3, Stage: issue_entity.StageDoing, Position: 10},
			{ID: 4, Stage: issue_entity.StageDoing, Position: 20},
		}, nil)
	mi.EXPECT().Update(ctx, gomock.Any()).DoAndReturn(func(_ context.Context, i *issue_entity.Issue) error {
		assert.Equal(t, issue_entity.StageDoing, i.Stage)
		assert.Equal(t, float64(15), i.Position)
		return nil
	})
	mil.EXPECT().ListByIssue(ctx, int64(5)).Return(nil, nil)
	ml.EXPECT().ListByIDs(ctx, gomock.Nil()).Return(nil, nil)

	_, err := svc.Move(ctx, &issue_svc.MoveIssueRequest{ID: 5, Stage: issue_entity.StageDoing, AfterID: 3})
	require.NoError(t, err)
}

func TestIssueSvcMove_TopOfColumn(t *testing.T) {
	ctx, mi, ml, mil, svc := setupIssueSvc(t)
	moving := &issue_entity.Issue{ID: 5, Stage: issue_entity.StageTodo, Position: 5, State: issue_entity.StateOpen}
	mi.EXPECT().Find(ctx, int64(5)).Return(moving, nil)
	mi.EXPECT().List(ctx, issue_repo.ListFilter{Stage: issue_entity.StageReview, Sort: "position"}).
		Return([]*issue_entity.Issue{{ID: 8, Stage: issue_entity.StageReview, Position: 40}}, nil)
	mi.EXPECT().Update(ctx, gomock.Any()).DoAndReturn(func(_ context.Context, i *issue_entity.Issue) error {
		assert.Equal(t, float64(40-65536), i.Position) // AfterID=0 → 顶部 = 首元素 - step
		return nil
	})
	mil.EXPECT().ListByIssue(ctx, int64(5)).Return(nil, nil)
	ml.EXPECT().ListByIDs(ctx, gomock.Nil()).Return(nil, nil)

	_, err := svc.Move(ctx, &issue_svc.MoveIssueRequest{ID: 5, Stage: issue_entity.StageReview, AfterID: 0})
	require.NoError(t, err)
}

func TestIssueSvcMove_WithinColumnReorder_FiltersSelf(t *testing.T) {
	ctx, mi, ml, mil, svc := setupIssueSvc(t)
	moving := &issue_entity.Issue{ID: 3, Stage: issue_entity.StageDoing, Position: 10, State: issue_entity.StateOpen}
	mi.EXPECT().Find(ctx, int64(3)).Return(moving, nil)
	// 目标列 doing 含自身(id=3) + id=4；AfterID=4 → computePosition 先过滤 id=3，剩 [{4,20}]，4 是末位 → 20+step。
	mi.EXPECT().List(ctx, issue_repo.ListFilter{Stage: issue_entity.StageDoing, Sort: "position"}).
		Return([]*issue_entity.Issue{
			{ID: 3, Stage: issue_entity.StageDoing, Position: 10},
			{ID: 4, Stage: issue_entity.StageDoing, Position: 20},
		}, nil)
	mi.EXPECT().Update(ctx, gomock.Any()).DoAndReturn(func(_ context.Context, i *issue_entity.Issue) error {
		// 自过滤后 siblings=[{4,20}]，afterID=4 是末位 → 20 + step
		assert.Equal(t, float64(20+65536), i.Position)
		return nil
	})
	mil.EXPECT().ListByIssue(ctx, int64(3)).Return(nil, nil)
	ml.EXPECT().ListByIDs(ctx, gomock.Nil()).Return(nil, nil)

	_, err := svc.Move(ctx, &issue_svc.MoveIssueRequest{ID: 3, Stage: issue_entity.StageDoing, AfterID: 4})
	require.NoError(t, err)
}
