package subagent_svc

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"go.uber.org/mock/gomock"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_entity"
	"github.com/agentre-hub/agentre/internal/service/subagent_svc/mock_subagent_svc"
)

func TestMCP_TokenRoundTrip(t *testing.T) {
	s := &subagentSvc{chains: map[int64][]int64{}}
	h := s.mcpHandlerInit()
	tok := h.MintToken(7, 42)
	ref, ok := h.Lookup(tok)
	if !ok || ref.AgentID != 7 || ref.SessionID != 42 {
		t.Fatalf("roundtrip failed: %+v ok=%v", ref, ok)
	}
	if _, ok := h.Lookup(tok + "x"); ok {
		t.Fatal("tampered token should fail")
	}
}

// TestMCP_TokenNotSharedAcrossInstances 锁住共享骨架文档承诺的鉴权边界:每个 subagentSvc
// 实例(mcpHandlerInit 懒初始化)自持一个 agenttool.Server,故一个实例签的 token 在另一个
// 实例上验不过 —— 这是「持 A 会话令牌无法操作 B 会话资源」的隔离屏障。
func TestMCP_TokenNotSharedAcrossInstances(t *testing.T) {
	s1 := &subagentSvc{chains: map[int64][]int64{}}
	s2 := &subagentSvc{chains: map[int64][]int64{}}
	tok := s1.mcpHandlerInit().MintToken(7, 42)
	if _, ok := s2.mcpHandlerInit().Lookup(tok); ok {
		t.Fatal("token minted by one subagentSvc instance's server must not validate against another instance's server")
	}
	// 而同一实例懒初始化出的 server 是稳定的单例(mcpOnce),自签发的 token 应始终有效。
	if _, ok := s1.mcpHandlerInit().Lookup(tok); !ok {
		t.Fatal("token minted by an instance's server must validate against the same instance's server")
	}
}

func TestMCP_ToolsList(t *testing.T) {
	s := &subagentSvc{chains: map[int64][]int64{}}
	h := s.mcpHandlerInit()
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/mcp/subagent/", strings.NewReader(`{"id":1,"method":"tools/list"}`)))
	var resp struct {
		Result struct {
			Tools []map[string]any `json:"tools"`
		} `json:"result"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Result.Tools) != 2 {
		t.Fatalf("want 2 tools, got %d", len(resp.Result.Tools))
	}
}

func TestMCP_AgentList(t *testing.T) {
	ctrl := gomock.NewController(t)
	agents := mock_subagent_svc.NewMockAgentGateway(ctrl)
	svc := &subagentSvc{agents: agents, chains: map[int64][]int64{}}
	agents.EXPECT().Find(gomock.Any(), int64(7)).Return(enabledAgent(7), nil)
	agents.EXPECT().List(gomock.Any()).Return([]*agent_entity.Agent{
		{ID: 1, Name: "Reviewer", Description: "审查代码"},
		{ID: 2, Name: "Writer"},
	}, nil)

	h := svc.mcpHandlerInit()
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/mcp/subagent/", strings.NewReader(`{"id":1,"method":"tools/call","params":{"name":"agent_list"}}`))
	req.Header.Set("Authorization", "Bearer "+h.MintToken(7, 42))
	h.ServeHTTP(rr, req)
	if !strings.Contains(rr.Body.String(), "Reviewer") {
		t.Fatalf("agent_list missing agent: %s", rr.Body.String())
	}
}

func TestMCP_ForbiddenWhenDisabled(t *testing.T) {
	ctrl := gomock.NewController(t)
	agents := mock_subagent_svc.NewMockAgentGateway(ctrl)
	svc := &subagentSvc{agents: agents, chains: map[int64][]int64{}}
	agents.EXPECT().Find(gomock.Any(), int64(7)).Return(&agent_entity.Agent{ID: 7}, nil)

	h := svc.mcpHandlerInit()
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/mcp/subagent/", strings.NewReader(`{"id":1,"method":"tools/call","params":{"name":"agent_list"}}`))
	req.Header.Set("Authorization", "Bearer "+h.MintToken(7, 42))
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("want 403, got %d", rr.Code)
	}
}

func TestMCP_DepsNotRegistered(t *testing.T) {
	svc := &subagentSvc{chains: map[int64][]int64{}}
	h := svc.mcpHandlerInit()
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/mcp/subagent/", strings.NewReader(`{"id":1,"method":"tools/call","params":{"name":"agent_list"}}`))
	req.Header.Set("Authorization", "Bearer "+h.MintToken(7, 42))
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("want 503 during bootstrap window (RegisterDeps not called), got %d", rr.Code)
	}
}

// TestMCP_AgentCallMissingArgs 锁住 agenttool.InvalidParams 到 JSON-RPC -32602 的映射,
// 与 org/hook 两个内置工具共用同一套错误码语义。
func TestMCP_AgentCallMissingArgs(t *testing.T) {
	ctrl := gomock.NewController(t)
	agents := mock_subagent_svc.NewMockAgentGateway(ctrl)
	svc := &subagentSvc{agents: agents, chains: map[int64][]int64{}}
	agents.EXPECT().Find(gomock.Any(), int64(7)).Return(enabledAgent(7), nil)

	h := svc.mcpHandlerInit()
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/mcp/subagent/", strings.NewReader(`{"id":1,"method":"tools/call","params":{"name":"agent_call","arguments":{}}}`))
	req.Header.Set("Authorization", "Bearer "+h.MintToken(7, 42))
	h.ServeHTTP(rr, req)

	var resp struct {
		Error struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Error.Code != -32602 {
		t.Fatalf("want JSON-RPC -32602 invalid params, got %+v body=%s", resp.Error, rr.Body.String())
	}
}
