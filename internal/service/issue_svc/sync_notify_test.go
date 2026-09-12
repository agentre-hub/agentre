package issue_svc_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/agentre-hub/agentre/internal/model/entity/issue_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/syncmeta_entity"
	"github.com/agentre-hub/agentre/internal/pkg/syncwire"
	"github.com/agentre-hub/agentre/internal/service/issue_svc"
	"github.com/agentre-hub/agentre/internal/service/sync_svc"
)

// recordingSync 记下域服务在改动落库成功之后交出来的每一条 LocalChange。
type recordingSync struct {
	sync_svc.SyncSvc
	changes []sync_svc.LocalChange
}

func (r *recordingSync) NotifyLocalChange(_ context.Context, ch sync_svc.LocalChange) {
	r.changes = append(r.changes, ch)
}

func registerRecordingSync(t *testing.T) *recordingSync {
	t.Helper()
	rec := &recordingSync{}
	sync_svc.SetDefault(rec)
	t.Cleanup(func() { sync_svc.SetDefault(nil) })
	return rec
}

// TestIssueCreate_NotifiesSyncOnce R3：编辑当场触发上行——任务落库成功后，同步层
// 拿到这一行的同步标识。没有这一步，看板就是一块「同步组里有它的表、却一条改动都
// 不入队」的板子。
func TestIssueCreate_NotifiesSyncOnce(t *testing.T) {
	ctx, mi, ml, mil, svc := setupIssueSvc(t)
	rec := registerRecordingSync(t)
	mi.EXPECT().List(ctx, gomock.Any()).Return(nil, nil)
	ml.EXPECT().ListByIDs(ctx, gomock.Any()).Return(nil, nil).AnyTimes()
	mi.EXPECT().Create(ctx, gomock.Any()).DoAndReturn(func(_ context.Context, i *issue_entity.Issue) error {
		i.ID = 9
		i.SyncID = "issue-sync-1"
		return nil
	})
	mil.EXPECT().SetLabels(ctx, int64(9), gomock.Any()).Return(nil)

	_, err := svc.Create(ctx, &issue_svc.CreateIssueRequest{Title: "demo"})
	require.NoError(t, err)

	require.Len(t, rec.changes, 1)
	assert.Equal(t, syncwire.KindIssue, rec.changes[0].Kind)
	assert.Equal(t, sync_svc.OpCreate, rec.changes[0].Op)
	assert.Equal(t, "issue-sync-1", rec.changes[0].Meta.SyncID)
	assert.Equal(t, int64(9), rec.changes[0].LocalID)
}

// TestIssueUpdate_NotifiesSyncOnce 编辑任务（标题 / 描述 / 项目 / 执行归属 / 阶段）
// 全都改的是随载荷上行的字段。
func TestIssueUpdate_NotifiesSyncOnce(t *testing.T) {
	ctx, mi, ml, mil, svc := setupIssueSvc(t)
	rec := registerRecordingSync(t)
	mi.EXPECT().Find(ctx, int64(9)).Return(&issue_entity.Issue{
		ID: 9, Title: "old", State: issue_entity.StateOpen, Stage: issue_entity.StageTodo,
		SyncMeta: syncmeta_entity.SyncMeta{SyncID: "issue-sync-1", SyncVersion: 4},
	}, nil)
	ml.EXPECT().ListByIDs(ctx, gomock.Any()).Return(nil, nil).AnyTimes()
	mi.EXPECT().Update(ctx, gomock.Any()).Return(nil)
	mil.EXPECT().ListRowsByIssue(ctx, int64(9)).Return(nil, nil)
	mil.EXPECT().SetLabels(ctx, int64(9), gomock.Any()).Return(nil)

	_, err := svc.Update(ctx, &issue_svc.UpdateIssueRequest{ID: 9, Title: "new"})
	require.NoError(t, err)

	require.Len(t, rec.changes, 1)
	assert.Equal(t, syncwire.KindIssue, rec.changes[0].Kind)
	assert.Equal(t, sync_svc.OpUpdate, rec.changes[0].Op)
	assert.Equal(t, int64(4), rec.changes[0].Meta.SyncVersion,
		"基版本是本端编辑时见到的那一版（R4a），冲突判定靠它")
}

