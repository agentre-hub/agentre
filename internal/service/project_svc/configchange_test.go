package project_svc_test

import (
	"context"
	"errors"
	"testing"

	"github.com/cago-frame/cago/pkg/consts"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/project_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/syncmeta_entity"
	"github.com/agentre-hub/agentre/internal/repository/project_repo"
	"github.com/agentre-hub/agentre/internal/repository/project_repo/mock_project_repo"
	"github.com/agentre-hub/agentre/internal/service/project_svc"
	"github.com/agentre-hub/agentre/internal/service/project_svc/mock_project_svc"
	"github.com/agentre-hub/agentre/internal/service/sync_svc"
	"github.com/agentre-hub/agentre/pkg/syncwire"
)

// registerConfigChangeSpy 装配 config:changed 的替身 emitter（各服务包各留一份，见
// llm_provider_svc 同名 helper）。
func registerConfigChangeSpy(t *testing.T) *[][]string {
	t.Helper()
	got := &[][]string{}
	sync_svc.SetConfigChangeEmitter(func(kinds []string) {
		*got = append(*got, kinds)
	})
	t.Cleanup(func() { sync_svc.SetConfigChangeEmitter(nil) })
	return got
}

func TestProjectSvcCreate_EmitsConfigChanged(t *testing.T) {
	ctx, mp, _, _, svc := setupProjectSvc(t)
	got := registerConfigChangeSpy(t)
	tmp := t.TempDir()
	mp.EXPECT().FindByName(ctx, int64(0), "Agentre").Return(nil, nil)
	mp.EXPECT().NextSortOrder(ctx, int64(0)).Return(1, nil)
	mp.EXPECT().Create(ctx, gomock.Any()).Return(nil)

	_, err := svc.Create(ctx, &project_svc.CreateProjectRequest{Name: "Agentre", Path: tmp})

	require.NoError(t, err)
	assert.Equal(t, [][]string{{syncwire.KindProject}}, *got)
}

// 带初始成员的 create：订阅方收到 config:changed 就重新拉取，所以事件要在成员写完之后才发，
// 否则拉到的项目可能还没有成员。
func TestProjectSvcCreate_GivenInitialMembers_EmitsConfigChangedAfterMembersAdded(t *testing.T) {
	ctx, mp, mpa, _, svc := setupProjectSvc(t)
	got := registerConfigChangeSpy(t)
	tmp := t.TempDir()
	mp.EXPECT().FindByName(ctx, int64(0), "Agentre").Return(nil, nil)
	mp.EXPECT().NextSortOrder(ctx, int64(0)).Return(1, nil)
	mp.EXPECT().Create(ctx, gomock.Any()).Return(nil)
	mpa.EXPECT().Add(ctx, gomock.Any(), int64(7)).DoAndReturn(func(context.Context, int64, int64) error {
		assert.Empty(t, *got, "config:changed must not fire before the initial members are in")
		return nil
	})

	_, err := svc.Create(ctx, &project_svc.CreateProjectRequest{Name: "Agentre", Path: tmp, InitialAgentIDs: []int64{7}})

	require.NoError(t, err)
	assert.Equal(t, [][]string{{syncwire.KindProject}}, *got)
}

func TestProjectSvcCreate_GivenRepoFails_DoesNotEmitConfigChanged(t *testing.T) {
	ctx, mp, _, _, svc := setupProjectSvc(t)
	got := registerConfigChangeSpy(t)
	tmp := t.TempDir()
	mp.EXPECT().FindByName(ctx, int64(0), "Agentre").Return(nil, nil)
	mp.EXPECT().NextSortOrder(ctx, int64(0)).Return(1, nil)
	mp.EXPECT().Create(ctx, gomock.Any()).Return(errors.New("db down"))

	_, err := svc.Create(ctx, &project_svc.CreateProjectRequest{Name: "Agentre", Path: tmp})

	require.Error(t, err)
	assert.Empty(t, *got)
}

