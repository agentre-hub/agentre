package project_svc_test

import (
	"context"
	"testing"

	"github.com/cago-frame/cago/pkg/consts"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
	"gorm.io/gorm"

	"github.com/agentre-hub/agentre/internal/model/entity/chat_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/issue_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/project_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/project_location_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/syncmeta_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/syncqueue_entity"
	"github.com/agentre-hub/agentre/internal/repository/chat_repo"
	"github.com/agentre-hub/agentre/internal/repository/issue_repo"
	"github.com/agentre-hub/agentre/internal/repository/project_location_repo"
	"github.com/agentre-hub/agentre/internal/repository/project_repo"
	"github.com/agentre-hub/agentre/internal/repository/project_repo/mock_project_repo"
	"github.com/agentre-hub/agentre/internal/repository/syncqueue_repo"
	"github.com/agentre-hub/agentre/internal/service/project_svc"
)

// 本文件锁住 R11a 的最后一句：**合并后不允许留下任何指向已消失项目的引用**。
//
// 用手写的内存假仓储而不是 gomock：要表达的正是「列表查询看不见的那些行」——
// 软删的会话 / issue / 路径记录，以及被 nonSubagentScope 排除的子 agent 委派会话。
// 这些行在 ListByProject / List 里天然不出现，只有让假仓储持有整张表，才断言得了
// 「合并之后一行都不再指向 drop」。

type mergeSessionTable struct {
	chat_repo.SessionRepo // 合并只该碰下面这几个方法；碰到别的直接 nil panic
	rows                  []*chat_entity.Session
}

// ListByProject 与真实仓储同判据：只出活跃、非子 agent 委派的行。
func (f *mergeSessionTable) ListByProject(_ context.Context, projectID int64) ([]*chat_entity.Session, error) {
	out := make([]*chat_entity.Session, 0, len(f.rows))
	for _, s := range f.rows {
		if s.ProjectID == projectID && s.Status == consts.ACTIVE && s.Purpose != chat_entity.SessionPurposeSubagent {
			out = append(out, s)
		}
	}
	return out, nil
}

func (f *mergeSessionTable) Update(context.Context, *chat_entity.Session) error { return nil }

func (f *mergeSessionTable) CountActiveByProject(context.Context, int64, []string) (int64, error) {
	return 0, nil
}

func (f *mergeSessionTable) ReassignProject(_ context.Context, fromProjectID, toProjectID int64) error {
	for _, s := range f.rows {
		if s.ProjectID == fromProjectID {
			s.ProjectID = toProjectID
		}
	}
	return nil
}

type mergeIssueTable struct {
	issue_repo.IssueRepo
	rows []*issue_entity.Issue
}

func (f *mergeIssueTable) List(_ context.Context, filter issue_repo.ListFilter) ([]*issue_entity.Issue, error) {
	out := make([]*issue_entity.Issue, 0, len(f.rows))
	for _, i := range f.rows {
		if i.ProjectID == filter.ProjectID && i.Status == consts.ACTIVE {
			out = append(out, i)
		}
	}
	return out, nil
}

func (f *mergeIssueTable) Update(context.Context, *issue_entity.Issue) error { return nil }

func (f *mergeIssueTable) ReassignProject(_ context.Context, fromProjectID, toProjectID int64) error {
	for _, i := range f.rows {
		if i.ProjectID == fromProjectID {
			i.ProjectID = toProjectID
		}
	}
	return nil
}

type mergeLocationTable struct {
	project_location_repo.ProjectLocationRepo
	rows []*project_location_entity.ProjectLocation
}

func (f *mergeLocationTable) ListByProject(_ context.Context, projectID int64) ([]*project_location_entity.ProjectLocation, error) {
	out := make([]*project_location_entity.ProjectLocation, 0, len(f.rows))
	for _, l := range f.rows {
		if l.ProjectID == projectID && l.Status == consts.ACTIVE {
			out = append(out, l)
		}
	}
	return out, nil
}

func (f *mergeLocationTable) FindByProjectAndFingerprint(
	_ context.Context, projectID int64, fingerprint string,
) (*project_location_entity.ProjectLocation, error) {
	for _, l := range f.rows {
		if l.ProjectID == projectID && l.DeviceFingerprint == fingerprint && l.Status == consts.ACTIVE {
			return l, nil
		}
	}
	return nil, gorm.ErrRecordNotFound
}