// TestIssueMove_NotifiesSyncOnce 拖一张卡改的是 stage 与 position，两个都在载荷里。
func TestIssueMove_NotifiesSyncOnce(t *testing.T) {
	ctx, mi, ml, mil, svc := setupIssueSvc(t)
	rec := registerRecordingSync(t)
	mi.EXPECT().Find(ctx, int64(9)).Return(&issue_entity.Issue{
		ID: 9, Title: "t", State: issue_entity.StateOpen, Stage: issue_entity.StageTodo,
		SyncMeta: syncmeta_entity.SyncMeta{SyncID: "issue-sync-1"},
	}, nil)
	mi.EXPECT().List(ctx, gomock.Any()).Return(nil, nil)
	mi.EXPECT().Update(ctx, gomock.Any()).Return(nil)
	mil.EXPECT().ListByIssue(ctx, int64(9)).Return(nil, nil)
	ml.EXPECT().ListByIDs(ctx, gomock.Any()).Return(nil, nil)

	_, err := svc.Move(ctx, &issue_svc.MoveIssueRequest{ID: 9, Stage: issue_entity.StageDoing})
	require.NoError(t, err)

	require.Len(t, rec.changes, 1)
	assert.Equal(t, syncwire.KindIssue, rec.changes[0].Kind)
	assert.Equal(t, sync_svc.OpUpdate, rec.changes[0].Op)
}

// TestIssueDelete_NotifiesSyncOnce 删除靠墓碑到达各端（R6）：软删之后必须入队，
// 否则这台机器上删掉的卡在别的机器上永远留着。
func TestIssueDelete_NotifiesSyncOnce(t *testing.T) {
	ctx, mi, _, _, svc := setupIssueSvc(t)
	rec := registerRecordingSync(t)
	mi.EXPECT().Find(ctx, int64(9)).Return(&issue_entity.Issue{
		ID: 9, SyncMeta: syncmeta_entity.SyncMeta{SyncID: "issue-sync-1", SyncVersion: 6},
	}, nil)
	mi.EXPECT().Delete(ctx, int64(9)).Return(nil)

	require.NoError(t, svc.Delete(ctx, 9))

	require.Len(t, rec.changes, 1)
	assert.Equal(t, syncwire.KindIssue, rec.changes[0].Kind)
	assert.Equal(t, sync_svc.OpDelete, rec.changes[0].Op)
	assert.Equal(t, "issue-sync-1", rec.changes[0].Meta.SyncID)
}

// TestLabelMutations_EachNotifySyncOnce 建 / 改名换色 / 软删标签各入队一次：标签
// 目录是账号级的，三个动作改的都是随载荷上行的字段（name / tone / status）。
func TestLabelMutations_EachNotifySyncOnce(t *testing.T) {
	t.Run("create", func(t *testing.T) {
		ctx, _, ml, mil, svc := setupIssueSvc(t)
		rec := registerRecordingSync(t)
		ml.EXPECT().FindByName(ctx, "spike").Return(nil, nil)
		ml.EXPECT().Create(ctx, gomock.Any()).DoAndReturn(func(_ context.Context, l *issue_entity.Label) error {
			l.ID = 12
			l.SyncID = "label-sync-1"
			return nil
		})
		mil.EXPECT().CountByLabel(ctx).Return(map[int64]int64{}, nil)

		_, err := svc.CreateLabel(ctx, &issue_svc.LabelRequest{
			Name: "spike", Tone: issue_entity.ToneViolet,
		})
		require.NoError(t, err)

		require.Len(t, rec.changes, 1)
		assert.Equal(t, syncwire.KindLabel, rec.changes[0].Kind)
		assert.Equal(t, sync_svc.OpCreate, rec.changes[0].Op)
		assert.Equal(t, "label-sync-1", rec.changes[0].Meta.SyncID)
		assert.Equal(t, int64(12), rec.changes[0].LocalID)
	})

	t.Run("rename and recolor", func(t *testing.T) {
		ctx, _, ml, mil, svc := setupIssueSvc(t)
		rec := registerRecordingSync(t)
		ml.EXPECT().Find(ctx, int64(12)).Return(&issue_entity.Label{
			ID: 12, Name: "spike", Tone: issue_entity.ToneGray,
			SyncMeta: syncmeta_entity.SyncMeta{SyncID: "label-sync-1", SyncVersion: 3},
		}, nil)
		ml.EXPECT().FindByName(ctx, "research").Return(nil, nil)
		ml.EXPECT().Update(ctx, gomock.Any()).Return(nil)
		mil.EXPECT().CountByLabel(ctx).Return(map[int64]int64{}, nil)

		_, err := svc.UpdateLabel(ctx, &issue_svc.LabelRequest{
			ID: 12, Name: "research", Tone: issue_entity.ToneBlue,
		})
		require.NoError(t, err)

		require.Len(t, rec.changes, 1)
		assert.Equal(t, syncwire.KindLabel, rec.changes[0].Kind)
		assert.Equal(t, sync_svc.OpUpdate, rec.changes[0].Op)
		assert.Equal(t, int64(3), rec.changes[0].Meta.SyncVersion)
	})

	t.Run("soft delete", func(t *testing.T) {
		ctx, _, ml, mil, svc := setupIssueSvc(t)
		rec := registerRecordingSync(t)
		ml.EXPECT().Find(ctx, int64(12)).Return(&issue_entity.Label{
			ID: 12, Name: "spike", SyncMeta: syncmeta_entity.SyncMeta{SyncID: "label-sync-1"},
		}, nil)
		mil.EXPECT().ListRowsByLabel(ctx, int64(12)).Return(nil, nil)
		mil.EXPECT().DeleteByLabel(ctx, int64(12)).Return(nil)
		ml.EXPECT().Delete(ctx, int64(12)).Return(nil)

		require.NoError(t, svc.DeleteLabel(ctx, 12))

		require.Len(t, rec.changes, 1)
		assert.Equal(t, syncwire.KindLabel, rec.changes[0].Kind)
		assert.Equal(t, sync_svc.OpDelete, rec.changes[0].Op)
	})
}

