package daemon

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"

	"github.com/agentre-hub/agentre/internal/daemon/auth"
	"github.com/agentre-hub/agentre/internal/daemon/state"
	"github.com/agentre-hub/agentre/internal/pkg/wireversion"
	"github.com/agentre-hub/agentre/pkg/wire/agentrewire"
	"github.com/agentre-hub/agentre/pkg/wire/devicefp"
	"github.com/agentre-hub/agentre/pkg/wire/protorpc"
	"github.com/agentre-hub/agentre/pkg/wire/rpcerror"
)

const (
	scopeAccount   = "acct-scope"
	patFingerprint = "pat:0123456789abcdef0123456789abcdef"
	otherInitiator = "sha256:console-browser"
)

// scopeRig is an empty agentred logged into scopeAccount with one bound
// connection. Its account server answers every introspection with the current
// verdict, so a real auth.account handshake (and the guard's re-verification on
// each call) can be driven against it.
type scopeRig struct {
	daemon         *Daemon
	client, server *protorpc.Conn
	ctx            context.Context
	introspections *atomic.Int32
	verdict        atomic.Value // map[string]any
}

func newScopeRig(t *testing.T, introspected map[string]any) *scopeRig {
	t.Helper()
	d, err := New(Options{DataDir: t.TempDir()})
	require.NoError(t, err)
	t.Cleanup(func() { closeDB(d.db) })
	rig := &scopeRig{daemon: d, introspections: &atomic.Int32{}}
	rig.verdict.Store(introspected)
	accountServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rig.introspections.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "msg": "ok", "data": rig.verdict.Load()})
	}))
	t.Cleanup(accountServer.Close)
	d.state.Login(scopeAccount, state.AccountCredential{AccessToken: "receiver-token"})
	d.state.Mutate(func(s *state.State) { s.AccountServerURL = accountServer.URL })
	require.NoError(t, d.state.Save())

	clientTransport, serverTransport := protobufTestPipePair()
	rig.client = protorpc.NewConn(clientTransport, protorpc.NewRegistry())
	rig.server = protorpc.NewConn(serverTransport, d.protobufRegistry.Clone())
	d.bindProtobufConn(rig.server)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	rig.ctx = ctx
	go rig.client.Serve(ctx)
	go rig.server.Serve(ctx)
	return rig
}

// as completes a real auth.account handshake as a credential of kind holding scopes.
func (r *scopeRig) as(t *testing.T, kind string, scopes ...string) *scopeRig {
	t.Helper()
	verdict := patIntrospection(scopes...)
	verdict["kind"] = kind
	r.verdict.Store(verdict)
	err := r.call(agentrewire.RpcMethod_RPC_METHOD_AUTH_ACCOUNT, &agentrewire.AuthAccountRequest{
		Credential: "credential-" + kind, ProtocolVersion: wireversion.Protocol, MinSupportedProtocolVersion: wireversion.MinSupported,
	}, &agentrewire.AuthAccountResponse{})
	require.NoError(t, err)
	return r
}

func (r *scopeRig) call(method agentrewire.RpcMethod, request, response proto.Message) error {
	return protorpc.CallMessage(r.ctx, r.client, uint32(method), request, response)
}

func requireCode(t *testing.T, err error, code int32, message string) {
	t.Helper()
	var rpcErr *protorpc.Error
	require.ErrorAs(t, err, &rpcErr)
	assert.Equal(t, code, rpcErr.Code)
	if message != "" {
		assert.Equal(t, message, rpcErr.Message)
	}
}

func requireNotScopeDenied(t *testing.T, err error) {
	t.Helper()
	var rpcErr *protorpc.Error
	if err != nil && assert.ErrorAs(t, err, &rpcErr) {
		assert.NotEqual(t, int32(rpcerror.CodeScopeDenied), rpcErr.Code, "the gate must let this call through: %v", err)
	}
}

func patIntrospection(scopes ...string) map[string]any {
	return map[string]any{
		"account_id": scopeAccount, "device_id": 0, "kind": auth.KindPersonalToken,
		"peer_fingerprint": patFingerprint, "expires_in": 0, "scopes": scopes, "token_name": "ci-bot",
	}
}

// Given a personal token, When it completes auth.account, Then the connection
// records the introspected kind, scopes and token name next to its fingerprint.
func TestDaemonScope_GivenAPersonalTokenHandshake_ThenTheConnectionRecordsKindScopesAndName(t *testing.T) {
	rig := newScopeRig(t, patIntrospection("read", "chat"))

	response := &agentrewire.AuthAccountResponse{}
	err := rig.call(agentrewire.RpcMethod_RPC_METHOD_AUTH_ACCOUNT, &agentrewire.AuthAccountRequest{
		Credential: "pat-secret", ProtocolVersion: wireversion.Protocol, MinSupportedProtocolVersion: wireversion.MinSupported,
	}, response)

	require.NoError(t, err)
	assert.Equal(t, protorpc.AuthState{
		Authenticated: true, DeviceFingerprint: patFingerprint, AccountID: scopeAccount,
		Kind: auth.KindPersonalToken, Scopes: []string{"read", "chat"}, TokenName: "ci-bot",
	}, rig.server.Auth())
}

