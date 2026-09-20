package acp

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	cagoblocks "github.com/cago-frame/agents/agent/blocks"
	acpsdk "github.com/coder/acp-go-sdk"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
)

// TestNegotiation_RejectsProtocolV2 对端回 protocolVersion=2 时:连接关掉、返回
// 可读错误,绝不硬按 v1 继续解析(那只会得到一屏莫名解析失败),也不发
// session/new。
func TestNegotiation_RejectsProtocolV2(t *testing.T) {
	fa := installFakeAgent(t, fakeAgentOptions{protocolVersion: 2})
	r := New()
	_, _, err := r.Run(context.Background(), agentruntime.RunRequest{
		Backend: acpTestBackend(), SessionID: 51, UserText: "hi", Cwd: t.TempDir(),
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "protocol version 2")
	assert.Contains(t, err.Error(), "only v1")
	assert.Equal(t, 0, fa.newSessionCalls(), "a v2 peer must not get past the handshake")
}

// TestNegotiation_ClientAdvertisesNoClientFS initialize 请求不宣告
// fs.readTextFile / fs.writeTextFile / terminal,clientInfo 名为 agentre。
func TestNegotiation_ClientAdvertisesNoClientFS(t *testing.T) {
	fa := installFakeAgent(t, fakeAgentOptions{})
	r := New()
	events, _, err := r.Run(context.Background(), agentruntime.RunRequest{
		Backend: acpTestBackend(), SessionID: 52, UserText: "hi", Cwd: t.TempDir(),
	})
	require.NoError(t, err)
	fa.waitPrompt(2 * time.Second)
	fa.respondPrompt(acpsdk.StopReasonEndTurn, nil)
	drainEvents(t, events)

	init := fa.initializeRequest()
	assert.Equal(t, acpsdk.ProtocolVersion(1), init.ProtocolVersion,
		"the client must advertise the highest version it supports (=1)")
	assert.False(t, init.ClientCapabilities.Fs.ReadTextFile)
	assert.False(t, init.ClientCapabilities.Fs.WriteTextFile)
	assert.False(t, init.ClientCapabilities.Terminal)
	require.NotNil(t, init.ClientInfo)
	assert.Equal(t, "agentre", init.ClientInfo.Name)
}

// TestNegotiation_ImageBlocks 图片输入按协商结果使用:协商到 image=true 时
// 渲染成 ACP image ContentBlock 发出去;协商不到时该轮返回可读错误(绝不静默丢图)。
func TestNegotiation_ImageBlocks(t *testing.T) {
	imageReq := func() agentruntime.RunRequest {
		return agentruntime.RunRequest{
			Backend: acpTestBackend(), SessionID: 53, UserText: "", Cwd: t.TempDir(),
			UserBlocks: []cagoblocks.ContentBlock{
				cagoblocks.TextBlock{Text: "look"},
				cagoblocks.ImageBlock{MediaType: "image/png", Source: cagoblocks.BlobSource{Inline: []byte("pngbytes")}},
			},
		}
	}

	t.Run("negotiated image sends an image content block", func(t *testing.T) {
		fa := installFakeAgent(t, fakeAgentOptions{promptImage: true})
		r := New()
		events, _, err := r.Run(context.Background(), imageReq())
		require.NoError(t, err)
		prompt := fa.waitPrompt(2 * time.Second)
		require.Len(t, prompt.Prompt, 2)
		require.NotNil(t, prompt.Prompt[0].Text)
		require.NotNil(t, prompt.Prompt[1].Image)
		assert.Equal(t, "cG5nYnl0ZXM=", prompt.Prompt[1].Image.Data, "inline bytes go base64")
		assert.Equal(t, "image/png", prompt.Prompt[1].Image.MimeType)
		fa.respondPrompt(acpsdk.StopReasonEndTurn, nil)
		drainEvents(t, events)
	})

	t.Run("not negotiated fails the turn with a readable error", func(t *testing.T) {
		fa := installFakeAgent(t, fakeAgentOptions{promptImage: false})
		r := New()
		_, _, err := r.Run(context.Background(), imageReq())
		require.Error(t, err)
		assert.Contains(t, err.Error(), "does not accept image input")
		// 握手已过、prompt 未发:agent 没收到 session/prompt。
		select {
		case <-fa.promptSeen:
			t.Fatal("no prompt must be sent for an image-carrying turn")
		case <-time.After(100 * time.Millisecond):
		}
	})
}

// TestNegotiation_MCPServers MCP 注入按协商结果使用:协商到 mcp-http 时渲染进
// session/new(与 session/load);协商不到时不注入、该轮照常跑。
func TestNegotiation_MCPServers(t *testing.T) {
	mcpReq := func(sessionID int64, providerSession string) agentruntime.RunRequest {
		return agentruntime.RunRequest{
			Backend: acpTestBackend(), SessionID: sessionID, UserText: "hi", Cwd: t.TempDir(),
			ProviderSessionID: providerSession,
			MCPServers: []agentruntime.MCPServerSpec{{
				Name:    "org",
				URL:     "http://127.0.0.1:60080/mcp/group/",
				Headers: map[string]string{"Authorization": "Bearer tok", "X-Scope": "org"},
			}},
		}
	}

	t.Run("negotiated http renders into session/new", func(t *testing.T) {
		fa := installFakeAgent(t, fakeAgentOptions{mcpHTTP: true})
		r := New()
		events, _, err := r.Run(context.Background(), mcpReq(54, ""))
		require.NoError(t, err)
		fa.waitPrompt(2 * time.Second)
		servers := fa.lastMCPServers()
		require.Len(t, servers, 1)
		require.NotNil(t, servers[0].Http)
		assert.Equal(t, "http", servers[0].Http.Type)
		assert.Equal(t, "org", servers[0].Http.Name)
		assert.Equal(t, "http://127.0.0.1:60080/mcp/group/", servers[0].Http.Url)
		require.Len(t, servers[0].Http.Headers, 2)
		assert.Equal(t, "Authorization", servers[0].Http.Headers[0].Name)
		assert.Equal(t, "Bearer tok", servers[0].Http.Headers[0].Value)
		assert.Equal(t, "X-Scope", servers[0].Http.Headers[1].Name)
		fa.respondPrompt(acpsdk.StopReasonEndTurn, nil)
		drainEvents(t, events)
	})

	t.Run("not negotiated skips injection and the turn still runs", func(t *testing.T) {
		fa := installFakeAgent(t, fakeAgentOptions{mcpHTTP: false})
		r := New()
		events, result, err := r.Run(context.Background(), mcpReq(55, ""))
		require.NoError(t, err)
		prompt := fa.waitPrompt(2 * time.Second)
		fa.respondPrompt(acpsdk.StopReasonEndTurn, nil)
		got := drainEvents(t, events)
		require.NotEmpty(t, got)
		assert.Equal(t, agentruntime.Done{}, got[len(got)-1], "the turn must complete normally without MCP injection")
		assert.NoError(t, result.StopErr)
		assert.Empty(t, fa.lastMCPServers(), "mcpServers must not be injected")
		require.Len(t, prompt.Prompt, 1)
	})

	t.Run("negotiated http re-passes servers on session/load", func(t *testing.T) {
		fa := installFakeAgent(t, fakeAgentOptions{mcpHTTP: true, loadSession: true})
		r := New()
		events, result, err := r.Run(context.Background(), mcpReq(56, "prev-session"))
		require.NoError(t, err)
		fa.waitPrompt(2 * time.Second)
		fa.respondPrompt(acpsdk.StopReasonEndTurn, nil)
		drainEvents(t, events)

		assert.Equal(t, 1, fa.loadSessionCalls(), "a persisted provider session must be restored via session/load")
		assert.Equal(t, "prev-session", fa.lastLoadedSessionID())
		assert.Len(t, fa.lastMCPServers(), 1, "mcpServers must be re-passed on session/load")
		assert.Equal(t, "prev-session", result.ProviderSessionID)
		assert.Equal(t, 0, fa.newSessionCalls())
	})
}

// TestNegotiation_MCPServersFrameIsAlwaysArray 钉死 session/new 与 session/load 在线上
// 的那一字节:mcpServers 必须是 `[]`,不能是 `null`。
//
// 结构体解码看不出差别(nil 与 [] 都解成空切片),只有原始字节能看出来 —— 而真实
// Agent 恰恰在这一字节上分岔:SDK 的 McpServers 字段没有 omitempty,nil 会序列化成
// `null`,hermes acp 的 pydantic 校验随即以 `-32602 Invalid params: Input should be a
// valid list` 拒掉 session/new,会话根本建不起来(实测:桌面端整轮报
// `acp session/new failed: Invalid params`)。
func TestNegotiation_MCPServersFrameIsAlwaysArray(t *testing.T) {
	assertArray := func(t *testing.T, raw json.RawMessage, label string) {
		t.Helper()
		require.NotEmpty(t, raw, "%s: fake agent never saw the params", label)
		var params map[string]json.RawMessage
		require.NoError(t, json.Unmarshal(raw, &params))
		got, ok := params["mcpServers"]
		require.True(t, ok, "%s: mcpServers key must be present on the wire", label)
		assert.Equal(t, "[]", string(got), "%s: mcpServers must be a JSON array, never null", label)
	}

	runTurn := func(t *testing.T, fa *fakeAgent, req agentruntime.RunRequest) {
		t.Helper()
		events, _, err := New().Run(context.Background(), req)
		require.NoError(t, err)
		fa.waitPrompt(2 * time.Second)
		fa.respondPrompt(acpsdk.StopReasonEndTurn, nil)
		drainEvents(t, events)
	}

	t.Run("no servers configured", func(t *testing.T) {
		fa := installFakeAgent(t, fakeAgentOptions{})
		runTurn(t, fa, agentruntime.RunRequest{
			Backend: acpTestBackend(), SessionID: 57, UserText: "hi", Cwd: t.TempDir(),
		})
		assertArray(t, fa.lastNewSessionParams(), "session/new")
	})

	t.Run("servers configured but mcpCapabilities.http not negotiated", func(t *testing.T) {
		fa := installFakeAgent(t, fakeAgentOptions{mcpHTTP: false})
		runTurn(t, fa, agentruntime.RunRequest{
			Backend: acpTestBackend(), SessionID: 58, UserText: "hi", Cwd: t.TempDir(),
			MCPServers: []agentruntime.MCPServerSpec{
				{Name: "org", URL: "http://127.0.0.1:60080/mcp/group/"},
			},
		})
		assertArray(t, fa.lastNewSessionParams(), "session/new (injection skipped)")
	})

	t.Run("session/load keeps the array shape", func(t *testing.T) {
		fa := installFakeAgent(t, fakeAgentOptions{loadSession: true})
		runTurn(t, fa, agentruntime.RunRequest{
			Backend: acpTestBackend(), SessionID: 59, UserText: "hi", Cwd: t.TempDir(),
			ProviderSessionID: "prev-session",
		})
		require.Equal(t, 1, fa.loadSessionCalls(), "expected the load path, not session/new")
		assertArray(t, fa.lastLoadSessionParams(), "session/load")
	})
}

// TestNegotiation_LoadSessionMissing 没 loadSession 能力时,带 ProviderSessionID
// 的一轮降级为 session/new(上下文不延续),轮次照常跑。
func TestNegotiation_LoadSessionMissing(t *testing.T) {
	fa := installFakeAgent(t, fakeAgentOptions{loadSession: false})
	r := New()
	events, result, err := r.Run(context.Background(), agentruntime.RunRequest{
		Backend: acpTestBackend(), SessionID: 57, UserText: "hi", Cwd: t.TempDir(),
		ProviderSessionID: "stale-session",
	})
	require.NoError(t, err)
	fa.waitPrompt(2 * time.Second)
	fa.respondPrompt(acpsdk.StopReasonEndTurn, nil)
	got := drainEvents(t, events)

	assert.Equal(t, 0, fa.loadSessionCalls())
	assert.Equal(t, 1, fa.newSessionCalls(), "must degrade to session/new when loadSession is not negotiated")
	require.NotEmpty(t, got)
	assert.Equal(t, agentruntime.Done{}, got[len(got)-1])
	assert.Equal(t, "acp-sess-1", result.ProviderSessionID, "the fresh ACP session id is reported so the next turn continues from it")
}

// TestNegotiation_FreshSessionForcesNewSession FreshSession=true 时强制
// session/new,不许 load。
func TestNegotiation_FreshSessionForcesNewSession(t *testing.T) {
	fa := installFakeAgent(t, fakeAgentOptions{loadSession: true})
	r := New()
	events, _, err := r.Run(context.Background(), agentruntime.RunRequest{
		Backend: acpTestBackend(), SessionID: 58, UserText: "hi", Cwd: t.TempDir(),
		ProviderSessionID: "stale-session",
		FreshSession:      true,
	})
	require.NoError(t, err)
	fa.waitPrompt(2 * time.Second)
	fa.respondPrompt(acpsdk.StopReasonEndTurn, nil)
	drainEvents(t, events)

	assert.Equal(t, 0, fa.loadSessionCalls())
	assert.Equal(t, 1, fa.newSessionCalls())
}