func TestProjectSvcUpdate_EmitsConfigChanged(t *testing.T) {
	ctx, mp, _, _, svc := setupProjectSvc(t)
	got := registerConfigChangeSpy(t)
	existing := &project_entity.Project{ID: 1, Name: "old", Path: t.TempDir(), Status: consts.ACTIVE}
	mp.EXPECT().Find(ctx, int64(1)).Return(existing, nil)
	mp.EXPECT().Update(ctx, gomock.Any()).Return(nil)

	_, err := svc.Update(ctx, &project_svc.UpdateProjectRequest{ID: 1, Name: "old"})

	require.NoError(t, err)
	assert.Equal(t, [][]string{{syncwire.KindProject}}, *got)
}

func TestProjectSvcDelete_EmitsConfigChanged(t *testing.T) {
	ctx, mp, _, ms, svc := setupProjectSvc(t)
	got := registerConfigChangeSpy(t)
	mp.EXPECT().Find(ctx, int64(1)).Return(&project_entity.Project{ID: 1, Status: consts.ACTIVE}, nil)
	mp.EXPECT().HasActiveChildren(ctx, int64(1)).Return(false, nil)
	ms.EXPECT().CountActiveByProject(ctx, int64(1), []string{"running", "waiting"}).Return(int64(0), nil)
	ms.EXPECT().ReassignProject(ctx, int64(1), int64(0)).Return(nil)
	mp.EXPECT().Delete(ctx, int64(1)).Return(nil)

	require.NoError(t, svc.Delete(ctx, 1))
	assert.Equal(t, [][]string{{syncwire.KindProject}}, *got)
}

// AddMember/RemoveMember 是 agrctl 命令面里 `update project --add-member/--remove-member`
// 的落点（command surface）；成员变化要刷组织页的项目成员列表，用 KindProject 而不是
// 内部同步用的 KindProjectAgent——前端没有「project_agent」这一份数据可刷。
func TestProjectSvcAddMember_EmitsConfigChanged(t *testing.T) {
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)
	mp := mock_project_repo.NewMockProjectRepo(ctrl)
	mpa := mock_project_repo.NewMockProjectAgentRepo(ctrl)
	agentMock := mock_project_svc.NewMockAgentPort(ctrl)
	project_repo.RegisterProject(mp)
	project_repo.RegisterProjectAgent(mpa)
	svc := project_svc.New(project_svc.WithAgentPort(agentMock))
	got := registerConfigChangeSpy(t)
	ctx := context.Background()

	mp.EXPECT().Find(ctx, int64(1)).Return(&project_entity.Project{ID: 1, Status: consts.ACTIVE}, nil)
	agentMock.EXPECT().Find(ctx, int64(2)).Return(&agent_entity.Agent{ID: 2, Status: consts.ACTIVE}, nil)
	mpa.EXPECT().Add(ctx, int64(1), int64(2)).Return(nil)

	require.NoError(t, svc.AddMember(ctx, 1, 2))
	assert.Equal(t, [][]string{{syncwire.KindProject}}, *got)
}

func TestProjectSvcRemoveMember_EmitsConfigChanged(t *testing.T) {
	ctx, _, mpa, _, svc := setupProjectSvc(t)
	got := registerConfigChangeSpy(t)
	mpa.EXPECT().Remove(ctx, int64(1), int64(2)).Return(nil)

	require.NoError(t, svc.RemoveMember(ctx, 1, 2))
	assert.Equal(t, [][]string{{syncwire.KindProject}}, *got)
}

func TestProjectSvcMove_EmitsConfigChanged(t *testing.T) {
	ctx, mp, _, _, svc := setupProjectSvc(t)
	got := registerConfigChangeSpy(t)
	mp.EXPECT().Find(ctx, int64(2)).Return(&project_entity.Project{ID: 2, ParentID: 1, Name: "child", Status: consts.ACTIVE}, nil)
	mp.EXPECT().FindByName(ctx, int64(0), "child").Return(nil, nil)
	mp.EXPECT().Update(ctx, gomock.Any()).Return(nil)

	_, err := svc.Move(ctx, &project_svc.MoveProjectRequest{ID: 2, NewParentID: 0})

	require.NoError(t, err)
	assert.Equal(t, [][]string{{syncwire.KindProject}}, *got)
}

