package remote

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/runtimes/remote/wire"
)

func TestHandleMCPProxy_DispatchesToRegisteredDispatcher(t *testing.T) {
	t.Cleanup(func() { RegisterMCPProxyDispatcher(nil) })

	var gotReq wire.MCPProxyRequest
	RegisterMCPProxyDispatcher(func(_ context.Context, req wire.MCPProxyRequest) (wire.MCPProxyResponse, error) {
		gotReq = req
		return wire.MCPProxyResponse{
			Status:  200,
			Headers: map[string][]string{"Content-Type": {"application/json"}},
			Body:    []byte(`{"jsonrpc":"2.0","id":1,"result":{}}`),
		}, nil
	})

	r := &Runtime{}
	raw, err := json.Marshal(wire.MCPProxyRequest{
		Path:    "/mcp/org/",
		Method:  "POST",
		Headers: map[string][]string{"Authorization": {"Bearer tok"}},
		Body:    []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`),
	})
	require.NoError(t, err)

	out, err := r.handleMCPProxy(context.Background(), raw)
	require.NoError(t, err)
	resp, ok := out.(wire.MCPProxyResponse)
	require.True(t, ok)
	require.Equal(t, 200, resp.Status)
	require.Equal(t, []byte(`{"jsonrpc":"2.0","id":1,"result":{}}`), resp.Body)
	// 转发的请求保真:path / method / 鉴权头原样到 dispatcher。
	require.Equal(t, "/mcp/org/", gotReq.Path)
	require.Equal(t, "POST", gotReq.Method)
	require.Equal(t, []string{"Bearer tok"}, gotReq.Headers["Authorization"])
}

// requireReadableToolError 断言这条应答就是 R17 要的那个形状:HTTP 200 + JSON-RPC error,
// id 与请求对得上,message 里三件事讲全(哪个能力、依赖发起端在线、别重试)。桌面端这一跳
// 与 daemon 那一跳答的必须是同一句话——daemon 收到后原样转给 CLI,模型分不出是哪一跳失败的。
func requireReadableToolError(t *testing.T, resp wire.MCPProxyResponse, wantID string) {
	t.Helper()
	require.Equal(t, 200, resp.Status,
		"非 2xx 会让 CLI 内嵌的 MCP 客户端把整条应答当传输层故障丢弃,body 里的话模型永远读不到")
	require.Equal(t, []string{"application/json"}, resp.Headers["Content-Type"])

	var parsed struct {
		JSONRPC string          `json:"jsonrpc"`
		ID      json.RawMessage `json:"id"`
		Result  json.RawMessage `json:"result"`
		Error   *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	require.NoError(t, json.Unmarshal(resp.Body, &parsed))
	require.Equal(t, "2.0", parsed.JSONRPC)
	require.Equal(t, json.RawMessage(wantID), parsed.ID, "MCP 客户端按 id 关联请求/应答")
	require.Nil(t, parsed.Result)
	require.NotNil(t, parsed.Error, "must be a JSON-RPC error object, not a bare status-code failure")
	require.Equal(t, -32000, parsed.Error.Code, "与 daemon 侧、内置工具 writeRPCError 同一个码")

	msg := parsed.Error.Message
	require.Contains(t, msg, "org", "names which capability is unavailable")
	require.Contains(t, msg, "offline", "states the dependency: the originating client must be online")
	require.Contains(t, msg, "do not retry", "tells the model to proceed instead of retry-looping")
}

// TestHandleMCPProxy_NoDispatcher_ReturnsReadableToolError 覆盖 R17 在桌面端这一跳的第一个
// 失败点:隧道请求送达了桌面端,但桌面端没装配 dispatcher,重放无从谈起。旧行为回
// `MCPProxyResponse{Status: 502, Body: "mcp proxy: desktop dispatcher unavailable"}`,daemon
// 原样透传给 CLI —— 那正是 R17 明令禁止的裸非 2xx,只是产生在更远的一跳上。
func TestHandleMCPProxy_NoDispatcher_ReturnsReadableToolError(t *testing.T) {
	t.Cleanup(func() { RegisterMCPProxyDispatcher(nil) })
	RegisterMCPProxyDispatcher(nil)

	r := &Runtime{}
	raw, err := json.Marshal(wire.MCPProxyRequest{
		Path:   "/mcp/org/",
		Method: "POST",
		Body:   []byte(`{"jsonrpc":"2.0","id":42,"method":"tools/call","params":{"name":"org_get"}}`),
	})
	require.NoError(t, err)

	out, err := r.handleMCPProxy(context.Background(), raw)
	require.NoError(t, err, "工具调用失败不该打挂整条 RPC 连接")
	resp, ok := out.(wire.MCPProxyResponse)
	require.True(t, ok)
	requireReadableToolError(t, resp, "42")
	require.NotContains(t, string(resp.Body), "mcp proxy:", "内部措辞不糊到模型脸上")
}

// TestHandleMCPProxy_ReplayFailed_ReturnsReadableToolError 覆盖桌面端这一跳的第二个失败点:
// dispatcher 装配了,但本机 gateway 重放失败(端口没起来 / 连接被拒)。对模型来说与上一个
// 一样:这次工具调用够不着发起端,没有结果。所以答的是同一句话,而不是把 Go 的 error 文本
// 塞进一个 502 body 里 —— 那对模型既不可读,也不构成可执行的建议。
func TestHandleMCPProxy_ReplayFailed_ReturnsReadableToolError(t *testing.T) {
	t.Cleanup(func() { RegisterMCPProxyDispatcher(nil) })
	RegisterMCPProxyDispatcher(func(context.Context, wire.MCPProxyRequest) (wire.MCPProxyResponse, error) {
		return wire.MCPProxyResponse{}, errors.New("dial tcp 127.0.0.1:52401: connect: connection refused")
	})

	r := &Runtime{}
	raw, err := json.Marshal(wire.MCPProxyRequest{
		Path:   "/mcp/org/",
		Method: "POST",
		Body:   []byte(`{"jsonrpc":"2.0","id":42,"method":"tools/call","params":{"name":"org_get"}}`),
	})
	require.NoError(t, err)

	out, err := r.handleMCPProxy(context.Background(), raw)
	require.NoError(t, err)
	resp, ok := out.(wire.MCPProxyResponse)
	require.True(t, ok)
	requireReadableToolError(t, resp, "42")
	require.NotContains(t, string(resp.Body), "connection refused",
		"传输层错误原文只留日志,不进给模型的应答")
}

// TestHandleMCPProxy_NoDispatcher_UnparsableBodyStillAnswers:body 不是合法 JSON-RPC(或没带
// id)时仍要答一个格式合法的 JSON-RPC error,id 退化成 null,而不是回落到旧的裸 502。
func TestHandleMCPProxy_NoDispatcher_UnparsableBodyStillAnswers(t *testing.T) {
	t.Cleanup(func() { RegisterMCPProxyDispatcher(nil) })
	RegisterMCPProxyDispatcher(nil)

	r := &Runtime{}
	raw, err := json.Marshal(wire.MCPProxyRequest{Path: "/mcp/org/", Method: "POST", Body: []byte("not json")})
	require.NoError(t, err)

	out, err := r.handleMCPProxy(context.Background(), raw)
	require.NoError(t, err)
	resp, ok := out.(wire.MCPProxyResponse)
	require.True(t, ok)
	requireReadableToolError(t, resp, "null")
}

func TestNewLocalGatewayDispatcher_ReplaysAgainstBaseURL(t *testing.T) {
	var gotPath, gotMethod, gotAuth string
	var gotBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotMethod, gotAuth = r.URL.Path, r.Method, r.Header.Get("Authorization")
		gotBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(201)
		_, _ = w.Write([]byte(`{"replayed":true}`))
	}))
	defer srv.Close()

	d := NewLocalGatewayDispatcher(func() string { return srv.URL }, srv.Client())
	resp, err := d(context.Background(), wire.MCPProxyRequest{
		Path:    "/mcp/org/",
		Method:  "POST",
		Headers: map[string][]string{"Authorization": {"Bearer tok"}},
		Body:    []byte(`{"q":1}`),
	})
	require.NoError(t, err)

	// desktop 本机 gateway 应答原样回传。
	require.Equal(t, 201, resp.Status)
	require.Equal(t, []byte(`{"replayed":true}`), resp.Body)
	require.Equal(t, "application/json", resp.Headers["Content-Type"][0])
	// 请求按 base+path 重放,method/鉴权头/body 保真。
	require.Equal(t, "/mcp/org/", gotPath)
	require.Equal(t, "POST", gotMethod)
	require.Equal(t, "Bearer tok", gotAuth)
	require.Equal(t, []byte(`{"q":1}`), gotBody)
}

func TestNewLocalGatewayDispatcher_EmptyBaseURLErrors(t *testing.T) {
	d := NewLocalGatewayDispatcher(func() string { return "" }, http.DefaultClient)
	_, err := d(context.Background(), wire.MCPProxyRequest{Path: "/mcp/org/", Method: "POST"})
	require.Error(t, err)
}
