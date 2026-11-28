package agent_svc

import (
	"context"

	"github.com/cago-frame/cago/pkg/i18n"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_entity"
	"github.com/agentre-hub/agentre/internal/pkg/code"
	"github.com/agentre-hub/agentre/internal/repository/agent_backend_repo"
	"github.com/agentre-hub/agentre/internal/repository/agent_repo"
	"github.com/agentre-hub/agentre/internal/service/department_svc"
	"github.com/agentre-hub/agentre/internal/service/sync_svc"
	"github.com/agentre-hub/agentre/pkg/syncwire"
	"github.com/agentre-hub/agentre/pkg/wire/devicefp"
)

// 成员的执行设置（spec 2026-11-25「成员详情『执行』栏」）：引用一份引擎配置（不引用 =
// 跟随部门链）、「在哪跑」约束、按运行时校验的技能授权。三块各自自动保存，各有一个写口，
// 都落到 agent_repo.UpdateEngineSettings 这一个窄写上。

// EffectiveEngine 解析成员生效的引擎配置：成员自己引用的活配置 › 所在部门的成员默认引擎
// › 上级部门 › 账号默认。没有部门的成员（CEO、挂在上级成员下的成员）直接跟随账号默认。
// 都没有时为 nil。给后续派发等 Go 侧调用方用；界面走 ResolveMemberEngine。
func (s *agentSvc) EffectiveEngine(ctx context.Context, agentID int64) (*department_svc.EffectiveEngine, error) {
	a, err := s.requireAgent(ctx, agentID)
	if err != nil {
		return nil, err
	}
	return s.effectiveEngine(ctx, a)
}

func (s *agentSvc) ResolveMemberEngine(ctx context.Context, req *ResolveMemberEngineRequest) (*ResolveMemberEngineResponse, error) {
	if req == nil {
		return nil, i18n.NewError(ctx, code.InvalidParameter)
	}
	eff, err := s.EffectiveEngine(ctx, req.AgentID)
	if err != nil {
		return nil, err
	}
	return &ResolveMemberEngineResponse{Effective: eff.View()}, nil
}

func (s *agentSvc) GetMemberEngine(ctx context.Context, req *GetMemberEngineRequest) (*MemberEngineView, error) {
	if req == nil {
		return nil, i18n.NewError(ctx, code.InvalidParameter)
	}
	a, err := s.requireAgent(ctx, req.AgentID)
	if err != nil {
		return nil, err
	}
	return s.memberEngineView(ctx, a)
}

// SetMemberEngine 换引用的引擎配置；0 = 跟随部门。技能授权原样保留：换到另一种运行时后
// 它们显示为失效、由用户一键移除，而不是被这里静默清掉。
func (s *agentSvc) SetMemberEngine(ctx context.Context, req *SetMemberEngineRequest) (*MemberEngineView, error) {
	if req == nil || req.EngineProfileID < 0 {
		return nil, i18n.NewError(ctx, code.InvalidParameter)
	}
	a, err := s.requireAgent(ctx, req.AgentID)
	if err != nil {
		return nil, err
	}
	if req.EngineProfileID > 0 {
		p, err := agent_backend_repo.EngineProfile().Find(ctx, req.EngineProfileID)
		if err != nil {
			return nil, err
		}
		if p == nil {
			return nil, i18n.NewError(ctx, code.EngineProfileNotFound)
		}
	}
	a.EngineProfileID = req.EngineProfileID
	return s.saveEngineSettings(ctx, a)
}

func (s *agentSvc) SetMemberExecConstraint(ctx context.Context, req *SetMemberExecConstraintRequest) (*MemberEngineView, error) {
	if req == nil {
		return nil, i18n.NewError(ctx, code.InvalidParameter)
	}
	a, err := s.requireAgent(ctx, req.AgentID)
	if err != nil {
		return nil, err
	}
	if err := a.SetExecConstraint(ctx, req.Constraint.toEntity()); err != nil {
		return nil, err
	}
	return s.saveEngineSettings(ctx, a)
}

// SetMemberSkillGrants 整份替换技能授权，并记下它们是对当前生效运行时做的授权。没有生效
// 的引擎配置时无从判断技能包属于哪种运行时，拒绝。
func (s *agentSvc) SetMemberSkillGrants(ctx context.Context, req *SetMemberSkillGrantsRequest) (*MemberEngineView, error) {
	if req == nil {
		return nil, i18n.NewError(ctx, code.InvalidParameter)
	}
	a, err := s.requireAgent(ctx, req.AgentID)
	if err != nil {
		return nil, err
	}
	eff, err := s.effectiveEngine(ctx, a)
	if err != nil {
		return nil, err
	}
	if eff == nil {
		return nil, i18n.NewError(ctx, code.EngineProfileRequired)
	}
	a.SetSkillGrants(eff.Profile.Type, skillsFromDTO(req.Skills))
	return s.saveEngineSettings(ctx, a)
}

