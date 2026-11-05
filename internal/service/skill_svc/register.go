package skill_svc

import "github.com/agentre-hub/agentre/internal/pkg/agentskill"

var defaultSvc *Service

// Default 返回 bootstrap 注册的默认实例。
func Default() *Service { return defaultSvc }

// Register bootstrap 接线:注入依赖实现。remote 为远端 backend 的 daemon 技能发现器。
func Register(agent AgentLookup, backend BackendLookup, execTarget ExecTargetLookup, remote RemoteDiscoverer) {
	defaultSvc = &Service{agent: agent, backend: backend, execTarget: execTarget, remote: remote, cache: agentskill.NewDiscoveryCache()}
}
