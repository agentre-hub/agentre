package handlers

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cago-frame/cago/pkg/logger"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
	"google.golang.org/protobuf/encoding/protojson"

	"github.com/agentre-hub/agentre/internal/daemon/state"
	"github.com/agentre-hub/agentre/internal/pkg/backendcred"
	transcriptblocks "github.com/agentre-hub/agentre/internal/pkg/transcript/blocks"
	"github.com/agentre-hub/agentre/pkg/wire/agentrewire"
	"github.com/agentre-hub/agentre/pkg/wire/devicefp"
)

// 规格 2026-09-22 agrctl-resource-management「server 执行者」:OpenClaw 后端的 --token 在
// server 路径上不支持,「除非该后端就绑定在发起请求的这台 agentred 上,这时由 agentred
// 在本地写入」—— 写进 agentred 已有的设备本地凭据(规格 2026-09-17 device-local-backend-
// credentials,按后端 syncId 存)。读的时候同理:绑在本机的后端由 agentred 报 set/unset,
// 其它的保持 server 报的 unknown。

const (
	ltSelf        = devicefp.Carrier("sha256:this-agentred")
	ltOther       = "sha256:another-box"
	ltSyncID      = "5b7c1d2e-0000-4000-8000-00000000c1a0"
	ltGatewayTok  = "SENTINEL_GATEWAY_TOKEN_VALUE"
	ltBackendID   = int64(12)
	ltBackendName = "claw"
)

// memCredentialState 是 state.json 里凭据那一段的内存替身。
type memCredentialState struct {
	mu      sync.Mutex
	secrets map[string]string
}

func newMemCredentialState() *memCredentialState {
	return &memCredentialState{secrets: map[string]string{}}
}

func (m *memCredentialState) BackendCredential(account string) (string, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	v, ok := m.secrets[account]
	return v, ok
}

func (m *memCredentialState) SetBackendCredential(account, secret string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.secrets[account] = secret
	return nil
}

func (m *memCredentialState) DeleteBackendCredential(account string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.secrets, account)
	return nil
}

func (m *memCredentialState) HermesIdentity(string) (state.HermesIdentity, bool) {
	return state.HermesIdentity{}, false
}
func (m *memCredentialState) SetHermesIdentity(string, state.HermesIdentity) error { return nil }
func (m *memCredentialState) DeleteHermesIdentity(string) error                    { return nil }

func (m *memCredentialState) gatewayToken() (string, bool) {
	return m.BackendCredential(backendcred.OpenClawTokenAccount(ltSyncID))
}

// clawDoc 是 server 眼里的那个 openclaw 后端:绑在 device 上,token 状态 server 不知道。
func clawDoc(device string) string {
	b, _ := protojson.Marshal(&agentrewire.CtlResponse{Result: &agentrewire.CtlResponse_Get{Get: &agentrewire.CtlGetResponse{
		Resource: &agentrewire.CtlResource{Doc: &agentrewire.CtlResource_Backend{Backend: &agentrewire.CtlBackend{
			Id: ltBackendID, Name: ltBackendName, Type: "openclaw", SyncId: ltSyncID, DeviceFingerprint: device,
			TokenState: agentrewire.CtlTokenState_CTL_TOKEN_STATE_UNKNOWN,
		}}},
	}}})
	return string(b)
}

func isGet(body string) bool { return strings.Contains(body, `"get"`) }

func tokenWrite(t *testing.T, fields []string, backend *agentrewire.CtlBackend) string {
	t.Helper()
	b, err := protojson.Marshal(&agentrewire.CtlRequest{Op: &agentrewire.CtlRequest_Write{Write: &agentrewire.CtlWriteRequest{
		Op: agentrewire.CtlOp_CTL_OP_UPDATE, Kind: agentrewire.CtlKind_CTL_KIND_BACKEND, Id: ltBackendID,
		Resource: &agentrewire.CtlResource{Doc: &agentrewire.CtlResource_Backend{Backend: backend}},
		Fields:   fields,
		Caller:   agentrewire.CtlCaller_CTL_CALLER_SESSION,
		Command:  "agrctl update backend claw --token=" + ltGatewayTok,
	}}})
	require.NoError(t, err)
	return string(b)
}