// TestReads_NotifyNothing 读路径一条也不该入队：列表、详情与标签目录都不改任何行，
// 把它们也交给同步层等于每刷新一次界面就推一轮空改动。
func TestReads_NotifyNothing(t *testing.T) {
	ctx, mi, ml, mil, svc := setupIssueSvc(t)
	rec := registerRecordingSync(t)
	mi.EXPECT().Find(ctx, int64(9)).Return(&issue_entity.Issue{ID: 9}, nil)
	mil.EXPECT().ListByIssue(ctx, int64(9)).Return([]int64{2}, nil)
	ml.EXPECT().ListByIDs(ctx, []int64{2}).Return(nil, nil)
	ml.EXPECT().List(ctx).Return(nil, nil)
	mil.EXPECT().CountByLabel(ctx).Return(map[int64]int64{}, nil)

	_, err := svc.Get(ctx, 9)
	require.NoError(t, err)
	_, err = svc.ListLabels(ctx)
	require.NoError(t, err)

	assert.Empty(t, rec.changes)
}

// TestIssueUpdate_TombstonesDetachedLabelLinks 从一条任务上摘掉一个标签，对端也要
// 摘掉。关联行是硬删（没有软删列），墓碑只能凭那一行**自己的**同步标识上行（R6）：
// 不入队，对端就永远挂着那个标签。留下来的那条关联一个字都没改，不该冒出第二条
// 改动——它的标识终身不变。
func TestIssueUpdate_TombstonesDetachedLabelLinks(t *testing.T) {
	ctx, mi, ml, mil, svc := setupIssueSvc(t)
	rec := registerRecordingSync(t)
	mi.EXPECT().Find(ctx, int64(9)).Return(&issue_entity.Issue{
		ID: 9, Title: "old", State: issue_entity.StateOpen, Stage: issue_entity.StageTodo,
		SyncMeta: syncmeta_entity.SyncMeta{SyncID: "issue-sync-1"},
	}, nil)
	ml.EXPECT().ListByIDs(ctx, []int64{2}).
		Return([]*issue_entity.Label{{ID: 2, Name: "bug", Tone: issue_entity.ToneRed}}, nil)
	// 编辑之前挂着 1 与 2，各自带着自己的同步标识。
	mil.EXPECT().ListRowsByIssue(ctx, int64(9)).Return([]*issue_entity.IssueLabel{
		{IssueID: 9, LabelID: 1, SyncMeta: syncmeta_entity.SyncMeta{SyncID: "link-1", SyncVersion: 3}},
		{IssueID: 9, LabelID: 2, SyncMeta: syncmeta_entity.SyncMeta{SyncID: "link-2", SyncVersion: 4}},
	}, nil)
	mi.EXPECT().Update(ctx, gomock.Any()).Return(nil)
	mil.EXPECT().SetLabels(ctx, int64(9), []int64{2}).Return(nil)

	_, err := svc.Update(ctx, &issue_svc.UpdateIssueRequest{ID: 9, Title: "new", LabelIDs: []int64{2}})
	require.NoError(t, err)

	var links []sync_svc.LocalChange
	for _, ch := range rec.changes {
		if ch.Kind == syncwire.KindIssueLabel {
			links = append(links, ch)
		}
	}
	require.Len(t, links, 1, "只有被摘掉的那一条关联才产生改动")
	assert.Equal(t, sync_svc.OpDelete, links[0].Op)
	assert.Equal(t, "link-1", links[0].Meta.SyncID, "墓碑带的是被摘掉那一行自己的标识")
	assert.Equal(t, int64(3), links[0].Meta.SyncVersion, "基版本是本端摘掉它时见到的那一版（R4a）")
}

