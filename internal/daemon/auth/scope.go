package auth

import (
	"context"
	"sync"

	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"

	"github.com/agentre-hub/agentre/pkg/wire/agentrewire"
	"github.com/agentre-hub/agentre/pkg/wire/protorpc"
	"github.com/agentre-hub/agentre/pkg/wire/rpcerror"
)

// Introspected credential kinds this package acts on. The server issues others
// (desktop, agentred, relay_client, server_mirror); they all carry full account
// trust, and so does a cli device: only a personal token is scope-limited.
const (
	KindCLI           = "cli"
	KindPersonalToken = "personal_token"
)

// Scope is one permission a personal token can hold (spec 「个人 token 与范围」).
type Scope string

const (
	ScopeRead           Scope = "read"
	ScopeChat           Scope = "chat"
	ScopeApprove        Scope = "approve"
	ScopeResourcesWrite Scope = "resources:write"
	ScopeDevicesWrite   Scope = "devices:write"
)

// methodScopes is the single table of what a personal token may call on this
// daemon over relay RPC, and the scope each call needs (design decision 16: the
// server cannot see relay RPC contents, so agentred checks here). A method that
// is not listed — terminal, filesystem, port forward, LLM upsert, session
// delete, transcript import … — is refused to every personal token.
var methodScopes = map[agentrewire.RpcMethod]Scope{
	// read: sessions, messages, pending items, following output, device facts.
	agentrewire.RpcMethod_RPC_METHOD_SESSION_LIST:            ScopeRead,
	agentrewire.RpcMethod_RPC_METHOD_SESSION_COUNTS:          ScopeRead,
	agentrewire.RpcMethod_RPC_METHOD_SESSION_PULL:            ScopeRead,
	agentrewire.RpcMethod_RPC_METHOD_SESSION_PENDING_WAITERS: ScopeRead,
	agentrewire.RpcMethod_RPC_METHOD_SESSION_ATTACH:          ScopeRead,
	agentrewire.RpcMethod_RPC_METHOD_RUNTIME_CAPABILITIES:    ScopeRead,
	agentrewire.RpcMethod_RPC_METHOD_HEALTH_PING:             ScopeRead,
	agentrewire.RpcMethod_RPC_METHOD_CLI_MODELS_LIST:         ScopeRead,
	agentrewire.RpcMethod_RPC_METHOD_DEVICE_FACTS:            ScopeRead,

	// chat: start, continue, steer, stop, answer questions, per-session overrides.
	agentrewire.RpcMethod_RPC_METHOD_RUNTIME_RUN:                  ScopeChat,
	agentrewire.RpcMethod_RPC_METHOD_RUNTIME_STEER:                ScopeChat,
	agentrewire.RpcMethod_RPC_METHOD_RUNTIME_CANCEL_STEER:         ScopeChat,
	agentrewire.RpcMethod_RPC_METHOD_RUNTIME_ABORT:                ScopeChat,
	agentrewire.RpcMethod_RPC_METHOD_RUNTIME_DRAIN_PENDING:        ScopeChat,
	agentrewire.RpcMethod_RPC_METHOD_RUNTIME_SUBMIT_ANSWER:        ScopeChat,
	agentrewire.RpcMethod_RPC_METHOD_SET_MODEL_TARGET:             ScopeChat,
	agentrewire.RpcMethod_RPC_METHOD_SET_SESSION_REASONING_EFFORT: ScopeChat,
	agentrewire.RpcMethod_RPC_METHOD_SET_SESSION_CLI_MODEL:        ScopeChat,
	agentrewire.RpcMethod_RPC_METHOD_RUNTIME_SET_CLI_MODEL:        ScopeChat,
	agentrewire.RpcMethod_RPC_METHOD_RUNTIME_SET_PERMISSION_MODE:  ScopeChat,

	// approve: tool permissions and approval cards.
	agentrewire.RpcMethod_RPC_METHOD_RUNTIME_SUBMIT_TOOL_PERMISSION: ScopeApprove,
	agentrewire.RpcMethod_RPC_METHOD_TOOL_APPROVAL_ANSWER:           ScopeApprove,

	// resources:write: test a saved provider, discover its models (decision 13).
	agentrewire.RpcMethod_RPC_METHOD_ENGINE_TEST:     ScopeResourcesWrite,
	agentrewire.RpcMethod_RPC_METHOD_ENGINE_DISCOVER: ScopeResourcesWrite,

	// devices:write: upgrade the device.
	agentrewire.RpcMethod_RPC_METHOD_AGENTRED_SELF_UPDATE: ScopeDevicesWrite,
}