func TestProjectSvcMove_GivenRepoFails_DoesNotEmitConfigChanged(t *testing.T) {
	ctx, mp, _, _, svc := setupProjectSvc(t)
	got := registerConfigChangeSpy(t)
	mp.EXPECT().Find(ctx, int64(2)).Return(&project_entity.Project{ID: 2, ParentID: 1, Name: "child", Status: consts.ACTIVE}, nil)
	mp.EXPECT().FindByName(ctx, int64(0), "child").Return(nil, nil)
	mp.EXPECT().Update(ctx, gomock.Any()).Return(errors.New("db down"))

	_, err := svc.Move(ctx, &project_svc.MoveProjectRequest{ID: 2, NewParentID: 0})

	require.Error(t, err)
	assert.Empty(t, *got)
}

func TestProjectSvcReorder_EmitsConfigChanged(t *testing.T) {
	ctx, mp, _, _, svc := setupProjectSvc(t)
	got := registerConfigChangeSpy(t)
	mp.EXPECT().ListByParent(ctx, int64(7)).Return([]*project_entity.Project{
		{ID: 1, ParentID: 7, Name: "A"},
		{ID: 2, ParentID: 7, Name: "B"},
	}, nil)
	mp.EXPECT().ReorderSiblings(ctx, int64(7), []int64{2, 1}).Return(nil)

	err := svc.Reorder(ctx, &project_svc.ReorderProjectsRequest{ParentID: 7, OrderedIDs: []int64{2, 1}})

	require.NoError(t, err)
	assert.Equal(t, [][]string{{syncwire.KindProject}}, *got, "一次重排只发一次，不按兄弟个数刷 N 遍")
}

func TestProjectSvcReorder_GivenRepoFails_DoesNotEmitConfigChanged(t *testing.T) {
	ctx, mp, _, _, svc := setupProjectSvc(t)
	got := registerConfigChangeSpy(t)
	mp.EXPECT().ListByParent(ctx, int64(7)).Return([]*project_entity.Project{
		{ID: 1, ParentID: 7, Name: "A"},
		{ID: 2, ParentID: 7, Name: "B"},
	}, nil)
	mp.EXPECT().ReorderSiblings(ctx, int64(7), []int64{2, 1}).Return(errors.New("db down"))

	err := svc.Reorder(ctx, &project_svc.ReorderProjectsRequest{ParentID: 7, OrderedIDs: []int64{2, 1}})

	require.Error(t, err)
	assert.Empty(t, *got)
}

// SetLocalPath/ClearLocalPath 不参与跨端同步（决策 6），但仍是这台桌面端项目树
// 要展示的字段——config:changed 与「要不要同步」是两条独立的判据（见协调者对
// 「project Merge or local-path writes」不豁免的澄清）。
func TestProjectSvcSetLocalPath_EmitsConfigChanged(t *testing.T) {
	ctx, mp, _, _, svc := setupProjectSvc(t)
	got := registerConfigChangeSpy(t)
	tmp := t.TempDir()
	mp.EXPECT().Find(ctx, int64(9)).Return(&project_entity.Project{ID: 9, LocalPathMissing: true}, nil)
	mp.EXPECT().Update(ctx, gomock.Any()).Return(nil)

	_, err := svc.SetLocalPath(ctx, 9, tmp)

	require.NoError(t, err)
	assert.Equal(t, [][]string{{syncwire.KindProject}}, *got)
}

func TestProjectSvcSetLocalPath_GivenRepoFails_DoesNotEmitConfigChanged(t *testing.T) {
	ctx, mp, _, _, svc := setupProjectSvc(t)
	got := registerConfigChangeSpy(t)
	tmp := t.TempDir()
	mp.EXPECT().Find(ctx, int64(9)).Return(&project_entity.Project{ID: 9, LocalPathMissing: true}, nil)
	mp.EXPECT().Update(ctx, gomock.Any()).Return(errors.New("db down"))

	_, err := svc.SetLocalPath(ctx, 9, tmp)

	require.Error(t, err)
	assert.Empty(t, *got)
}

