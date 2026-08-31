package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/agentre-hub/agentre/internal/daemon/protorpc"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/runtimes/remote/wire"
	"github.com/agentre-hub/agentre/pkg/wire/agentrewire"
)

func TestRewriteMCPServersForDaemon(t *testing.T) {
	specs := []agentruntime.MCPServerSpec{
		{
			Name:    "org",
			URL:     "http://127.0.0.1:52401/mcp/org/",
			Headers: map[string]string{"Authorization": "Bearer tok"},
			Tools:   []string{"org_get"},
		},
		{Name: "group", URL: "http://127.0.0.1:52401/mcp/group/", Tools: []string{"group_send"}},
	}

	out := rewriteMCPServersForDaemon(specs, func() string { return "http://127.0.0.1:7777" }, "sha256:peer-a", testConversationID)

	// 只换 scheme+host,保留 path,使 CLI 打到 daemon 本地隧道。
	require.Equal(t, "http://127.0.0.1:7777/mcp/org/?conversationId=00000000-0000-7000-8000-000000000042&peerFingerprint=sha256%3Apeer-a", out[0].URL)
	require.Equal(t, "http://127.0.0.1:7777/mcp/group/?conversationId=00000000-0000-7000-8000-000000000042&peerFingerprint=sha256%3Apeer-a", out[1].URL)
	// desktop 签的 token(Headers)+ tools + name 原样保留(token 在 desktop 侧校验)。
	require.Equal(t, "Bearer tok", out[0].Headers["Authorization"])
	require.Equal(t, []string{"org_get"}, out[0].Tools)
	require.Equal(t, "org", out[0].Name)
	// 不就地改原 slice(desktop 侧若复用同一 spec 不被污染)。
	require.Equal(t, "http://127.0.0.1:52401/mcp/org/", specs[0].URL)

	// 空 base / 空 specs / nil baseFn:原样返回,不炸。
	require.Equal(t, specs, rewriteMCPServersForDaemon(specs, func() string { return "" }, "sha256:peer-a", testConversationID))
	require.Equal(t, specs, rewriteMCPServersForDaemon(specs, nil, "sha256:peer-a", testConversationID))
	require.Nil(t, rewriteMCPServersForDaemon(nil, func() string { return "http://127.0.0.1:7777" }, "sha256:peer-a", testConversationID))
}

// fakeTunnelNotifier 实现 NotifierPort:记录反向 Request 的 method/params,按预置应答回填 result。
type fakeTunnelNotifier struct {
	gotMethod string
	gotParams any
	resp      wire.MCPProxyResponse
	err       error
}

func (f *fakeTunnelNotifier) Notify(*agentrewire.RpcNotification) error { return nil }
func (f *fakeTunnelNotifier) Request(_ context.Context, method string, params, result any) error {
	f.gotMethod = method
	f.gotParams = params
	if f.err != nil {
		return f.err
	}
	if rp, ok := result.(*wire.MCPProxyResponse); ok {
		*rp = f.resp
	}
	return nil
}

func TestMCPTunnelHandler_ForwardsRequestAndWritesResponse(t *testing.T) {
	fn := &fakeTunnelNotifier{resp: wire.MCPProxyResponse{
		Status:  200,
		Headers: map[string][]string{"Content-Type": {"application/json"}},
		Body:    []byte(`{"ok":true}`),
	}}
	var gotPeer string
	var gotConversationID string
	h := NewMCPTunnelHandler(func(peer string, conversationID string) NotifierPort {
		gotPeer, gotConversationID = peer, conversationID
		return fn
	})

	body := `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`
	req := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:7777/mcp/org/?conversationId=00000000-0000-7000-8000-000000000042&peerFingerprint=sha256%3Apeer-a", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer tok")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)

	// desktop 应答原样写回 CLI。
	require.Equal(t, 200, rec.Code)
	require.Equal(t, "application/json", rec.Header().Get("Content-Type"))
	require.Equal(t, `{"ok":true}`, rec.Body.String())

	// The local URL's explicit origin selects the peer/session, not a global
	// newest-connection heuristic.
	require.Equal(t, "sha256:peer-a", gotPeer)
	require.Equal(t, testConversationID, gotConversationID,
		"写进隧道 URL 的对话身份必须原样解回来 —— 这是本轮唯一一处身份离开进程边界的地方")
	// 经 MethodMCPProxy 反向请求转发,且请求保真(path/method/body/鉴权头)。
	require.Equal(t, wire.MethodMCPProxy, fn.gotMethod)
	fwd, ok := fn.gotParams.(wire.MCPProxyRequest)
	require.True(t, ok)
	require.Equal(t, "/mcp/org/", fwd.Path)
	require.Equal(t, http.MethodPost, fwd.Method)
	require.Equal(t, []byte(body), fwd.Body)
	require.Equal(t, []string{"Bearer tok"}, fwd.Headers["Authorization"])
	// 逐跳头 / Content-Length 不转发(desktop 重放时重算)。
	require.NotContains(t, fwd.Headers, "Content-Length")
}