// handshakeMethods stay callable by anyone: each judges its own credential and
// replaces the connection's identity, so no scope of the old identity applies.
var handshakeMethods = map[agentrewire.RpcMethod]struct{}{
	agentrewire.RpcMethod_RPC_METHOD_AUTH_PAIR:    {},
	agentrewire.RpcMethod_RPC_METHOD_AUTH_CONNECT: {},
	agentrewire.RpcMethod_RPC_METHOD_AUTH_ACCOUNT: {},
	agentrewire.RpcMethod_RPC_METHOD_AUTH_DIRECT:  {},
}

var (
	// errMethodNotTabled refuses a personal token a method no scope grants.
	errMethodNotTabled = &rpcerror.Error{Code: rpcerror.CodeScopeDenied, Message: "personal tokens cannot call this method"}
	// ErrForeignSession refuses a personal token acting (anything but a read) on
	// a session whose recorded initiator is not the token's own fingerprint.
	ErrForeignSession = &rpcerror.Error{Code: rpcerror.CodeScopeDenied, Message: "token may only act on sessions it started"}
)

// AuthorizeMethod decides whether a connection may call methodID. For a
// personal token it returns the scope class of the call (read for a session
// read even when chat satisfied it); for every other principal it returns ""
// and never refuses — they are not scope-limited.
func AuthorizeMethod(state protorpc.AuthState, methodID uint32) (Scope, error) {
	if state.Kind != KindPersonalToken {
		return "", nil
	}
	method := agentrewire.RpcMethod(methodID)
	if _, ok := handshakeMethods[method]; ok {
		return "", nil
	}
	required, ok := methodScopes[method]
	if !ok {
		return "", errMethodNotTabled
	}
	if !holds(state.Scopes, required) {
		return "", rpcerror.ErrScopeDenied(string(required))
	}
	return required, nil
}

// holds reports whether granted satisfies required. chat includes the session
// reads (spec: chat 「含 read 中对会话的读取」).
func holds(granted []string, required Scope) bool {
	for _, scope := range granted {
		if Scope(scope) == required || (required == ScopeRead && Scope(scope) == ScopeChat) {
			return true
		}
	}
	return false
}

type methodScopeKey struct{}

// WithMethodScope records the scope class AuthorizeMethod gave this call.
func WithMethodScope(ctx context.Context, scope Scope) context.Context {
	return context.WithValue(ctx, methodScopeKey{}, scope)
}

// RequireOwnSession refuses a personal token that names an initiator other than
// itself on any call that is not a read: chat and approve reach only the
// sessions the token started (the sessions recorded under its fingerprint).
// A call without a recorded scope class counts as an action — closed by default.
func RequireOwnSession(ctx context.Context, originPeer string) error {
	if MayActOnSession(ctx, originPeer) {
		return nil
	}
	if scope, _ := ctx.Value(methodScopeKey{}).(Scope); scope == ScopeRead {
		return nil
	}
	return ErrForeignSession
}

// MayActOnSession reports whether the calling connection may act (chat or
// approve, not just read) on the session recorded under originPeer: always,
// except a personal token on a session another initiator started. A read that
// reaches such a session — session.attach following its output — must not
// hand the token anything it can act on afterwards.
func MayActOnSession(ctx context.Context, originPeer string) bool {
	conn := protorpc.ConnFromContext(ctx)
	if conn == nil {
		return true
	}
	state := conn.Auth()
	return state.Kind != KindPersonalToken || originPeer == "" || originPeer == state.DeviceFingerprint
}

