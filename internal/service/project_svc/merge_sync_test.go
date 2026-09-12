package project_svc_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/agentre-hub/agentre/internal/model/entity/issue_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/project_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/syncmeta_entity"
	"github.com/agentre-hub/agentre/internal/pkg/syncwire"
	"github.com/agentre-hub/agentre/internal/repository/issue_repo"
	"github.com/agentre-hub/agentre/internal/service/project_svc"
	"github.com/agentre-hub/agentre/internal/service/sync_svc"
)

// TestProjectSvcMerge_NotifiesEachReassignedIssue 合并把 drop 名下每一条任务的
// project_id 整批改写，而 project_id 就是任务同步载荷里的 project_sync_id ——
// 不为它们各发一次上行，对端的任务就还挂在那个已经被合并掉的项目上。
//
// 标识只在行上，因此必须在整批改挂**之前**读出来（与 ReorderSiblings 同一形状）。
func TestProjectSvcMerge_NotifiesEachReassignedIssue(t *testing.T) {
	ctx, m, svc := setupMergeTest(t)
	rec := registerRecordingSync(t)

	older := &project_entity.Project{
		ID: 5, Name: "older", Createtime: 100, Path: "/p1",
		SyncMeta: syncmeta_entity.SyncMeta{SyncID: "proj-keep"},
	}
	newer := &project_entity.Project{
		ID: 6, Name: "newer", Createtime: 200, Path: "/p2",
		SyncMeta: syncmeta_entity.SyncMeta{SyncID: "proj-drop"},
	}
	m.project.EXPECT().Find(ctx, int64(6)).Return(newer, nil)
	m.project.EXPECT().Find(ctx, int64(5)).Return(older, nil)
	m.project.EXPECT().Update(ctx, gomock.Any()).Return(nil)
	m.session.EXPECT().ReassignProject(ctx, int64(6), int64(5)).Return(nil)
	m.pa.EXPECT().ListByProject(ctx, int64(5)).Return(nil, nil)
	m.pa.EXPECT().ListByProject(ctx, int64(6)).Return(nil, nil)
	m.project.EXPECT().ListByParent(ctx, int64(6)).Return(nil, nil)
	m.project.EXPECT().ReassignParent(ctx, int64(6), int64(5)).Return(nil)

	// drop(6) 名下的两条任务，各自带着自己的同步标识与本端见到的那一版。
	m.issue.EXPECT().List(ctx, issue_repo.ListFilter{ProjectIDs: []int64{6}}).
		Return([]*issue_entity.Issue{
			{ID: 11, SyncMeta: syncmeta_entity.SyncMeta{SyncID: "issue-a", SyncVersion: 3}},
			{ID: 12, SyncMeta: syncmeta_entity.SyncMeta{SyncID: "issue-b", SyncVersion: 4}},
		}, nil)
	m.issue.EXPECT().ReassignProject(ctx, int64(6), int64(5)).Return(nil)

	m.location.EXPECT().ListByProject(ctx, int64(6)).Return(nil, nil)
	m.location.EXPECT().ReassignProject(ctx, int64(6), int64(5)).Return(nil)
	expectCleanDelete(ctx, m, 6, newer)

	_, err := svc.Merge(ctx, &project_svc.MergeProjectsRequest{SourceID: 6, TargetID: 5})
	require.NoError(t, err)

	moved := map[string]sync_svc.LocalChange{}
	for _, ch := range rec.changes {
		if ch.Kind == syncwire.KindIssue {
			moved[ch.Meta.SyncID] = ch
		}
	}
	require.Len(t, moved, 2, "被改挂的每一条任务都要各入队一次上行")
	require.Contains(t, moved, "issue-a")
	require.Contains(t, moved, "issue-b")
	assert.Equal(t, sync_svc.OpUpdate, moved["issue-a"].Op)
	assert.Equal(t, int64(11), moved["issue-a"].LocalID)
	assert.Equal(t, int64(4), moved["issue-b"].Meta.SyncVersion,
		"基版本是改挂之前行上的那一版（R4a）")
}

// txAwareSync 记下每条通知发出的那一刻：事务提交了没有、手里拿的是不是事务的 ctx。
type txAwareSync struct {
	sync_svc.SyncSvc
	tx   *fakeTx
	seen []seenNotice
}

type seenNotice struct {
	change      sync_svc.LocalChange
	afterCommit bool
	withTxCtx   bool
}

func (r *txAwareSync) NotifyLocalChange(ctx context.Context, ch sync_svc.LocalChange) {
	r.seen = append(r.seen, seenNotice{
		change:      ch,
		afterCommit: r.tx.committed,
		withTxCtx:   ctx.Value(txMarkerKey{}) != nil,
	})
}

