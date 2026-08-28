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
type BackendLookup interface {
	Find(ctx context.Context, id int64) (*agent_backend_entity.AgentBackend, error)
}

// ExecTargetLookup 按 sort_order 升序给出一个 Agent 的有序执行目标列表。技能授权
// 存放在每一档自己的行上（R15e / 决策 33：存放位置从 agents.skills_json 下沉到
// 执行目标行）；这里只消费最靠前的那一档 —— 多档各自独立管理（不做并集）是
// task 12 的界面工作，本服务不做跨档聚合。
type ExecTargetLookup interface {
	ListByAgent(ctx context.Context, agentID int64) ([]*agent_entity.AgentExecTarget, error)
}

// RemoteDiscoverer 枚举远端 device(daemon)本机已装技能包。远端 backend 的技能包在
// daemon 那台机器上,desktop 本地的 claude plugin list 看不到 —— 经此端口走 daemon
// skills.list RPC 发现。生产实现在 agent_backend_svc(借 device 连接池);测试注入替身。
type RemoteDiscoverer interface {
	ListSkills(ctx context.Context, deviceID int64, backendType string) ([]agentskill.SkillPack, error)
}
