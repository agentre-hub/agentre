package hooktool_svc

import (
	"github.com/agentre-hub/agentre/internal/pkg/agenttool"
)

type hooktoolSvc struct {
	*agenttool.Mount

	hooks       HookService
	agentLookup AgentLookup
	approval    ApprovalGateway
}

var defaultHooktool = newHooktoolSvc()

func newHooktoolSvc() *hooktoolSvc {
	s := &hooktoolSvc{}
	s.Mount = agenttool.NewMount(agenttool.KeyHook, s.newMCPServer)
	return s
}

// Default 取默认服务单例。
func Default() *hooktoolSvc { return defaultHooktool }

// RegisterDeps bootstrap 接线(生产传 hook_svc.Hook()/agent_repo.Agent()/chat_svc.Chat());测试注 mock。
func (s *hooktoolSvc) RegisterDeps(h HookService, l AgentLookup, ap ApprovalGateway) {
	s.hooks, s.agentLookup, s.approval = h, l, ap
}
