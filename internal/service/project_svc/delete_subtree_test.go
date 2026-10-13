package project_svc_test

import (
	"context"
	"slices"
	"testing"

	"github.com/cago-frame/cago/pkg/consts"
	"github.com/cago-frame/cago/pkg/utils/httputils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/agentre-hub/agentre/internal/model/entity/chat_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/project_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/syncmeta_entity"
	"github.com/agentre-hub/agentre/internal/pkg/code"
	"github.com/agentre-hub/agentre/internal/repository/project_repo"
	"github.com/agentre-hub/agentre/internal/service/project_svc"
	"github.com/agentre-hub/agentre/internal/service/sync_svc"
	"github.com/agentre-hub/agentre/pkg/syncwire"
)

// 本文件锁住「删项目连它的整棵子树一起删」。
//
// 删除确认框（与 web 端共用）对用户说的是「它的 N 个子项目会一并删除」，并要求把
// 项目名敲一遍；桌面端此前在这一步直接拒绝（ProjectHasChildren），界面承诺的事
// 一次都没发生过。web 后端早就是级联的（workspace_svc.cascadeProjectDelete）。
//
// 每一个被删的项目照旧守着单个项目的那几条规矩：子树里任何一处还有 running /
// waiting 会话就整体拒绝、一行都不动；幸存会话先摘成自由会话再软删项目行；各自发
// 一条删除通知。整件事跑在一个事务里——删了一半的子树比一次拒绝更糟。

type deleteSubtreeFixture struct {
	projects *projectTreeTable
	sessions *projectSessionTable
	tx       *fakeTx
	log      *[]string
}

func setupDeleteSubtreeTest(
	t *testing.T, projects []*project_entity.Project, sessions []*chat_entity.Session,
) (context.Context, *deleteSubtreeFixture, project_svc.ProjectSvc) {
	t.Helper()
	log := &[]string{}
	f := &deleteSubtreeFixture{
		projects: &projectTreeTable{rows: projects, log: log},
		sessions: &projectSessionTable{rows: sessions, log: log},
		tx:       &fakeTx{},
		log:      log,
	}
	project_repo.RegisterProject(f.projects)
	svc := project_svc.New(project_svc.WithSessionPort(f.sessions), project_svc.WithTxRunner(f.tx))
	return context.Background(), f, svc
}

// syncedProject 是一行带同步标识的活跃项目：删除通知要靠标识才发得出去。
func syncedProject(id, parentID int64, name string) *project_entity.Project {
	p := active(id, parentID, name)
	p.SyncMeta = syncmeta_entity.SyncMeta{SyncID: "sync-" + name, SyncVersion: id}
	return p
}

func idleSession(id, projectID int64) *chat_entity.Session {
	return &chat_entity.Session{ID: id, ProjectID: projectID, AgentStatus: "idle", Status: consts.ACTIVE}
}

// twoLevelTree 是 1 → {2 → 3, 4} 这棵两层深的子树，外加一棵不相干的 9 → 10。
func twoLevelTree() []*project_entity.Project {
	return []*project_entity.Project{
		syncedProject(1, 0, "root"), syncedProject(2, 1, "mid"), syncedProject(3, 2, "leaf"),
		syncedProject(4, 1, "side"), syncedProject(9, 0, "other"), syncedProject(10, 9, "other-child"),
	}
}

func TestProjectSvcDelete_GivenSubtreeTwoLevelsDeep_ThenEveryDescendantGoesAndItsSessionsAreFreed(t *testing.T) {
	ctx, f, svc := setupDeleteSubtreeTest(t, twoLevelTree(), []*chat_entity.Session{
		idleSession(101, 1), idleSession(102, 2), idleSession(103, 3), idleSession(104, 4),
		idleSession(109, 9),
	})

	require.NoError(t, svc.Delete(ctx, 1))

	assert.Equal(t, []int64{9, 10}, f.projects.activeIDs(), "整棵子树都该删掉，旁边那棵树一行不动")
	for _, sessionID := range []int64{101, 102, 103, 104} {
		assert.Equal(t, int64(0), f.sessions.projectOf(sessionID),
			"会话 %d 应改挂成自由会话，而不是留着指向已删项目的悬空引用", sessionID)
	}
	assert.Equal(t, int64(9), f.sessions.projectOf(109), "别的项目名下的会话不该被动到")
	// 逐个项目都是「先摘引用、再删项目行」，与只删一个项目时同一个次序。
	for _, id := range []string{"1", "2", "3", "4"} {
		reassigned, deleted := slices.Index(*f.log, "reassign:"+id), slices.Index(*f.log, "delete:"+id)
		require.GreaterOrEqual(t, reassigned, 0, "项目 %s 名下的会话没有被摘", id)
		assert.Less(t, reassigned, deleted, "项目 %s 应先摘会话再软删", id)
	}
	assert.True(t, f.tx.committed)
}

