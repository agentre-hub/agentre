package department_svc

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"go.uber.org/mock/gomock"

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
	assert.Equal(t, [][]string{{syncwire.KindDepartment}, {syncwire.KindDepartment}}, *got)
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

// Delete 内部走 db.Ctx(ctx).Transaction(...)（department.go 的 Delete，非注入式
// TxRunner），这在只装配 mockgen repo、不连库的服务单测里会在拿到真实 *gorm.DB 之前
// 就 panic——这不是本次改动引入的缺口：改动前 Delete 就没有任何成功路径的单测
// （department_test.go 里只有下面这条 CEO/系统豁免式的早退错误路径）。NotifyConfigChanged
// 加在与既有 sync_svc.NotifyDelete 完全相同的三处调用点上（department.go:520-529），
// 成功路径的证据是代码位置对照，不是新跑通的测试；重构 Delete 用注入式 TxRunner
// 让它可测，是比这次「补 config:changed」大得多的改动，不在本任务范围内。
func TestDeleteDepartment_GivenDepartmentNotFound_DoesNotEmitConfigChanged(t *testing.T) {
	ctx, deptMock, _, svc := setupSvc(t)
	got := registerConfigChangeSpy(t)

	deptMock.EXPECT().Find(gomock.Any(), int64(404)).Return(nil, nil)

	_, err := svc.Delete(ctx, &DeleteDepartmentRequest{ID: 404})

	assert.Error(t, err)
	assert.Empty(t, *got)
}