func (f *mergeLocationTable) Update(context.Context, *project_location_entity.ProjectLocation) error {
	return nil
}

func (f *mergeLocationTable) Delete(_ context.Context, id int64) error {
	for _, l := range f.rows {
		if l.ID == id {
			l.Status = consts.DELETE
		}
	}
	return nil
}

func (f *mergeLocationTable) ReassignProject(_ context.Context, fromProjectID, toProjectID int64) error {
	for _, l := range f.rows {
		if l.ProjectID == fromProjectID {
			l.ProjectID = toProjectID
		}
	}
	return nil
}

// recordingLostChange 收下 R5「没能同步的改动」的写入。
type recordingLostChange struct {
	syncqueue_repo.LostChangeRepo
	rows []*syncqueue_entity.LostChange
}

func (r *recordingLostChange) Create(_ context.Context, row *syncqueue_entity.LostChange) error {
	r.rows = append(r.rows, row)
	return nil
}

func TestProjectSvcMerge_GivenHiddenReferences_ThenNothingStillPointsAtTheDroppedProject(t *testing.T) {
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)
	projectRepo := mock_project_repo.NewMockProjectRepo(ctrl)
	agentRepo := mock_project_repo.NewMockProjectAgentRepo(ctrl)
	sessions := &mergeSessionTable{rows: []*chat_entity.Session{
		{ID: 1, ProjectID: 41, Status: consts.ACTIVE},
		// 软删的会话：ListByProject 看不见，但它的 project_id 照样指着 41。
		{ID: 2, ProjectID: 41, Status: consts.DELETE},
		// 子 agent 委派会话：被 nonSubagentScope 排除，同样看不见。
		{ID: 3, ProjectID: 41, Status: consts.ACTIVE, Purpose: chat_entity.SessionPurposeSubagent},
	}}
	issues := &mergeIssueTable{rows: []*issue_entity.Issue{
		{ID: 11, ProjectID: 41, Status: consts.ACTIVE},
		{ID: 12, ProjectID: 41, Status: consts.DELETE},
	}}
	locations := &mergeLocationTable{rows: []*project_location_entity.ProjectLocation{
		{ID: 21, ProjectID: 41, DeviceFingerprint: "fp-a", Path: "/a", Status: consts.ACTIVE},
		{ID: 22, ProjectID: 41, DeviceFingerprint: "fp-b", Path: "/b", Status: consts.DELETE},
	}}
	lost := &recordingLostChange{}

	project_repo.RegisterProject(projectRepo)
	project_repo.RegisterProjectAgent(agentRepo)
	issue_repo.RegisterIssue(issues)
	project_location_repo.RegisterProjectLocation(locations)
	syncqueue_repo.RegisterLostChange(lost)

	ctx := context.Background()
	keep := &project_entity.Project{ID: 40, Createtime: 100, SyncMeta: syncmeta_entity.SyncMeta{SyncID: "sync-40", SyncAccountID: 3}}
	drop := &project_entity.Project{ID: 41, Createtime: 50}
	projectRepo.EXPECT().Find(ctx, int64(40)).Return(keep, nil)
	projectRepo.EXPECT().Find(ctx, int64(41)).Return(drop, nil)
	projectRepo.EXPECT().Update(ctx, gomock.Any()).Return(nil)
	agentRepo.EXPECT().ListByProject(ctx, int64(40)).Return(nil, nil)
	agentRepo.EXPECT().ListByProject(ctx, int64(41)).Return(nil, nil)
	projectRepo.EXPECT().ListByParent(ctx, int64(41)).Return(nil, nil)
	// R11a 点名的第五类引用：软删的子项目 ListByParent 同样看不见，它的 parent_id
	// 还指着 41。可见的那些走上面的 ListByParent + Update（要发同步通知），剩下的
	// 由这一次改挂扫干净。
	projectRepo.EXPECT().ReassignParent(ctx, int64(41), int64(40)).Return(nil)
	projectRepo.EXPECT().Find(ctx, int64(41)).Return(drop, nil)
	projectRepo.EXPECT().HasActiveChildren(ctx, int64(41)).Return(false, nil)
	projectRepo.EXPECT().Delete(ctx, int64(41)).Return(nil)

	_, err := project_svc.New(project_svc.WithSessionPort(sessions)).Merge(ctx, &project_svc.MergeProjectsRequest{SourceID: 40, TargetID: 41})
	require.NoError(t, err)

	for _, s := range sessions.rows {
		assert.NotEqual(t, int64(41), s.ProjectID, "会话 %d 仍指向已消失的项目", s.ID)
	}
	for _, i := range issues.rows {
		assert.NotEqual(t, int64(41), i.ProjectID, "issue %d 仍指向已消失的项目", i.ID)
	}
	for _, l := range locations.rows {
		assert.NotEqual(t, int64(41), l.ProjectID, "路径记录 %d 仍指向已消失的项目", l.ID)
	}
}