// IsPersonalToken reports whether the calling connection is a personal token,
// and its fingerprint (the initiator recorded on the sessions it starts).
func IsPersonalToken(ctx context.Context) (string, bool) {
	conn := protorpc.ConnFromContext(ctx)
	if conn == nil {
		return "", false
	}
	state := conn.Auth()
	return state.DeviceFingerprint, state.Kind == KindPersonalToken
}

// MethodGuard is agentred's protorpc.MethodGuard. Every call on a personal
// token's connection re-verifies the token it handshook with — the
// Introspector's 60-second cache keeps that to one server round trip a minute,
// and is what bounds revocation on an open relay connection to 60 seconds — and
// then passes AuthorizeMethod against the fresh verdict. The verdict's scope
// class travels on the context so RequireOwnSession can tell a read from an
// action. Every other principal passes untouched.
type MethodGuard struct {
	reverify func(ctx context.Context, credential string) (Introspection, error)

	mu          sync.Mutex
	credentials map[*protorpc.Conn]string
}

// NewMethodGuard builds the guard over reverify, the same account verification
// the handshake ran (AuthHandlers.VerifyAccount).
func NewMethodGuard(reverify func(ctx context.Context, credential string) (Introspection, error)) *MethodGuard {
	return &MethodGuard{reverify: reverify, credentials: map[*protorpc.Conn]string{}}
}

// Bind remembers the personal token conn handshook with, until conn closes or
// re-authenticates (Forget). The token stays in memory only, never on disk.
func (g *MethodGuard) Bind(conn *protorpc.Conn, credential string) {
	if conn == nil {
		return
	}
	g.mu.Lock()
	_, watched := g.credentials[conn]
	g.credentials[conn] = credential
	g.mu.Unlock()
	if !watched {
		go func() {
			<-conn.Done()
			g.Forget(conn)
		}()
	}
}

// Forget drops what Bind remembered for conn.
func (g *MethodGuard) Forget(conn *protorpc.Conn) {
	g.mu.Lock()
	defer g.mu.Unlock()
	delete(g.credentials, conn)
}

// Guard judges one call; see MethodGuard.
func (g *MethodGuard) Guard(ctx context.Context, methodID uint32) (context.Context, error) {
	conn := protorpc.ConnFromContext(ctx)
	if conn == nil {
		return ctx, nil
	}
	state := conn.Auth()
	if state.Kind != KindPersonalToken {
		return ctx, nil
	}
	if _, ok := handshakeMethods[agentrewire.RpcMethod(methodID)]; ok {
		return ctx, nil
	}
	fresh, err := g.freshState(ctx, conn, state)
	if err != nil {
		logger.Ctx(ctx).Info("auth.MethodGuard.Guard: personal token no longer verifies",
			zap.Uint32("methodId", methodID), zap.String("peerFingerprint", state.DeviceFingerprint),
			zap.String("tokenName", state.TokenName), zap.Error(err))
		return ctx, err
	}
	scope, err := AuthorizeMethod(fresh, methodID)
	if err != nil {
		logger.Ctx(ctx).Info("auth.MethodGuard.Guard: refused a personal token call",
			zap.Uint32("methodId", methodID), zap.String("peerFingerprint", state.DeviceFingerprint),
			zap.String("tokenName", state.TokenName), zap.Error(err))
		return ctx, err
	}
	return WithMethodScope(ctx, scope), nil
}

// freshState re-verifies conn's remembered token and returns its identity with
// the server's current scopes. A token that is gone, revoked, or now names
// another account or peer is refused as credential invalid.
func (g *MethodGuard) freshState(ctx context.Context, conn *protorpc.Conn, state protorpc.AuthState) (protorpc.AuthState, error) {
	g.mu.Lock()
	credential, ok := g.credentials[conn]
	g.mu.Unlock()
	if !ok || g.reverify == nil {
		return protorpc.AuthState{}, ErrCredentialInvalid
	}
	verified, err := g.reverify(ctx, credential)
	if err != nil {
		return protorpc.AuthState{}, err
	}
	if verified.Kind != KindPersonalToken || verified.PeerFingerprint != state.DeviceFingerprint || verified.AccountID != state.AccountID {
		return protorpc.AuthState{}, ErrCredentialInvalid
	}
	state.Scopes = verified.Scopes
	return state, nil
}
