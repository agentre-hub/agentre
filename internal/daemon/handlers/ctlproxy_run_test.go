package handlers_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/agentre-hub/agentre/internal/daemon/handlers"
	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/runtimes/remote/wire"
	"github.com/agentre-hub/agentre/pkg/wire/agentrewire"
	"github.com/agentre-hub/agentre/pkg/wire/devicefp"
)

// tunnelRecorder 是桌面端那一跳的替身:记下经隧道转回去的请求。
type tunnelRecorder struct{ got wire.MCPProxyRequest }

func (*tunnelRecorder) Notify(*agentrewire.RpcNotification) error { return nil }
func (r *tunnelRecorder) Request(_ context.Context, _ string, params, result any) error {
	r.got = params.(wire.MCPProxyRequest)
	*result.(*wire.MCPProxyResponse) = wire.MCPProxyResponse{Status: 200, Body: []byte(`{}`)}
	return nil
}

type serverRecorder struct{ paths []string }

func (s *serverRecorder) Post(_ context.Context, path string, _ []byte) (int, []byte, error) {
	s.paths = append(s.paths, path)
	return 200, []byte(`{}`), nil
}

// TestRuntime_Run_GivenCtlSessions_ThenCLIGetsASessionTokenThatRoutesByOwnership 钉住
// 注入与路由接在同一张表上:runtime.run 让本轮 CLI 子进程带上 daemon 签的会话 token 与
// daemon gateway 端点,ctl 代理凭这个 token 认出会话 —— 桌面端派发的(带桌面 token)转回
// 那台桌面端,控制台派发的交给 server。
func TestRuntime_Run_GivenCtlSessions_ThenCLIGetsASessionTokenThatRoutesByOwnership(t *testing.T) {
	const desktopToken = "SENTINEL_DESKTOP_CTL_TOKEN_RUN"
	ctl := handlers.NewCtlSessions(func() string { return "http://127.0.0.1:7777" })
	agentruntime.RegisterCtlCredentialSource(ctl.Credentials)
	t.Cleanup(func() { agentruntime.RegisterCtlCredentialSource(nil) })

	captured := captureRuntimeLogs(t)
	rt := &fullRT{}
	rt.runFn = func(context.Context) (<-chan agentruntime.Event, *agentruntime.RunResult, error) {
		ch := make(chan agentruntime.Event)
		close(ch)
		return ch, &agentruntime.RunResult{}, nil
	}
	notif := newRecordingOutbound()
	sess := newRecordingSessions()
	h := handlers.NewRuntimeHandlers(handlers.RuntimeDeps{
		NotifyFor: notif.notifierFor, Sessions: sess, SessionQuery: sess, Ctl: ctl,
		RuntimeFor: func(agent_backend_entity.BackendType) agentruntime.Runtime { return rt },
	})
	be := agent_backend_entity.AgentBackend{ID: 1, Type: string(agent_backend_entity.TypeClaudeCode), Name: "x"}

	// 与生产同形：带 agent sync id 的跨主机派发不带本地 agent id（remote.crossHostAgentID
	// 把它置 0，浏览器派发同样是 0）。
	_, err := h.Run(context.Background(), &agentrewire.RuntimeRunRequest{
		Backend: backendProto(t, be), ConversationId: convID(71), AgentSyncId: "01HXAGENTSYNCID0000000000A", UserText: "desktop",
		DesktopCtlToken: desktopToken, DesktopSessionId: 55,
	})
	require.NoError(t, err)
	_, err = h.Run(context.Background(), &agentrewire.RuntimeRunRequest{
		Backend: backendProto(t, be), ConversationId: convID(72), AgentSyncId: "01HXAGENTSYNCID0000000000A", UserText: "console",
	})
	require.NoError(t, err)
	_ = notif.waitFrames(t, 4) // 两轮各 turnStarted + runResultDone
	// runResultDone 之后 fanout 还要记一条汇总(读全局 logger):两轮都记完才算收尾,
	// 否则它会越过本用例,与后面用例换全局 logger 竞争。
	require.Eventually(t, func() bool {
		return strings.Count(captured.String(), "fanout: session ended") >= 2
	}, 5*time.Second, 5*time.Millisecond)

	rt.mu.Lock()
	reqs := append([]runCall(nil), rt.runReqs...)
	rt.mu.Unlock()
	require.Len(t, reqs, 2)
	desktopCred, consoleCred := reqs[0].req.CtlCredentials(), reqs[1].req.CtlCredentials()
	assert.Equal(t, "http://127.0.0.1:7777", desktopCred.Endpoint, "the CLI dials the daemon's own gateway")
	require.NotEmpty(t, desktopCred.Token)
	assert.NotEqual(t, desktopToken, desktopCred.Token, "the desktop's token never leaves agentred's memory")
	require.NotEmpty(t, consoleCred.Token)
	assert.NotEqual(t, desktopCred.Token, consoleCred.Token, "tokens are per session")

	tunnel := &tunnelRecorder{}
	server := &serverRecorder{}
	var tunneled string
	proxy := httptest.NewServer(handlers.NewCtlProxyHandler(handlers.CtlProxyDeps{
		Sessions: ctl, Server: server,
		Tunnel: func(_ devicefp.Initiator, conversationID string) handlers.NotifierPort {
			tunneled = conversationID
			return tunnel
		},
	}))
	defer proxy.Close()

	call := func(token string) int {
		req, err := http.NewRequest(http.MethodPost, proxy.URL+"/ctl/v1/resources", strings.NewReader(`{"list":{"kind":"CTL_KIND_AGENT"}}`))
		require.NoError(t, err)
		req.Header.Set("Authorization", "Bearer "+token)
		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
		return resp.StatusCode
	}

	require.Equal(t, 200, call(desktopCred.Token))
	assert.Equal(t, convID(71), tunneled)
	assert.Equal(t, []string{"Bearer " + desktopToken}, tunnel.got.Headers["Authorization"])
	assert.Empty(t, server.paths, "a desktop-owned session never reaches the server")

	require.Equal(t, 200, call(consoleCred.Token))
	assert.Equal(t, []string{"/v1/ctl/resources"}, server.paths)
}
