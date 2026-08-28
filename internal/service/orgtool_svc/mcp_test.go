package orgtool_svc

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
	"github.com/agentre-hub/agentre/internal/service/department_svc"
	"github.com/agentre-hub/agentre/internal/service/orgtool_svc/mock_orgtool_svc"
)

// newTestSvc 构造一个全新的 orgtoolSvc(避免 Default() 单例跨测试串台),只接 AgentLookup
// 与 OrgQuery —— 本任务读路径只用到这两个依赖。
func newTestSvc(lookup AgentLookup, query OrgQuery) *orgtoolSvc {
	s := &orgtoolSvc{}
	s.RegisterDeps(query, nil, nil, lookup, nil)
	return s
}

// orgEnabledAgent 返回一个 org 开关 ON 的 agent。
func orgEnabledAgent(id int64) *agent_entity.Agent {
	a := &agent_entity.Agent{ID: id}
	a.SetTools([]agent_entity.AgentToolItem{{Key: "org", Enabled: true}})
	return a
}

// orgDisabledAgent 返回一个 org 开关 OFF 的 agent。
func orgDisabledAgent(id int64) *agent_entity.Agent {
	a := &agent_entity.Agent{ID: id}
	a.SetTools([]agent_entity.AgentToolItem{{Key: "org", Enabled: false}})
	return a
}

// rpcCall 发一次 JSON-RPC POST(可选 bearer token),返回 recorder。
func rpcCall(h http.Handler, body, token string) *httptest.ResponseRecorder {
	req := httptest.NewRequest("POST", "/mcp/org/", strings.NewReader(body))
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	return w
}

func TestOrgMCP_TokenRoundTrip(t *testing.T) {
	Convey("MintToken → lookup 解出原 (agent, session);篡改/格式非法 → 401", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()
		lookup := mock_orgtool_svc.NewMockAgentLookup(ctrl)
		query := mock_orgtool_svc.NewMockOrgQuery(ctrl)
		// 合法 token 走到 org_get → 需要 Find + Load 各一次。
		lookup.EXPECT().Find(gomock.Any(), int64(7)).Return(orgEnabledAgent(7), nil)
		query.EXPECT().Load(gomock.Any(), gomock.Any()).Return(&department_svc.LoadOrgResponse{}, nil)

		s := newTestSvc(lookup, query)
		h := s.MCPHandler()
		token := s.mcpHandlerInit().MintToken(7, 99)

		Convey("合法 token → 200", func() {
			w := rpcCall(h, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"org_get"}}`, token)
			So(w.Code, ShouldEqual, http.StatusOK)
		})
	})

	Convey("篡改签名 / 格式非法 token → 401(不调任何依赖)", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()
		lookup := mock_orgtool_svc.NewMockAgentLookup(ctrl) // 无 EXPECT:不该被调用
		s := newTestSvc(lookup, mock_orgtool_svc.NewMockOrgQuery(ctrl))
		h := s.MCPHandler()
		good := s.mcpHandlerInit().MintToken(7, 99)

		body := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"org_get"}}`
		So(rpcCall(h, body, good+"tampered").Code, ShouldEqual, http.StatusUnauthorized)
		So(rpcCall(h, body, "not-a-token").Code, ShouldEqual, http.StatusUnauthorized)
		So(rpcCall(h, body, "").Code, ShouldEqual, http.StatusUnauthorized)
	})
}

