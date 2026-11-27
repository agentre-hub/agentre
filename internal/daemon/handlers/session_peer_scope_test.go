package handlers_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/agentre-hub/agentre/internal/daemon/auth"
	"github.com/agentre-hub/agentre/internal/daemon/handlers"
	"github.com/agentre-hub/agentre/pkg/wire/agentrewire"
	"github.com/agentre-hub/agentre/pkg/wire/devicefp"
	"github.com/agentre-hub/agentre/pkg/wire/protorpc"
	"github.com/agentre-hub/agentre/pkg/wire/rpcerror"
)

const (
	peerScopeAccount = "acct-1"
	patOwn           = devicefp.Initiator("pat:own")
	foreignOrigin    = devicefp.Initiator("sha256:someone-else")
)

// principalContext captures the dispatch ctx of a connection authenticated as
// kind under the daemon's account, the way a real request sees it.
func principalContext(t *testing.T, kind string) context.Context {
	t.Helper()
	clientTransport, serverTransport := newMemFrameConnPair()
	const probeMethodID = 0x7000_0002
	captured := make(chan context.Context, 1)
	registry := protorpc.NewRegistry()
	protorpc.RegisterMethod(registry, probeMethodID,
		func() *agentrewire.Empty { return &agentrewire.Empty{} },
		func(ctx context.Context, _ *agentrewire.Empty) (*agentrewire.Empty, error) {
			captured <- ctx
			return &agentrewire.Empty{}, nil
		})
	client := protorpc.NewConn(clientTransport, protorpc.NewRegistry())
	server := protorpc.NewConn(serverTransport, registry)
	server.SetAuth(protorpc.AuthState{Authenticated: true, DeviceFingerprint: string(patOwn), AccountID: peerScopeAccount, Kind: kind})
	go client.Serve(context.Background())
	go server.Serve(context.Background())
	t.Cleanup(func() { _ = client.Close(); _ = server.Close() })
	_, err := protorpc.CallMethod(context.Background(), client, probeMethodID,
		&agentrewire.Empty{}, func() *agentrewire.Empty { return &agentrewire.Empty{} })
	require.NoError(t, err)
	return <-captured
}

func daemonAccount() string { return peerScopeAccount }

// Given a personal token on a chat call, When it names another initiator, Then
// ResolveSessionPeer refuses: chat reaches only the sessions the token started.
func TestResolveSessionPeer_GivenAPersonalTokensChatCall_WhenItNamesAnotherInitiator_ThenRefused(t *testing.T) {
	ctx := auth.WithMethodScope(principalContext(t, auth.KindPersonalToken), auth.ScopeChat)

	_, err := handlers.ResolveSessionPeer(ctx, foreignOrigin, daemonAccount)

	var rpcErr *rpcerror.Error
	require.ErrorAs(t, err, &rpcErr)
	assert.Equal(t, rpcerror.CodeScopeDenied, rpcErr.Code)
	assert.Equal(t, "token may only act on sessions it started", rpcErr.Message)
}

func TestResolveSessionPeer_GivenAPersonalTokensChatCall_WhenItNamesItself_ThenResolvesItsOwnPeer(t *testing.T) {
	ctx := auth.WithMethodScope(principalContext(t, auth.KindPersonalToken), auth.ScopeChat)

	for _, origin := range []devicefp.Initiator{"", patOwn} {
		peer, err := handlers.ResolveSessionPeer(ctx, origin, daemonAccount)
		require.NoError(t, err)
		assert.Equal(t, patOwn, peer)
	}
}

// Given a personal token on a read call, Then it may name any initiator of the
// account: read sees every session.
func TestResolveSessionPeer_GivenAPersonalTokensReadCall_WhenItNamesAnotherInitiator_ThenResolvesThatInitiator(t *testing.T) {
	ctx := auth.WithMethodScope(principalContext(t, auth.KindPersonalToken), auth.ScopeRead)

	peer, err := handlers.ResolveSessionPeer(ctx, foreignOrigin, daemonAccount)

	require.NoError(t, err)
	assert.Equal(t, foreignOrigin, peer)
}

// Given a personal token whose call carries no scope verdict at all, Then naming
// another initiator is refused: the default is closed, not open.
func TestResolveSessionPeer_GivenAPersonalTokenWithoutAScopeVerdict_WhenItNamesAnotherInitiator_ThenRefused(t *testing.T) {
	ctx := principalContext(t, auth.KindPersonalToken)

	_, err := handlers.ResolveSessionPeer(ctx, foreignOrigin, daemonAccount)

	var rpcErr *rpcerror.Error
	require.ErrorAs(t, err, &rpcErr)
	assert.Equal(t, rpcerror.CodeScopeDenied, rpcErr.Code)
}

func TestResolveSessionPeer_GivenACLIDevice_WhenItNamesAnotherInitiator_ThenResolvesThatInitiator(t *testing.T) {
	ctx := principalContext(t, auth.KindCLI)

	peer, err := handlers.ResolveSessionPeer(ctx, foreignOrigin, daemonAccount)

	require.NoError(t, err)
	assert.Equal(t, foreignOrigin, peer)
}