func registerTxAwareSync(t *testing.T, tx *fakeTx) *txAwareSync {
	t.Helper()
	rec := &txAwareSync{tx: tx}
	sync_svc.SetDefault(rec)
	t.Cleanup(func() { sync_svc.SetDefault(nil) })
	return rec
}

// 要求 4：合并的任一步失败时，库里不留下已执行步骤的改动，也不发出任何同步通知。
// 这里前五步都已经执行（任务也已整批改挂），第六步读路径记录时失败。
func TestProjectSvcMerge_GivenALaterStepFails_ThenTheTxRollsBackAndNothingIsAnnounced(t *testing.T) {
	ctx, m, svc := setupMergeTest(t)
	rec := registerTxAwareSync(t, m.tx)

	older := &project_entity.Project{ID: 5, Name: "older", Createtime: 100, Path: "/p1"}
	newer := &project_entity.Project{
		ID: 6, Name: "newer", Createtime: 200, Path: "/p2",
		SyncMeta: syncmeta_entity.SyncMeta{SyncID: "proj-drop"},
	}
	m.project.EXPECT().Find(ctx, int64(6)).Return(newer, nil)
	m.project.EXPECT().Find(ctx, int64(5)).Return(older, nil)
	m.project.EXPECT().Update(ctx, gomock.Any()).Return(nil)
	m.session.EXPECT().ReassignProject(ctx, int64(6), int64(5)).Return(nil)
	m.pa.EXPECT().ListByProject(ctx, int64(5)).Return(nil, nil)
	m.pa.EXPECT().ListByProject(ctx, int64(6)).Return(nil, nil)
	m.project.EXPECT().ListByParent(ctx, int64(6)).Return(nil, nil)
	m.project.EXPECT().ReassignParent(ctx, int64(6), int64(5)).Return(nil)
	m.issue.EXPECT().List(ctx, issue_repo.ListFilter{ProjectIDs: []int64{6}}).
		Return([]*issue_entity.Issue{
			{ID: 11, SyncMeta: syncmeta_entity.SyncMeta{SyncID: "issue-a", SyncVersion: 3}},
		}, nil)
	m.issue.EXPECT().ReassignProject(ctx, int64(6), int64(5)).Return(nil)
	boom := errors.New("database is locked")
	m.location.EXPECT().ListByProject(ctx, int64(6)).Return(nil, boom)

	keep, err := svc.Merge(ctx, &project_svc.MergeProjectsRequest{SourceID: 6, TargetID: 5})
	require.ErrorIs(t, err, boom)
	assert.Nil(t, keep)
	assert.ErrorIs(t, m.tx.fnErr, boom, "失败必须从事务回调里返回，前五步的写入才会随之回滚")
	assert.False(t, m.tx.committed)
	assert.Empty(t, rec.seen, "合并没有成功，已改挂的任务一条都不能上行")
}