// TestIssueUpdate_KeepsSurvivingLabelLinksSilent 只改标题、标签一个没动时，关联行
// 一条改动都不该发：发了就说明它们被重写过（重写 = 重铸标识）。
func TestIssueUpdate_KeepsSurvivingLabelLinksSilent(t *testing.T) {
	ctx, mi, ml, mil, svc := setupIssueSvc(t)
	rec := registerRecordingSync(t)
	mi.EXPECT().Find(ctx, int64(9)).Return(&issue_entity.Issue{
		ID: 9, Title: "old", State: issue_entity.StateOpen, Stage: issue_entity.StageTodo,
		SyncMeta: syncmeta_entity.SyncMeta{SyncID: "issue-sync-1"},
	}, nil)
	ml.EXPECT().ListByIDs(ctx, []int64{1}).
		Return([]*issue_entity.Label{{ID: 1, Name: "docs", Tone: issue_entity.ToneBlue}}, nil)
	mil.EXPECT().ListRowsByIssue(ctx, int64(9)).Return([]*issue_entity.IssueLabel{
		{IssueID: 9, LabelID: 1, SyncMeta: syncmeta_entity.SyncMeta{SyncID: "link-1"}},
	}, nil)
	mi.EXPECT().Update(ctx, gomock.Any()).Return(nil)
	mil.EXPECT().SetLabels(ctx, int64(9), []int64{1}).Return(nil)

	_, err := svc.Update(ctx, &issue_svc.UpdateIssueRequest{ID: 9, Title: "new", LabelIDs: []int64{1}})
	require.NoError(t, err)

	for _, ch := range rec.changes {
		assert.NotEqual(t, syncwire.KindIssueLabel, ch.Kind,
			"没有增删就不该有关联行的改动")
	}
}

// TestIssueDeleteLabel_NotifiesEachLinkTombstone 规格 2026-09-04：删一个标签时，被它
// 从任务上摘掉的每一条关联行各自落墓碑并上行（R6）。
//
// 这一半**不能**走同步层的 children：DeleteLabel 先硬删关联行、再交出标签的删除，
// 等 children 去取的时候那些行已经不在库里了。所以快照必须在硬删**之前**取，与
// agent_svc 处理被挤掉的执行目标是同一个做法。
func TestIssueDeleteLabel_NotifiesEachLinkTombstone(t *testing.T) {
	ctx, _, ml, mil, svc := setupIssueSvc(t)
	rec := registerRecordingSync(t)
	ml.EXPECT().Find(ctx, int64(3)).Return(&issue_entity.Label{
		ID: 3, Name: "bug", SyncMeta: syncmeta_entity.SyncMeta{SyncID: "label-3", SyncVersion: 9},
	}, nil)
	mil.EXPECT().ListRowsByLabel(ctx, int64(3)).Return([]*issue_entity.IssueLabel{
		{IssueID: 11, LabelID: 3, SyncMeta: syncmeta_entity.SyncMeta{SyncID: "link-a", SyncVersion: 4}},
		{IssueID: 12, LabelID: 3, SyncMeta: syncmeta_entity.SyncMeta{SyncID: "link-b", SyncVersion: 5}},
	}, nil)
	mil.EXPECT().DeleteByLabel(ctx, int64(3)).Return(nil)
	ml.EXPECT().Delete(ctx, int64(3)).Return(nil)

	require.NoError(t, svc.DeleteLabel(ctx, 3))

	var links []string
	var labelDeleted bool
	for _, ch := range rec.changes {
		assert.Equal(t, sync_svc.OpDelete, ch.Op)
		switch ch.Kind {
		case syncwire.KindIssueLabel:
			links = append(links, ch.Meta.SyncID)
		case syncwire.KindLabel:
			labelDeleted = true
			assert.Equal(t, "label-3", ch.Meta.SyncID)
		}
	}
	assert.ElementsMatch(t, []string{"link-a", "link-b"}, links,
		"被摘掉的每一条关联行都要有自己的墓碑，否则 server 上它们一直活着")
	assert.True(t, labelDeleted, "标签目录行自己的墓碑照旧")
}