func consoleOwnedHere(t *testing.T, server CtlServerPort, creds *memCredentialState) (*CtlSessions, *fakeApprovalSink, string, *httptest.Server) {
	t.Helper()
	c := NewCtlSessions(func() string { return "http://gw" })
	c.bind(ctlTestRID, "sha256:browser", ctlTestConversation, DesktopCtlSession{}, false)
	sink := newFakeSink()
	c.attachTurn(ctlTestRID, sink)
	h := NewCtlProxyHandler(CtlProxyDeps{
		Sessions: c, Server: server, ApprovalTimeout: time.Minute,
		Self:           ltSelf,
		OpenClawTokens: NewBackendCredentialHandlers(BackendCredentialDeps{State: creds}),
	})
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return c, sink, c.Credentials(7, ctlTestRID).Token, srv
}

func approveNext(t *testing.T, c *CtlSessions, sink *fakeApprovalSink) string {
	t.Helper()
	var requestID string
	select {
	case requestID = <-sink.begunC:
	case <-time.After(5 * time.Second):
		t.Fatal("no approval card was raised")
	}
	_, err := c.AnswerToolApproval(context.Background(), &agentrewire.ToolApprovalAnswerRequest{
		ConversationId: ctlTestConversation, RequestId: requestID, Allow: true,
	})
	require.NoError(t, err)
	return requestID
}

func TestCtlProxy_GivenTokenOnlyWriteForBackendBoundHere_WhenApproved_ThenSavedLocallyAndServerNeverSeesIt(t *testing.T) {
	server := &fakeCtlServer{answer: func(path string) (int, string) { return 200, clawDoc(string(ltSelf)) }}
	creds := newMemCredentialState()
	c, sink, tok, srv := consoleOwnedHere(t, server, creds)

	done := make(chan ctlResult, 1)
	go func() {
		done <- ctlPost(t, srv.URL+"/ctl/v1/resources", tok, tokenWrite(t, []string{"token"}, &agentrewire.CtlBackend{Token: ltGatewayTok}))
	}()
	requestID := approveNext(t, c, sink)
	res := <-done

	require.Equal(t, http.StatusOK, res.status, res.body)
	assert.Equal(t, []string{"session=9"}, res.waiting)
	saved, ok := creds.gatewayToken()
	require.True(t, ok, "the token lands in this agentred's device-local store, keyed by the backend's sync id")
	assert.Equal(t, ltGatewayTok, saved)

	server.mu.Lock()
	for _, call := range server.calls {
		assert.True(t, isGet(call.body), "only the backend lookup reaches the server: %s %s", call.path, call.body)
		assert.NotContains(t, call.body, ltGatewayTok)
	}
	server.mu.Unlock()

	begun, resolved := sink.snapshot()
	require.Len(t, begun, 1)
	assert.Equal(t, "ctl_update_backend", begun[0].ToolName)
	input, err := transcriptblocks.ParseCtlApprovalInput(begun[0].ToolInput)
	require.NoError(t, err)
	assert.NotContains(t, input.Command, ltGatewayTok)
	require.Len(t, input.Changes, 1)
	assert.Equal(t, ltBackendName, input.Changes[0].Name)
	assert.Equal(t, []transcriptblocks.CtlApprovalField{{Field: "token", Secret: true}}, input.Changes[0].Fields,
		"the card shows the token as updated without its value")
	assert.Equal(t, []resolvedApproval{{requestID, "approved", "已更新 backend claw"}}, resolved)

	var resp agentrewire.CtlResponse
	require.NoError(t, protojson.Unmarshal([]byte(res.body), &resp))
	assert.Equal(t, ltBackendName, resp.GetWrite().GetName())
	assert.NotContains(t, res.body, ltGatewayTok)
}

