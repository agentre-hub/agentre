package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/agentre-hub/agentre/internal/pkg/relaypeer"
	"github.com/agentre-hub/agentre/internal/pkg/relaypeer/relaypeertest"
	"github.com/agentre-hub/agentre/internal/pkg/wireversion"
	"github.com/agentre-hub/agentre/pkg/wire/agentrewire"
	"github.com/agentre-hub/agentre/pkg/wire/devicefp"
	"github.com/agentre-hub/agentre/pkg/wire/protorpc"
)

// ctlproxy_account_internal_test.go 钉住生产接线(newCtlAccount):控制台拥有的会话里的 send
// 经 ctlaccount 以这台 agentred 的设备凭据作为账号对端派发 —— 资源与派发计划走 server 的设备
// Bearer 接口,子会话经 relay 在目标设备上起轮并保存进账号;目标就是这台 agentred 时走进程内
// 通道,不经 relay 自拨。

const (
	accountTestSelf  = "sha256:this-agentred"
	accountTestBox   = "sha256:build-box"
	accountTestToken = "agentred-device-token"
)

// accountServer 是假的账号 server(CtlServerPort):ctl 资源、派发计划、保存会话。
type accountServer struct {
	mu     sync.Mutex
	chosen string // 派发计划选中的设备指纹
	saves  []map[string]string
	plans  []string
}

func (s *accountServer) Post(_ context.Context, path string, body []byte) (int, []byte, error) {
	switch path {
	case serverResourcesPath:
		if strings.Contains(string(body), "CTL_KIND_ENGINE") {
			return http.StatusOK, []byte(`{"list":{"items":[{"engine":{"id":2,"name":"codex-alt","syncId":"EN-2","runtime":"codex"}}]}}`), nil
		}
		return http.StatusOK, []byte(`{"list":{"items":[{"agent":{"id":4,"name":"reviewer","syncId":"AG-4","backendIds":["9"]}}]}}`), nil
	case "/v1/saved-sessions":
		var saved map[string]string
		_ = json.Unmarshal(body, &saved)
		s.mu.Lock()
		s.saves = append(s.saves, saved)
		s.mu.Unlock()
		return http.StatusOK, []byte(`{"code":0,"msg":"ok","data":{}}`), nil
	}
	return http.StatusNotFound, []byte(`{"code":404,"msg":"no route"}`), nil
}

func (s *accountServer) Get(_ context.Context, path string) (int, []byte, error) {
	if strings.HasPrefix(path, "/v1/workspace/dispatch-target?") {
		s.mu.Lock()
		s.plans = append(s.plans, path)
		chosen := s.chosen
		s.mu.Unlock()
		plan := map[string]any{
			"found":   true,
			"engine":  map[string]any{"sync_id": "EN-9", "name": "codex-main", "runtime": "codex"},
			"devices": []map[string]any{{"device_id": 2, "device_fingerprint": chosen, "device_name": "build-box", "kind": "agentred", "eligible": true}},
			"chosen": map[string]any{"device_fingerprint": chosen, "device_name": "build-box", "engine_sync_id": "EN-9",
				"runtime": "codex", "kind": "agentred", "cwd": "/srv"},
		}
		raw, _ := json.Marshal(map[string]any{"code": 0, "msg": "ok", "data": plan})
		return http.StatusOK, raw, nil
	}
	return http.StatusNotFound, []byte(`{"code":404,"msg":"no route"}`), nil
}

func (s *accountServer) savedBodies() []map[string]string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]map[string]string(nil), s.saves...)
}

// machineRuns 记下一台假机器收到的 runtime.run(连同调用方被认成的身份)。
type machineRuns struct {
	mu   sync.Mutex
	runs []*agentrewire.RuntimeRunRequest
	who  []string
}

func (m *machineRuns) registry(withAuth bool) *protorpc.Registry {
	reg := protorpc.NewRegistry()
	if withAuth {
		protorpc.RegisterMethod(reg, uint32(agentrewire.RpcMethod_RPC_METHOD_AUTH_ACCOUNT),
			func() *agentrewire.AuthAccountRequest { return &agentrewire.AuthAccountRequest{} },
			func(ctx context.Context, req *agentrewire.AuthAccountRequest) (*agentrewire.AuthAccountResponse, error) {
				fp := "fp:" + req.GetCredential()
				protorpc.ConnFromContext(ctx).SetAuth(protorpc.AuthState{Authenticated: true, DeviceFingerprint: fp})
				return &agentrewire.AuthAccountResponse{Ok: true, ProtocolVersion: wireversion.Protocol, PeerFingerprint: fp}, nil
			})
	}
	protorpc.RegisterMethod(reg, uint32(agentrewire.RpcMethod_RPC_METHOD_RUNTIME_RUN),
		func() *agentrewire.RuntimeRunRequest { return &agentrewire.RuntimeRunRequest{} },
		func(ctx context.Context, req *agentrewire.RuntimeRunRequest) (*agentrewire.RuntimeRunResponse, error) {
			m.mu.Lock()
			m.runs = append(m.runs, req)
			m.who = append(m.who, protorpc.ConnFromContext(ctx).Auth().DeviceFingerprint)
			m.mu.Unlock()
			return &agentrewire.RuntimeRunResponse{ConversationId: req.GetConversationId()}, nil
		})
	return reg
}

