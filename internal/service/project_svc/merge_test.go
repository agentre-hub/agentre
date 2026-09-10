package project_svc_test

import (
	"context"
	"testing"

	"github.com/cago-frame/cago/pkg/consts"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/agentre-hub/agentre/internal/model/entity/project_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/project_location_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/syncmeta_entity"
	"github.com/agentre-hub/agentre/internal/repository/issue_repo"
	"github.com/agentre-hub/agentre/internal/repository/issue_repo/mock_issue_repo"
	"github.com/agentre-hub/agentre/internal/repository/project_location_repo"
	"github.com/agentre-hub/agentre/internal/repository/project_location_repo/mock_project_location_repo"
	"github.com/agentre-hub/agentre/internal/repository/project_repo"
	"github.com/agentre-hub/agentre/internal/repository/project_repo/mock_project_repo"
	"github.com/agentre-hub/agentre/internal/service/project_svc"
	"github.com/agentre-hub/agentre/internal/service/project_svc/mock_project_svc"
)

// 本文件锁住 R11a「合并到已有项目」：合并后只剩一个项目，沿用账号侧的同步标识
// （两边都没有时沿用先创建的那个的），保留本机项目的本机路径；chat_sessions /
// project_agents / projects.parent_id / issues / project_locations 五类引用
// 全部改挂到保留下来的那一行，另一行随后被软删——合并后不允许留下任何指向
// 已消失项目的引用。

type mergeMocks struct {
	project  *mock_project_repo.MockProjectRepo
	pa       *mock_project_repo.MockProjectAgentRepo
	session  *mock_project_svc.MockSessionPort
	issue    *mock_issue_repo.MockIssueRepo
	location *mock_project_location_repo.MockProjectLocationRepo
}

func setupMergeTest(t *testing.T) (context.Context, *mergeMocks, project_svc.ProjectSvc) {
	t.Helper()
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)
	m := &mergeMocks{
		project:  mock_project_repo.NewMockProjectRepo(ctrl),
		pa:       mock_project_repo.NewMockProjectAgentRepo(ctrl),
		session:  mock_project_svc.NewMockSessionPort(ctrl),
		issue:    mock_issue_repo.NewMockIssueRepo(ctrl),
		location: mock_project_location_repo.NewMockProjectLocationRepo(ctrl),
	}
	project_repo.RegisterProject(m.project)
	project_repo.RegisterProjectAgent(m.pa)
	issue_repo.RegisterIssue(m.issue)
	project_location_repo.RegisterProjectLocation(m.location)
	return context.Background(), m, project_svc.New(project_svc.WithSessionPort(m.session))
}

// expectCleanDelete 给「合并已经把 dropID 名下的东西清空」之后、Merge 复用既有
// Delete() 收尾时要满足的守卫桩：无子项目、无活跃会话、把残余会话摘成自由会话、
// 真正执行软删。
//
// 那次 ReassignProject(dropID, 0) 在合并路径上**必然匹配 0 行** —— 上一步已经把
// 会话整批改挂到 keep 了。留着它与 Merge 自己的设计意图一致（见 merge.go 的
// 「Delete 内部的守卫在这里等于免费的二次校验」）：万一上一步漏了或有并发写入，
// 这一下把它摘成自由会话，而不是留下指向已删项目的悬空引用。
func expectCleanDelete(ctx context.Context, m *mergeMocks, dropID int64, drop *project_entity.Project) {
	m.project.EXPECT().Find(ctx, dropID).Return(drop, nil)
	m.project.EXPECT().HasActiveChildren(ctx, dropID).Return(false, nil)
	m.session.EXPECT().CountActiveByProject(ctx, dropID, []string{"running", "waiting"}).Return(int64(0), nil)
	m.session.EXPECT().ReassignProject(ctx, dropID, int64(0)).Return(nil)
	m.project.EXPECT().Delete(ctx, dropID).Return(nil)
}