// 子树里每一个被删的项目各发一条删除通知，子项目在前、父项目在后，并且都在事务
// 提交之后、拿外层 ctx 发出（同 Merge：NotifyLocalChange 用拿到的 ctx 写出站队列，
// 事务的 ctx 会把一个已经提交的 tx 交给它）。
//
// 子在前是同步层要的次序：出站队列按入队次序上行、server 逐条处理，父项目的墓碑若
// 先到，其它端在子项目的墓碑到达之前会看到一批指向已删父项目的子项目。
func TestProjectSvcDelete_GivenSubtree_ThenEachProjectIsAnnouncedChildrenFirstAfterTheCommit(t *testing.T) {
	ctx, f, svc := setupDeleteSubtreeTest(t, twoLevelTree(), nil)
	f.tx.txCtx = context.WithValue(ctx, txMarkerKey{}, "delete-tx")
	rec := registerTxAwareSync(t, f.tx)
	changed := registerConfigChangeSpy(t)

	require.NoError(t, svc.Delete(ctx, 1))

	assert.Zero(t, f.projects.outsideTx, "项目表的每一次读写都该在事务里")
	assert.Zero(t, f.sessions.outsideTx, "会话表的每一次读写都该在事务里")

	announced := make([]int64, 0, len(rec.seen))
	for _, n := range rec.seen {
		assert.True(t, n.afterCommit, "项目 %d 的删除在提交之前就发出了", n.change.LocalID)
		assert.False(t, n.withTxCtx, "项目 %d 的删除拿着事务的 ctx 发出", n.change.LocalID)
		assert.Equal(t, syncwire.KindProject, n.change.Kind)
		assert.Equal(t, sync_svc.OpDelete, n.change.Op)
		announced = append(announced, n.change.LocalID)
	}
	assert.ElementsMatch(t, []int64{1, 2, 3, 4}, announced, "子树里每个项目各发一条，别的项目一条都不发")
	assert.Less(t, slices.Index(announced, 3), slices.Index(announced, 2), "孙项目先于它的父项目")
	assert.Less(t, slices.Index(announced, 2), slices.Index(announced, 1), "子项目先于被删的那个项目")
	assert.Less(t, slices.Index(announced, 4), slices.Index(announced, 1), "子项目先于被删的那个项目")
	// 每条通知带的是那一行自己的同步元数据（基版本取自它，R4a）。
	for _, n := range rec.seen {
		if n.change.LocalID == 3 {
			assert.Equal(t, syncmeta_entity.SyncMeta{SyncID: "sync-leaf", SyncVersion: 3}, n.change.Meta)
		}
	}
	assert.Equal(t, [][]string{{syncwire.KindProject}}, *changed, "界面只需要重拉一次项目树")
}

// 子树深处还有一条进行中的会话：整体拒绝，一行都不动。守卫必须在任何写入之前把
// 整棵子树看完——先删了几个子项目再撞上它，靠回滚才收得回来的就不是「拒绝」了。
func TestProjectSvcDelete_GivenRunningSessionDeepInTheSubtree_ThenRefusesAndTouchesNothing(t *testing.T) {
	for _, status := range []string{"running", "waiting"} {
		t.Run(status, func(t *testing.T) {
			ctx, f, svc := setupDeleteSubtreeTest(t, twoLevelTree(), []*chat_entity.Session{
				idleSession(101, 1), idleSession(102, 2), idleSession(104, 4),
				{ID: 103, ProjectID: 3, AgentStatus: status, Status: consts.ACTIVE},
			})
			rec := registerRecordingSync(t)
			changed := registerConfigChangeSpy(t)

			err := svc.Delete(ctx, 1)

			var httpErr *httputils.Error
			require.ErrorAs(t, err, &httpErr)
			assert.Equal(t, code.ProjectHasActiveSessions, httpErr.Code)
			assert.Equal(t, []int64{1, 2, 3, 4, 9, 10}, f.projects.activeIDs(), "拒绝时一个项目都不该被删")
			assert.Empty(t, *f.log, "拒绝时不该有任何写入：既不摘会话，也不删项目行")
			assert.False(t, f.tx.committed)
			assert.Empty(t, rec.changes)
			assert.Empty(t, *changed)
		})
	}
}

// 叶子项目照旧：只删它自己，父项目与兄弟都还在。
func TestProjectSvcDelete_GivenLeaf_ThenOnlyThatProjectGoes(t *testing.T) {
	ctx, f, svc := setupDeleteSubtreeTest(t, twoLevelTree(), []*chat_entity.Session{
		idleSession(102, 2), idleSession(103, 3),
	})
	rec := registerRecordingSync(t)

	require.NoError(t, svc.Delete(ctx, 3))

	assert.Equal(t, []int64{1, 2, 4, 9, 10}, f.projects.activeIDs())
	assert.Equal(t, int64(0), f.sessions.projectOf(103))
	assert.Equal(t, int64(2), f.sessions.projectOf(102), "父项目名下的会话不该被动到")
	require.Len(t, rec.changes, 1)
	assert.Equal(t, int64(3), rec.changes[0].LocalID)
}

