package subagent_svc

// newSubagentSvcForTest 造一个带 Mount 的测试实例。直接写 struct literal 会漏掉
// Mount 初始化(Handler / BuildTurnMCP 提升自 Mount,未初始化会 panic)。
func newSubagentSvcForTest(agents AgentGateway, chat ChatGateway) *subagentSvc {
	s := newSubagentSvc()
	s.agents, s.chat = agents, chat
	return s
}