func TestOrgMCP_SwitchOffForbids(t *testing.T) {
	Convey("token 合法但 agent org 开关 OFF → 403,不查 org", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()
		lookup := mock_orgtool_svc.NewMockAgentLookup(ctrl)
		query := mock_orgtool_svc.NewMockOrgQuery(ctrl) // 无 Load EXPECT:不该被调用
		lookup.EXPECT().Find(gomock.Any(), int64(7)).Return(orgDisabledAgent(7), nil)

		s := newTestSvc(lookup, query)
		h := s.MCPHandler()
		token := s.mcpHandlerInit().MintToken(7, 99)

		w := rpcCall(h, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"org_get"}}`, token)
		So(w.Code, ShouldEqual, http.StatusForbidden)
	})

	Convey("Find 报错 / 返回 nil → 403", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()
		lookup := mock_orgtool_svc.NewMockAgentLookup(ctrl)
		lookup.EXPECT().Find(gomock.Any(), int64(7)).Return(nil, nil)
		s := newTestSvc(lookup, mock_orgtool_svc.NewMockOrgQuery(ctrl))
		token := s.mcpHandlerInit().MintToken(7, 99)
		w := rpcCall(s.MCPHandler(), `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"org_get"}}`, token)
		So(w.Code, ShouldEqual, http.StatusForbidden)
	})
}

func TestOrgMCP_DepsNotRegistered(t *testing.T) {
	Convey("bootstrap 窗口期(RegisterDeps 未执行)tools/call → 503 service unavailable,不 panic", t, func() {
		s := &orgtoolSvc{} // 未 RegisterDeps:gateway 已挂 handler 但 deps 还没接线
		token := s.mcpHandlerInit().MintToken(7, 99)
		w := rpcCall(s.MCPHandler(), `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"org_get"}}`, token)
		So(w.Code, ShouldEqual, http.StatusServiceUnavailable)
		So(w.Body.String(), ShouldContainSubstring, "service unavailable")
	})
}

func TestOrgMCP_InitializeAndToolsList(t *testing.T) {
	Convey("initialize 回显 protocolVersion + serverInfo(agentre-org)", t, func() {
		s := newTestSvc(nil, nil)
		w := rpcCall(s.MCPHandler(), `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25"}}`, "")
		So(w.Code, ShouldEqual, http.StatusOK)
		So(w.Body.String(), ShouldContainSubstring, "2025-11-25")
		So(w.Body.String(), ShouldContainSubstring, "agentre-org")
	})

	Convey("tools/list 暴露全部 7 个工具名", t, func() {
		s := newTestSvc(nil, nil)
		w := rpcCall(s.MCPHandler(), `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`, "")
		So(w.Code, ShouldEqual, http.StatusOK)
		body := w.Body.String()
		for _, name := range []string{
			"org_get",
			"org_create_department", "org_update_department", "org_delete_department",
			"org_create_agent", "org_update_agent", "org_delete_agent",
		} {
			So(body, ShouldContainSubstring, name)
		}
		// 写工具描述注明需审批
		So(body, ShouldContainSubstring, "需要用户审批")
	})
}

func TestOrgMCP_OrgGetReturnsLoadResult(t *testing.T) {
	Convey("org_get → content[0].text 是 LLM 投影:含部门/agent 挂载关系,不含头像 base64 等前端专用字段", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()
		lookup := mock_orgtool_svc.NewMockAgentLookup(ctrl)
		query := mock_orgtool_svc.NewMockOrgQuery(ctrl)
		lookup.EXPECT().Find(gomock.Any(), int64(7)).Return(orgEnabledAgent(7), nil)
		query.EXPECT().Load(gomock.Any(), gomock.Any()).Return(&department_svc.LoadOrgResponse{
			Departments: []*department_svc.DepartmentItem{{ID: 1, Name: "研发部", LeadAgentID: 7}},
			Agents: []*department_svc.AgentItem{{
				ID: 7, Name: "前端工程师", DepartmentID: 1, DepartmentName: "研发部",
				AvatarDataURL: "data:image/png;base64,AAAAAAAA", // 数百 KB 级头像,绝不能进 tool result
				Prompt:        []string{"你是前端"},
				Backend:       &department_svc.BackendSummary{ID: 3, Type: "claude-code", Name: "默认后端"},
			}},
			AvailableTools: []string{"org"},
		}, nil)

		s := newTestSvc(lookup, query)
		token := s.mcpHandlerInit().MintToken(7, 99)
		w := rpcCall(s.MCPHandler(), `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"org_get"}}`, token)

		So(w.Code, ShouldEqual, http.StatusOK)
		body := w.Body.String()
		So(body, ShouldContainSubstring, "研发部")
		So(body, ShouldContainSubstring, "前端工程师")
		So(body, ShouldContainSubstring, "claude-code")
		So(body, ShouldContainSubstring, `"type":"text"`)
		// LoadOrgResponse 是给 Wails 前端渲染的 DTO;LLM 投影必须丢弃前端专用字段。
		So(body, ShouldNotContainSubstring, "base64")
		So(body, ShouldNotContainSubstring, "avatarDataUrl")
		So(body, ShouldNotContainSubstring, "availableTools")
		So(body, ShouldNotContainSubstring, "你是前端") // prompt 不进 tool result
	})
}