// TestMCPTunnelHandler_NoActiveConn_ReturnsReadableToolError covers R17: when the
// tunnel has no live desktop connection to route to, the CLI subprocess's MCP client
// must get back something it can hand to the model as an ordinary tool-call error —
// not a bare HTTP 503 whose body a JSON-RPC/MCP client discards as a transport
// failure. The response must therefore be HTTP 200 carrying a JSON-RPC error object
// (the same shape org/subagent/hooktool's own writeRPCError uses for a real tool
// execution failure), correlated to the request's id, whose message names the
// unavailable capability, states the dependency on the originating client being
// online, and tells the model not to retry.
func TestMCPTunnelHandler_NoActiveConn_ReturnsReadableToolError(t *testing.T) {
	h := NewMCPTunnelHandler(func(string, string) NotifierPort { return nil })

	body := `{"jsonrpc":"2.0","id":42,"method":"tools/call","params":{"name":"org_get"}}`
	req := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:7777/mcp/org/", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)

	// Not a bare transport failure: HTTP 200 + JSON-RPC error is the shape an
	// MCP-over-HTTP client reads as an ordinary (readable) tool-call error instead
	// of discarding the body because the status line said "infrastructure fault".
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "application/json", rec.Header().Get("Content-Type"))

	var resp struct {
		JSONRPC string          `json:"jsonrpc"`
		ID      json.RawMessage `json:"id"`
		Result  json.RawMessage `json:"result"`
		Error   *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	require.Equal(t, "2.0", resp.JSONRPC)
	require.Equal(t, json.RawMessage("42"), resp.ID, "must echo the request id back so the MCP client correlates the response")
	require.Nil(t, resp.Result)
	require.NotNil(t, resp.Error, "must be a JSON-RPC error object, not a bare status-code failure")

	msg := resp.Error.Message
	require.Contains(t, msg, "org", "names which capability is unavailable")
	require.Contains(t, msg, "offline", "states the dependency: the originating client must be online")
	require.Contains(t, msg, "do not retry", "tells the model to proceed instead of retry-looping")
	require.NotContains(t, rec.Body.String(), "503", "must not leak the old bare-503 wording")
}

// TestMCPTunnelHandler_NoActiveConn_LogsTheDegradation 钉住 observability.md 的强制
// 埋点 3(降级 / 回落 / 重试):隧道无目标是这条路径唯一的降级分支,而它此刻**没有任何
// 其它可见痕迹**——应答只进 CLI 子进程,发起端按定义已经离线,daemon 侧运维想事后回答
// 「为什么 agent 说这个工具不可用」只能靠日志。对面同一跳的 desktop 侧
// (remote.handleMCPProxy 的无 dispatcher 分支)本来就打了这条 Warn。
func TestMCPTunnelHandler_NoActiveConn_LogsTheDegradation(t *testing.T) {
	var buf bytes.Buffer
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })

	h := NewMCPTunnelHandler(func(string, string) NotifierPort { return nil })
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "http://127.0.0.1:7777/mcp/org/",
		strings.NewReader(`{"jsonrpc":"2.0","id":42,"method":"tools/call","params":{"name":"org_get"}}`)))

	line := buf.String()
	require.Contains(t, line, "mcpproxy.tunnel:", "daemon 侧日志按 package.Method: 前缀,便于 grep")
	require.Contains(t, line, "/mcp/org/", "要能看出是哪个内置工具被拒")
	// 红线:请求 body 可能带工具入参(路径 / 内容),整包不进日志。
	require.NotContains(t, line, "org_get", "must not log the request body")
}