func TestCtlProxy_GivenMixedWriteForBackendBoundHere_WhenApproved_ThenRestGoesToServerWithoutTheToken(t *testing.T) {
	const preview = `{"write":{"id":12,"name":"claw2","changes":[{"op":"CTL_OP_UPDATE","kind":"CTL_KIND_BACKEND","id":12,"name":"claw2","fields":[{"field":"name","before":"claw","after":"claw2"}]}]}}`
	server := &fakeCtlServer{answer: func(path string) (int, string) {
		if path == serverResourcesPath+"?preview=1" {
			return 200, preview
		}
		return 200, clawDoc(string(ltSelf))
	}}
	creds := newMemCredentialState()
	c, sink, tok, srv := consoleOwnedHere(t, server, creds)

	done := make(chan ctlResult, 1)
	go func() {
		done <- ctlPost(t, srv.URL+"/ctl/v1/resources", tok,
			tokenWrite(t, []string{"name", "token"}, &agentrewire.CtlBackend{Name: "claw2", Token: ltGatewayTok}))
	}()
	approveNext(t, c, sink)
	res := <-done

	require.Equal(t, http.StatusOK, res.status, res.body)
	saved, _ := creds.gatewayToken()
	assert.Equal(t, ltGatewayTok, saved)

	server.mu.Lock()
	var writes []serverCall
	for _, call := range server.calls {
		assert.NotContains(t, call.body, ltGatewayTok, call.path)
		if !isGet(call.body) {
			writes = append(writes, call)
		}
	}
	server.mu.Unlock()
	require.Len(t, writes, 2, "preview + commit")
	for _, w := range writes {
		var req agentrewire.CtlRequest
		require.NoError(t, protojson.Unmarshal([]byte(w.body), &req))
		assert.Equal(t, []string{"name"}, req.GetWrite().GetFields(), "the token field is taken off what the server sees")
	}

	begun, _ := sink.snapshot()
	input, err := transcriptblocks.ParseCtlApprovalInput(begun[0].ToolInput)
	require.NoError(t, err)
	require.Len(t, input.Changes, 1)
	require.Len(t, input.Changes[0].Fields, 2)
	assert.Equal(t, "name", input.Changes[0].Fields[0].Field)
	assert.Equal(t, transcriptblocks.CtlApprovalField{Field: "token", Secret: true}, input.Changes[0].Fields[1])
}

func TestCtlProxy_GivenEmptyTokenForBackendBoundHere_WhenApproved_ThenLocalTokenCleared(t *testing.T) {
	server := &fakeCtlServer{answer: func(string) (int, string) { return 200, clawDoc(string(ltSelf)) }}
	creds := newMemCredentialState()
	require.NoError(t, creds.SetBackendCredential(backendcred.OpenClawTokenAccount(ltSyncID), "old"))
	c, sink, tok, srv := consoleOwnedHere(t, server, creds)

	done := make(chan ctlResult, 1)
	go func() {
		done <- ctlPost(t, srv.URL+"/ctl/v1/resources", tok, tokenWrite(t, []string{"token"}, &agentrewire.CtlBackend{}))
	}()
	approveNext(t, c, sink)
	res := <-done

	require.Equal(t, http.StatusOK, res.status, res.body)
	_, ok := creds.gatewayToken()
	assert.False(t, ok)
}

func TestCtlProxy_GivenTokenWriteForBackendBoundHere_WhenRejected_ThenNothingSaved(t *testing.T) {
	server := &fakeCtlServer{answer: func(string) (int, string) { return 200, clawDoc(string(ltSelf)) }}
	creds := newMemCredentialState()
	c, sink, tok, srv := consoleOwnedHere(t, server, creds)

	done := make(chan ctlResult, 1)
	go func() {
		done <- ctlPost(t, srv.URL+"/ctl/v1/resources", tok, tokenWrite(t, []string{"token"}, &agentrewire.CtlBackend{Token: ltGatewayTok}))
	}()
	var requestID string
	select {
	case requestID = <-sink.begunC:
	case <-time.After(5 * time.Second):
		t.Fatal("no approval card was raised")
	}
	_, err := c.AnswerToolApproval(context.Background(), &agentrewire.ToolApprovalAnswerRequest{
		ConversationId: ctlTestConversation, RequestId: requestID, Allow: false,
	})
	require.NoError(t, err)
	res := <-done

	assert.Equal(t, http.StatusForbidden, res.status)
	_, ok := creds.gatewayToken()
	assert.False(t, ok)
}

