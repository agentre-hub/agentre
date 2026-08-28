package hooktool_svc

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"go.uber.org/mock/gomock"

	. "github.com/smartystreets/goconvey/convey"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_entity"
	"github.com/agentre-hub/agentre/internal/pkg/agenttool"
	"github.com/agentre-hub/agentre/internal/service/hook_svc"
	"github.com/agentre-hub/agentre/internal/service/hooktool_svc/mock_hooktool_svc"
)

// newTestSvc 构造一个全新的 hooktoolSvc(避免 Default() 单例跨测试串台),只接 AgentLookup + HookService。
func newTestSvc(lookup AgentLookup, hooks HookService) *hooktoolSvc {
	s := &hooktoolSvc{}
	s.RegisterDeps(hooks, lookup, nil)
	return s
}

// hookEnabledAgent / hookDisabledAgent 返回 hook 开关 ON/OFF 的 agent。
func hookEnabledAgent(id int64) *agent_entity.Agent {
	a := &agent_entity.Agent{ID: id}
	a.SetTools([]agent_entity.AgentToolItem{{Key: "hook", Enabled: true}})
	return a
}

func hookDisabledAgent(id int64) *agent_entity.Agent {
	a := &agent_entity.Agent{ID: id}
	a.SetTools([]agent_entity.AgentToolItem{{Key: "hook", Enabled: false}})
	return a
}

// rpcCall 发一次 JSON-RPC POST(可选 bearer token),返回 recorder。
func rpcCall(h http.Handler, body, token string) *httptest.ResponseRecorder {
	req := httptest.NewRequest("POST", "/mcp/hook/", strings.NewReader(body))
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	return w
}

func TestHookMCP_TokenRoundTrip(t *testing.T) {
	Convey("MintToken → lookup 解出原 (agent, session);篡改/格式非法 → 401", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()
		lookup := mock_hooktool_svc.NewMockAgentLookup(ctrl)
		hooks := mock_hooktool_svc.NewMockHookService(ctrl)
		lookup.EXPECT().Find(gomock.Any(), int64(7)).Return(hookEnabledAgent(7), nil)
		hooks.EXPECT().Load(gomock.Any(), gomock.Any()).Return(&hook_svc.LoadHooksResponse{}, nil)

		s := newTestSvc(lookup, hooks)
		token := s.mcpHandlerInit().MintToken(7, 99)

		Convey("合法 token + hook_list → 200", func() {
			w := rpcCall(s.MCPHandler(), `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"hook_list"}}`, token)
			So(w.Code, ShouldEqual, http.StatusOK)
		})
	})

	Convey("篡改签名 / 格式非法 token → 401(不调任何依赖)", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()
		lookup := mock_hooktool_svc.NewMockAgentLookup(ctrl) // 无 EXPECT
		s := newTestSvc(lookup, mock_hooktool_svc.NewMockHookService(ctrl))
		good := s.mcpHandlerInit().MintToken(7, 99)

		body := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"hook_list"}}`
		So(rpcCall(s.MCPHandler(), body, good+"tampered").Code, ShouldEqual, http.StatusUnauthorized)
		So(rpcCall(s.MCPHandler(), body, "not-a-token").Code, ShouldEqual, http.StatusUnauthorized)
		So(rpcCall(s.MCPHandler(), body, "").Code, ShouldEqual, http.StatusUnauthorized)
	})
}

func TestHookMCP_SwitchOffForbids(t *testing.T) {
	Convey("token 合法但 hook 开关 OFF → 403,不查 hooks", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()
		lookup := mock_hooktool_svc.NewMockAgentLookup(ctrl)
		hooks := mock_hooktool_svc.NewMockHookService(ctrl) // 无 Load EXPECT
		lookup.EXPECT().Find(gomock.Any(), int64(7)).Return(hookDisabledAgent(7), nil)

		s := newTestSvc(lookup, hooks)
		token := s.mcpHandlerInit().MintToken(7, 99)
		w := rpcCall(s.MCPHandler(), `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"hook_list"}}`, token)
		So(w.Code, ShouldEqual, http.StatusForbidden)
	})

	Convey("Find 返回 nil → 403", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()
		lookup := mock_hooktool_svc.NewMockAgentLookup(ctrl)
		lookup.EXPECT().Find(gomock.Any(), int64(7)).Return(nil, nil)
		s := newTestSvc(lookup, mock_hooktool_svc.NewMockHookService(ctrl))
		token := s.mcpHandlerInit().MintToken(7, 99)
		w := rpcCall(s.MCPHandler(), `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"hook_list"}}`, token)
		So(w.Code, ShouldEqual, http.StatusForbidden)
	})
}

func TestHookMCP_DepsNotRegistered(t *testing.T) {
	Convey("bootstrap 窗口期(RegisterDeps 未执行)tools/call → 503,不 panic", t, func() {
		s := &hooktoolSvc{}
		token := s.mcpHandlerInit().MintToken(7, 99)
		w := rpcCall(s.MCPHandler(), `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"hook_list"}}`, token)
		So(w.Code, ShouldEqual, http.StatusServiceUnavailable)
		So(w.Body.String(), ShouldContainSubstring, "service unavailable")
	})
}

func TestHookMCP_InitializeAndToolsList(t *testing.T) {
	Convey("initialize 回显 protocolVersion + serverInfo(agentre-hook)", t, func() {
		s := newTestSvc(nil, nil)
		w := rpcCall(s.MCPHandler(), `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25"}}`, "")
		So(w.Code, ShouldEqual, http.StatusOK)
		So(w.Body.String(), ShouldContainSubstring, "2025-11-25")
		So(w.Body.String(), ShouldContainSubstring, "agentre-hook")
	})

	Convey("tools/list 暴露全部 6 个工具名 + 写工具注明需审批", t, func() {
		s := newTestSvc(nil, nil)
		w := rpcCall(s.MCPHandler(), `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`, "")
		So(w.Code, ShouldEqual, http.StatusOK)
		body := w.Body.String()
		for _, name := range []string{"hook_list", "hook_get", "hook_create", "hook_update", "hook_delete", "hook_run"} {
			So(body, ShouldContainSubstring, name)
		}
		So(body, ShouldContainSubstring, "需要用户审批")
	})
}