// 中途有一步落库失败：错误从事务回调里返回（前面几行的写入才会随之回滚），删除通知
// 一条都不发——没有这一层事务，库里留下的就是删了一半的子树。
func TestProjectSvcDelete_GivenALaterRowFails_ThenTheTxRollsBackAndNothingIsAnnounced(t *testing.T) {
	ctx, f, svc := setupDeleteSubtreeTest(t, twoLevelTree(), nil)
	// 子项目们已经软删，轮到被删的那个项目自己时失败。
	f.projects.failDeleteOn = 1
	rec := registerRecordingSync(t)
	changed := registerConfigChangeSpy(t)

	err := svc.Delete(ctx, 1)

	require.ErrorIs(t, err, errProjectDeleteFailed)
	assert.ErrorIs(t, f.tx.fnErr, errProjectDeleteFailed, "失败必须从事务回调里返回，已删的子项目才会随之回滚")
	assert.False(t, f.tx.committed)
	assert.Empty(t, rec.changes, "删除没有成功，一条墓碑都不能上行")
	assert.Empty(t, *changed)
}

func TestProjectSvcDelete_GivenProjectNotFound_ThenRejects(t *testing.T) {
	ctx, f, svc := setupDeleteSubtreeTest(t, twoLevelTree(), nil)

	err := svc.Delete(ctx, 404)

	var httpErr *httputils.Error
	require.ErrorAs(t, err, &httpErr)
	assert.Equal(t, code.ProjectNotFound, httpErr.Code)
	assert.Empty(t, *f.log)
}

// DeleteLeaf 是不级联的那一条：agrctl 的 `delete project` 走它。命令面的约定是带
// 子项目的项目拒删（docs/specs/2026-09-22-agrctl-resource-management.md，server 侧
// 执行者在 ctl_svc 里判的是同一条），审批卡上也只写得出被删的那一个项目——让它跟着
// Delete 一起级联，就是凭一张只提了一个名字的卡删掉一整棵子树。
func TestProjectSvcDeleteLeaf_GivenSubprojects_ThenRefusesAndTouchesNothing(t *testing.T) {
	ctx, f, svc := setupDeleteSubtreeTest(t, twoLevelTree(), []*chat_entity.Session{idleSession(101, 1)})
	rec := registerRecordingSync(t)
	changed := registerConfigChangeSpy(t)

	err := svc.DeleteLeaf(ctx, 1)

	var httpErr *httputils.Error
	require.ErrorAs(t, err, &httpErr)
	assert.Equal(t, code.ProjectHasChildren, httpErr.Code)
	assert.Equal(t, []int64{1, 2, 3, 4, 9, 10}, f.projects.activeIDs(), "拒绝时一个项目都不该被删")
	assert.Empty(t, *f.log, "拒绝时不该有任何写入")
	assert.False(t, f.tx.committed)
	assert.Empty(t, rec.changes)
	assert.Empty(t, *changed)
}

func TestProjectSvcDeleteLeaf_GivenLeaf_ThenDeletesItAndFreesItsSessions(t *testing.T) {
	ctx, f, svc := setupDeleteSubtreeTest(t, twoLevelTree(), []*chat_entity.Session{idleSession(103, 3)})
	f.tx.txCtx = context.WithValue(ctx, txMarkerKey{}, "delete-tx")
	rec := registerTxAwareSync(t, f.tx)
	changed := registerConfigChangeSpy(t)

	require.NoError(t, svc.DeleteLeaf(ctx, 3))

	assert.Equal(t, []int64{1, 2, 4, 9, 10}, f.projects.activeIDs())
	assert.Equal(t, int64(0), f.sessions.projectOf(103))
	assert.Equal(t, []string{"reassign:3", "delete:3"}, *f.log, "先摘会话再软删")
	assert.Zero(t, f.projects.outsideTx, "摘会话与软删要么都落库要么都不落")
	assert.Zero(t, f.sessions.outsideTx)
	require.Len(t, rec.seen, 1)
	assert.True(t, rec.seen[0].afterCommit)
	assert.False(t, rec.seen[0].withTxCtx)
	assert.Equal(t, sync_svc.LocalChange{
		Kind: syncwire.KindProject, LocalID: 3, Op: sync_svc.OpDelete,
		Meta: syncmeta_entity.SyncMeta{SyncID: "sync-leaf", SyncVersion: 3},
	}, rec.seen[0].change)
	assert.Equal(t, [][]string{{syncwire.KindProject}}, *changed)
}
