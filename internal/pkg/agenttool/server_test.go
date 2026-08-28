package agenttool

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	. "github.com/smartystreets/goconvey/convey"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_entity"
)

// agentWithTool 造一个内置工具开关为 enabled 的 agent。
func agentWithTool(id int64, key string, enabled bool) *agent_entity.Agent {
	a := &agent_entity.Agent{ID: id}
	a.SetTools([]agent_entity.AgentToolItem{{Key: key, Enabled: enabled}})
	return a
}

// recordingApproval 是 WriteGate 的测试替身:记录 Begin/Finish 的入参,审批应答由测试
// 往 ch 里 push(不 push 即触发超时分支)。
type recordingApproval struct {
	mu        sync.Mutex
	ch        chan bool
	beginErr  error
	begins    []beginCall
	finishes  []finishCall
	execCalls []execCall
	execOut   string
	execErr   error
}

type beginCall struct {
	sessionID int64
	requestID string
	tool      string
	input     map[string]any
}

type finishCall struct {
	sessionID      int64
	requestID      string
	status, result string
	ctxDone        bool
}

type execCall struct {
	ref  Ref
	tool string
	args string
}

func newRecordingApproval() *recordingApproval {
	return &recordingApproval{ch: make(chan bool, 1), execOut: "已执行"}
}

func (a *recordingApproval) gate(timeout time.Duration) *WriteGate {
	return &WriteGate{
		Timeout: func() time.Duration { return timeout },
		Begin: func(_ context.Context, sessionID int64, requestID, tool string, input map[string]any) (<-chan bool, error) {
			a.mu.Lock()
			a.begins = append(a.begins, beginCall{sessionID: sessionID, requestID: requestID, tool: tool, input: input})
			a.mu.Unlock()
			if a.beginErr != nil {
				return nil, a.beginErr
			}
			return a.ch, nil
		},
		Finish: func(ctx context.Context, sessionID int64, requestID, status, result string) error {
			a.mu.Lock()
			a.finishes = append(a.finishes, finishCall{
				sessionID: sessionID, requestID: requestID, status: status, result: result,
				ctxDone: ctx.Err() != nil,
			})
			a.mu.Unlock()
			return nil
		},
		Exec: func(_ context.Context, ref Ref, tool string, rawArgs json.RawMessage) (string, error) {
			a.mu.Lock()
			a.execCalls = append(a.execCalls, execCall{ref: ref, tool: tool, args: string(rawArgs)})
			a.mu.Unlock()
			return a.execOut, a.execErr
		},
	}
}

func (a *recordingApproval) snapshot() ([]beginCall, []finishCall, []execCall) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]beginCall(nil), a.begins...), append([]finishCall(nil), a.finishes...), append([]execCall(nil), a.execCalls...)
}

// orgLikeServer 造一个 org 形状的 server(读工具 org_get + 注册表里其余全是写工具)。
func orgLikeServer(lookup AgentLookupFunc, gate *WriteGate) *Server {
	return NewServer(ServerConfig{
		ToolKey:     KeyOrg,
		ServerName:  "agentre-org",
		Schemas:     []any{map[string]any{"name": "org_get"}},
		Ready:       func() bool { return true },
		LookupAgent: lookup,
		Read: map[string]ReadHandler{
			"org_get": func(_ context.Context, ref Ref, _ json.RawMessage) (string, error) {
				return "org of agent " + strconv.FormatInt(ref.AgentID, 10), nil
			},
		},
		Write: gate,
	})
}

// call 发一次 JSON-RPC POST(可选 bearer token)。
func call(h http.Handler, body, token string) *httptest.ResponseRecorder {
	req := httptest.NewRequest("POST", "/mcp/x/", strings.NewReader(body))
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	return w
}