// 要求 4：合并成功时，每一步读写都在同一个事务里，同步通知在提交之后、用外层 ctx
// 发出，次序与逐条发出时一致（先各条被改挂的任务，再是被合并掉的项目）。
//
// 用外层 ctx 是硬约束：NotifyLocalChange 用 ctx 写出站队列，再以
// context.WithoutCancel(ctx) 起后台同步，事务的 ctx 会把一个已提交的 tx 带进去。
func TestProjectSvcMerge_GivenItSucceeds_ThenNotificationsFollowTheCommitOutsideTheTx(t *testing.T) {
	ctx, m, svc := setupMergeTest(t)
	txCtx := context.WithValue(ctx, txMarkerKey{}, "merge-tx")
	m.tx.txCtx = txCtx
	rec := registerTxAwareSync(t, m.tx)

	older := &project_entity.Project{
		ID: 5, Name: "older", Createtime: 100, Path: "/p1",
		SyncMeta: syncmeta_entity.SyncMeta{SyncID: "proj-keep"},
	}
	newer := &project_entity.Project{
		ID: 6, Name: "newer", Createtime: 200, Path: "/p2",
		SyncMeta: syncmeta_entity.SyncMeta{SyncID: "proj-drop", SyncVersion: 2},
	}
	// 期望全部挂在 txCtx 上：哪一步拿外层 ctx 去读写，它就不在事务里，gomock 当场报错。
	m.project.EXPECT().Find(txCtx, int64(6)).Return(newer, nil)
	m.project.EXPECT().Find(txCtx, int64(5)).Return(older, nil)
	m.project.EXPECT().Update(txCtx, gomock.Any()).Return(nil)
	m.session.EXPECT().ReassignProject(txCtx, int64(6), int64(5)).Return(nil)
	m.pa.EXPECT().ListByProject(txCtx, int64(5)).Return(nil, nil)
	m.pa.EXPECT().ListByProject(txCtx, int64(6)).Return(nil, nil)
	m.project.EXPECT().ListByParent(txCtx, int64(6)).Return(nil, nil)
	m.project.EXPECT().ReassignParent(txCtx, int64(6), int64(5)).Return(nil)
	m.issue.EXPECT().List(txCtx, issue_repo.ListFilter{ProjectIDs: []int64{6}}).
		Return([]*issue_entity.Issue{
			{ID: 11, SyncMeta: syncmeta_entity.SyncMeta{SyncID: "issue-a", SyncVersion: 3}},
			{ID: 12, SyncMeta: syncmeta_entity.SyncMeta{SyncID: "issue-b", SyncVersion: 4}},
		}, nil)
	m.issue.EXPECT().ReassignProject(txCtx, int64(6), int64(5)).Return(nil)
	m.location.EXPECT().ListByProject(txCtx, int64(6)).Return(nil, nil)
	m.location.EXPECT().ReassignProject(txCtx, int64(6), int64(5)).Return(nil)
	expectCleanDelete(txCtx, m, 6, newer)

	keep, err := svc.Merge(ctx, &project_svc.MergeProjectsRequest{SourceID: 6, TargetID: 5})
	require.NoError(t, err)
	assert.Equal(t, int64(5), keep.ID)
	require.True(t, m.tx.committed)

	got := make([]sync_svc.LocalChange, 0, len(rec.seen))
	for _, n := range rec.seen {
		assert.True(t, n.afterCommit, "%s %s 在提交之前就发出了", n.change.Kind, n.change.Meta.SyncID)
		assert.False(t, n.withTxCtx, "%s %s 拿着事务的 ctx 发出", n.change.Kind, n.change.Meta.SyncID)
		got = append(got, n.change)
	}
	assert.Equal(t, []sync_svc.LocalChange{
		{Kind: syncwire.KindIssue, LocalID: 11, Op: sync_svc.OpUpdate, Meta: syncmeta_entity.SyncMeta{SyncID: "issue-a", SyncVersion: 3}},
		{Kind: syncwire.KindIssue, LocalID: 12, Op: sync_svc.OpUpdate, Meta: syncmeta_entity.SyncMeta{SyncID: "issue-b", SyncVersion: 4}},
		{Kind: syncwire.KindProject, LocalID: 6, Op: sync_svc.OpDelete, Meta: syncmeta_entity.SyncMeta{SyncID: "proj-drop", SyncVersion: 2}},
	}, got)
}

// TestProjectSvcMerge_GivenTheIssueListFails_StillCompletesTheMerge 那一次读取只为
// 同步层服务（同步未装配时压根不发生），因此它失败时不能否决用户的合并（R8，与
// project_svc.memberSyncMeta / issue_svc.labelLinksBefore 同一口径）。
func TestProjectSvcMerge_GivenTheIssueListFails_StillCompletesTheMerge(t *testing.T) {
	ctx, m, svc := setupMergeTest(t)
	rec := registerRecordingSync(t)

	older := &project_entity.Project{ID: 5, Name: "older", Createtime: 100, Path: "/p1"}
	newer := &project_entity.Project{ID: 6, Name: "newer", Createtime: 200, Path: "/p2"}
	m.project.EXPECT().Find(ctx, int64(6)).Return(newer, nil)
	m.project.EXPECT().Find(ctx, int64(5)).Return(older, nil)
	m.project.EXPECT().Update(ctx, gomock.Any()).Return(nil)
	m.session.EXPECT().ReassignProject(ctx, int64(6), int64(5)).Return(nil)
	m.pa.EXPECT().ListByProject(ctx, int64(5)).Return(nil, nil)
	m.pa.EXPECT().ListByProject(ctx, int64(6)).Return(nil, nil)
	m.project.EXPECT().ListByParent(ctx, int64(6)).Return(nil, nil)
	m.project.EXPECT().ReassignParent(ctx, int64(6), int64(5)).Return(nil)

	m.issue.EXPECT().List(ctx, issue_repo.ListFilter{ProjectIDs: []int64{6}}).
		Return(nil, errors.New("database is locked"))
	// 改挂照常发生：本机不能留下指向已消失项目的悬空引用。
	m.issue.EXPECT().ReassignProject(ctx, int64(6), int64(5)).Return(nil)

	m.location.EXPECT().ListByProject(ctx, int64(6)).Return(nil, nil)
	m.location.EXPECT().ReassignProject(ctx, int64(6), int64(5)).Return(nil)
	expectCleanDelete(ctx, m, 6, newer)

	keep, err := svc.Merge(ctx, &project_svc.MergeProjectsRequest{SourceID: 6, TargetID: 5})
	require.NoError(t, err, "同步层的读失败不该否决合并")
	require.NotNil(t, keep)
	assert.Equal(t, int64(5), keep.ID)

	for _, ch := range rec.changes {
		assert.NotEqual(t, syncwire.KindIssue, ch.Kind, "读不到就发不出，这一轮少发几条而已")
	}
}
