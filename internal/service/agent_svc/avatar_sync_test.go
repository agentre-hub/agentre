package agent_svc

import (
	"context"
	"errors"
	"testing"

	"github.com/cago-frame/cago/pkg/consts"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/syncmeta_entity"
	"github.com/agentre-hub/agentre/internal/pkg/syncwire"
	"github.com/agentre-hub/agentre/internal/repository/agent_repo"
	"github.com/agentre-hub/agentre/internal/repository/agent_repo/mock_agent_repo"
	"github.com/agentre-hub/agentre/internal/service/sync_svc"
)

// recordingSync 记下域服务交出来的每一条改动（与 project_svc/sync_notify_test.go
// 同一手法）。
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
	// 同步已装配 → execTargetSnapshot 会真的去查执行目标行，装一个空列表桩。
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)
	et := mock_agent_repo.NewMockAgentExecTargetRepo(ctrl)
	et.EXPECT().ListByAgent(gomock.Any(), gomock.Any()).Return(nil, nil).AnyTimes()
	agent_repo.RegisterAgentExecTarget(et)
	return rec
}

// TestUpdateAgent_GivenExecTargetReadFails_DoesNotTombstoneSurvivingTargets
// 「写入后重读执行目标」失败时，绝不能把还活着的档全部报成删除。
//
// execTargetSnapshot 读失败时只记日志并返回 nil，与「一档都没有」在类型上不可区分；
// notifyDroppedExecTargets 拿着 after=nil 去和 before 求差，就会把 before 里的每一
// 档都当成「被这次写入挤掉了」。可是 UpdateWithTargets 已经提交成功，这些行在本机
// 好好活着——墓碑一上行，别的端就会把三档全删掉，而本机还是三档。一次 SQLite
// BUSY 就能触发（本仓有据可查的既有现象），且不可自愈。
func TestUpdateAgent_GivenExecTargetReadFails_DoesNotTombstoneSurvivingTargets(t *testing.T) {
	ctx, agentMock, _, backendMock, svc := setupSvc(t)
	rec := &recordingSync{}
	sync_svc.SetDefault(rec)
	t.Cleanup(func() { sync_svc.SetDefault(nil) })

	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)

	agentMock.EXPECT().Find(gomock.Any(), int64(42)).Return(&agent_entity.Agent{
		ID: 42, Name: "Eva", Status: consts.ACTIVE, AvatarColor: "agent-2",
		DepartmentID: 2, AgentBackendID: 5, PromptJSON: "[]",
		SyncMeta: syncmeta_entity.SyncMeta{SyncID: "agent-42"},
	}, nil)
	backendMock.EXPECT().Find(gomock.Any(), int64(5)).Return(activeBackend(5), nil).AnyTimes()
	backendMock.EXPECT().Find(gomock.Any(), int64(6)).Return(activeBackend(6), nil).AnyTimes()
	agentMock.EXPECT().UpdateWithTargets(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)

	before := []*agent_entity.AgentExecTarget{
		{ID: 1, AgentID: 42, AgentBackendID: 5, SortOrder: 0, SyncMeta: syncmeta_entity.SyncMeta{SyncID: "t-1"}},
		{ID: 2, AgentID: 42, AgentBackendID: 6, SortOrder: 1, SyncMeta: syncmeta_entity.SyncMeta{SyncID: "t-2"}},
	}
	targetMock := mock_agent_repo.NewMockAgentExecTargetRepo(ctrl)
	gomock.InOrder(
		// 写之前读成功：两档都在。
		targetMock.EXPECT().ListByAgent(gomock.Any(), int64(42)).Return(before, nil),
		// 写之后重读失败（DB 忙）。
		targetMock.EXPECT().ListByAgent(gomock.Any(), int64(42)).Return(nil, errors.New("database is locked")),
	)
	agent_repo.RegisterAgentExecTarget(targetMock)

	_, err := svc.Update(ctx, &UpdateAgentRequest{
		ID: 42, Name: "Eva", AvatarColor: "agent-2",
		ExecTargets: []ExecTargetInputDTO{{AgentBackendID: 5}, {AgentBackendID: 6}},
	})
	require.NoError(t, err, "重读失败不该让这次写入报错——它已经提交了")

	for _, ch := range rec.changes {
		assert.NotEqual(t, syncwire.KindAgentExecTarget+"/"+sync_svc.OpDelete, ch.Kind+"/"+ch.Op,
			"重读失败时不得对执行目标落墓碑：本机这些行还活着（多报的档=%s）", ch.Meta.SyncID)
	}
}

func avatarAgent(id int64) *agent_entity.Agent {
	return &agent_entity.Agent{
		ID: id, Name: "Eva",
		SyncMeta: syncmeta_entity.SyncMeta{SyncID: "agent-sync-1"},
	}
}

// TestUploadAvatar_NotifiesSync R16a：「哈希参与内容比较，因此**换头像照常触发
// 同步**」。头像正文按内容哈希单独传，但触发这条同步的仍然是 Agent 行的一次普通
// 修改——不发通知的话，新头像只有等用户碰巧改了这个 Agent 的别的字段才会到对端。
func TestUploadAvatar_NotifiesSync(t *testing.T) {
	ctx, agentMock, _, _, svc := setupSvc(t)
	rec := registerRecordingSync(t)
	agentMock.EXPECT().Find(gomock.Any(), int64(9)).Return(avatarAgent(9), nil)
	agentMock.EXPECT().UpdateAvatar(gomock.Any(), int64(9), gomock.Any(), gomock.Any()).Return(nil)

	_, err := svc.UploadAvatar(ctx, &UploadAvatarRequest{
		ID: 9, DataURL: "data:image/png;base64,iVBORw0KGgo=",
	})
	require.NoError(t, err)

	require.Len(t, rec.changes, 1)
	assert.Equal(t, syncwire.KindAgent, rec.changes[0].Kind)
	assert.Equal(t, sync_svc.OpUpdate, rec.changes[0].Op)
	assert.Equal(t, "agent-sync-1", rec.changes[0].Meta.SyncID)
}

// TestDeleteAvatar_NotifiesSync 同上：清掉自定义头像也是一次内容变化，对端必须
// 跟着退回色块 + 图标呈现。
func TestDeleteAvatar_NotifiesSync(t *testing.T) {
	ctx, agentMock, _, _, svc := setupSvc(t)
	rec := registerRecordingSync(t)
	agentMock.EXPECT().Find(gomock.Any(), int64(9)).Return(avatarAgent(9), nil)
	agentMock.EXPECT().UpdateAvatar(gomock.Any(), int64(9), "", gomock.Any()).Return(nil)

	_, err := svc.DeleteAvatar(ctx, &DeleteAvatarRequest{ID: 9})
	require.NoError(t, err)

	require.Len(t, rec.changes, 1)
	assert.Equal(t, syncwire.KindAgent, rec.changes[0].Kind)
	assert.Equal(t, sync_svc.OpUpdate, rec.changes[0].Op)
}
