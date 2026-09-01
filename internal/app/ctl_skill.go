package app

import (
	"github.com/agentre-hub/agentre/internal/pkg/ctlskill"
	"github.com/agentre-hub/agentre/internal/service/ctlskill_svc"
)

// GetCtlSkillStatus 报出 ctl 控制通道技能包（Claude Code 插件 + 通用 Agent Skill 目录）
// 两种形态各自的安装态与路径，供设置页渲染。
func (a *App) GetCtlSkillStatus() (*ctlskill.Info, error) {
	return ctlskill_svc.CtlSkill().Status(a.ctx)
}

// InstallCtlSkill 清除「用户已拒绝」标记并执行一次安装。
func (a *App) InstallCtlSkill() (*ctlskill.Info, error) {
	return ctlskill_svc.CtlSkill().Install(a.ctx)
}

// UninstallCtlSkill 卸载两种形态并写下「用户已拒绝」标记，阻止下次启动自动复装。
func (a *App) UninstallCtlSkill() (*ctlskill.Info, error) {
	return ctlskill_svc.CtlSkill().Uninstall(a.ctx)
}