func TestCtlProxy_GivenTokenWriteForBackendBoundElsewhere_ThenServerRejectionStandsAndNothingSaved(t *testing.T) {
	server := &fakeCtlServer{answer: func(path string) (int, string) {
		if path == serverResourcesPath+"?preview=1" {
			return http.StatusUnprocessableEntity, `{"error":"setting an openclaw token is not supported here"}`
		}
		return 200, clawDoc(ltOther)
	}}
	creds := newMemCredentialState()
	_, sink, tok, srv := consoleOwnedHere(t, server, creds)

	status, got, waiting := postCtl(t, srv.URL+"/ctl/v1/resources", tok,
		tokenWrite(t, []string{"token"}, &agentrewire.CtlBackend{Token: ltGatewayTok}))

	assert.Equal(t, http.StatusUnprocessableEntity, status)
	assert.JSONEq(t, `{"error":"setting an openclaw token is not supported here"}`, got)
	assert.Empty(t, waiting)
	_, ok := creds.gatewayToken()
	assert.False(t, ok)
	begun, _ := sink.snapshot()
	assert.Empty(t, begun)
}

// ── 读:绑在本机的 openclaw 后端由 agentred 报 set/unset ──────────────────────

func backendListBody(t *testing.T) string {
	t.Helper()
	b, err := protojson.Marshal(&agentrewire.CtlRequest{Op: &agentrewire.CtlRequest_List{
		List: &agentrewire.CtlListRequest{Kind: agentrewire.CtlKind_CTL_KIND_BACKEND},
	}})
	require.NoError(t, err)
	return string(b)
}

func TestCtlProxy_GivenConsoleOwnedBackendRead_ThenBackendsBoundHereReportLocalTokenState(t *testing.T) {
	unknown := agentrewire.CtlTokenState_CTL_TOKEN_STATE_UNKNOWN
	list := &agentrewire.CtlResponse{Result: &agentrewire.CtlResponse_List{List: &agentrewire.CtlListResponse{Items: []*agentrewire.CtlResource{
		{Doc: &agentrewire.CtlResource_Backend{Backend: &agentrewire.CtlBackend{Id: 12, Name: "here-set", Type: "openclaw", SyncId: ltSyncID, DeviceFingerprint: string(ltSelf), TokenState: unknown}}},
		{Doc: &agentrewire.CtlResource_Backend{Backend: &agentrewire.CtlBackend{Id: 13, Name: "here-unset", Type: "openclaw", SyncId: "other-sync", DeviceFingerprint: string(ltSelf), TokenState: unknown}}},
		{Doc: &agentrewire.CtlResource_Backend{Backend: &agentrewire.CtlBackend{Id: 14, Name: "far", Type: "openclaw", SyncId: "far-sync", DeviceFingerprint: ltOther, TokenState: unknown}}},
		{Doc: &agentrewire.CtlResource_Backend{Backend: &agentrewire.CtlBackend{Id: 15, Name: "cc", Type: "claudecode", SyncId: "cc-sync", DeviceFingerprint: string(ltSelf)}}},
	}}}}
	raw, err := protojson.Marshal(list)
	require.NoError(t, err)
	server := &fakeCtlServer{answer: func(string) (int, string) { return 200, string(raw) }}
	creds := newMemCredentialState()
	require.NoError(t, creds.SetBackendCredential(backendcred.OpenClawTokenAccount(ltSyncID), ltGatewayTok))
	_, _, tok, srv := consoleOwnedHere(t, server, creds)

	status, got, _ := postCtl(t, srv.URL+"/ctl/v1/resources", tok, backendListBody(t))
	require.Equal(t, 200, status, got)
	assert.NotContains(t, got, ltGatewayTok)
	var resp agentrewire.CtlResponse
	require.NoError(t, protojson.Unmarshal([]byte(got), &resp))
	states := map[string]agentrewire.CtlTokenState{}
	for _, it := range resp.GetList().GetItems() {
		states[it.GetBackend().GetName()] = it.GetBackend().GetTokenState()
	}
	assert.Equal(t, map[string]agentrewire.CtlTokenState{
		"here-set":   agentrewire.CtlTokenState_CTL_TOKEN_STATE_SET,
		"here-unset": agentrewire.CtlTokenState_CTL_TOKEN_STATE_UNSET,
		"far":        unknown,
		"cc":         agentrewire.CtlTokenState_CTL_TOKEN_STATE_UNSPECIFIED,
	}, states)

	// get 同理。
	getBody, err := protojson.Marshal(&agentrewire.CtlRequest{Op: &agentrewire.CtlRequest_Get{
		Get: &agentrewire.CtlGetRequest{Kind: agentrewire.CtlKind_CTL_KIND_BACKEND, Id: ltBackendID},
	}})
	require.NoError(t, err)
	server.answer = func(string) (int, string) { return 200, clawDoc(string(ltSelf)) }
	status, got, _ = postCtl(t, srv.URL+"/ctl/v1/resources", tok, string(getBody))
	require.Equal(t, 200, status, got)
	require.NoError(t, protojson.Unmarshal([]byte(got), &resp))
	assert.Equal(t, agentrewire.CtlTokenState_CTL_TOKEN_STATE_SET, resp.GetGet().GetResource().GetBackend().GetTokenState())
}