func TestHookMCP_ListReturnsCompactRows(t *testing.T) {
	Convey("hook_list → content[0].text 是精简行:含名/解释器/调度,不含 command 正文与 env", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()
		lookup := mock_hooktool_svc.NewMockAgentLookup(ctrl)
		hooks := mock_hooktool_svc.NewMockHookService(ctrl)
		lookup.EXPECT().Find(gomock.Any(), int64(7)).Return(hookEnabledAgent(7), nil)
		hooks.EXPECT().Load(gomock.Any(), gomock.Any()).Return(&hook_svc.LoadHooksResponse{
			Hooks: []*hook_svc.HookItem{{
				ID: 1, Name: "巡检", Interpreter: "bash", ScheduleExpr: "*/5 * * * *", Enabled: true,
				Command: "curl https://secret-internal/health", LastStatus: "ok",
				Env: []hook_svc.EnvVar{{Key: "TOKEN", Value: "********", Secret: true}},
			}},
		}, nil)

		s := newTestSvc(lookup, hooks)
		token := s.mcpHandlerInit().MintToken(7, 99)
		w := rpcCall(s.MCPHandler(), `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"hook_list"}}`, token)
		So(w.Code, ShouldEqual, http.StatusOK)
		body := w.Body.String()
		So(body, ShouldContainSubstring, "巡检")
		So(body, ShouldContainSubstring, "*/5 * * * *")
		So(body, ShouldContainSubstring, `"type":"text"`)
		// 精简行剔除 command 正文与 env(列表不该泄露脚本内容/密钥位)。
		So(body, ShouldNotContainSubstring, "secret-internal")
		So(body, ShouldNotContainSubstring, "TOKEN")
	})
}

func TestHookMCP_GetReturnsFullHookAndEvents(t *testing.T) {
	Convey("hook_get(id) → 含 command 全文 + 脱敏 env + 最近事件;id 不存在 → error", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()
		lookup := mock_hooktool_svc.NewMockAgentLookup(ctrl)
		hooks := mock_hooktool_svc.NewMockHookService(ctrl)
		lookup.EXPECT().Find(gomock.Any(), int64(7)).Return(hookEnabledAgent(7), nil)
		hooks.EXPECT().Load(gomock.Any(), &hook_svc.LoadHooksRequest{HookID: 3, Limit: 20}).Return(&hook_svc.LoadHooksResponse{
			Hooks: []*hook_svc.HookItem{
				{ID: 1, Name: "其它"},
				{ID: 3, Name: "巡检", Command: "echo hello", Env: []hook_svc.EnvVar{{Key: "TOKEN", Value: "********", Secret: true}}},
			},
			Events: []*hook_svc.HookEventItem{{ID: 9, HookID: 3, Title: "磁盘告警"}},
		}, nil)

		s := newTestSvc(lookup, hooks)
		token := s.mcpHandlerInit().MintToken(7, 99)
		w := rpcCall(s.MCPHandler(), `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"hook_get","arguments":{"id":3}}}`, token)
		So(w.Code, ShouldEqual, http.StatusOK)
		body := w.Body.String()
		So(body, ShouldContainSubstring, "echo hello")
		So(body, ShouldContainSubstring, "磁盘告警")
		So(body, ShouldContainSubstring, "********")
	})

	Convey("hook_get id 不存在 → JSON-RPC error", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()
		lookup := mock_hooktool_svc.NewMockAgentLookup(ctrl)
		hooks := mock_hooktool_svc.NewMockHookService(ctrl)
		lookup.EXPECT().Find(gomock.Any(), int64(7)).Return(hookEnabledAgent(7), nil)
		hooks.EXPECT().Load(gomock.Any(), gomock.Any()).Return(&hook_svc.LoadHooksResponse{Hooks: []*hook_svc.HookItem{{ID: 1}}}, nil)

		s := newTestSvc(lookup, hooks)
		token := s.mcpHandlerInit().MintToken(7, 99)
		w := rpcCall(s.MCPHandler(), `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"hook_get","arguments":{"id":999}}}`, token)
		So(w.Code, ShouldEqual, http.StatusOK) // JSON-RPC error 仍是 HTTP 200
		So(w.Body.String(), ShouldContainSubstring, "不存在")
	})
}

