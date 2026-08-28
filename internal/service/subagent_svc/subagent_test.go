package subagent_svc

import (
	"context"
	"testing"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_entity"
	"github.com/agentre-hub/agentre/internal/pkg/agenttool"
)

func enabledAgent(id int64) *agent_entity.Agent {
	a := &agent_entity.Agent{ID: id, Name: "Reviewer"}
	a.SetTools([]agent_entity.AgentToolItem{{Key: agenttool.KeySubagent, Enabled: true}})
	return a
}

func TestBuildTurnMCP(t *testing.T) {
	s := &subagentSvc{gatewayBaseURL: "http://127.0.0.1:9/", chains: map[int64][]int64{}}

	if got := s.BuildTurnMCP(context.Background(), &agent_entity.Agent{ID: 1}, 5, 0); got != nil {
		t.Fatalf("disabled agent should get no spec, got %v", got)
	}
	specs := s.BuildTurnMCP(context.Background(), enabledAgent(1), 5, 0)
	if len(specs) != 1 {
		t.Fatalf("want 1 spec, got %d", len(specs))
	}
	if specs[0].Name != agenttool.KeySubagent || specs[0].URL != "http://127.0.0.1:9//mcp/subagent/" {
		t.Fatalf("bad spec: %+v", specs[0])
	}
	if specs[0].Headers["Authorization"] == "" {
		t.Fatal("missing Authorization header")
	}
	s.gatewayBaseURL = ""
	if got := s.BuildTurnMCP(context.Background(), enabledAgent(1), 5, 0); got != nil {
		t.Fatalf("no gateway should get nil, got %v", got)
	}
}

func TestResolveChain_Cycle(t *testing.T) {
	s := &subagentSvc{chains: map[int64][]int64{}}

	// 顶层调用(父会话无链)放行
	chain, _, ok := s.resolveChain(100, 10, 20)
	if !ok || len(chain) != 1 || chain[0] != 10 {
		t.Fatalf("top-level call should pass: chain=%v ok=%v", chain, ok)
	}
	// 环: 父链含 10, 父=20, 调 10 → 拒绝
	s.registerChain(200, []int64{10})
	if _, _, ok := s.resolveChain(200, 20, 10); ok {
		t.Fatal("cycle should be rejected")
	}
	// 自调用拒绝
	if _, _, ok := s.resolveChain(300, 30, 30); ok {
		t.Fatal("self-call should be rejected")
	}
	// 无深度上限: 很长但无环的链应放行
	s.registerChain(400, []int64{1, 2, 3, 4, 5})
	if _, _, ok := s.resolveChain(400, 6, 7); !ok {
		t.Fatal("deep but acyclic chain should be allowed (no depth cap)")
	}
}