// 红线:本地写入 token 的整条路径(含失败)不把 token 写进日志。
func TestCtlProxy_GivenLocalTokenWriteFails_ThenErrorAndTokenNeverLogged(t *testing.T) {
	core, logs := observer.New(zapcore.DebugLevel)
	old := logger.Default()
	logger.SetLogger(zap.New(core))
	t.Cleanup(func() { logger.SetLogger(old) })

	server := &fakeCtlServer{answer: func(string) (int, string) { return 200, clawDoc(string(ltSelf)) }}
	creds := &failingCredentialState{memCredentialState: newMemCredentialState()}
	c := NewCtlSessions(func() string { return "http://gw" })
	c.bind(ctlTestRID, "sha256:browser", ctlTestConversation, DesktopCtlSession{}, false)
	sink := newFakeSink()
	c.attachTurn(ctlTestRID, sink)
	srv := httptest.NewServer(NewCtlProxyHandler(CtlProxyDeps{
		Sessions: c, Server: server, ApprovalTimeout: time.Minute, Self: ltSelf,
		OpenClawTokens: NewBackendCredentialHandlers(BackendCredentialDeps{State: creds}),
	}))
	t.Cleanup(srv.Close)

	done := make(chan ctlResult, 1)
	go func() {
		done <- ctlPost(t, srv.URL+"/ctl/v1/resources", c.Credentials(7, ctlTestRID).Token,
			tokenWrite(t, []string{"token"}, &agentrewire.CtlBackend{Token: ltGatewayTok}))
	}()
	requestID := approveNext(t, c, sink)
	res := <-done

	assert.Equal(t, http.StatusInternalServerError, res.status)
	assert.NotContains(t, res.body, ltGatewayTok)
	_, resolved := sink.snapshot()
	require.Len(t, resolved, 1)
	assert.Equal(t, requestID, resolved[0].requestID)
	assert.True(t, strings.HasPrefix(resolved[0].result, "执行失败："), resolved[0].result)
	require.NotZero(t, logs.Len())
	for _, entry := range logs.All() {
		line := entry.Message
		for k, v := range entry.ContextMap() {
			line += " " + k + "=" + fmt.Sprint(v)
		}
		assert.NotContains(t, line, ltGatewayTok)
	}
}

type failingCredentialState struct{ *memCredentialState }

func (f *failingCredentialState) SetBackendCredential(string, string) error {
	return errors.New("state.json is read-only")
}