func TestHookMCP_GetMissingID(t *testing.T) {
	Convey("hook_get 缺 id → -32602(参数非法),不查 hooks", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()
		lookup := mock_hooktool_svc.NewMockAgentLookup(ctrl)
		lookup.EXPECT().Find(gomock.Any(), int64(7)).Return(hookEnabledAgent(7), nil)
		s := newTestSvc(lookup, mock_hooktool_svc.NewMockHookService(ctrl)) // 无 Load EXPECT
		token := s.mcpHandlerInit().MintToken(7, 99)
		w := rpcCall(s.MCPHandler(), `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"hook_get","arguments":{}}}`, token)
		So(w.Code, ShouldEqual, http.StatusOK) // JSON-RPC error 仍是 HTTP 200
		So(w.Body.String(), ShouldContainSubstring, "-32602")
		So(w.Body.String(), ShouldContainSubstring, "缺少 id")
	})
}

func TestHookMCP_UnknownTool(t *testing.T) {
	Convey("非 hook 工具名 → -32601 unknown tool", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()
		lookup := mock_hooktool_svc.NewMockAgentLookup(ctrl)
		lookup.EXPECT().Find(gomock.Any(), int64(7)).Return(hookEnabledAgent(7), nil)
		s := newTestSvc(lookup, mock_hooktool_svc.NewMockHookService(ctrl))
		token := s.mcpHandlerInit().MintToken(7, 99)
		w := rpcCall(s.MCPHandler(), `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"not_a_hook_tool","arguments":{}}}`, token)
		So(w.Code, ShouldEqual, http.StatusOK)
		So(w.Body.String(), ShouldContainSubstring, "unknown tool")
	})
}

func TestHookMCP_BuildTurnMCP(t *testing.T) {
	Convey("BuildTurnMCP", t, func() {
		s := newTestSvc(nil, nil)
		s.SetGatewayBaseURL("http://127.0.0.1:52401")

		Convey("hook 开关 ON → 返回 1 个 spec(URL/header token/6 个 Tools)", func() {
			specs := s.BuildTurnMCP(context.Background(), hookEnabledAgent(7), 99, 0)
			So(len(specs), ShouldEqual, 1)
			So(specs[0].Name, ShouldEqual, "hook")
			So(specs[0].URL, ShouldEqual, "http://127.0.0.1:52401/mcp/hook/")
			So(specs[0].Headers["Authorization"], ShouldStartWith, "Bearer ")
			So(len(specs[0].Tools), ShouldEqual, 6)
			tok := strings.TrimPrefix(specs[0].Headers["Authorization"], "Bearer ")
			ref, ok := s.mcpHandlerInit().Lookup(tok)
			So(ok, ShouldBeTrue)
			So(ref, ShouldResemble, agenttool.Ref{AgentID: 7, SessionID: 99})
		})

		Convey("hook 开关 OFF → nil", func() {
			So(s.BuildTurnMCP(context.Background(), hookDisabledAgent(7), 99, 0), ShouldBeNil)
		})

		Convey("agent 为 nil → nil", func() {
			So(s.BuildTurnMCP(context.Background(), nil, 99, 0), ShouldBeNil)
		})
	})

	Convey("gatewayBaseURL 未配置 → nil(即使开关 ON)", t, func() {
		s := newTestSvc(nil, nil)
		So(s.BuildTurnMCP(context.Background(), hookEnabledAgent(7), 99, 0), ShouldBeNil)
	})
}