// TestServer_TokenAuthorizesOnlyItsOwnResource 是本共享缝最重要的断言:令牌只对签发它
// 的那个 server 与那一对 (agent, session) 有效。lookup 若在这里出错就是提权,不是风格问题。
func TestServer_TokenAuthorizesOnlyItsOwnResource(t *testing.T) {
	Convey("Given 两个各自签发令牌的工具服务器(org 与 hook)", t, func() {
		var orgFinds, hookFinds []int64
		var mu sync.Mutex
		orgSrv := NewServer(ServerConfig{
			ToolKey: KeyOrg, ServerName: "agentre-org", Ready: func() bool { return true },
			LookupAgent: func(_ context.Context, id int64) (*agent_entity.Agent, error) {
				mu.Lock()
				orgFinds = append(orgFinds, id)
				mu.Unlock()
				return agentWithTool(id, KeyOrg, true), nil
			},
			Read: map[string]ReadHandler{"org_get": func(context.Context, Ref, json.RawMessage) (string, error) { return "org", nil }},
		})
		hookSrv := NewServer(ServerConfig{
			ToolKey: KeyHook, ServerName: "agentre-hook", Ready: func() bool { return true },
			LookupAgent: func(_ context.Context, id int64) (*agent_entity.Agent, error) {
				mu.Lock()
				hookFinds = append(hookFinds, id)
				mu.Unlock()
				return agentWithTool(id, KeyHook, true), nil
			},
			Read: map[string]ReadHandler{"hook_list": func(context.Context, Ref, json.RawMessage) (string, error) { return "hooks", nil }},
		})

		orgToken := orgSrv.MintToken(7, 99)
		hookToken := hookSrv.MintToken(7, 99)

		Convey("When 持 org 令牌调用 hook 服务器, Then 401 且不查任何 agent", func() {
			w := call(hookSrv, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"hook_list"}}`, orgToken)
			So(w.Code, ShouldEqual, http.StatusUnauthorized)
			mu.Lock()
			defer mu.Unlock()
			So(hookFinds, ShouldBeEmpty)
		})

		Convey("When 持 hook 令牌调用 org 服务器, Then 401 且不查任何 agent", func() {
			w := call(orgSrv, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"org_get"}}`, hookToken)
			So(w.Code, ShouldEqual, http.StatusUnauthorized)
			mu.Lock()
			defer mu.Unlock()
			So(orgFinds, ShouldBeEmpty)
		})

		Convey("Then 两个服务器对同一 (agent, session) 签出的令牌不相同(各自 per-instance 密钥)", func() {
			So(orgToken, ShouldNotEqual, hookToken)
		})

		Convey("When 持本服务器的令牌, Then 200 且查的是令牌里的 agent", func() {
			w := call(orgSrv, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"org_get"}}`, orgToken)
			So(w.Code, ShouldEqual, http.StatusOK)
			mu.Lock()
			defer mu.Unlock()
			So(orgFinds, ShouldResemble, []int64{7})
		})
	})

	Convey("Given 同一个服务器为不同 (agent, session) 签发的令牌", t, func() {
		srv := orgLikeServer(func(_ context.Context, id int64) (*agent_entity.Agent, error) {
			return agentWithTool(id, KeyOrg, true), nil
		}, nil)

		Convey("Then 令牌解出的就是签发时那一对,换 agent 或换 session 都是另一个令牌", func() {
			ref, ok := srv.Lookup(srv.MintToken(7, 99))
			So(ok, ShouldBeTrue)
			So(ref, ShouldResemble, Ref{AgentID: 7, SessionID: 99})
			So(srv.MintToken(8, 99), ShouldNotEqual, srv.MintToken(7, 99))
			So(srv.MintToken(7, 100), ShouldNotEqual, srv.MintToken(7, 99))
			So(srv.MintToken(7, 99), ShouldEqual, srv.MintToken(7, 99)) // 确定性:复用轮不重发
		})

		Convey("Then 签名被篡改 / 载荷被替换 / 非法格式的令牌一律不认", func() {
			good := srv.MintToken(7, 99)
			payload, sig, _ := strings.Cut(good, ".")
			forged, _, _ := strings.Cut(srv.MintToken(8, 42), ".")
			So(ok(srv.Lookup(good+"tampered")), ShouldBeFalse)
			So(ok(srv.Lookup(forged+"."+sig)), ShouldBeFalse) // 换载荷保原签名
			So(ok(srv.Lookup(payload)), ShouldBeFalse)        // 无签名段
			So(ok(srv.Lookup("not-a-token")), ShouldBeFalse)
			So(ok(srv.Lookup("")), ShouldBeFalse)
			So(ok(srv.Lookup("!!!.xxx")), ShouldBeFalse) // 非 base64 载荷
		})
	})

	Convey("Given 令牌绑定 agent 8(其 org 开关 OFF)而请求参数自称 agent 7", t, func() {
		srv := orgLikeServer(func(_ context.Context, id int64) (*agent_entity.Agent, error) {
			return agentWithTool(id, KeyOrg, id == 7), nil // 只有 7 开着
		}, nil)
		w := call(srv, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"org_get","arguments":{"agentId":7}}}`, srv.MintToken(8, 99))
		Convey("Then 403 —— 开关校验只看令牌里的 agent,不看请求参数", func() {
			So(w.Code, ShouldEqual, http.StatusForbidden)
		})
	})

	Convey("Given 令牌绑定 session 99 而写工具参数自称 session 100", t, func() {
		apv := newRecordingApproval()
		srv := orgLikeServer(func(_ context.Context, id int64) (*agent_entity.Agent, error) {
			return agentWithTool(id, KeyOrg, true), nil
		}, apv.gate(time.Minute))
		apv.ch <- true
		w := call(srv, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"org_create_agent","arguments":{"sessionId":100}}}`, srv.MintToken(7, 99))

		Convey("Then 审批登记与执行都落在令牌绑定的 session 99 / agent 7", func() {
			So(w.Code, ShouldEqual, http.StatusOK)
			begins, _, execs := apv.snapshot()
			So(len(begins), ShouldEqual, 1)
			So(begins[0].sessionID, ShouldEqual, 99)
			So(len(execs), ShouldEqual, 1)
			So(execs[0].ref, ShouldResemble, Ref{AgentID: 7, SessionID: 99})
		})
	})
}

// ok 取 (Ref, bool) 的第二个返回值。
func ok(_ Ref, valid bool) bool { return valid }

func TestServer_Envelope(t *testing.T) {
	srv := orgLikeServer(nil, nil)

	Convey("Given MCP-over-HTTP 信封", t, func() {
		Convey("When GET(claude 尝试开 server→client SSE), Then 405", func() {
			req := httptest.NewRequest("GET", "/mcp/x/", nil)
			w := httptest.NewRecorder()
			srv.ServeHTTP(w, req)
			So(w.Code, ShouldEqual, http.StatusMethodNotAllowed)
		})

		Convey("When body 不是合法 JSON, Then -32700 parse error", func() {
			w := call(srv, `{not json`, "")
			So(w.Body.String(), ShouldContainSubstring, "-32700")
			So(w.Body.String(), ShouldContainSubstring, "parse error")
		})

		Convey("When initialize 带 protocolVersion, Then 回显该版本与 serverInfo", func() {
			w := call(srv, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25"}}`, "")
			So(w.Code, ShouldEqual, http.StatusOK)
			So(w.Body.String(), ShouldContainSubstring, "2025-11-25")
			So(w.Body.String(), ShouldContainSubstring, "agentre-org")
			So(w.Body.String(), ShouldContainSubstring, `"listChanged":false`)
		})

		Convey("When initialize 未带 protocolVersion, Then 用默认 2025-06-18", func() {
			w := call(srv, `{"jsonrpc":"2.0","id":1,"method":"initialize"}`, "")
			So(w.Body.String(), ShouldContainSubstring, "2025-06-18")
		})

		Convey("When notifications/initialized, Then 202", func() {
			w := call(srv, `{"jsonrpc":"2.0","method":"notifications/initialized"}`, "")
			So(w.Code, ShouldEqual, http.StatusAccepted)
		})

		Convey("When tools/list, Then 返回调用方给的 schema 列表(无需令牌)", func() {
			w := call(srv, `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`, "")
			So(w.Code, ShouldEqual, http.StatusOK)
			So(w.Body.String(), ShouldContainSubstring, "org_get")
		})

		Convey("When 未知方法, Then -32601 method not found", func() {
			w := call(srv, `{"jsonrpc":"2.0","id":1,"method":"resources/list"}`, "")
			So(w.Body.String(), ShouldContainSubstring, "method not found")
			So(w.Body.String(), ShouldContainSubstring, "-32601")
		})
	})
}

