package department_svc

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"go.uber.org/mock/gomock"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/department_entity"
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

func TestCreateDepartment_EmitsConfigChanged(t *testing.T) {
	ctx, deptMock, _, svc := setupSvc(t)
	got := registerConfigChangeSpy(t)

	deptMock.EXPECT().FindByName(gomock.Any(), "工程部", int64(0)).Return(nil, nil)
	deptMock.EXPECT().NextSortOrder(gomock.Any(), int64(0)).Return(1, nil)
	deptMock.EXPECT().Create(gomock.Any(), gomock.Any()).Return(nil)

	_, err := svc.Create(ctx, &CreateDepartmentRequest{Name: "工程部", AccentColor: "agent-2"})

	assert.NoError(t, err)
	assert.Equal(t, [][]string{{syncwire.KindDepartment}}, *got)
}

func TestCreateDepartment_GivenRepoFails_DoesNotEmitConfigChanged(t *testing.T) {
	ctx, deptMock, _, svc := setupSvc(t)
	got := registerConfigChangeSpy(t)

	deptMock.EXPECT().FindByName(gomock.Any(), "工程部", int64(0)).Return(nil, nil)
	deptMock.EXPECT().NextSortOrder(gomock.Any(), int64(0)).Return(1, nil)
	deptMock.EXPECT().Create(gomock.Any(), gomock.Any()).Return(errors.New("db down"))

	_, err := svc.Create(ctx, &CreateDepartmentRequest{Name: "工程部", AccentColor: "agent-2"})

	assert.Error(t, err)
	assert.Empty(t, *got)
}

func TestUpdateDepartment_EmitsConfigChanged(t *testing.T) {
	ctx, deptMock, _, svc := setupSvc(t)
	got := registerConfigChangeSpy(t)

	existing := &department_entity.Department{ID: 3, Name: "old", Status: 1}
	deptMock.EXPECT().Find(gomock.Any(), int64(3)).Return(existing, nil)
	deptMock.EXPECT().FindByName(gomock.Any(), "工程部", int64(0)).Return(nil, nil)
	deptMock.EXPECT().Update(gomock.Any(), gomock.Any()).Return(nil)

	_, err := svc.Update(ctx, &UpdateDepartmentRequest{ID: 3, Name: "工程部", AccentColor: "agent-2"})

	assert.NoError(t, err)
	assert.Equal(t, [][]string{{syncwire.KindDepartment}}, *got)
}

func TestMoveDepartment_EmitsConfigChanged(t *testing.T) {
	ctx, deptMock, _, svc := setupSvc(t)
	got := registerConfigChangeSpy(t)

	deptMock.EXPECT().Find(gomock.Any(), int64(3)).Return(&department_entity.Department{ID: 3, ParentID: 1, Status: 1}, nil)
	deptMock.EXPECT().Find(gomock.Any(), int64(2)).Return(&department_entity.Department{ID: 2, ParentID: 0, Status: 1}, nil)
	deptMock.EXPECT().List(gomock.Any()).Return([]*department_entity.Department{
		{ID: 1, ParentID: 0}, {ID: 2, ParentID: 0}, {ID: 3, ParentID: 1},
	}, nil)
	deptMock.EXPECT().NextSortOrder(gomock.Any(), int64(2)).Return(1, nil)
	deptMock.EXPECT().Update(gomock.Any(), gomock.Any()).Return(nil)

	_, err := svc.Move(ctx, &MoveDepartmentRequest{ID: 3, NewParentID: 2})

	assert.NoError(t, err)
	assert.Equal(t, [][]string{{syncwire.KindDepartment}}, *got)
}

func TestMoveDepartment_GivenRepoFails_DoesNotEmitConfigChanged(t *testing.T) {
	ctx, deptMock, _, svc := setupSvc(t)
	got := registerConfigChangeSpy(t)

	deptMock.EXPECT().Find(gomock.Any(), int64(3)).Return(&department_entity.Department{ID: 3, ParentID: 1, Status: 1}, nil)
	deptMock.EXPECT().Find(gomock.Any(), int64(2)).Return(&department_entity.Department{ID: 2, ParentID: 0, Status: 1}, nil)
	deptMock.EXPECT().List(gomock.Any()).Return([]*department_entity.Department{
		{ID: 1, ParentID: 0}, {ID: 2, ParentID: 0}, {ID: 3, ParentID: 1},
	}, nil)
	deptMock.EXPECT().NextSortOrder(gomock.Any(), int64(2)).Return(1, nil)
	deptMock.EXPECT().Update(gomock.Any(), gomock.Any()).Return(errors.New("db down"))

	_, err := svc.Move(ctx, &MoveDepartmentRequest{ID: 3, NewParentID: 2})

	assert.Error(t, err)
	assert.Empty(t, *got)
}

func TestReorderDepartments_EmitsConfigChanged(t *testing.T) {
	ctx, deptMock, _, svc := setupSvc(t)
	got := registerConfigChangeSpy(t)

	deptMock.EXPECT().ListByParent(gomock.Any(), int64(0)).Return([]*department_entity.Department{
		{ID: 1, ParentID: 0}, {ID: 2, ParentID: 0},
	}, nil)
	deptMock.EXPECT().ReorderSiblings(gomock.Any(), int64(0), []int64{2, 1}).Return(nil)

	err := svc.Reorder(ctx, &ReorderDepartmentsRequest{ParentID: 0, OrderedIDs: []int64{2, 1}})

	assert.NoError(t, err)
	assert.Equal(t, [][]string{{syncwire.KindDepartment}}, *got, "一次重排只发一次，不按兄弟个数刷 N 遍")
}

