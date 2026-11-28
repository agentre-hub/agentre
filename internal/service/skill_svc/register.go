package skill_svc

import "github.com/agentre-hub/agentre/internal/pkg/agentskill"

var defaultSvc *Service

// Default 返回 bootstrap 注册的默认实例。
func Default() *Service { return defaultSvc }

// Register bootstrap 接线:注入依赖实现。remote 为 agentred 的 daemon 技能发现器。
func Register(agent AgentLookup, engine EngineLookup, placement Placement, cliPaths CLIPaths, remote RemoteDiscoverer) {
	defaultSvc = newService(agent, engine, placement, cliPaths, remote)
}

func newService(agent AgentLookup, engine EngineLookup, placement Placement, cliPaths CLIPaths, remote RemoteDiscoverer) *Service {
	return &Service{
		agent: agent, engine: engine, placement: placement, cliPaths: cliPaths, remote: remote,
		cache: agentskill.NewDiscoveryCache(),
	}
}
