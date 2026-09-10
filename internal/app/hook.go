package app

import (
	"github.com/agentre-hub/agentre/internal/service/hook_svc"
)

// LoadHooks 返回脚本 Hook 列表与产出事件日志。
func (a *App) LoadHooks(req *hook_svc.LoadHooksRequest) (*hook_svc.LoadHooksResponse, error) {
	return hook_svc.Hook().Load(a.ctx, req)
}

// CreateHook 新建脚本 Hook。
func (a *App) CreateHook(req *hook_svc.CreateHookRequest) (*hook_svc.HookItem, error) {
	return hook_svc.Hook().CreateHook(a.ctx, req)
}

// UpdateHook 更新脚本 Hook。
func (a *App) UpdateHook(req *hook_svc.UpdateHookRequest) (*hook_svc.HookItem, error) {
	return hook_svc.Hook().UpdateHook(a.ctx, req)
}

// DeleteHook 软删除脚本 Hook。
func (a *App) DeleteHook(id int64) error {
	return hook_svc.Hook().DeleteHook(a.ctx, id)
}

// ToggleHook 启用/停用脚本 Hook。
func (a *App) ToggleHook(id int64, enabled bool) (*hook_svc.HookItem, error) {
	return hook_svc.Hook().ToggleHook(a.ctx, id, enabled)
}

// RunHook 立即执行一次(dryRun=true 为试运行,不落库)。
func (a *App) RunHook(req *hook_svc.RunHookRequest) (*hook_svc.RunHookResult, error) {
	return hook_svc.Hook().RunHook(a.ctx, req)
}

// ProbeInterpreters 返回本平台可用的解释器及其安装情况(供 Hook 表单下拉)。
func (a *App) ProbeInterpreters() ([]hook_svc.InterpreterOption, error) {
	return hook_svc.Hook().ProbeInterpreters(a.ctx)
}