func (m *machineRuns) snapshot() ([]*agentrewire.RuntimeRunRequest, []string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]*agentrewire.RuntimeRunRequest(nil), m.runs...), append([]string(nil), m.who...)
}

type accountWorld struct {
	server    *accountServer
	relay     *relaypeertest.Relay
	box, self *machineRuns
	selfDials int
	// selfConns 是每条进程内通道在这台 agentred 一侧的那一端。
	selfMu    sync.Mutex
	selfConns []*protorpc.Conn
	proxy     *httptest.Server
	token     string
}

func newAccountWorld(t *testing.T, chosen string) *accountWorld {
	t.Helper()
	w := &accountWorld{server: &accountServer{chosen: chosen}, box: &machineRuns{}, self: &machineRuns{}}
	w.relay = relaypeertest.New(func(bearer string) bool { return bearer == accountTestToken })
	w.relay.AddMachine(&relaypeertest.Machine{Fingerprint: accountTestBox, Registry: w.box.registry(false)})
	mux := http.NewServeMux()
	mux.Handle(w.relay.Path, w.relay)
	relaySrv := httptest.NewServer(mux)
	t.Cleanup(relaySrv.Close)

	c := NewCtlSessions(func() string { return "http://gw" })
	c.bind(ctlTestRID, "sha256:browser", ctlTestConversation, DesktopCtlSession{}, false)
	w.proxy = httptest.NewServer(NewCtlProxyHandler(CtlProxyDeps{
		Sessions: c, Server: w.server, Self: devicefp.Carrier(accountTestSelf),
		Relay: CtlRelay{
			ServerURL: func() string { return relaySrv.URL },
			Token:     func(context.Context) (string, error) { return accountTestToken, nil },
			DialSelf: func(ctx context.Context) (*relaypeer.Conn, error) {
				w.selfDials++
				client, server := relaypeer.Pipe()
				conn := protorpc.NewConn(protorpc.NewPayloadFrameConn(server), w.self.registry(true))
				w.selfMu.Lock()
				w.selfConns = append(w.selfConns, conn)
				w.selfMu.Unlock()
				go conn.Serve(context.WithoutCancel(ctx))
				return relaypeer.OpenChannel(ctx, client, accountTestToken)
			},
			HostsConversation: func(context.Context, string) bool { return false },
		},
	}))
	t.Cleanup(w.proxy.Close)
	w.token = c.Credentials(7, ctlTestRID).Token
	return w
}

func TestCtlProxy_GivenConsoleOwnedSend_WhenTheAgentRunsOnAnotherDevice_ThenThisAgentredStartsAndSavesItThroughTheRelay(t *testing.T) {
	w := newAccountWorld(t, accountTestBox)

	status, got, _ := postCtl(t, w.proxy.URL+"/ctl/v1/send", w.token, `{"agent":"reviewer","text":"审一下 PR 42"}`)

	require.Equal(t, http.StatusOK, status, got)
	var out struct {
		ConversationID string `json:"conversationId"`
		Agent, Device  string
		Status         string
	}
	require.NoError(t, json.Unmarshal([]byte(got), &out))
	require.NotEmpty(t, out.ConversationID)
	assert.Equal(t, "reviewer", out.Agent)
	assert.Equal(t, "build-box", out.Device)
	assert.Equal(t, "running", out.Status)

	runs, who := w.box.snapshot()
	require.Len(t, runs, 1, "the child turn starts on the chosen device")
	assert.Equal(t, out.ConversationID, runs[0].GetConversationId())
	assert.Equal(t, "AG-4", runs[0].GetAgentSyncId())
	assert.Equal(t, "审一下 PR 42", runs[0].GetUserText())
	assert.Equal(t, "EN-9", runs[0].GetEngineProfileSyncId())
	assert.Equal(t, "codex", runs[0].GetBackend().GetType())
	assert.Empty(t, runs[0].GetBackend().GetSyncId(),
		"the engine profile is named by engine_profile_sync_id; agentred resolves it from its snapshot")
	assert.Equal(t, []string{"fp:" + accountTestToken}, who, "the child is filed under this agentred's own account identity")
	assert.Equal(t, []string{accountTestToken}, w.relay.Credentials(), "the relay and auth.account carry this agentred's device token")
	assert.Equal(t, []map[string]string{{
		"device_fingerprint": accountTestBox, "peer_fingerprint": "fp:" + accountTestToken, "conversation_id": out.ConversationID,
	}}, w.server.savedBodies(), "the child is saved to the account, so the console lists it")
	assert.Zero(t, w.selfDials)
}