func TestOrgMCP_WriteToolRouting(t *testing.T) {
	Convey("写工具经共享审批闸门(登记审批);非组织工具 → JSON-RPC error(-32601 unknown tool)", t, func() {
		Convey("org 写工具 → 走审批编排(BeginToolApproval 被调到),拒绝后返回成功体", func() {
			ctrl := gomock.NewController(t)
			defer ctrl.Finish()
			s, d := newWriteSvc(ctrl)
			d.lookup.EXPECT().Find(gomock.Any(), int64(7)).Return(orgEnabledAgent(7), nil)
			apvCh := beginCh(d, 99)
			d.apv.EXPECT().FinishToolApproval(gomock.Any(), int64(99), gomock.Any(), "denied", gomock.Any()).Return(nil)

			token := s.mcpHandlerInit().MintToken(7, 99)
			w, done := callWrite(s, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"org_create_department","arguments":{"name":"x"}}}`, token)
			apvCh <- false // 经 chat_svc 返回的 channel 模拟前端拒绝
			<-done
			So(w.Code, ShouldEqual, http.StatusOK)
		})

		Convey("非组织工具名 → -32601 unknown tool", func() {
			ctrl := gomock.NewController(t)
			defer ctrl.Finish()
			lookup := mock_orgtool_svc.NewMockAgentLookup(ctrl)
			lookup.EXPECT().Find(gomock.Any(), int64(7)).Return(orgEnabledAgent(7), nil)
			s := newTestSvc(lookup, mock_orgtool_svc.NewMockOrgQuery(ctrl))
			token := s.mcpHandlerInit().MintToken(7, 99)
			w := rpcCall(s.MCPHandler(), `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"not_an_org_tool","arguments":{}}}`, token)
			So(w.Code, ShouldEqual, http.StatusOK) // JSON-RPC error 仍是 HTTP 200
			So(w.Body.String(), ShouldContainSubstring, "unknown tool")
		})
	})
}

func TestOrgMCP_BuildTurnMCP(t *testing.T) {
	Convey("BuildTurnMCP", t, func() {
		s := newTestSvc(nil, nil)
		s.SetGatewayBaseURL("http://127.0.0.1:52401")

		Convey("org 开关 ON → 返回 1 个 spec(URL/header token/7 个 Tools)", func() {
			specs := s.BuildTurnMCP(context.Background(), orgEnabledAgent(7), 99, 0)
			So(len(specs), ShouldEqual, 1)
			So(specs[0].Name, ShouldEqual, "org")
			So(specs[0].URL, ShouldEqual, "http://127.0.0.1:52401/mcp/org/")
			So(specs[0].Headers["Authorization"], ShouldStartWith, "Bearer ")
			So(len(specs[0].Tools), ShouldEqual, 7)
			// header 里的 token 应能被本 handler 验签解出 (7, 99)
			tok := strings.TrimPrefix(specs[0].Headers["Authorization"], "Bearer ")
			ref, ok := s.mcpHandlerInit().Lookup(tok)
			So(ok, ShouldBeTrue)
			So(ref, ShouldResemble, agenttool.Ref{AgentID: 7, SessionID: 99})
		})

		Convey("org 开关 OFF → nil", func() {
			So(s.BuildTurnMCP(context.Background(), orgDisabledAgent(7), 99, 0), ShouldBeNil)
		})

		Convey("agent 为 nil → nil", func() {
			So(s.BuildTurnMCP(context.Background(), nil, 99, 0), ShouldBeNil)
		})
	})

	Convey("gatewayBaseURL 未配置 → nil(即使开关 ON)", t, func() {
		s := newTestSvc(nil, nil) // 没 SetGatewayBaseURL
		So(s.BuildTurnMCP(context.Background(), orgEnabledAgent(7), 99, 0), ShouldBeNil)
	})
}