// Given a personal token presented by a peer that does not speak the
// scope-checking protocol, Then the handshake fails on the protocol version
// before the credential is ever introspected.
func TestDaemonScope_GivenAPersonalTokenFromAnOlderPeer_ThenRefusedOnProtocolBeforeIntrospection(t *testing.T) {
	rig := newScopeRig(t, patIntrospection("read", "chat"))

	err := rig.call(agentrewire.RpcMethod_RPC_METHOD_AUTH_ACCOUNT, &agentrewire.AuthAccountRequest{
		Credential: "pat-secret", ProtocolVersion: "0.8.0", MinSupportedProtocolVersion: "0.8.0",
	}, &agentrewire.AuthAccountResponse{})

	requireCode(t, err, rpcerror.CodeProtocolVersion, "")
	assert.Zero(t, rig.introspections.Load())
	assert.False(t, rig.server.Auth().Authenticated)
}

func TestDaemonScope_GivenAReadOnlyToken_WhenItRuns_ThenRefusedNamingChat(t *testing.T) {
	rig := newScopeRig(t, nil).as(t, auth.KindPersonalToken, "read")

	err := rig.call(agentrewire.RpcMethod_RPC_METHOD_RUNTIME_RUN, &agentrewire.RuntimeRunRequest{ConversationId: convID(1), UserText: "go"}, &agentrewire.RuntimeRunResponse{})

	requireCode(t, err, rpcerror.CodeScopeDenied, "token lacks scope chat")
}

// Given a chat token, When it continues a session it started itself, Then the
// gate lets it through to the runtime (which answers on an idle session).
func TestDaemonScope_GivenAChatToken_WhenItActsOnItsOwnSession_ThenTheRuntimeAnswers(t *testing.T) {
	rig := newScopeRig(t, nil).as(t, auth.KindPersonalToken, "chat")

	err := rig.call(agentrewire.RpcMethod_RPC_METHOD_RUNTIME_STEER, &agentrewire.RuntimeSteerRequest{ConversationId: convID(1), Text: "more"}, &agentrewire.RuntimeSteerResponse{})
	requireCode(t, err, -32010, "agentruntime: no active turn for session")

	err = rig.call(agentrewire.RpcMethod_RPC_METHOD_RUNTIME_STEER, &agentrewire.RuntimeSteerRequest{ConversationId: convID(1), Text: "more", PeerFingerprint: patFingerprint}, &agentrewire.RuntimeSteerResponse{})
	requireCode(t, err, -32010, "agentruntime: no active turn for session")
}

// Given a chat token, When it names another initiator's session, Then it is
// refused — chat only ever reaches the sessions the token started.
func TestDaemonScope_GivenAChatToken_WhenItActsOnAnotherInitiatorsSession_ThenRefused(t *testing.T) {
	rig := newScopeRig(t, nil).as(t, auth.KindPersonalToken, "chat", "approve")

	for name, call := range map[string]func() error{
		"runtime.run": func() error {
			return rig.call(agentrewire.RpcMethod_RPC_METHOD_RUNTIME_RUN, &agentrewire.RuntimeRunRequest{ConversationId: convID(1), UserText: "go", PeerFingerprint: otherInitiator}, &agentrewire.RuntimeRunResponse{})
		},
		"runtime.abort": func() error {
			return rig.call(agentrewire.RpcMethod_RPC_METHOD_RUNTIME_ABORT, &agentrewire.RuntimeAbortRequest{ConversationId: convID(1), PeerFingerprint: otherInitiator}, &agentrewire.RuntimeAbortResponse{})
		},
		"session.setModelTarget": func() error {
			return rig.call(agentrewire.RpcMethod_RPC_METHOD_SET_MODEL_TARGET, &agentrewire.SetModelTargetRequest{ConversationId: convID(1), ProviderKey: "p", ModelKey: "m", PeerFingerprint: otherInitiator}, &agentrewire.SetModelTargetResponse{})
		},
		"runtime.submitToolPermission": func() error {
			return rig.call(agentrewire.RpcMethod_RPC_METHOD_RUNTIME_SUBMIT_TOOL_PERMISSION, &agentrewire.RuntimeSubmitToolPermissionRequest{ConversationId: convID(1), RequestId: "tool-1", Allow: true, PeerFingerprint: otherInitiator}, &agentrewire.PeerSessionControlResponse{})
		},
	} {
		t.Run(name, func(t *testing.T) {
			requireCode(t, call(), rpcerror.CodeScopeDenied, "token may only act on sessions it started")
		})
	}
}

