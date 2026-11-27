package auth

import (
	"context"
	"io"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/agentre-hub/agentre/pkg/wire/agentrewire"
	"github.com/agentre-hub/agentre/pkg/wire/protorpc"
	"github.com/agentre-hub/agentre/pkg/wire/rpcerror"
)

type chanFrameConn struct {
	in, out chan []byte
	done    chan struct{}
	once    *sync.Once
}

func chanFramePair() (*chanFrameConn, *chanFrameConn) {
	a, b := make(chan []byte, 8), make(chan []byte, 8)
	done, once := make(chan struct{}), &sync.Once{}
	return &chanFrameConn{in: a, out: b, done: done, once: once}, &chanFrameConn{in: b, out: a, done: done, once: once}
}

func (c *chanFrameConn) ReadFrame() ([]byte, error) {
	select {
	case frame := <-c.in:
		return frame, nil
	case <-c.done:
		return nil, io.EOF
	}
}

func (c *chanFrameConn) WriteFrame(frame []byte) error {
	select {
	case c.out <- append([]byte(nil), frame...):
		return nil
	case <-c.done:
		return io.EOF
	}
}

func (c *chanFrameConn) Close() error          { c.once.Do(func() { close(c.done) }); return nil }
func (c *chanFrameConn) Done() <-chan struct{} { return c.done }

// fakeReverifier answers re-verifications of the remembered credential with
// whatever the test sets, and counts them.
type fakeReverifier struct {
	mu      sync.Mutex
	verdict Introspection
	err     error
	calls   int
	seen    []string
}

func (f *fakeReverifier) set(verdict Introspection, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.verdict, f.err = verdict, err
}

func (f *fakeReverifier) verify(_ context.Context, credential string) (Introspection, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	f.seen = append(f.seen, credential)
	return f.verdict, f.err
}

const guardedMethod = agentrewire.RpcMethod_RPC_METHOD_RUNTIME_STEER

// guardRig serves one chat method behind a MethodGuard and authenticates the
// serving connection as state.
func guardRig(t *testing.T, state protorpc.AuthState) (*MethodGuard, *protorpc.Conn, *protorpc.Conn, *fakeReverifier) {
	t.Helper()
	reverifier := &fakeReverifier{}
	guard := NewMethodGuard(reverifier.verify)
	registry := protorpc.NewRegistry()
	registry.SetMethodGuard(guard.Guard)
	protorpc.RegisterMethod(registry, uint32(guardedMethod),
		func() *agentrewire.Empty { return &agentrewire.Empty{} },
		func(context.Context, *agentrewire.Empty) (*agentrewire.Empty, error) {
			return &agentrewire.Empty{}, nil
		})
	a, b := chanFramePair()
	client, server := protorpc.NewConn(a, protorpc.NewRegistry()), protorpc.NewConn(b, registry)
	server.SetAuth(state)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go client.Serve(ctx)
	go server.Serve(ctx)
	return guard, client, server, reverifier
}

func callGuarded(client *protorpc.Conn) error {
	_, err := protorpc.CallMethod(context.Background(), client, uint32(guardedMethod), &agentrewire.Empty{}, func() *agentrewire.Empty { return &agentrewire.Empty{} })
	return err
}

func requireCallCode(t *testing.T, err error, code int32) {
	t.Helper()
	var rpcErr *protorpc.Error
	require.ErrorAs(t, err, &rpcErr)
	assert.Equal(t, code, rpcErr.Code)
}

func patState(scopes ...string) protorpc.AuthState {
	return protorpc.AuthState{Authenticated: true, DeviceFingerprint: "pat:own", AccountID: "42", Kind: KindPersonalToken, Scopes: scopes}
}

func patVerdict(scopes ...string) Introspection {
	return Introspection{AccountID: "42", Kind: KindPersonalToken, PeerFingerprint: "pat:own", Scopes: scopes}
}

// Given an established personal-token connection, When the token is revoked,
// Then its next call is refused as credential invalid: every call re-verifies the
// token (the introspection cache bounds that to one server round trip a minute,
// so revocation reaches an open relay connection within 60 seconds).
func TestMethodGuard_GivenAnEstablishedPersonalToken_WhenItIsRevoked_ThenTheNextCallIsRefused(t *testing.T) {
	guard, client, server, reverifier := guardRig(t, patState("chat"))
	guard.Bind(server, "pat-secret")
	reverifier.set(patVerdict("chat"), nil)
	require.NoError(t, callGuarded(client))

	reverifier.set(Introspection{}, ErrCredentialInvalid)

	requireCallCode(t, callGuarded(client), rpcerror.CodeUnauthorized)
	assert.Equal(t, []string{"pat-secret", "pat-secret"}, reverifier.seen)
}

// Given a token whose scopes changed on the server, Then the fresh verdict — not
// the handshake's — decides.
func TestMethodGuard_GivenTheServerNowGrantsFewerScopes_ThenTheFreshVerdictDecides(t *testing.T) {
	guard, client, server, reverifier := guardRig(t, patState("chat"))
	guard.Bind(server, "pat-secret")
	reverifier.set(patVerdict("read"), nil)

	err := callGuarded(client)

	requireCallCode(t, err, rpcerror.CodeScopeDenied)
	assert.EqualError(t, err, "token lacks scope chat")
}

// Given the credential now names another peer, Then the call is refused: the
// connection's identity cannot drift under it.
func TestMethodGuard_GivenTheCredentialNowNamesAnotherPeer_ThenRefused(t *testing.T) {
	guard, client, server, reverifier := guardRig(t, patState("chat"))
	guard.Bind(server, "pat-secret")
	verdict := patVerdict("chat")
	verdict.PeerFingerprint = "pat:someone-else"
	reverifier.set(verdict, nil)

	requireCallCode(t, callGuarded(client), rpcerror.CodeUnauthorized)
}

// Given a personal-token connection with no remembered credential, Then every
// call is refused: the default is closed.
func TestMethodGuard_GivenAPersonalTokenWithoutARememberedCredential_ThenRefused(t *testing.T) {
	_, client, _, reverifier := guardRig(t, patState("chat"))

	requireCallCode(t, callGuarded(client), rpcerror.CodeUnauthorized)
	assert.Zero(t, reverifier.calls)
}

// Given a cli device, Then nothing is re-verified and nothing is restricted.
func TestMethodGuard_GivenACLIDevice_ThenTheGuardStaysOutOfTheWay(t *testing.T) {
	_, client, _, reverifier := guardRig(t, protorpc.AuthState{Authenticated: true, DeviceFingerprint: "fp", AccountID: "42", Kind: KindCLI})

	require.NoError(t, callGuarded(client))
	assert.Zero(t, reverifier.calls)
}

// Given a connection that re-authenticated as something other than a personal
// token, Then the remembered token is forgotten.
func TestMethodGuard_GivenForget_ThenTheRememberedCredentialIsGone(t *testing.T) {
	guard, client, server, reverifier := guardRig(t, patState("chat"))
	guard.Bind(server, "pat-secret")
	guard.Forget(server)
	reverifier.set(patVerdict("chat"), nil)

	requireCallCode(t, callGuarded(client), rpcerror.CodeUnauthorized)
	assert.Zero(t, reverifier.calls)
}