func TestReorderDepartments_GivenRepoFails_DoesNotEmitConfigChanged(t *testing.T) {
	ctx, deptMock, _, svc := setupSvc(t)
	got := registerConfigChangeSpy(t)

	deptMock.EXPECT().ListByParent(gomock.Any(), int64(0)).Return([]*department_entity.Department{
		{ID: 1, ParentID: 0}, {ID: 2, ParentID: 0},
	}, nil)
	deptMock.EXPECT().ReorderSiblings(gomock.Any(), int64(0), []int64{2, 1}).Return(errors.New("db down"))

	err := svc.Reorder(ctx, &ReorderDepartmentsRequest{ParentID: 0, OrderedIDs: []int64{2, 1}})

	assert.Error(t, err)
	assert.Empty(t, *got)
}

// 删部门一次写动了部门与 Agent 两类：只发一次、带齐两类，不按搬走 / 删掉的条数刷 N 遍。
func TestDeleteDepartment_GivenReparent_EmitsConfigChangedOnce(t *testing.T) {
	ctx, deptMock, agentMock, svc := setupSvc(t)
	got := registerConfigChangeSpy(t)

	deptMock.EXPECT().Find(gomock.Any(), int64(3)).Return(&department_entity.Department{ID: 3, ParentID: 1, Status: 1}, nil)
	deptMock.EXPECT().ReparentChildren(gomock.Any(), int64(3), int64(1)).Return(nil)
	agentMock.EXPECT().ListByDepartment(gomock.Any(), int64(3)).Return([]*agent_entity.Agent{{ID: 7}, {ID: 8}}, nil)
	agentMock.EXPECT().UpdatePlacement(gomock.Any(), gomock.Any(), int64(1), int64(0), gomock.Any()).Return(nil).Times(2)
	deptMock.EXPECT().Delete(gomock.Any(), int64(3)).Return(nil)

	_, err := svc.Delete(ctx, &DeleteDepartmentRequest{ID: 3, Strategy: StrategyReparent})

	assert.NoError(t, err)
	assert.Equal(t, [][]string{{syncwire.KindDepartment, syncwire.KindAgent}}, *got)
}

func TestDeleteDepartment_GivenCascade_EmitsConfigChangedOnce(t *testing.T) {
	ctx, deptMock, agentMock, svc := setupSvc(t)
	got := registerConfigChangeSpy(t)

	deptMock.EXPECT().Find(gomock.Any(), int64(3)).Return(&department_entity.Department{ID: 3, ParentID: 1, Status: 1}, nil)
	deptMock.EXPECT().List(gomock.Any()).Return([]*department_entity.Department{
		{ID: 1}, {ID: 3, ParentID: 1}, {ID: 4, ParentID: 3},
	}, nil)
	agentMock.EXPECT().List(gomock.Any()).Return([]*agent_entity.Agent{
		{ID: 7, DepartmentID: 3}, {ID: 8, DepartmentID: 4}, {ID: 9, DepartmentID: 1},
	}, nil)
	agentMock.EXPECT().Delete(gomock.Any(), int64(7)).Return(nil)
	agentMock.EXPECT().Delete(gomock.Any(), int64(8)).Return(nil)
	deptMock.EXPECT().Delete(gomock.Any(), int64(3)).Return(nil)
	deptMock.EXPECT().Delete(gomock.Any(), int64(4)).Return(nil)

	_, err := svc.Delete(ctx, &DeleteDepartmentRequest{ID: 3, Strategy: StrategyCascade})

	assert.NoError(t, err)
	assert.Equal(t, [][]string{{syncwire.KindDepartment, syncwire.KindAgent}}, *got)
}

func TestDeleteDepartment_GivenTxFails_DoesNotEmitConfigChanged(t *testing.T) {
	ctx, deptMock, agentMock, svc := setupSvc(t)
	got := registerConfigChangeSpy(t)

	deptMock.EXPECT().Find(gomock.Any(), int64(3)).Return(&department_entity.Department{ID: 3, ParentID: 1, Status: 1}, nil)
	deptMock.EXPECT().ReparentChildren(gomock.Any(), int64(3), int64(1)).Return(nil)
	agentMock.EXPECT().ListByDepartment(gomock.Any(), int64(3)).Return(nil, nil)
	deptMock.EXPECT().Delete(gomock.Any(), int64(3)).Return(errors.New("db down"))

	_, err := svc.Delete(ctx, &DeleteDepartmentRequest{ID: 3, Strategy: StrategyReparent})

	assert.Error(t, err)
	assert.Empty(t, *got)
}

func TestDeleteDepartment_GivenDepartmentNotFound_DoesNotEmitConfigChanged(t *testing.T) {
	ctx, deptMock, _, svc := setupSvc(t)
	got := registerConfigChangeSpy(t)

	deptMock.EXPECT().Find(gomock.Any(), int64(404)).Return(nil, nil)

	_, err := svc.Delete(ctx, &DeleteDepartmentRequest{ID: 404})

	assert.Error(t, err)
	assert.Empty(t, *got)
}