// Given a read token, When it reads another initiator's session, Then the read
// goes through: read sees every session of the account.
func TestDaemonScope_GivenAReadToken_WhenItReadsAnotherInitiatorsSession_ThenAllowed(t *testing.T) {
	rig := newScopeRig(t, nil).as(t, auth.KindPersonalToken, "read")

	err := rig.call(agentrewire.RpcMethod_RPC_METHOD_SESSION_PULL, &agentrewire.SessionPullRequest{ConversationId: convID(1), Limit: 10, PeerFingerprint: otherInitiator}, &agentrewire.SessionPullResponse{})

	require.NoError(t, err)
}

func TestDaemonScope_GivenAChatToken_WhenItApproves_ThenRefusedNamingApprove(t *testing.T) {
	rig := newScopeRig(t, nil).as(t, auth.KindPersonalToken, "read", "chat")

	err := rig.call(agentrewire.RpcMethod_RPC_METHOD_RUNTIME_SUBMIT_TOOL_PERMISSION, &agentrewire.RuntimeSubmitToolPermissionRequest{ConversationId: convID(1), RequestId: "tool-1", Allow: true}, &agentrewire.PeerSessionControlResponse{})
	requireCode(t, err, rpcerror.CodeScopeDenied, "token lacks scope approve")

	err = rig.call(agentrewire.RpcMethod_RPC_METHOD_TOOL_APPROVAL_ANSWER, &agentrewire.ToolApprovalAnswerRequest{ConversationId: convID(1), RequestId: "tool-1", Allow: true}, &agentrewire.ToolApprovalAnswerResponse{})
	requireCode(t, err, rpcerror.CodeScopeDenied, "token lacks scope approve")
}

// Given an approve token, When it answers an approval card, Then only a card of a
// session the token started reaches the waiter table; another initiator's card
// is refused even though toolApproval.answer names no initiator.
func TestDaemonScope_GivenAnApproveToken_WhenItAnswersAnApprovalCard_ThenOnlyItsOwnSessionsCardsAreReached(t *testing.T) {
	rig := newScopeRig(t, nil).as(t, auth.KindPersonalToken, "approve")
	own, foreign := convID(1), convID(2)
	seedSession(t, rig.ctx, rig.daemon.sessionStore, devicefp.Initiator(patFingerprint), own, "idle")
	seedSession(t, rig.ctx, rig.daemon.sessionStore, devicefp.Initiator(otherInitiator), foreign, "idle")

	err := rig.call(agentrewire.RpcMethod_RPC_METHOD_TOOL_APPROVAL_ANSWER, &agentrewire.ToolApprovalAnswerRequest{ConversationId: own, RequestId: "tool-1", Allow: true}, &agentrewire.ToolApprovalAnswerResponse{})
	requireCode(t, err, rpcerror.CodeInvalidParams, `no pending tool approval "tool-1" in this session`)

	err = rig.call(agentrewire.RpcMethod_RPC_METHOD_TOOL_APPROVAL_ANSWER, &agentrewire.ToolApprovalAnswerRequest{ConversationId: foreign, RequestId: "tool-1", Allow: true}, &agentrewire.ToolApprovalAnswerResponse{})
	requireCode(t, err, rpcerror.CodeScopeDenied, "token may only act on sessions it started")
}

// Given a personal token with every scope, When it calls a method outside the
// scope table, Then it is refused before the handler runs.
func TestDaemonScope_GivenAPersonalToken_WhenItCallsAnUntabledMethod_ThenRefused(t *testing.T) {
	rig := newScopeRig(t, nil).as(t, auth.KindPersonalToken, "read", "chat", "approve", "resources:write", "devices:write")

	err := rig.call(agentrewire.RpcMethod_RPC_METHOD_LLM_LIST, &agentrewire.LLMListRequest{}, &agentrewire.LLMListResponse{})
	requireCode(t, err, rpcerror.CodeScopeDenied, "")

	err = rig.call(agentrewire.RpcMethod_RPC_METHOD_SESSION_DELETE, &agentrewire.SessionDeleteRequest{ConversationId: convID(1)}, &agentrewire.SessionDeleteResponse{})
	requireCode(t, err, rpcerror.CodeScopeDenied, "")
}

// Given a cli device credential, Then it keeps full account trust: untabled
// methods and other initiators' sessions are reachable as before.
func TestDaemonScope_GivenACLIDevice_ThenItKeepsFullAccountTrust(t *testing.T) {
	rig := newScopeRig(t, nil).as(t, auth.KindCLI)

	err := rig.call(agentrewire.RpcMethod_RPC_METHOD_LLM_LIST, &agentrewire.LLMListRequest{}, &agentrewire.LLMListResponse{})
	require.NoError(t, err)

	err = rig.call(agentrewire.RpcMethod_RPC_METHOD_RUNTIME_STEER, &agentrewire.RuntimeSteerRequest{ConversationId: convID(1), Text: "more", PeerFingerprint: otherInitiator}, &agentrewire.RuntimeSteerResponse{})
	requireNotScopeDenied(t, err)
	requireCode(t, err, -32010, "")
}
