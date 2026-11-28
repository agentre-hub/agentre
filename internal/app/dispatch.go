package app

import (
	"github.com/agentre-hub/agentre/internal/service/dispatch_svc"
	"github.com/agentre-hub/agentre/internal/service/skill_svc"
)

// 派发(spec 2026-11-25「派发规则」):新会话在哪台设备上跑、成员现在能跑在哪。原因保持为
// 码,由界面渲染。

// ListMemberAvailability 成员详情「现在能跑在」:成员不带项目时,按派发顺序列出每台参与
// 派发的设备的判定结果。
func (a *App) ListMemberAvailability(agentID int64) (*dispatch_svc.Plan, error) {
	res, err := dispatch_svc.Dispatch().Availability(a.ctx, agentID)
	return res, codedError(err)
}

// ListNewSessionCandidates 新会话的设备候选:按派发顺序的每台设备、选中的那台与所用的引擎
// 配置。可带本会话换用的引擎配置与手动指定的设备;手动设备不可用时回落到自动结果并标出。
func (a *App) ListNewSessionCandidates(req *dispatch_svc.PlanRequest) (*dispatch_svc.Plan, error) {
	res, err := dispatch_svc.Dispatch().Plan(a.ctx, req)
	return res, codedError(err)
}

// GetMemberSkillCatalog 成员详情「技能」:按生效引擎配置的运行时取目录(本机优先,从一台能
// 运行它的在线设备读),带失效授权与各设备缺装的授权包。
func (a *App) GetMemberSkillCatalog(agentID int64, refresh bool) (skill_svc.MemberSkillCatalogDTO, error) {
	res, err := skill_svc.Default().MemberSkillCatalog(a.ctx, agentID, refresh)
	return res, codedError(err)
}