func TestServer_ToolsCallGuards(t *testing.T) {
	body := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"org_get"}}`

	Convey("Given bootstrap 窗口期(依赖未接线)", t, func() {
		var looked bool
		srv := orgLikeServer(func(context.Context, int64) (*agent_entity.Agent, error) {
			looked = true
			return nil, nil
		}, nil)
		srv.cfg.Ready = func() bool { return false }

		Convey("When 持合法令牌 tools/call, Then 503 service unavailable 且不查 agent", func() {
			w := call(srv, body, srv.MintToken(7, 99))
			So(w.Code, ShouldEqual, http.StatusServiceUnavailable)
			So(w.Body.String(), ShouldContainSubstring, "service unavailable")
			So(looked, ShouldBeFalse)
		})

		Convey("When 令牌非法, Then 先判 401(鉴权在可用性之前)", func() {
			So(call(srv, body, "bogus").Code, ShouldEqual, http.StatusUnauthorized)
		})
	})

	Convey("Given 工具开关的实时校验", t, func() {
		Convey("When agent 的该工具开关 OFF, Then 403 且不进读处理器", func() {
			var entered bool
			srv := NewServer(ServerConfig{
				ToolKey: KeyOrg, ServerName: "agentre-org", Ready: func() bool { return true },
				LookupAgent: func(_ context.Context, id int64) (*agent_entity.Agent, error) {
					return agentWithTool(id, KeyOrg, false), nil
				},
				Read: map[string]ReadHandler{"org_get": func(context.Context, Ref, json.RawMessage) (string, error) {
					entered = true
					return "", nil
				}},
			})
			w := call(srv, body, srv.MintToken(7, 99))
			So(w.Code, ShouldEqual, http.StatusForbidden)
			So(entered, ShouldBeFalse)
		})

		Convey("When agent 查不到(nil), Then 403", func() {
			srv := orgLikeServer(func(context.Context, int64) (*agent_entity.Agent, error) { return nil, nil }, nil)
			So(call(srv, body, srv.MintToken(7, 99)).Code, ShouldEqual, http.StatusForbidden)
		})

		Convey("When 查 agent 报错, Then 403", func() {
			srv := orgLikeServer(func(context.Context, int64) (*agent_entity.Agent, error) {
				return agentWithTool(7, KeyOrg, true), errors.New("db down")
			}, nil)
			So(call(srv, body, srv.MintToken(7, 99)).Code, ShouldEqual, http.StatusForbidden)
		})
	})
}

func TestServer_ToolDispatch(t *testing.T) {
	enabled := func(_ context.Context, id int64) (*agent_entity.Agent, error) {
		return agentWithTool(id, KeyOrg, true), nil
	}

	Convey("Given 只读处理器表与写工具闸门", t, func() {
		Convey("When 命中只读处理器, Then 其文本进 MCP text content", func() {
			srv := orgLikeServer(enabled, nil)
			w := call(srv, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"org_get"}}`, srv.MintToken(7, 99))
			So(w.Code, ShouldEqual, http.StatusOK)
			So(w.Body.String(), ShouldContainSubstring, `"type":"text"`)
			So(w.Body.String(), ShouldContainSubstring, "org of agent 7")
		})

		Convey("When 只读处理器返回普通 error, Then -32000 携其文案", func() {
			srv := NewServer(ServerConfig{
				ToolKey: KeyOrg, ServerName: "agentre-org", Ready: func() bool { return true }, LookupAgent: enabled,
				Read: map[string]ReadHandler{"org_get": func(context.Context, Ref, json.RawMessage) (string, error) {
					return "", errors.New("load failed")
				}},
			})
			w := call(srv, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"org_get"}}`, srv.MintToken(7, 99))
			So(w.Code, ShouldEqual, http.StatusOK) // JSON-RPC error 仍是 HTTP 200
			So(w.Body.String(), ShouldContainSubstring, "-32000")
			So(w.Body.String(), ShouldContainSubstring, "load failed")
		})

		Convey("When 只读处理器返回 InvalidParams, Then -32602", func() {
			srv := NewServer(ServerConfig{
				ToolKey: KeyOrg, ServerName: "agentre-org", Ready: func() bool { return true }, LookupAgent: enabled,
				Read: map[string]ReadHandler{"org_get": func(context.Context, Ref, json.RawMessage) (string, error) {
					return "", InvalidParams("缺少 id")
				}},
			})
			w := call(srv, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"org_get"}}`, srv.MintToken(7, 99))
			So(w.Body.String(), ShouldContainSubstring, "-32602")
			So(w.Body.String(), ShouldContainSubstring, "缺少 id")
		})

		Convey("When 工具名不在注册表里, Then -32601 unknown tool 且不登记审批", func() {
			apv := newRecordingApproval()
			srv := orgLikeServer(enabled, apv.gate(time.Minute))
			w := call(srv, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"not_an_org_tool"}}`, srv.MintToken(7, 99))
			So(w.Code, ShouldEqual, http.StatusOK)
			So(w.Body.String(), ShouldContainSubstring, "unknown tool")
			begins, _, _ := apv.snapshot()
			So(begins, ShouldBeEmpty)
		})

		Convey("When 注册表里的写工具名但本 server 没配写闸门, Then -32601 unknown tool", func() {
			srv := orgLikeServer(enabled, nil)
			w := call(srv, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"org_create_agent"}}`, srv.MintToken(7, 99))
			So(w.Body.String(), ShouldContainSubstring, "unknown tool")
		})
	})
}