func TestProjectSvcMerge_GivenAccountSideAndLocalOnlySide_ThenKeepsAccountIdentityAndLocalPath(t *testing.T) {
	ctx, m, svc := setupMergeTest(t)

	// target(2)：账号侧已认领、但本机未配置路径（决策 21 的典型触发场景）。
	target := &project_entity.Project{
		ID: 2, Name: "agentre-hub", LocalPathMissing: true, Createtime: 200,
		SyncMeta: syncmeta_entity.SyncMeta{SyncID: "sync-2", SyncAccountID: 7},
	}
	// source(1)：登录前就存在的本机项目，没有同步标识，但有真实本机路径。
	source := &project_entity.Project{
		ID: 1, Name: "agentre-hub (local)", Path: "/Users/me/Code/agentre-hub",
		Createtime: 100,
	}
	m.project.EXPECT().Find(ctx, int64(1)).Return(source, nil)
	m.project.EXPECT().Find(ctx, int64(2)).Return(target, nil)

	// keep = target（账号侧已认领），drop = source。
	m.project.EXPECT().Update(ctx, gomock.Any()).DoAndReturn(func(_ interface{}, p *project_entity.Project) error {
		assert.Equal(t, int64(2), p.ID)
		assert.Equal(t, "sync-2", p.SyncID, "必须沿用账号侧的同步标识")
		assert.False(t, p.LocalPathMissing, "应借用本机项目的本机路径解除未配置状态")
		assert.Equal(t, "/Users/me/Code/agentre-hub", p.Path)
		return nil
	})

	// 五类引用改挂。会话 / issue / 路径记录按 project_id 整批改挂（软删行与子 agent
	// 委派会话在列表查询里看不见，逐行改挂会把它们留在原地，见 merge_dangling_test.go）。
	m.session.EXPECT().ReassignProject(ctx, int64(1), int64(2)).Return(nil)

	m.pa.EXPECT().ListByProject(ctx, int64(2)).Return(nil, nil)
	m.pa.EXPECT().ListByProject(ctx, int64(1)).Return([]*project_entity.ProjectAgent{
		{ProjectID: 1, AgentID: 30},
	}, nil)
	m.pa.EXPECT().Add(ctx, int64(2), int64(30)).Return(nil)
	m.pa.EXPECT().Remove(ctx, int64(1), int64(30)).Return(nil)

	m.project.EXPECT().ListByParent(ctx, int64(1)).Return(nil, nil)
	m.project.EXPECT().ReassignParent(ctx, int64(1), int64(2)).Return(nil)

	m.issue.EXPECT().ReassignProject(ctx, int64(1), int64(2)).Return(nil)

	m.location.EXPECT().ListByProject(ctx, int64(1)).Return(nil, nil)
	m.location.EXPECT().ReassignProject(ctx, int64(1), int64(2)).Return(nil)

	expectCleanDelete(ctx, m, 1, source)

	got, err := svc.Merge(ctx, &project_svc.MergeProjectsRequest{SourceID: 1, TargetID: 2})
	require.NoError(t, err)
	assert.Equal(t, int64(2), got.ID)
}

func TestProjectSvcMerge_GivenNeitherClaimed_ThenEarlierCreatedWins(t *testing.T) {
	ctx, m, svc := setupMergeTest(t)

	older := &project_entity.Project{ID: 5, Name: "older", Createtime: 100, Path: "/p1"}
	newer := &project_entity.Project{ID: 6, Name: "newer", Createtime: 200, Path: "/p2"}
	m.project.EXPECT().Find(ctx, int64(6)).Return(newer, nil)
	m.project.EXPECT().Find(ctx, int64(5)).Return(older, nil)

	m.project.EXPECT().Update(ctx, gomock.Any()).DoAndReturn(func(_ interface{}, p *project_entity.Project) error {
		assert.Equal(t, int64(5), p.ID, "两边都未认领时应沿用先创建的那个")
		return nil
	})
	m.session.EXPECT().ReassignProject(ctx, int64(6), int64(5)).Return(nil)
	m.pa.EXPECT().ListByProject(ctx, int64(5)).Return(nil, nil)
	m.pa.EXPECT().ListByProject(ctx, int64(6)).Return(nil, nil)
	m.project.EXPECT().ListByParent(ctx, int64(6)).Return(nil, nil)
	m.project.EXPECT().ReassignParent(ctx, int64(6), int64(5)).Return(nil)
	m.issue.EXPECT().ReassignProject(ctx, int64(6), int64(5)).Return(nil)
	m.location.EXPECT().ListByProject(ctx, int64(6)).Return(nil, nil)
	m.location.EXPECT().ReassignProject(ctx, int64(6), int64(5)).Return(nil)
	expectCleanDelete(ctx, m, 6, newer)

	// 入参顺序是 source=6(较新)/target=5(较早)——用户选中谁在前不影响赢家判定。
	got, err := svc.Merge(ctx, &project_svc.MergeProjectsRequest{SourceID: 6, TargetID: 5})
	require.NoError(t, err)
	assert.Equal(t, int64(5), got.ID)
}