func TestProjectSvcClearLocalPath_EmitsConfigChanged(t *testing.T) {
	ctx, mp, _, _, svc := setupProjectSvc(t)
	got := registerConfigChangeSpy(t)
	mp.EXPECT().Find(ctx, int64(21)).Return(&project_entity.Project{ID: 21, Path: "/Users/x/code/hub"}, nil)
	mp.EXPECT().Update(ctx, gomock.Any()).Return(nil)

	_, err := svc.ClearLocalPath(ctx, 21)

	require.NoError(t, err)
	assert.Equal(t, [][]string{{syncwire.KindProject}}, *got)
}

func TestProjectSvcClearLocalPath_GivenRepoFails_DoesNotEmitConfigChanged(t *testing.T) {
	ctx, mp, _, _, svc := setupProjectSvc(t)
	got := registerConfigChangeSpy(t)
	mp.EXPECT().Find(ctx, int64(21)).Return(&project_entity.Project{ID: 21, Path: "/Users/x/code/hub"}, nil)
	mp.EXPECT().Update(ctx, gomock.Any()).Return(errors.New("db down"))

	_, err := svc.ClearLocalPath(ctx, 21)

	require.Error(t, err)
	assert.Empty(t, *got)
}

// Merge 复用 Delete 的落库部分（deleteRows）收尾；协调者明确它不在「不发」的例外
// 之列，即使它同时也把 issue/project_location 这类不属于五类资源的引用改挂。
func TestProjectSvcMerge_EmitsConfigChanged(t *testing.T) {
	ctx, m, svc := setupMergeTest(t)
	got := registerConfigChangeSpy(t)

	target := &project_entity.Project{
		ID: 2, Name: "agentre-hub", LocalPathMissing: true, Createtime: 200,
		SyncMeta: syncmeta_entity.SyncMeta{SyncID: "sync-2", SyncAccountID: 7},
	}
	source := &project_entity.Project{
		ID: 1, Name: "agentre-hub (local)", Path: "/Users/me/Code/agentre-hub", Createtime: 100,
	}
	m.project.EXPECT().Find(ctx, int64(1)).Return(source, nil)
	m.project.EXPECT().Find(ctx, int64(2)).Return(target, nil)
	m.project.EXPECT().Update(ctx, gomock.Any()).Return(nil)
	m.session.EXPECT().ReassignProject(ctx, int64(1), int64(2)).Return(nil)
	m.pa.EXPECT().ListByProject(ctx, int64(2)).Return(nil, nil)
	m.pa.EXPECT().ListByProject(ctx, int64(1)).Return(nil, nil)
	m.project.EXPECT().ListByParent(ctx, int64(1)).Return(nil, nil)
	m.project.EXPECT().ReassignParent(ctx, int64(1), int64(2)).Return(nil)
	m.issue.EXPECT().ReassignProject(ctx, int64(1), int64(2)).Return(nil)
	m.location.EXPECT().ListByProject(ctx, int64(1)).Return(nil, nil)
	m.location.EXPECT().ReassignProject(ctx, int64(1), int64(2)).Return(nil)
	expectCleanDelete(ctx, m, 1, source)

	_, err := svc.Merge(ctx, &project_svc.MergeProjectsRequest{SourceID: 1, TargetID: 2})

	require.NoError(t, err)
	assert.Equal(t, [][]string{{syncwire.KindProject}}, *got)
}

func TestProjectSvcMerge_GivenTxFails_DoesNotEmitConfigChanged(t *testing.T) {
	ctx, m, svc := setupMergeTest(t)
	got := registerConfigChangeSpy(t)

	target := &project_entity.Project{ID: 2, Name: "agentre-hub", LocalPathMissing: true, Createtime: 200}
	source := &project_entity.Project{ID: 1, Name: "agentre-hub (local)", Path: "/Users/me/Code/agentre-hub", Createtime: 100}
	m.project.EXPECT().Find(ctx, int64(1)).Return(source, nil)
	m.project.EXPECT().Find(ctx, int64(2)).Return(target, nil)
	m.project.EXPECT().Update(ctx, gomock.Any()).Return(errors.New("db down"))

	_, err := svc.Merge(ctx, &project_svc.MergeProjectsRequest{SourceID: 1, TargetID: 2})

	require.Error(t, err)
	assert.Empty(t, *got)
}
