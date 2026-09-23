package agent_svc

import (
	"errors"
	"testing"

	"github.com/cago-frame/cago/pkg/consts"
	"github.com/stretchr/testify/assert"
	"go.uber.org/mock/gomock"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_entity"
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

func TestCreateAgent_EmitsConfigChanged(t *testing.T) {
	ctx, agentMock, deptMock, backendMock, svc := setupSvc(t)
	got := registerConfigChangeSpy(t)

	deptMock.EXPECT().Find(gomock.Any(), int64(2)).Return(activeDept(2), nil)
	backendMock.EXPECT().Find(gomock.Any(), int64(5)).Return(activeBackend(5), nil)
	agentMock.EXPECT().FindByName(gomock.Any(), "Eva").Return(nil, nil)
	agentMock.EXPECT().NextSortOrder(gomock.Any(), int64(2)).Return(1, nil)
	agentMock.EXPECT().Create(gomock.Any(), gomock.Any()).Return(nil)

	_, err := svc.Create(ctx, &CreateAgentRequest{
		Name: "Eva", AvatarColor: "agent-2", AvatarIcon: "sparkles",
		DepartmentID: 2, AgentBackendID: 5, Prompt: []string{"hi"},
	})

	assert.NoError(t, err)
	assert.Equal(t, [][]string{{syncwire.KindAgent}}, *got)
}

func TestCreateAgent_GivenRepoFails_DoesNotEmitConfigChanged(t *testing.T) {
	ctx, agentMock, deptMock, backendMock, svc := setupSvc(t)
	got := registerConfigChangeSpy(t)

	deptMock.EXPECT().Find(gomock.Any(), int64(2)).Return(activeDept(2), nil)
	backendMock.EXPECT().Find(gomock.Any(), int64(5)).Return(activeBackend(5), nil)
	agentMock.EXPECT().FindByName(gomock.Any(), "Eva").Return(nil, nil)
	agentMock.EXPECT().NextSortOrder(gomock.Any(), int64(2)).Return(1, nil)
	agentMock.EXPECT().Create(gomock.Any(), gomock.Any()).Return(errors.New("db down"))

	_, err := svc.Create(ctx, &CreateAgentRequest{
		Name: "Eva", AvatarColor: "agent-2", AvatarIcon: "sparkles",
		DepartmentID: 2, AgentBackendID: 5, Prompt: []string{"hi"},
	})

	assert.Error(t, err)
	assert.Empty(t, *got)
}

func TestUpdateAgent_EmitsConfigChanged(t *testing.T) {
	ctx, agentMock, _, backendMock, svc := setupSvc(t)
	got := registerConfigChangeSpy(t)

	agentMock.EXPECT().Find(gomock.Any(), int64(42)).
		Return(&agent_entity.Agent{
			ID: 42, Name: "Eva", AvatarColor: "agent-2",
			DepartmentID: 2, AgentBackendID: 5, Status: consts.ACTIVE,
			PromptJSON: "[]", SkillsJSON: "[]",
		}, nil)
	backendMock.EXPECT().Find(gomock.Any(), int64(5)).Return(activeBackend(5), nil)
	agentMock.EXPECT().UpdateWithTargets(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)

	_, err := svc.Update(ctx, &UpdateAgentRequest{
		ID: 42, Name: "Eva", AvatarColor: "agent-2", AvatarIcon: "hammer",
		ExecTargets: []ExecTargetInputDTO{{AgentBackendID: 5}},
	})

	assert.NoError(t, err)
	assert.Equal(t, [][]string{{syncwire.KindAgent}}, *got)
}

func TestMoveAgent_EmitsConfigChanged(t *testing.T) {
	ctx, agentMock, deptMock, _, svc := setupSvc(t)
	got := registerConfigChangeSpy(t)

	agentMock.EXPECT().Find(gomock.Any(), int64(42)).
		Return(&agent_entity.Agent{ID: 42, Name: "Eva", DepartmentID: 2, AgentBackendID: 5, Status: consts.ACTIVE}, nil)
	deptMock.EXPECT().Find(gomock.Any(), int64(8)).Return(activeDept(8), nil)
	agentMock.EXPECT().NextSortOrder(gomock.Any(), int64(8)).Return(3, nil)
	agentMock.EXPECT().UpdatePlacement(gomock.Any(), int64(42), int64(8), int64(0), 3).Return(nil)

	_, err := svc.Move(ctx, &MoveAgentRequest{ID: 42, NewDepartmentID: 8})

	assert.NoError(t, err)
	assert.Equal(t, [][]string{{syncwire.KindAgent}}, *got)
}

func TestMoveAgent_GivenRepoFails_DoesNotEmitConfigChanged(t *testing.T) {
	ctx, agentMock, deptMock, _, svc := setupSvc(t)
	got := registerConfigChangeSpy(t)

	agentMock.EXPECT().Find(gomock.Any(), int64(42)).
		Return(&agent_entity.Agent{ID: 42, Name: "Eva", DepartmentID: 2, AgentBackendID: 5, Status: consts.ACTIVE}, nil)
	deptMock.EXPECT().Find(gomock.Any(), int64(8)).Return(activeDept(8), nil)
	agentMock.EXPECT().NextSortOrder(gomock.Any(), int64(8)).Return(3, nil)
	agentMock.EXPECT().UpdatePlacement(gomock.Any(), int64(42), int64(8), int64(0), 3).Return(errors.New("db down"))

	_, err := svc.Move(ctx, &MoveAgentRequest{ID: 42, NewDepartmentID: 8})

	assert.Error(t, err)
	assert.Empty(t, *got)
}

func TestReorderAgents_EmitsConfigChanged(t *testing.T) {
	ctx, agentMock, _, _, svc := setupSvc(t)
	got := registerConfigChangeSpy(t)

	agentMock.EXPECT().ListByDepartment(gomock.Any(), int64(2)).
		Return([]*agent_entity.Agent{{ID: 1}, {ID: 3}}, nil)
	agentMock.EXPECT().ReorderSiblings(gomock.Any(), int64(2), int64(0), []int64{3, 1}).Return(nil)

	err := svc.Reorder(ctx, &ReorderAgentsRequest{DepartmentID: 2, OrderedIDs: []int64{3, 1}})

	assert.NoError(t, err)
	assert.Equal(t, [][]string{{syncwire.KindAgent}, {syncwire.KindAgent}}, *got)
}

func TestReorderAgents_GivenRepoFails_DoesNotEmitConfigChanged(t *testing.T) {
	ctx, agentMock, _, _, svc := setupSvc(t)
	got := registerConfigChangeSpy(t)

	agentMock.EXPECT().ListByDepartment(gomock.Any(), int64(2)).
		Return([]*agent_entity.Agent{{ID: 1}, {ID: 3}}, nil)
	agentMock.EXPECT().ReorderSiblings(gomock.Any(), int64(2), int64(0), []int64{3, 1}).Return(errors.New("db down"))

	err := svc.Reorder(ctx, &ReorderAgentsRequest{DepartmentID: 2, OrderedIDs: []int64{3, 1}})

	assert.Error(t, err)
	assert.Empty(t, *got)
}

func TestAgentSvcSetPinned_EmitsConfigChanged(t *testing.T) {
	ctx, agentMock, _, _, svc := setupSvc(t)
	got := registerConfigChangeSpy(t)

	agentMock.EXPECT().Find(ctx, int64(7)).Return(&agent_entity.Agent{ID: 7, Status: consts.ACTIVE}, nil)
	agentMock.EXPECT().SetPinned(ctx, int64(7), true).Return(nil)

	_, err := svc.SetPinned(ctx, &SetPinnedRequest{ID: 7, Pinned: true})

	assert.NoError(t, err)
	assert.Equal(t, [][]string{{syncwire.KindAgent}}, *got)
}

func TestAgentSvcSetPinned_GivenRepoFails_DoesNotEmitConfigChanged(t *testing.T) {
	ctx, agentMock, _, _, svc := setupSvc(t)
	got := registerConfigChangeSpy(t)

	agentMock.EXPECT().Find(ctx, int64(7)).Return(&agent_entity.Agent{ID: 7, Status: consts.ACTIVE}, nil)
	agentMock.EXPECT().SetPinned(ctx, int64(7), true).Return(errors.New("db down"))

	_, err := svc.SetPinned(ctx, &SetPinnedRequest{ID: 7, Pinned: true})

	assert.Error(t, err)
	assert.Empty(t, *got)
}

func TestUploadAgentAvatar_EmitsConfigChanged(t *testing.T) {
	ctx, agentMock, _, _, svc := setupSvc(t)
	got := registerConfigChangeSpy(t)

	agentMock.EXPECT().Find(gomock.Any(), int64(42)).
		Return(&agent_entity.Agent{ID: 42, Name: "Eva", DepartmentID: 2, AgentBackendID: 5, Status: consts.ACTIVE}, nil)
	agentMock.EXPECT().UpdateAvatar(gomock.Any(), int64(42), pngDataURL, int64(1700000000)).Return(nil)

	_, err := svc.UploadAvatar(ctx, &UploadAvatarRequest{ID: 42, DataURL: pngDataURL})

	assert.NoError(t, err)
	assert.Equal(t, [][]string{{syncwire.KindAgent}}, *got)
}

func TestUploadAgentAvatar_GivenRepoFails_DoesNotEmitConfigChanged(t *testing.T) {
	ctx, agentMock, _, _, svc := setupSvc(t)
	got := registerConfigChangeSpy(t)

	agentMock.EXPECT().Find(gomock.Any(), int64(42)).
		Return(&agent_entity.Agent{ID: 42, Name: "Eva", DepartmentID: 2, AgentBackendID: 5, Status: consts.ACTIVE}, nil)
	agentMock.EXPECT().UpdateAvatar(gomock.Any(), int64(42), pngDataURL, int64(1700000000)).Return(errors.New("db down"))

	_, err := svc.UploadAvatar(ctx, &UploadAvatarRequest{ID: 42, DataURL: pngDataURL})

	assert.Error(t, err)
	assert.Empty(t, *got)
}

func TestDeleteAgentAvatar_EmitsConfigChanged(t *testing.T) {
	ctx, agentMock, _, _, svc := setupSvc(t)
	got := registerConfigChangeSpy(t)

	agentMock.EXPECT().Find(gomock.Any(), int64(42)).
		Return(&agent_entity.Agent{ID: 42, Name: "Eva", AvatarDataURL: pngDataURL, DepartmentID: 2, AgentBackendID: 5, Status: consts.ACTIVE}, nil)
	agentMock.EXPECT().UpdateAvatar(gomock.Any(), int64(42), "", int64(1700000000)).Return(nil)

	_, err := svc.DeleteAvatar(ctx, &DeleteAvatarRequest{ID: 42})

	assert.NoError(t, err)
	assert.Equal(t, [][]string{{syncwire.KindAgent}}, *got)
}

func TestDeleteAgentAvatar_GivenRepoFails_DoesNotEmitConfigChanged(t *testing.T) {
	ctx, agentMock, _, _, svc := setupSvc(t)
	got := registerConfigChangeSpy(t)

	agentMock.EXPECT().Find(gomock.Any(), int64(42)).
		Return(&agent_entity.Agent{ID: 42, Name: "Eva", AvatarDataURL: pngDataURL, DepartmentID: 2, AgentBackendID: 5, Status: consts.ACTIVE}, nil)
	agentMock.EXPECT().UpdateAvatar(gomock.Any(), int64(42), "", int64(1700000000)).Return(errors.New("db down"))

	_, err := svc.DeleteAvatar(ctx, &DeleteAvatarRequest{ID: 42})

	assert.Error(t, err)
	assert.Empty(t, *got)
}

// Delete 走 db.Ctx(ctx).Transaction(...)（agent.go 的 Delete，非注入式 TxRunner），
// 同 department_svc.Delete 的既有限制：只装配 mockgen repo、不连库的服务单测在拿到
// 真实 *gorm.DB 之前就会 panic，而这不是本次改动引入的缺口——改动前 Delete 就没有
// 任何成功路径的单测。NotifyConfigChanged 加在与既有 sync_svc.NotifyDelete 完全
// 相同的调用点上（agent.go:317-318），成功路径的证据是代码位置对照。
func TestDeleteAgent_GivenAgentNotFound_DoesNotEmitConfigChanged(t *testing.T) {
	ctx, agentMock, _, _, svc := setupSvc(t)
	got := registerConfigChangeSpy(t)

	agentMock.EXPECT().Find(gomock.Any(), int64(404)).Return(nil, nil)

	_, err := svc.Delete(ctx, &DeleteAgentRequest{ID: 404})

	assert.Error(t, err)
	assert.Empty(t, *got)
}