func TestProjectSvcMerge_GivenLocationsCollideOnSameFingerprint_ThenLoserLocationDropped(t *testing.T) {
	ctx, m, svc := setupMergeTest(t)

	keep := &project_entity.Project{ID: 20, Createtime: 100, SyncMeta: syncmeta_entity.SyncMeta{SyncAccountID: 1}}
	drop := &project_entity.Project{ID: 21, Createtime: 50}
	m.project.EXPECT().Find(ctx, int64(20)).Return(keep, nil)
	m.project.EXPECT().Find(ctx, int64(21)).Return(drop, nil)
	m.project.EXPECT().Update(ctx, gomock.Any()).Return(nil)
	m.session.EXPECT().ReassignProject(ctx, int64(21), int64(20)).Return(nil)
	m.pa.EXPECT().ListByProject(ctx, int64(20)).Return(nil, nil)
	m.pa.EXPECT().ListByProject(ctx, int64(21)).Return(nil, nil)
	m.project.EXPECT().ListByParent(ctx, int64(21)).Return(nil, nil)
	m.project.EXPECT().ReassignParent(ctx, int64(21), int64(20)).Return(nil)
	m.issue.EXPECT().ReassignProject(ctx, int64(21), int64(20)).Return(nil)

	// drop(21) 与 keep(20) 都对同一台 agentred(指纹 "fp-1") 有一行——不能两行并存,
	// 必须择一(此处保留 keep 已有的那行), drop 的那行被删除而不是改挂。落败那行按
	// R5 记录的断言在 merge_dangling_test.go；这里的行未认领(SyncAccountID=0),
	// 按 R12 不写同步侧任何一行。
	m.location.EXPECT().ListByProject(ctx, int64(21)).Return([]*project_location_entity.ProjectLocation{
		{ID: 71, ProjectID: 21, DeviceFingerprint: "fp-1", Path: "/loser"},
	}, nil)
	m.location.EXPECT().FindByProjectAndFingerprint(ctx, int64(20), "fp-1").Return(
		&project_location_entity.ProjectLocation{ID: 70, ProjectID: 20, DeviceFingerprint: "fp-1", Path: "/winner"}, nil)
	m.location.EXPECT().Delete(ctx, int64(71)).Return(nil)
	m.location.EXPECT().ReassignProject(ctx, int64(21), int64(20)).Return(nil)

	expectCleanDelete(ctx, m, 21, drop)

	_, err := svc.Merge(ctx, &project_svc.MergeProjectsRequest{SourceID: 20, TargetID: 21})
	require.NoError(t, err)
}

func TestProjectSvcMerge_GivenKeepIsChildOfDrop_ThenReparentedToDropsOwnParent(t *testing.T) {
	ctx, m, svc := setupMergeTest(t)

	// keep(30) 今天挂在 drop(31) 底下；drop 本身挂在 99 底下。合并后 drop 消失，
	// keep 必须改挂到 99，不能继续指向一个已经消失的 parent_id。
	keep := &project_entity.Project{ID: 30, ParentID: 31, Createtime: 100, SyncMeta: syncmeta_entity.SyncMeta{SyncAccountID: 1}}
	drop := &project_entity.Project{ID: 31, ParentID: 99, Createtime: 50}
	m.project.EXPECT().Find(ctx, int64(30)).Return(keep, nil)
	m.project.EXPECT().Find(ctx, int64(31)).Return(drop, nil)
	m.project.EXPECT().Update(ctx, gomock.Any()).DoAndReturn(func(_ interface{}, p *project_entity.Project) error {
		assert.Equal(t, int64(99), p.ParentID, "keep 不能继续指向即将消失的 drop")
		return nil
	})
	m.session.EXPECT().ReassignProject(ctx, int64(31), int64(30)).Return(nil)
	m.pa.EXPECT().ListByProject(ctx, int64(30)).Return(nil, nil)
	m.pa.EXPECT().ListByProject(ctx, int64(31)).Return(nil, nil)
	m.project.EXPECT().ListByParent(ctx, int64(31)).Return(nil, nil)
	m.project.EXPECT().ReassignParent(ctx, int64(31), int64(30)).Return(nil)
	m.issue.EXPECT().ReassignProject(ctx, int64(31), int64(30)).Return(nil)
	m.location.EXPECT().ListByProject(ctx, int64(31)).Return(nil, nil)
	m.location.EXPECT().ReassignProject(ctx, int64(31), int64(30)).Return(nil)
	expectCleanDelete(ctx, m, 31, drop)

	got, err := svc.Merge(ctx, &project_svc.MergeProjectsRequest{SourceID: 30, TargetID: 31})
	require.NoError(t, err)
	assert.Equal(t, int64(99), got.ParentID)
}

func TestProjectSvcMerge_GivenSameProjectTwice_ThenRejects(t *testing.T) {
	ctx, _, svc := setupMergeTest(t)
	_, err := svc.Merge(ctx, &project_svc.MergeProjectsRequest{SourceID: 1, TargetID: 1})
	require.Error(t, err)
}

func TestProjectSvcMerge_GivenMissingProject_ThenRejects(t *testing.T) {
	ctx, m, svc := setupMergeTest(t)
	m.project.EXPECT().Find(ctx, int64(1)).Return(nil, nil)
	m.project.EXPECT().Find(ctx, int64(2)).Return(&project_entity.Project{ID: 2, Status: consts.ACTIVE}, nil)

	_, err := svc.Merge(ctx, &project_svc.MergeProjectsRequest{SourceID: 1, TargetID: 2})
	require.Error(t, err)
}