func (s *agentSvc) saveEngineSettings(ctx context.Context, a *agent_entity.Agent) (*MemberEngineView, error) {
	a.Updatetime = s.now()
	if err := agent_repo.Agent().UpdateEngineSettings(ctx, a); err != nil {
		return nil, err
	}
	// 引擎引用 / 约束 / 技能授权都走这一条窄写口：落库成功后统一排进上行。
	sync_svc.NotifyUpdate(ctx, syncwire.KindAgent, a.ID, a.SyncMeta)
	return s.memberEngineView(ctx, a)
}

func (s *agentSvc) requireAgent(ctx context.Context, id int64) (*agent_entity.Agent, error) {
	a, err := agent_repo.Agent().Find(ctx, id)
	if err != nil {
		return nil, err
	}
	if a == nil {
		return nil, i18n.NewError(ctx, code.AgentNotFound)
	}
	return a, nil
}

func (s *agentSvc) effectiveEngine(ctx context.Context, a *agent_entity.Agent) (*department_svc.EffectiveEngine, error) {
	own, err := s.ownEngine(ctx, a)
	if err != nil || own != nil {
		return own, err
	}
	return s.inheritedEngine(ctx, a)
}

// ownEngine 成员自己引用的活配置；没引用、或引用已失效时为 nil（回落到下一级）。
func (s *agentSvc) ownEngine(ctx context.Context, a *agent_entity.Agent) (*department_svc.EffectiveEngine, error) {
	if a.EngineProfileID <= 0 {
		return nil, nil
	}
	p, err := agent_backend_repo.EngineProfile().Find(ctx, a.EngineProfileID)
	if err != nil || p == nil {
		return nil, err
	}
	return &department_svc.EffectiveEngine{Profile: p, Source: department_svc.EngineSourceMember}, nil
}

// inheritedEngine 成员「跟随」时会落到的配置：所在部门链，没有部门时账号默认。
func (s *agentSvc) inheritedEngine(ctx context.Context, a *agent_entity.Agent) (*department_svc.EffectiveEngine, error) {
	return department_svc.Department().ResolveDepartmentEngine(ctx, a.DepartmentID)
}

func (s *agentSvc) memberEngineView(ctx context.Context, a *agent_entity.Agent) (*MemberEngineView, error) {
	inherited, err := s.inheritedEngine(ctx, a)
	if err != nil {
		return nil, err
	}
	effective, err := s.ownEngine(ctx, a)
	if err != nil {
		return nil, err
	}
	if effective == nil {
		effective = inherited
	}
	var valid, invalid []agent_entity.AgentSkillItem
	if effective != nil {
		valid, invalid = a.SkillGrantsFor(effective.Profile.Type)
	} else {
		valid = a.GetSkillGrants()
	}
	return &MemberEngineView{
		AgentID:            a.ID,
		EngineProfileID:    a.EngineProfileID,
		Constraint:         execConstraintDTO(a.GetExecConstraint()),
		SkillGrants:        skillsToDTO(valid),
		InvalidSkillGrants: skillsToDTO(invalid),
		Inherited:          inherited.View(),
		Effective:          effective.View(),
	}, nil
}

func (c ExecConstraintDTO) toEntity() agent_entity.ExecConstraint {
	devices := make([]devicefp.Carrier, 0, len(c.Devices))
	for _, d := range c.Devices {
		devices = append(devices, devicefp.Carrier(d))
	}
	return agent_entity.ExecConstraint{Mode: c.Mode, Labels: c.Labels, Devices: devices}
}

func execConstraintDTO(c agent_entity.ExecConstraint) ExecConstraintDTO {
	out := ExecConstraintDTO{Mode: c.Mode, Labels: []string{}, Devices: []string{}}
	out.Labels = append(out.Labels, c.Labels...)
	for _, d := range c.Devices {
		out.Devices = append(out.Devices, string(d))
	}
	return out
}

func skillsToDTO(items []agent_entity.AgentSkillItem) []department_svc.AgentSkillDTO {
	out := make([]department_svc.AgentSkillDTO, 0, len(items))
	for _, it := range items {
		out = append(out, department_svc.AgentSkillDTO{ID: it.ID, Enabled: it.Enabled})
	}
	return out
}