func TestServer_WriteApprovalGate(t *testing.T) {
	enabled := func(_ context.Context, id int64) (*agent_entity.Agent, error) {
		return agentWithTool(id, KeyOrg, true), nil
	}
	const writeBody = `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"org_create_department","arguments":{"name":"市场部"}}}`

	Convey("Given 一个写工具调用挂在审批闸门上", t, func() {
		Convey("When 用户批准, Then 执行并以 approved 收尾,结果回给 agent", func() {
			apv := newRecordingApproval()
			srv := orgLikeServer(enabled, apv.gate(time.Minute))
			apv.ch <- true
			w := call(srv, writeBody, srv.MintToken(7, 99))

			So(w.Code, ShouldEqual, http.StatusOK)
			So(w.Body.String(), ShouldContainSubstring, "已执行")
			begins, finishes, execs := apv.snapshot()
			So(len(begins), ShouldEqual, 1)
			So(begins[0].tool, ShouldEqual, "org_create_department")
			So(begins[0].input["name"], ShouldEqual, "市场部")
			So(begins[0].requestID, ShouldNotBeEmpty)
			So(len(execs), ShouldEqual, 1)
			So(execs[0].tool, ShouldEqual, "org_create_department")
			So(len(finishes), ShouldEqual, 1)
			So(finishes[0].status, ShouldEqual, "approved")
			So(finishes[0].requestID, ShouldEqual, begins[0].requestID)
			So(finishes[0].result, ShouldEqual, "已执行")
		})

		Convey("When 用户拒绝, Then 不执行,以 denied 收尾并告知 agent", func() {
			apv := newRecordingApproval()
			srv := orgLikeServer(enabled, apv.gate(time.Minute))
			apv.ch <- false
			w := call(srv, writeBody, srv.MintToken(7, 99))

			So(w.Code, ShouldEqual, http.StatusOK)
			So(w.Body.String(), ShouldContainSubstring, "用户拒绝了此操作")
			_, finishes, execs := apv.snapshot()
			So(execs, ShouldBeEmpty)
			So(len(finishes), ShouldEqual, 1)
			So(finishes[0].status, ShouldEqual, "denied")
		})

		Convey("When 批准后执行报业务错, Then 仍是 approved 终态,错误进结果供 agent 纠错", func() {
			apv := newRecordingApproval()
			apv.execErr = errors.New("循环挂载")
			srv := orgLikeServer(enabled, apv.gate(time.Minute))
			apv.ch <- true
			w := call(srv, writeBody, srv.MintToken(7, 99))

			So(w.Body.String(), ShouldContainSubstring, "已批准但执行失败: 循环挂载")
			_, finishes, _ := apv.snapshot()
			So(len(finishes), ShouldEqual, 1)
			So(finishes[0].status, ShouldEqual, "approved")
			So(finishes[0].result, ShouldContainSubstring, "执行失败: 循环挂载")
		})

		Convey("When 用户一直不应答, Then 超时以 expired 收尾且不执行", func() {
			apv := newRecordingApproval()
			srv := orgLikeServer(enabled, apv.gate(30*time.Millisecond))
			w := call(srv, writeBody, srv.MintToken(7, 99))

			So(w.Body.String(), ShouldContainSubstring, "审批超时")
			_, finishes, execs := apv.snapshot()
			So(execs, ShouldBeEmpty)
			So(len(finishes), ShouldEqual, 1)
			So(finishes[0].status, ShouldEqual, "expired")
		})

		Convey("When 审批通道开不出来(无活跃 turn), Then -32000 审批通道不可用,不挂起", func() {
			apv := newRecordingApproval()
			apv.beginErr = errors.New("no active turn")
			srv := orgLikeServer(enabled, apv.gate(time.Minute))
			w := call(srv, writeBody, srv.MintToken(7, 99))

			So(w.Code, ShouldEqual, http.StatusOK)
			So(w.Body.String(), ShouldContainSubstring, "审批通道不可用: no active turn")
			_, finishes, execs := apv.snapshot()
			So(finishes, ShouldBeEmpty)
			So(execs, ShouldBeEmpty)
		})

		Convey("When 请求在挂起中被中断, Then 用未取消的 ctx 以 expired 收尾", func() {
			apv := newRecordingApproval()
			srv := orgLikeServer(enabled, apv.gate(time.Minute))
			ctx, cancel := context.WithCancel(context.Background())
			req := httptest.NewRequest("POST", "/mcp/x/", strings.NewReader(writeBody)).WithContext(ctx)
			req.Header.Set("Authorization", "Bearer "+srv.MintToken(7, 99))
			done := make(chan struct{})
			go func() {
				srv.ServeHTTP(httptest.NewRecorder(), req)
				close(done)
			}()
			// 等 Begin 落记录后再取消,确保打断的是挂起中的那一段。
			for {
				if begins, _, _ := apv.snapshot(); len(begins) == 1 {
					break
				}
				time.Sleep(time.Millisecond)
			}
			cancel()
			<-done

			_, finishes, execs := apv.snapshot()
			So(execs, ShouldBeEmpty)
			So(len(finishes), ShouldEqual, 1)
			So(finishes[0].status, ShouldEqual, "expired")
			So(finishes[0].ctxDone, ShouldBeFalse) // 请求 ctx 已死 → 必须换未取消的 ctx 才写得进去
		})
	})
}
