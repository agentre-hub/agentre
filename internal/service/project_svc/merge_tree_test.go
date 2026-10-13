package project_svc_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/agentre-hub/agentre/internal/model/entity/project_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/syncmeta_entity"
	"github.com/agentre-hub/agentre/internal/repository/issue_repo"
	"github.com/agentre-hub/agentre/internal/repository/project_location_repo"
	"github.com/agentre-hub/agentre/internal/repository/project_repo"
	"github.com/agentre-hub/agentre/internal/repository/project_repo/mock_project_repo"
	"github.com/agentre-hub/agentre/internal/service/project_svc"
)

// 本文件锁住合并之后项目树的形状：keep 与 drop 是祖孙关系时，合并出来的仍然得是
// 一棵树——没有环，也没有指向已消失项目的父级。
//
// 赢家是按「账号侧已认领 / 先创建」挑的（chooseMergeWinner），与两个项目在树上谁高
// 谁低无关，所以 keep 完全可能挂在 drop 的子树深处。drop 的子项目随后整批改挂到
// keep 名下：keep 若还留在那棵子树里，它自己的祖先就成了它的子项目——一个环，项目树
// 从根上再也走不到这两段，界面上两棵子树一起消失。

func setupMergeTreeTest(
	t *testing.T, rows ...*project_entity.Project,
) (context.Context, *projectTreeTable, project_svc.ProjectSvc) {
	t.Helper()
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)
	table := &projectTreeTable{rows: rows}
	members := mock_project_repo.NewMockProjectAgentRepo(ctrl)
	members.EXPECT().ListByProject(gomock.Any(), gomock.Any()).Return(nil, nil).AnyTimes()
	project_repo.RegisterProject(table)
	project_repo.RegisterProjectAgent(members)
	issue_repo.RegisterIssue(&mergeIssueTable{})
	project_location_repo.RegisterProjectLocation(&mergeLocationTable{})
	svc := project_svc.New(project_svc.WithSessionPort(&mergeSessionTable{}), project_svc.WithTxRunner(&fakeTx{}))
	return context.Background(), table, svc
}

// claimed 把一行标成账号侧已认领——合并时它是赢家（keep），不论它在树上多深。
func claimed(p *project_entity.Project) *project_entity.Project {
	p.SyncMeta = syncmeta_entity.SyncMeta{SyncID: "sync-" + p.Name, SyncAccountID: 1}
	return p
}

func treeNodeCount(nodes []*project_svc.ProjectNode) int {
	n := len(nodes)
	for _, node := range nodes {
		n += treeNodeCount(node.Children)
	}
	return n
}

func TestProjectSvcMerge_GivenKeepIsGrandchildOfDrop_ThenKeepTakesDropsPlaceAndTheTreeStaysWhole(t *testing.T) {
	// 99 ── drop(31) ─┬─ mid(32) ── keep(30) ── keep-child(34)
	//                 └─ side(33)
	ctx, table, svc := setupMergeTreeTest(t,
		active(99, 0, "top"),
		active(31, 99, "drop"),
		active(32, 31, "mid"),
		active(33, 31, "side"),
		claimed(active(30, 32, "keep")),
		active(34, 30, "keep-child"),
	)

	got, err := svc.Merge(ctx, &project_svc.MergeProjectsRequest{SourceID: 30, TargetID: 31})
	require.NoError(t, err)
	require.Equal(t, int64(30), got.ID)

	assert.Equal(t, []int64{30, 32, 33, 34, 99}, table.activeIDs(), "只有 drop 这一行消失")
	table.assertStillATree(t)
	// keep 顶到 drop 原来的位置上，drop 的子项目（连同 keep 原先的父级）都归到它名下——
	// 与 keep 是 drop 的直接子项目时同一条规矩。
	assert.Equal(t, int64(99), table.row(30).ParentID, "keep 应改挂到 drop 自己的父级")
	assert.Equal(t, int64(99), got.ParentID)
	assert.Equal(t, int64(30), table.row(32).ParentID, "drop 的子项目改挂到 keep")
	assert.Equal(t, int64(30), table.row(33).ParentID, "drop 的子项目改挂到 keep")
	assert.Equal(t, int64(30), table.row(34).ParentID, "keep 自己的子项目不动")

	// 用户看到的那一面：项目树从根上走得到每一个存活的项目。
	roots, err := svc.ListTree(ctx)
	require.NoError(t, err)
	assert.Equal(t, 5, treeNodeCount(roots), "合并后有项目从项目树上消失了")
}

// 镜像的那一种：drop 挂在 keep 的子树深处。这一种本来就是安全的——keep 不动，drop
// 的子项目上提到 keep 名下，而它们的子树里不可能有 keep（keep 是它们的祖先）。
func TestProjectSvcMerge_GivenDropIsGrandchildOfKeep_ThenDropsChildrenMoveUpToKeep(t *testing.T) {
	// keep(40) ── mid(41) ── drop(42) ── drop-child(43)
	ctx, table, svc := setupMergeTreeTest(t,
		claimed(active(40, 0, "keep")),
		active(41, 40, "mid"),
		active(42, 41, "drop"),
		active(43, 42, "drop-child"),
	)

	got, err := svc.Merge(ctx, &project_svc.MergeProjectsRequest{SourceID: 42, TargetID: 40})
	require.NoError(t, err)
	require.Equal(t, int64(40), got.ID)

	assert.Equal(t, []int64{40, 41, 43}, table.activeIDs(), "只有 drop 这一行消失")
	table.assertStillATree(t)
	assert.Equal(t, int64(0), table.row(40).ParentID, "keep 的位置不变")
	assert.Equal(t, int64(40), table.row(41).ParentID, "夹在中间的那一层不动")
	assert.Equal(t, int64(40), table.row(43).ParentID, "drop 的子项目改挂到 keep")

	roots, err := svc.ListTree(ctx)
	require.NoError(t, err)
	assert.Equal(t, 3, treeNodeCount(roots))
}

// 库里已经有环的机器（这个缺陷修好之前合并出来的）：keep 的父链绕进一个不经过 drop
// 的环。判「keep 在不在 drop 底下」时不能顺着那个环一直转下去——这一步在事务里，
// 转不出来就是攥着写锁把整个应用卡死。
func TestProjectSvcMerge_GivenKeepsAncestorsAlreadyFormACycle_ThenStillReturns(t *testing.T) {
	// keep(50) ── 60 ⇄ 61（环）      drop(51)
	ctx, table, svc := setupMergeTreeTest(t,
		claimed(active(50, 60, "keep")),
		active(60, 61, "loop-a"),
		active(61, 60, "loop-b"),
		active(51, 0, "drop"),
	)

	done := make(chan error, 1)
	go func() {
		_, err := svc.Merge(ctx, &project_svc.MergeProjectsRequest{SourceID: 50, TargetID: 51})
		done <- err
	}()

	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("合并顺着库里已有的环转不出来")
	}
	assert.Equal(t, []int64{50, 60, 61}, table.activeIDs())
	assert.Equal(t, int64(60), table.row(50).ParentID, "keep 不在 drop 底下，位置不变")
}