// TestMCPTunnelHandler_TargetLostMidCall_ReturnsReadableToolError 覆盖 R17 的第二个
// 失败点:目标连接是在隧道调用**途中**没的(桌面端进程被杀 / 链路被 RST,daemon 侧
// 等应答的那次 Call 拿到 rpc.ErrConnClosed)。对模型来说这与「调用之前就没有目标」
// 是同一件事——发起端不在线——所以答回 CLI 的必须是同一个可读形状。
//
// 真机复现里这里回的是 `502 text/plain: mcp tunnel: rpc: connection closed`,正是
// R17 明令禁止的裸 HTTP 形状:非 2xx 会让 CLI 内嵌的 MCP 客户端把整条应答当传输层
// 故障丢弃,模型最后只拿到一句读不懂的基础设施错误。
func TestMCPTunnelHandler_TargetLostMidCall_ReturnsReadableToolError(t *testing.T) {
	var buf bytes.Buffer
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })

	fn := &fakeTunnelNotifier{err: protorpc.ErrConnClosed}
	h := NewMCPTunnelHandler(func(string, string) NotifierPort { return fn })

	body := `{"jsonrpc":"2.0","id":42,"method":"tools/call","params":{"name":"org_get"}}`
	req := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:7777/mcp/org/?conversationId=00000000-0000-7000-8000-000000000042&peerFingerprint=sha256%3Apeer-a", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code, "不能回裸 HTTP 错误:body 里的话模型就读不到了")
	require.Equal(t, "application/json", rec.Header().Get("Content-Type"))

	var resp struct {
		JSONRPC string          `json:"jsonrpc"`
		ID      json.RawMessage `json:"id"`
		Result  json.RawMessage `json:"result"`
		Error   *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	require.Equal(t, "2.0", resp.JSONRPC)
	require.Equal(t, json.RawMessage("42"), resp.ID, "must echo the request id back so the MCP client correlates the response")
	require.Nil(t, resp.Result)
	require.NotNil(t, resp.Error, "must be a JSON-RPC error object, not a bare status-code failure")

	msg := resp.Error.Message
	require.Contains(t, msg, "org", "names which capability is unavailable")
	require.Contains(t, msg, "offline", "states the dependency: the originating client must be online")
	require.Contains(t, msg, "do not retry", "tells the model to proceed instead of retry-looping")
	require.NotContains(t, rec.Body.String(), "rpc: connection closed",
		"传输层错误原文不糊到模型脸上——它对模型不可读,也不构成可执行的建议")

	// 降级留痕(observability.md 埋点 3):真实原因(那条连接怎么没的)只剩日志知道,
	// 应答里已经换成给模型看的话。红线同无目标分支:body 可能带工具入参,不进日志。
	line := buf.String()
	require.Contains(t, line, "mcpproxy.tunnel:", "daemon 侧日志按 package.Method: 前缀,便于 grep")
	require.Contains(t, line, `"org"`, "要能看出是哪个内置工具被拒")
	require.Contains(t, line, "connection closed", "真实失败原因留在 daemon 日志里")
	require.NotContains(t, line, "org_get", "must not log the request body")
}

// TestMCPTunnelHandler_NoActiveConn_UnparsableBodyStillAnswers covers the edge case
// where the request body isn't valid JSON-RPC (or has no id): the handler must still
// answer with a well-formed JSON-RPC error (id degrades to null) instead of panicking
// or falling back to the old bare-503 behavior.
func TestMCPTunnelHandler_NoActiveConn_UnparsableBodyStillAnswers(t *testing.T) {
	h := NewMCPTunnelHandler(func(string, string) NotifierPort { return nil })
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "http://127.0.0.1:7777/mcp/org/", strings.NewReader("not json")))

	require.Equal(t, http.StatusOK, rec.Code)
	var resp struct {
		ID    json.RawMessage           `json:"id"`
		Error *struct{ Message string } `json:"error"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	require.Equal(t, json.RawMessage("null"), resp.ID)
	require.NotNil(t, resp.Error)
}

// testConversationID 是这些用例里那条对话的身份。写死一个可读的合法 uuid:隧道 URL
// 这一跳要证的是"写进去的与解回来的逐字相同",不是它长什么样。
const testConversationID = "00000000-0000-7000-8000-000000000042"
