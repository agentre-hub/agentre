package orgtool_svc

import (
	"github.com/agentre-hub/agentre/internal/pkg/agenttool"
)

type orgtoolSvc struct {
	*agenttool.Mount

	orgQuery     OrgQuery
	deptCommand  DeptCommand
	agentCommand AgentCommand
	agentLookup  AgentLookup
	approval     ApprovalGateway
}

var defaultOrgtool = newOrgtoolSvc()

func newOrgtoolSvc() *orgtoolSvc {
	s := &orgtoolSvc{}
	s.Mount = agenttool.NewMount(agenttool.KeyOrg, s.newMCPServer)
	return s
}

// Default 取默认服务单例。
func Default() *orgtoolSvc { return defaultOrgtool }

// RegisterDeps bootstrap 接线(生产传 department_svc.Department()/agent_svc.Agent()/
// agent_repo.Agent()/chat_svc.Chat());测试注 mock。
func (s *orgtoolSvc) RegisterDeps(q OrgQuery, d DeptCommand, a AgentCommand, l AgentLookup, ap ApprovalGateway) {
	s.orgQuery, s.deptCommand, s.agentCommand, s.agentLookup, s.approval = q, d, a, l, ap
}
