package app

import (
	"github.com/agentre-hub/agentre/internal/service/agent_svc"
	"github.com/agentre-hub/agentre/internal/service/skill_svc"
)

// CreateAgent 新建 Agent。
func (a *App) CreateAgent(req *agent_svc.CreateAgentRequest) (*agent_svc.CreateAgentResponse, error) {
	return agent_svc.Agent().Create(a.ctx, req)
}

// UpdateAgent 更新 Agent。
func (a *App) UpdateAgent(req *agent_svc.UpdateAgentRequest) (*agent_svc.UpdateAgentResponse, error) {
	return agent_svc.Agent().Update(a.ctx, req)
}

// MoveAgent 换部门 + 同级排序。
func (a *App) MoveAgent(req *agent_svc.MoveAgentRequest) (*agent_svc.MoveAgentResponse, error) {
	return agent_svc.Agent().Move(a.ctx, req)
}

// ReorderAgents 同级密集重排(orderedIds 为该组完整集合)。
func (a *App) ReorderAgents(req *agent_svc.ReorderAgentsRequest) error {
	return agent_svc.Agent().Reorder(a.ctx, req)
}

// DeleteAgent 软删 Agent。CEO 拒绝。
func (a *App) DeleteAgent(req *agent_svc.DeleteAgentRequest) (*agent_svc.DeleteAgentResponse, error) {
	return agent_svc.Agent().Delete(a.ctx, req)
}

// UploadAgentAvatar 写入 Agent 头像（base64 data URL，≤ 2MB，PNG/JPEG/WEBP）。
func (a *App) UploadAgentAvatar(req *agent_svc.UploadAvatarRequest) (*agent_svc.UploadAvatarResponse, error) {
	return agent_svc.Agent().UploadAvatar(a.ctx, req)
}

// DeleteAgentAvatar 清空 Agent 头像，回退到首字母派生。
func (a *App) DeleteAgentAvatar(req *agent_svc.DeleteAvatarRequest) (*agent_svc.DeleteAvatarResponse, error) {
	return agent_svc.Agent().DeleteAvatar(a.ctx, req)
}

// SetAgentPinned 置顶/取消置顶某 Agent（侧栏混排列表浮顶）。
func (a *App) SetAgentPinned(req *agent_svc.SetPinnedRequest) (*agent_svc.SetPinnedResponse, error) {
	return agent_svc.Agent().SetPinned(a.ctx, req)
}

// ListAgentSkillPacks 返回某 agent 名下 agentBackendID 这一档执行目标可见的技能包
// 目录(推荐 + 发现 + 已授权)——R15e「一档一块」：发现来源与授权都钉死在这一档，
// 不与 Agent 名下别的档合并（任务 12：组织架构页技能节改成一档一块之后，每块各自
// 用自己的 agentBackendID 调这里，不再只看 Agent 的主档）。
func (a *App) ListAgentSkillPacks(agentID int64, agentBackendID int64, refresh bool) (skill_svc.SkillCatalogDTO, error) {
	return skill_svc.Default().ListAgentSkillPacksForTarget(a.ctx, agentID, agentBackendID, refresh)
}

// ListAgentSkillCommands 返回当前 agent 在 cwd 中可调用的 Skill 命令目录。
func (a *App) ListAgentSkillCommands(agentID int64, cwd string) (skill_svc.SkillCommandCatalogDTO, error) {
	return skill_svc.Default().ListAgentSkillCommands(a.ctx, agentID, cwd)
}
