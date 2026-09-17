package subagent_svc

import (
	"slices"
	"sync"

	"github.com/agentre-hub/agentre/internal/pkg/agenttool"
)

type subagentSvc struct {
	*agenttool.Mount

	agents AgentGateway
	chat   ChatGateway

	chainsMu sync.Mutex
	chains   map[int64][]int64 // 一次性会话ID → 祖先 agentID 链(不含被调者)
}

var defaultSubagent = newSubagentSvc()

func newSubagentSvc() *subagentSvc {
	s := &subagentSvc{chains: map[int64][]int64{}}
	s.Mount = agenttool.NewMount(agenttool.KeySubagent, s.newMCPServer)
	return s
}

// Default 取默认服务单例。
func Default() *subagentSvc { return defaultSubagent }

// RegisterDeps bootstrap 接线(生产传 agent_repo.Agent() + ChatSvcGateway());测试注 mock。
func (s *subagentSvc) RegisterDeps(agents AgentGateway, chat ChatGateway) {
	s.agents, s.chat = agents, chat
}

// resolveChain 按父会话取祖先链, 拼上父 agent, 做环检测。不设深度上限 —— 环检测已把链长
// 天然封顶在「不同 agent 数」内(委派不能成环, 故不会无限递归)。ok=false 时 reason 是 MCP error 文本。
func (s *subagentSvc) resolveChain(parentSessionID, parentAgentID, calleeAgentID int64) (newChain []int64, reason string, ok bool) {
	s.chainsMu.Lock()
	parent := s.chains[parentSessionID]
	s.chainsMu.Unlock()
	newChain = make([]int64, 0, len(parent)+1)
	newChain = append(newChain, parent...)
	newChain = append(newChain, parentAgentID)
	if slices.Contains(newChain, calleeAgentID) {
		return nil, "检测到循环调用(目标 agent 已在调用链上),拒绝", false
	}
	return newChain, "", true
}

func (s *subagentSvc) registerChain(sessionID int64, chain []int64) {
	s.chainsMu.Lock()
	s.chains[sessionID] = chain
	s.chainsMu.Unlock()
}

func (s *subagentSvc) clearChain(sessionID int64) {
	s.chainsMu.Lock()
	delete(s.chains, sessionID)
	s.chainsMu.Unlock()
}