// Given a console-owned send that names an engine, When it is dispatched, Then the engine is resolved
// and the dispatch plan is asked for it (it is not silently dropped on the way to the account peer).
func TestCtlProxy_GivenConsoleOwnedSend_WhenItNamesAnEngine_ThenThePlanIsAskedForThatEngine(t *testing.T) {
	w := newAccountWorld(t, accountTestBox)

	status, got, _ := postCtl(t, w.proxy.URL+"/ctl/v1/send", w.token, `{"agent":"reviewer","text":"hi","engine":"codex-alt"}`)

	require.Equal(t, http.StatusOK, status, got)
	w.server.mu.Lock()
	defer w.server.mu.Unlock()
	require.NotEmpty(t, w.server.plans)
	assert.Contains(t, w.server.plans[0], "engine_sync_id=EN-2")
}

func TestCtlProxy_GivenConsoleOwnedSend_WhenTheAgentRunsOnThisAgentred_ThenItStartsInProcessWithoutDialingItselfThroughTheRelay(t *testing.T) {
	w := newAccountWorld(t, accountTestSelf)

	status, got, _ := postCtl(t, w.proxy.URL+"/ctl/v1/send", w.token, `{"agent":"reviewer","text":"hi"}`)

	require.Equal(t, http.StatusOK, status, got)
	runs, who := w.self.snapshot()
	require.Len(t, runs, 1)
	assert.Equal(t, []string{"fp:" + accountTestToken}, who, "the in-process channel authenticates like the relay one")
	boxRuns, _ := w.box.snapshot()
	assert.Empty(t, boxRuns)
	assert.Equal(t, 1, w.selfDials)
	assert.Zero(t, w.relay.Dials(), "no self-dial through the relay")
	require.Len(t, w.server.savedBodies(), 1)
	assert.Equal(t, accountTestSelf, w.server.savedBodies()[0]["device_fingerprint"])
}

// Given a console-owned send whose child runs on this agentred, When the send has
// answered, Then the in-process channel it opened is closed: on this side it is a
// full connection (per-connection runtime table, session claims and push
// subscription once it attaches), and nothing else would ever end it.
func TestCtlProxy_GivenConsoleOwnedSendOnThisAgentred_WhenTheSendHasAnswered_ThenTheInProcessChannelIsClosed(t *testing.T) {
	w := newAccountWorld(t, accountTestSelf)

	status, got, _ := postCtl(t, w.proxy.URL+"/ctl/v1/send", w.token, `{"agent":"reviewer","text":"hi"}`)

	require.Equal(t, http.StatusOK, status, got)
	w.selfMu.Lock()
	conns := append([]*protorpc.Conn(nil), w.selfConns...)
	w.selfMu.Unlock()
	require.Len(t, conns, 1)
	select {
	case <-conns[0].Done():
	case <-time.After(5 * time.Second):
		t.Fatal("the in-process channel to this agentred outlived the request that opened it")
	}
}

func TestCtlProxy_GivenConsoleOwnedSend_WhenThisAgentredIsNotSignedIn_ThenServiceUnavailable(t *testing.T) {
	c := NewCtlSessions(func() string { return "http://gw" })
	c.bind(ctlTestRID, "sha256:browser", ctlTestConversation, DesktopCtlSession{}, false)
	srv := httptest.NewServer(NewCtlProxyHandler(CtlProxyDeps{Sessions: c, Server: &accountServer{},
		Relay: CtlRelay{ServerURL: func() string { return "" }, Token: func(context.Context) (string, error) { return "", nil }}}))
	t.Cleanup(srv.Close)

	status, got, _ := postCtl(t, srv.URL+"/ctl/v1/send", c.Credentials(7, ctlTestRID).Token, `{"agent":"reviewer","text":"hi"}`)

	assert.Equal(t, http.StatusServiceUnavailable, status)
	assert.JSONEq(t, `{"error":"this agentred is not signed in to an Agentre account"}`, got)
}
