package hermes

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeCreds is a scripted CredentialSource: it hands out a queue of results and
// records invalidations.
type fakeCreds struct {
	mu         sync.Mutex
	tokens     []string
	errs       []error
	invalidFor []string
}

func (f *fakeCreds) AccessToken(_ context.Context, _ string, _ string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.tokens) == 0 {
		if len(f.errs) == 0 {
			return "", ErrLoginRequired
		}
		err := f.errs[0]
		f.errs = f.errs[1:]
		return "", err
	}
	tok := f.tokens[0]
	f.tokens = f.tokens[1:]
	return tok, nil
}

func (f *fakeCreds) Invalidate(baseURL string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.invalidFor = append(f.invalidFor, baseURL)
}

// mountGatedGateway wires what a gated `hermes serve` exposes besides the root
// page: the ws-ticket minter and an `/api/ws` upgrade that requires `?ticket=`.
func mountGatedGateway(t *testing.T, mux *http.ServeMux, validBearer, ticket string, rejectFirstBearer bool) {
	t.Helper()
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	var seenBearerMu sync.Mutex
	seen := 0
	mux.HandleFunc("/api/auth/ws-ticket", func(w http.ResponseWriter, r *http.Request) {
		seenBearerMu.Lock()
		seen++
		first := seen == 1
		seenBearerMu.Unlock()
		if rejectFirstBearer && first {
			http.Error(w, "expired", http.StatusUnauthorized)
			return
		}
		if r.Header.Get("Authorization") != "Bearer "+validBearer {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		_, _ = w.Write([]byte(`{"ticket":"` + ticket + `","ttl_seconds":30}`))
	})
	mux.HandleFunc("/api/ws", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("ticket") != ticket {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()
		writeReady(t, conn)
		holdConn(conn)
	})
}

// gatedWSServer serves a token-less root page (404, the older shape), the
// ws-ticket endpoint and the ticket-only upgrade.
func gatedWSServer(t *testing.T, validBearer, ticket string, rejectFirstBearer bool) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		// A gated serve disables the web UI: no session token anywhere.
		http.NotFound(w, r)
	})
	mountGatedGateway(t, mux, validBearer, ticket, rejectFirstBearer)
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	return server
}

// realGatedWSServer mirrors what a live gated `hermes serve` answers at "/":
// a 302 to /login whose page carries no session token and no auth marker.
func realGatedWSServer(t *testing.T, validBearer, ticket string) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/login", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte("<!doctype html><html><head><title>Sign in — Hermes Agent</title></head>" +
			"<body><form method=\"post\">Sign in</form></body></html>"))
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		http.Redirect(w, r, "/login?next=%2F", http.StatusFound)
	})
	mountGatedGateway(t, mux, validBearer, ticket, false)
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	return server
}

func TestDialGatewayWithAuth_GivenGatedServe_ThenConnectsWithTicket(t *testing.T) {
	server := gatedWSServer(t, "good-access", "ticket-xyz", false)

	var seenURL string
	dialer := testDialer(func(ctx context.Context, urlStr string, header http.Header) (*websocket.Conn, *http.Response, error) {
		seenURL = urlStr
		return websocket.DefaultDialer.DialContext(ctx, urlStr, header)
	})

	sess, err := dialGatewayWithAuth(context.Background(), server.URL, "basic", &fakeCreds{tokens: []string{"good-access"}}, dialer, nil)

	require.NoError(t, err)
	defer func() { _ = sess.Close(context.Background()) }()
	require.NoError(t, sess.WaitReady(context.Background()))
	assert.Contains(t, seenURL, "ticket=ticket-xyz")
	assert.NotContains(t, seenURL, "token=")
}

// A live gated serve answers "/" with a 302 to /login (no auth marker, no
// token). The credential path must be reached from that shape too.
func TestDialGatewayWithAuth_GivenRealGatedLoginPage_ThenConnectsWithTicket(t *testing.T) {
	server := realGatedWSServer(t, "good-access", "ticket-real")

	var seenURL string
	dialer := testDialer(func(ctx context.Context, urlStr string, header http.Header) (*websocket.Conn, *http.Response, error) {
		seenURL = urlStr
		return websocket.DefaultDialer.DialContext(ctx, urlStr, header)
	})

	sess, err := dialGatewayWithAuth(context.Background(), server.URL, "basic", &fakeCreds{tokens: []string{"good-access"}}, dialer, nil)

	require.NoError(t, err)
	defer func() { _ = sess.Close(context.Background()) }()
	require.NoError(t, sess.WaitReady(context.Background()))
	assert.Contains(t, seenURL, "ticket=ticket-real")
	assert.NotContains(t, seenURL, "token=")
}

