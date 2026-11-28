package skill_svc

import (
	"context"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/agent_entity"
	"github.com/agentre-hub/agentre/internal/pkg/agentskill"
)

//go:generate mockgen -source deps.go -destination mock_skill_svc/mock_deps.go -package mock_skill_svc

type AgentLookup interface {
	Find(ctx context.Context, id int64) (*agent_entity.Agent, error)
}

// EngineLookup 成员生效的引擎配置（成员 › 部门链 › 账号默认）；都没有回 nil。技能包 ID
// 按它的运行时区分，授权也按它校验（spec 2026-11-25 决策 9）。
type EngineLookup interface {
	Effective(ctx context.Context, agentID int64) (*agent_backend_entity.EngineProfile, error)
}

// Placement 成员（不带项目）在每台参与派发的设备上的判定，按派发顺序。生产实现由
// bootstrap 接到派发服务：技能目录从其中一台能跑它的在线设备读，缺包按其中能跑的设备报。
type Placement interface {
	MemberDevices(ctx context.Context, agentID int64) ([]Device, error)
}

// CLIPaths 本机某运行时的 CLI 路径覆盖（按（运行时类型, 设备）存，决策 13）；空 = 自动探测。
type CLIPaths interface {
	LocalCLIPath(ctx context.Context, runtimeType string) (string, error)
}

// RemoteDiscoverer 枚举远端 device(daemon)本机已装技能包。派到 agentred 时技能包在
// daemon 那台机器上,desktop 本地的 claude plugin list 看不到 —— 经此端口走 daemon
// skills.list RPC 发现。生产实现在 agent_backend_svc(借 device 连接池);测试注入替身。
type RemoteDiscoverer interface {
	ListSkills(ctx context.Context, deviceID int64, backendType string) ([]agentskill.SkillPack, error)

	// ListSkillCommands 问那台机器「这个运行时此刻叫得动哪些 skill」。
	//
	// 它与 ListSkills 不能合并:后者只答**可配置的 plugin 包**,而输入框要的名字还含
	// CLI 自己解析的 user / project / system skill —— 那一半不是包,且只有那台机器
	// 解析得出(本机发现器看的是桌面端自己这台机器上的目录,拿它答远端档等于答错人)。
	//
	// authorized 由这一侧带过去:组织架构库在桌面端,那台机器上没有。cwd 同理 ——
	// 项目级 skill 要靠它才解析得出,而「这一轮在哪跑」是会话的事实。
	ListSkillCommands(ctx context.Context, deviceID int64, backendType, cwd string,
		authorized []agent_entity.AgentSkillItem) ([]agentskill.SkillCommand, error)
}
