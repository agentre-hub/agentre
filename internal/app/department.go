package app

import (
	"github.com/agentre-hub/agentre/internal/service/department_svc"
)

// LoadOrg 聚合返回部门 + Agent 全量，给前端组织架构页首屏使用。
func (a *App) LoadOrg() (*department_svc.LoadOrgResponse, error) {
	return department_svc.Department().Load(a.ctx, &department_svc.LoadOrgRequest{})
}

// CreateDepartment 新建部门。
func (a *App) CreateDepartment(req *department_svc.CreateDepartmentRequest) (*department_svc.CreateDepartmentResponse, error) {
	return department_svc.Department().Create(a.ctx, req)
}

// UpdateDepartment 更新部门基本信息（不含 parent_id）。
func (a *App) UpdateDepartment(req *department_svc.UpdateDepartmentRequest) (*department_svc.UpdateDepartmentResponse, error) {
	return department_svc.Department().Update(a.ctx, req)
}

// MoveDepartment 改父部门 + 同级排序。
func (a *App) MoveDepartment(req *department_svc.MoveDepartmentRequest) (*department_svc.MoveDepartmentResponse, error) {
	return department_svc.Department().Move(a.ctx, req)
}

// DeleteDepartment 软删部门。Strategy: reparent | cascade。
func (a *App) DeleteDepartment(req *department_svc.DeleteDepartmentRequest) (*department_svc.DeleteDepartmentResponse, error) {
	return department_svc.Department().Delete(a.ctx, req)
}

// ReorderDepartments 同级密集重排（orderedIds 为该父级下完整集合）。
func (a *App) ReorderDepartments(req *department_svc.ReorderDepartmentsRequest) error {
	return department_svc.Department().Reorder(a.ctx, req)
}