func TestDialGatewayWithAuth_GivenNoCredential_ThenLoginRequired(t *testing.T) {
	server := gatedWSServer(t, "good-access", "ticket-xyz", false)

	_, err := dialGatewayWithAuth(context.Background(), server.URL, "basic", &fakeCreds{errs: []error{ErrLoginRequired}}, nil, nil)

	require.ErrorIs(t, err, ErrLoginRequired)
}

func TestDialGatewayWithAuth_GivenExpiredStoredToken_ThenLoginExpired(t *testing.T) {
	server := gatedWSServer(t, "good-access", "ticket-xyz", false)

	_, err := dialGatewayWithAuth(context.Background(), server.URL, "basic", &fakeCreds{errs: []error{ErrLoginExpired}}, nil, nil)

	require.ErrorIs(t, err, ErrLoginExpired)
}

func TestDialGatewayWithAuth_GivenRejectedAccessToken_ThenRefreshesOnceAndRetries(t *testing.T) {
	server := gatedWSServer(t, "fresh-access", "ticket-xyz", true)
	creds := &fakeCreds{tokens: []string{"stale-access", "fresh-access"}}

	sess, err := dialGatewayWithAuth(context.Background(), server.URL, "basic", creds, nil, nil)

	require.NoError(t, err)
	defer func() { _ = sess.Close(context.Background()) }()
	require.NoError(t, sess.WaitReady(context.Background()))
	assert.Equal(t, []string{server.URL}, creds.invalidFor, "a rejected access token must trigger exactly one invalidation")
}

func TestDialGatewayWithAuth_GivenRejectedEvenAfterRefresh_ThenLoginExpired(t *testing.T) {
	server := gatedWSServer(t, "never-matches", "ticket-xyz", false)
	// The first bearer is rejected, refresh yields another rejected one.
	creds := &fakeCreds{tokens: []string{"stale-access", "also-stale"}}

	_, err := dialGatewayWithAuth(context.Background(), server.URL, "basic", creds, nil, nil)

	require.ErrorIs(t, err, ErrLoginExpired)
}

func TestDialGatewayWithAuth_GivenLoopbackPath_DoesNotUseCredentials(t *testing.T) {
	server := newHermesTestServer(t, "tok-loopback", func(conn *websocket.Conn) {
		writeReady(t, conn)
		holdConn(conn)
	})
	creds := &fakeCreds{tokens: []string{"should-not-be-used"}}

	sess, err := dialGatewayWithAuth(context.Background(), server.URL, "basic", creds, nil, nil)

	require.NoError(t, err)
	defer func() { _ = sess.Close(context.Background()) }()
	require.NoError(t, sess.WaitReady(context.Background()))
	assert.Empty(t, creds.invalidFor)
}

func TestRuntime_GivenGatedBackend_PassesAuthProviderAndCredentials(t *testing.T) {
	var captured sessionSpec
	rt := NewWithCredentials(func(_ context.Context, spec sessionSpec) (Session, error) {
		captured = spec
		return newFakeSession(), nil
	}, &fakeCreds{})

	out, _, err := rt.Run(context.Background(), runRequest(77))
	require.NoError(t, err)
	assert.Equal(t, "http://127.0.0.1:9119", captured.URL)
	_ = out
}

func TestRuntime_GivenGatedBackend_CarriesAuthProvider(t *testing.T) {
	var captured sessionSpec
	rt := NewWithCredentials(func(_ context.Context, spec sessionSpec) (Session, error) {
		captured = spec
		return newFakeSession(), nil
	}, &fakeCreds{})

	req := runRequest(78)
	req.Backend.HermesAuthProvider = "basic"
	out, _, err := rt.Run(context.Background(), req)
	require.NoError(t, err)
	assert.Equal(t, "basic", captured.AuthProvider)
	assert.NotNil(t, captured.Credentials)
	_ = out
}

func TestGatedCredentialErrorsAreDistinctFromGatewayErrors(t *testing.T) {
	assert.False(t, errors.Is(ErrLoginRequired, ErrGatewayUnauthorized))
}
