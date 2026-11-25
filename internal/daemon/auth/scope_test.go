package auth

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/agentre-hub/agentre/pkg/wire/agentrewire"
	"github.com/agentre-hub/agentre/pkg/wire/protorpc"
	"github.com/agentre-hub/agentre/pkg/wire/rpcerror"
)

func personalToken(scopes ...string) protorpc.AuthState {
	return protorpc.AuthState{Authenticated: true, DeviceFingerprint: "pat:own", AccountID: "42", Kind: KindPersonalToken, Scopes: scopes}
}

func requireScopeDenied(t *testing.T, err error, scope string) {
	t.Helper()
	var rpcErr *rpcerror.Error
	require.ErrorAs(t, err, &rpcErr)
	assert.Equal(t, rpcerror.CodeScopeDenied, rpcErr.Code)
	assert.Equal(t, "token lacks scope "+scope, rpcErr.Message)
}

// Given a personal token, Then every tabled method demands its scope, chat also
// covers the session reads, and the refusal names the scope that is missing.
func TestAuthorizeMethod_GivenAPersonalToken_ThenEachTabledMethodDemandsItsScope(t *testing.T) {
	cases := []struct {
		method agentrewire.RpcMethod
		class  Scope
	}{
		{agentrewire.RpcMethod_RPC_METHOD_SESSION_LIST, ScopeRead},
		{agentrewire.RpcMethod_RPC_METHOD_SESSION_PULL, ScopeRead},
		{agentrewire.RpcMethod_RPC_METHOD_SESSION_ATTACH, ScopeRead},
		{agentrewire.RpcMethod_RPC_METHOD_SESSION_PENDING_WAITERS, ScopeRead},
		{agentrewire.RpcMethod_RPC_METHOD_RUNTIME_RUN, ScopeChat},
		{agentrewire.RpcMethod_RPC_METHOD_RUNTIME_STEER, ScopeChat},
		{agentrewire.RpcMethod_RPC_METHOD_RUNTIME_CANCEL_STEER, ScopeChat},
		{agentrewire.RpcMethod_RPC_METHOD_RUNTIME_ABORT, ScopeChat},
		{agentrewire.RpcMethod_RPC_METHOD_RUNTIME_SUBMIT_ANSWER, ScopeChat},
		{agentrewire.RpcMethod_RPC_METHOD_SET_MODEL_TARGET, ScopeChat},
		{agentrewire.RpcMethod_RPC_METHOD_SET_SESSION_REASONING_EFFORT, ScopeChat},
		{agentrewire.RpcMethod_RPC_METHOD_RUNTIME_SET_PERMISSION_MODE, ScopeChat},
		{agentrewire.RpcMethod_RPC_METHOD_RUNTIME_SUBMIT_TOOL_PERMISSION, ScopeApprove},
		{agentrewire.RpcMethod_RPC_METHOD_TOOL_APPROVAL_ANSWER, ScopeApprove},
		{agentrewire.RpcMethod_RPC_METHOD_ENGINE_TEST, ScopeResourcesWrite},
		{agentrewire.RpcMethod_RPC_METHOD_AGENTRED_SELF_UPDATE, ScopeDevicesWrite},
	}
	for _, tc := range cases {
		t.Run(tc.method.String(), func(t *testing.T) {
			class, err := AuthorizeMethod(personalToken(string(tc.class)), uint32(tc.method))
			require.NoError(t, err)
			assert.Equal(t, tc.class, class)

			_, err = AuthorizeMethod(personalToken("unrelated"), uint32(tc.method))
			requireScopeDenied(t, err, string(tc.class))
		})
	}
}

func TestAuthorizeMethod_GivenAChatToken_ThenItAlsoReadsSessions(t *testing.T) {
	class, err := AuthorizeMethod(personalToken(string(ScopeChat)), uint32(agentrewire.RpcMethod_RPC_METHOD_SESSION_PULL))

	require.NoError(t, err)
	assert.Equal(t, ScopeRead, class, "the call is still a read: chat only satisfies it")
}

func TestAuthorizeMethod_GivenAChatOnlyToken_ThenApprovingIsRefusedNamingApprove(t *testing.T) {
	_, err := AuthorizeMethod(personalToken(string(ScopeRead), string(ScopeChat)), uint32(agentrewire.RpcMethod_RPC_METHOD_RUNTIME_SUBMIT_TOOL_PERMISSION))

	requireScopeDenied(t, err, string(ScopeApprove))
}

// Given a personal token holding every scope, When it calls a method outside the
// table (terminal, filesystem, port forward, LLM upsert, delete …), Then it is
// refused: absence from the table is a refusal, never a pass.
func TestAuthorizeMethod_GivenAPersonalToken_WhenTheMethodIsNotTabled_ThenRefused(t *testing.T) {
	all := personalToken("read", "chat", "approve", "resources:write", "devices:write")
	for _, method := range []agentrewire.RpcMethod{
		agentrewire.RpcMethod_RPC_METHOD_TERMINAL_OPEN,
		agentrewire.RpcMethod_RPC_METHOD_LLM_UPSERT,
		agentrewire.RpcMethod_RPC_METHOD_SESSION_DELETE,
		agentrewire.RpcMethod_RPC_METHOD_AUTH_REVOKE,
		agentrewire.RpcMethod_RPC_METHOD_RUNTIME_LIST_SESSION_PROCESSES,
	} {
		t.Run(method.String(), func(t *testing.T) {
			_, err := AuthorizeMethod(all, uint32(method))
			var rpcErr *rpcerror.Error
			require.ErrorAs(t, err, &rpcErr)
			assert.Equal(t, rpcerror.CodeScopeDenied, rpcErr.Code)
		})
	}
}

// Given any principal, Then the handshakes stay callable: they re-establish
// identity and judge their own credential.
func TestAuthorizeMethod_GivenAHandshake_ThenAnyPrincipalMayCallIt(t *testing.T) {
	for _, method := range []agentrewire.RpcMethod{
		agentrewire.RpcMethod_RPC_METHOD_AUTH_PAIR, agentrewire.RpcMethod_RPC_METHOD_AUTH_CONNECT,
		agentrewire.RpcMethod_RPC_METHOD_AUTH_ACCOUNT, agentrewire.RpcMethod_RPC_METHOD_AUTH_DIRECT,
	} {
		_, err := AuthorizeMethod(personalToken(), uint32(method))
		require.NoError(t, err, method.String())
	}
}

// Given a cli device, a desktop, a browser ticket or a paired peer, Then no scope
// table applies: they keep full account (or pairing) trust.
func TestAuthorizeMethod_GivenAPrincipalThatIsNotAPersonalToken_ThenNothingIsRestricted(t *testing.T) {
	for _, kind := range []string{KindCLI, "desktop", "relay_client", ""} {
		state := protorpc.AuthState{Authenticated: true, DeviceFingerprint: "fp", AccountID: "42", Kind: kind}
		for _, method := range []agentrewire.RpcMethod{
			agentrewire.RpcMethod_RPC_METHOD_TERMINAL_OPEN, agentrewire.RpcMethod_RPC_METHOD_RUNTIME_RUN,
			agentrewire.RpcMethod_RPC_METHOD_RUNTIME_SUBMIT_TOOL_PERMISSION,
		} {
			class, err := AuthorizeMethod(state, uint32(method))
			require.NoError(t, err, kind+" "+method.String())
			assert.Empty(t, class)
		}
	}
}