// R4b / R11a：两边对同一台 agentred 各有一行时，落败的那一份要按 R5 进「没能同步
// 的改动」列表——今天它被直接删掉，用户再也看不到那条路径。
func TestProjectSvcMerge_GivenLocationsCollide_ThenLoserIsRecordedAsALostChange(t *testing.T) {
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)
	projectRepo := mock_project_repo.NewMockProjectRepo(ctrl)
	agentRepo := mock_project_repo.NewMockProjectAgentRepo(ctrl)
	sessions := &mergeSessionTable{}
	issues := &mergeIssueTable{}
	locations := &mergeLocationTable{rows: []*project_location_entity.ProjectLocation{
		{
			ID: 60, ProjectID: 50, DeviceFingerprint: "fp-1", Path: "/winner", Status: consts.ACTIVE,
			SyncMeta: syncmeta_entity.SyncMeta{SyncID: "loc-win", SyncAccountID: 3, SyncVersion: 9},
		},
		{
			ID: 61, ProjectID: 51, DeviceFingerprint: "fp-1", Path: "/loser", Status: consts.ACTIVE,
			SyncMeta: syncmeta_entity.SyncMeta{SyncID: "loc-lose", SyncAccountID: 3, SyncVersion: 4},
		},
	}}
	lost := &recordingLostChange{}

	project_repo.RegisterProject(projectRepo)
	project_repo.RegisterProjectAgent(agentRepo)
	issue_repo.RegisterIssue(issues)
	project_location_repo.RegisterProjectLocation(locations)
	syncqueue_repo.RegisterLostChange(lost)

	ctx := context.Background()
	keep := &project_entity.Project{ID: 50, Createtime: 100, SyncMeta: syncmeta_entity.SyncMeta{SyncID: "sync-50", SyncAccountID: 3}}
	drop := &project_entity.Project{ID: 51, Createtime: 50}
	projectRepo.EXPECT().Find(ctx, int64(50)).Return(keep, nil)
	projectRepo.EXPECT().Find(ctx, int64(51)).Return(drop, nil)
	projectRepo.EXPECT().Update(ctx, gomock.Any()).Return(nil)
	agentRepo.EXPECT().ListByProject(ctx, int64(50)).Return(nil, nil)
	agentRepo.EXPECT().ListByProject(ctx, int64(51)).Return(nil, nil)
	projectRepo.EXPECT().ListByParent(ctx, int64(51)).Return(nil, nil)
	projectRepo.EXPECT().ReassignParent(ctx, int64(51), int64(50)).Return(nil)
	projectRepo.EXPECT().Find(ctx, int64(51)).Return(drop, nil)
	projectRepo.EXPECT().HasActiveChildren(ctx, int64(51)).Return(false, nil)
	projectRepo.EXPECT().Delete(ctx, int64(51)).Return(nil)

	_, err := project_svc.New(project_svc.WithSessionPort(sessions)).Merge(ctx, &project_svc.MergeProjectsRequest{SourceID: 50, TargetID: 51})
	require.NoError(t, err)

	require.Len(t, lost.rows, 1, "落败的那一行必须留下一条可追回的记录")
	got := lost.rows[0]
	assert.Equal(t, "project_location", got.EntityType)
	assert.Equal(t, "loc-lose", got.EntitySyncID)
	assert.Equal(t, syncqueue_entity.ReasonOverwritten, got.Reason)
	assert.Equal(t, int64(3), got.SyncAccountID)
	assert.Equal(t, int64(4), got.BaseVersion)
	assert.Equal(t, "sync-50", got.ProjectSyncID, "恢复要落回保留下来的那个项目")
	assert.Equal(t, "fp-1", got.AgentredFingerprint)
	assert.Contains(t, got.PayloadJSON, "/loser")
	assert.NotZero(t, got.OccurredAt)
}