// TestIssueDeleteLabel_SkipsLinksAlreadyTombstoned 已经落过墓碑的关联行不再落第二条。
//
// 具体走到这里的路：任务 #11 上挂着标签 #3（关联行 link-buried）。用户先删任务
// ——issues 是软删、关联行一行不动，同步层凭 issueAdapter.children 把 link-buried 的
// 墓碑送达 server，applyPushResult 于是在**还留在 issue_labels 里的那一行**上写下
// sync_deleted_at。用户随后删标签 #3：ListRowsByLabel 照样取到那一行，不看这一列就
// 会把同一条删除再推一遍，server 回「已是墓碑」→ acceptRemoteTombstone 往「没能同步
// 的改动」里记一条——用户看到一条自己从没动过的关联行失败了，还要在那张列表上躺
// 30 天。
func TestIssueDeleteLabel_SkipsLinksAlreadyTombstoned(t *testing.T) {
	ctx, _, ml, mil, svc := setupIssueSvc(t)
	rec := registerRecordingSync(t)
	ml.EXPECT().Find(ctx, int64(3)).Return(&issue_entity.Label{
		ID: 3, Name: "bug", SyncMeta: syncmeta_entity.SyncMeta{SyncID: "label-3", SyncVersion: 9},
	}, nil)
	mil.EXPECT().ListRowsByLabel(ctx, int64(3)).Return([]*issue_entity.IssueLabel{
		{IssueID: 11, LabelID: 3, SyncMeta: syncmeta_entity.SyncMeta{
			SyncID: "link-buried", SyncVersion: 4, SyncDeletedAt: 1700,
		}},
		{IssueID: 12, LabelID: 3, SyncMeta: syncmeta_entity.SyncMeta{SyncID: "link-live", SyncVersion: 5}},
	}, nil)
	mil.EXPECT().DeleteByLabel(ctx, int64(3)).Return(nil)
	ml.EXPECT().Delete(ctx, int64(3)).Return(nil)

	require.NoError(t, svc.DeleteLabel(ctx, 3))

	var links []string
	for _, ch := range rec.changes {
		if ch.Kind == syncwire.KindIssueLabel {
			links = append(links, ch.Meta.SyncID)
		}
	}
	assert.Equal(t, []string{"link-live"}, links,
		"送达过的那条删除不该再发一遍——server 只会回「已是墓碑」，白记一条错账")
}

// TestIssueDeleteLabel_GivenTheLabelRowFailsToDelete_StillNotifiesTheLinkTombstones
// 关联行的硬删已经生效了，它们欠的墓碑就必须入队——哪怕紧接着的目录行软删失败。
//
// 关联表是硬删，本机这一侧**没有**任何兜底认得回它：ListUnsyncedTombstones 对硬删
// 的几类明确返回空集（行已经不在了，「软删但没上行」这个状态在它们身上不存在），
// 而 children / 删前快照都要从库里取行才编得出墓碑。所以这一刻不入队，就是永远
// 不入队：本机没有这些关联行、server 上它们一直活着，两边从此对不上，且没有任何
// 错误可循。
func TestIssueDeleteLabel_GivenTheLabelRowFailsToDelete_StillNotifiesTheLinkTombstones(t *testing.T) {
	ctx, _, ml, mil, svc := setupIssueSvc(t)
	rec := registerRecordingSync(t)
	ml.EXPECT().Find(ctx, int64(3)).Return(&issue_entity.Label{
		ID: 3, Name: "bug", SyncMeta: syncmeta_entity.SyncMeta{SyncID: "label-3", SyncVersion: 9},
	}, nil)
	mil.EXPECT().ListRowsByLabel(ctx, int64(3)).Return([]*issue_entity.IssueLabel{
		{IssueID: 11, LabelID: 3, SyncMeta: syncmeta_entity.SyncMeta{SyncID: "link-a", SyncVersion: 4}},
	}, nil)
	mil.EXPECT().DeleteByLabel(ctx, int64(3)).Return(nil)
	ml.EXPECT().Delete(ctx, int64(3)).Return(errors.New("database is locked"))

	require.Error(t, svc.DeleteLabel(ctx, 3))

	kinds := make([]string, 0, len(rec.changes))
	var links []string
	for _, ch := range rec.changes {
		kinds = append(kinds, ch.Kind)
		if ch.Kind == syncwire.KindIssueLabel {
			links = append(links, ch.Meta.SyncID)
		}
	}
	assert.Equal(t, []string{"link-a"}, links,
		"关联行已经从库里消失了，这条删除只有这一刻能入队")
	assert.NotContains(t, kinds, syncwire.KindLabel,
		"目录行还在本机活着，它自己那条墓碑不该发")
}
