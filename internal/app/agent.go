package app

import (
	"github.com/agentre-hub/agentre/internal/service/agent_svc"
	"github.com/agentre-hub/agentre/internal/service/skill_svc"
)

// CreateAgent 新建 Agent。
func (a *App) CreateAgent(req *agent_svc.CreateAgentRequest) (*agent_svc.CreateAgentResponse, error) {
	res, err := agent_svc.Agent().Create(a.ctx, req)
	return res, codedError(err)
}

// UpdateAgent 更新 Agent。
func (a *App) UpdateAgent(req *agent_svc.UpdateAgentRequest) (*agent_svc.UpdateAgentResponse, error) {
	res, err := agent_svc.Agent().Update(a.ctx, req)
	return res, codedError(err)
}

// MoveAgent 换部门 + 同级排序。
func (a *App) MoveAgent(req *agent_svc.MoveAgentRequest) (*agent_svc.MoveAgentResponse, error) {
	res, err := agent_svc.Agent().Move(a.ctx, req)
	return res, codedError(err)
}

// ReorderAgents 同级密集重排(orderedIds 为该组完整集合)。
func (a *App) ReorderAgents(req *agent_svc.ReorderAgentsRequest) error {
	return codedError(agent_svc.Agent().Reorder(a.ctx, req))
}

// DeleteAgent 软删 Agent。CEO 拒绝。
func (a *App) DeleteAgent(req *agent_svc.DeleteAgentRequest) (*agent_svc.DeleteAgentResponse, error) {
	res, err := agent_svc.Agent().Delete(a.ctx, req)
	return res, codedError(err)
}

// UploadAgentAvatar 写入 Agent 头像（base64 data URL，≤ 2MB，PNG/JPEG/WEBP）。
func (a *App) UploadAgentAvatar(req *agent_svc.UploadAvatarRequest) (*agent_svc.UploadAvatarResponse, error) {
	res, err := agent_svc.Agent().UploadAvatar(a.ctx, req)
	return res, codedError(err)
}

// DeleteAgentAvatar 清空 Agent 头像，回退到首字母派生。
func (a *App) DeleteAgentAvatar(req *agent_svc.DeleteAvatarRequest) (*agent_svc.DeleteAvatarResponse, error) {
	res, err := agent_svc.Agent().DeleteAvatar(a.ctx, req)
	return res, codedError(err)
}

// SetAgentPinned 置顶/取消置顶某 Agent（侧栏混排列表浮顶）。
func (a *App) SetAgentPinned(req *agent_svc.SetPinnedRequest) (*agent_svc.SetPinnedResponse, error) {
	return agent_svc.Agent().SetPinned(a.ctx, req)
}

// SetAgentMemoryEnabled 打开 / 关闭某 Agent 的记忆。
func (a *App) SetAgentMemoryEnabled(req *agent_svc.SetMemoryEnabledRequest) (*agent_svc.SetMemoryEnabledResponse, error) {
	res, err := agent_svc.Agent().SetMemoryEnabled(a.ctx, req)
	return res, codedError(err)
}

// GetMemberEngine 读成员的执行设置：引用的引擎配置、继承到 / 生效的那份、「在哪跑」约束、
// 技能授权（有效与失效两组）。
func (a *App) GetMemberEngine(req *agent_svc.GetMemberEngineRequest) (*agent_svc.MemberEngineView, error) {
	res, err := agent_svc.Agent().GetMemberEngine(a.ctx, req)
	return res, codedError(err)
}

// SetMemberEngine 设成员引用的引擎配置；0 = 跟随部门。
func (a *App) SetMemberEngine(req *agent_svc.SetMemberEngineRequest) (*agent_svc.MemberEngineView, error) {
	res, err := agent_svc.Agent().SetMemberEngine(a.ctx, req)
	return res, codedError(err)
}

// SetMemberExecConstraint 设成员的「在哪跑」约束（任意设备 / 带标签 / 指定设备）。
func (a *App) SetMemberExecConstraint(req *agent_svc.SetMemberExecConstraintRequest) (*agent_svc.MemberEngineView, error) {
	res, err := agent_svc.Agent().SetMemberExecConstraint(a.ctx, req)
	return res, codedError(err)
}

// SetMemberSkillGrants 整份替换成员的技能授权（按生效运行时记录）。
func (a *App) SetMemberSkillGrants(req *agent_svc.SetMemberSkillGrantsRequest) (*agent_svc.MemberEngineView, error) {
	res, err := agent_svc.Agent().SetMemberSkillGrants(a.ctx, req)
	return res, codedError(err)
}

// ResolveMemberEngine 成员生效的引擎配置，以及它从哪一级继承来。
func (a *App) ResolveMemberEngine(req *agent_svc.ResolveMemberEngineRequest) (*agent_svc.ResolveMemberEngineResponse, error) {
	res, err := agent_svc.Agent().ResolveMemberEngine(a.ctx, req)
	return res, codedError(err)
}

// ListAgentSkillPacks 返回成员可见的技能包目录(推荐 + 发现 + 已授权)。技能授权已挂到成员
// 上、按生效引擎配置的运行时校验,目录不再按执行目标一档一块 —— agentBackendID 只为现有
// 界面的调用形状保留、不再参与取数;新界面用 GetMemberSkillCatalog。
func (a *App) ListAgentSkillPacks(agentID int64, agentBackendID int64, refresh bool) (skill_svc.SkillCatalogDTO, error) {
	res, err := skill_svc.Default().MemberSkillCatalog(a.ctx, agentID, refresh)
	if err != nil {
		return skill_svc.SkillCatalogDTO{}, err
	}
	return skill_svc.SkillCatalogDTO{Packs: res.Packs}, nil
}

// ListAgentSkillCommands 返回当前 agent 在 cwd 中可调用的 Skill 命令目录。
func (a *App) ListAgentSkillCommands(agentID int64, cwd string) (skill_svc.SkillCommandCatalogDTO, error) {
	return skill_svc.Default().ListAgentSkillCommands(a.ctx, agentID, cwd)
}
