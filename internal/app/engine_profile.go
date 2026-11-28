package app

import (
	"github.com/agentre-hub/agentre/internal/service/agent_backend_svc"
)

// 引擎配置（spec 2026-11-25 决策 2）：后端去掉设备与 CLI 路径之后的那份配置。旧的
// *AgentBackend 绑定在执行目标被移除前照常保留。

// ListEngineProfiles 列出全部活的引擎配置（带账号默认标记与直接引用它的成员数）。
func (a *App) ListEngineProfiles() (*agent_backend_svc.ListEngineProfilesResponse, error) {
	res, err := agent_backend_svc.EngineProfile().List(a.ctx, &agent_backend_svc.ListEngineProfilesRequest{})
	return res, codedError(err)
}

// GetEngineProfile 取一份引擎配置；已删除的也取得到（Active=false）。
func (a *App) GetEngineProfile(req *agent_backend_svc.GetEngineProfileRequest) (*agent_backend_svc.GetEngineProfileResponse, error) {
	res, err := agent_backend_svc.EngineProfile().Get(a.ctx, req)
	return res, codedError(err)
}

// CreateEngineProfile 新建引擎配置。名称留空取运行时名；第一份自动成为账号默认。
func (a *App) CreateEngineProfile(req *agent_backend_svc.CreateEngineProfileRequest) (*agent_backend_svc.CreateEngineProfileResponse, error) {
	res, err := agent_backend_svc.EngineProfile().Create(a.ctx, req)
	return res, codedError(err)
}

// UpdateEngineProfile 更新引擎配置。运行时类型不可改。
func (a *App) UpdateEngineProfile(req *agent_backend_svc.UpdateEngineProfileRequest) (*agent_backend_svc.UpdateEngineProfileResponse, error) {
	res, err := agent_backend_svc.EngineProfile().Update(a.ctx, req)
	return res, codedError(err)
}

// ListEngineProfileReferences 直接引用某份配置的成员与部门（删除确认里列出）。
func (a *App) ListEngineProfileReferences(req *agent_backend_svc.ListEngineProfileReferencesRequest) (*agent_backend_svc.EngineProfileReferences, error) {
	res, err := agent_backend_svc.EngineProfile().ListReferences(a.ctx, req)
	return res, codedError(err)
}

// DeleteEngineProfile 删除引擎配置；引用它的成员与部门改为跟随上一级。账号默认不能删。
func (a *App) DeleteEngineProfile(req *agent_backend_svc.DeleteEngineProfileRequest) (*agent_backend_svc.DeleteEngineProfileResponse, error) {
	res, err := agent_backend_svc.EngineProfile().Delete(a.ctx, req)
	return res, codedError(err)
}

// SetAccountDefaultEngineProfile 把一份引擎配置设为账号默认。
func (a *App) SetAccountDefaultEngineProfile(req *agent_backend_svc.SetAccountDefaultEngineProfileRequest) (*agent_backend_svc.SetAccountDefaultEngineProfileResponse, error) {
	res, err := agent_backend_svc.EngineProfile().SetAccountDefault(a.ctx, req)
	return res, codedError(err)
}
