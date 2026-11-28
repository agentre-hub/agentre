package bootstrap

import (
	"context"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/agent_entity"
	"github.com/agentre-hub/agentre/internal/service/agent_svc"
	"github.com/agentre-hub/agentre/internal/service/device_pref_svc"
	"github.com/agentre-hub/agentre/internal/service/dispatch_svc"
	"github.com/agentre-hub/agentre/internal/service/skill_svc"
)

// skill_svc 的三个窄口接到成员引擎解析、派发服务与设备偏好上(spec 2026-11-25 决策 9:
// 授权挂在成员上按运行时校验,目录从能跑它的在线设备读,缺包按能跑的设备报)。

type skillEngine struct{}

func (skillEngine) Effective(ctx context.Context, agentID int64) (*agent_backend_entity.EngineProfile, error) {
	eff, err := agent_svc.Agent().EffectiveEngine(ctx, agentID)
	if err != nil || eff == nil {
		return nil, err
	}
	return eff.Profile, nil
}

// memberEffectiveEngine 是 chat_svc/sessionengine 的「成员生效的引擎配置」口，与技能那条
// 同一条 agent_svc 继承链。
func memberEffectiveEngine(ctx context.Context, a *agent_entity.Agent) (*agent_backend_entity.EngineProfile, error) {
	return skillEngine{}.Effective(ctx, a.ID)
}

type skillPlacement struct{}

func (skillPlacement) MemberDevices(ctx context.Context, agentID int64) ([]skill_svc.Device, error) {
	plan, err := dispatch_svc.Dispatch().Availability(ctx, agentID)
	if err != nil {
		return nil, err
	}
	out := make([]skill_svc.Device, 0, len(plan.Devices))
	for _, d := range plan.Devices {
		packs, known := d.SkillPacks()
		out = append(out, skill_svc.Device{
			Fingerprint: d.Fingerprint, Name: d.Name, IsSelf: d.IsSelf, PairedID: d.PairedID(),
			CanRun: d.Eligible, InstalledPacks: packs, PacksKnown: known,
		})
	}
	return out, nil
}

type skillCLIPaths struct{}

func (skillCLIPaths) LocalCLIPath(ctx context.Context, runtimeType string) (string, error) {
	return device_pref_svc.Default().GetCLIPath(ctx, runtimeType, "")
}

// turnEnabledPlugins 是 chat_svc 每轮注入技能包覆盖的口:授权取成员上的那份,按这一轮引擎
// 配置的运行时校验;运行时为空时按成员生效的引擎配置的运行时。发现/查询失败软降级(本轮不
// 约束技能集),不阻断对话。
func turnEnabledPlugins(ctx context.Context, a *agent_entity.Agent, runtime string) map[string]bool {
	m, err := skill_svc.Default().EnabledPluginsMapFor(ctx, a, runtime)
	if err != nil {
		return nil
	}
	return m
}
