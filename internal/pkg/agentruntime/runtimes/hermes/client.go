package hermes

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/cago-frame/agents/provider"
	"github.com/gorilla/websocket"
)

// ErrGatewayUnreachable marks a failure to reach the `hermes serve` endpoint at
// all: a refused connection, a DNS failure, or a request that never completed.
var ErrGatewayUnreachable = errors.New("hermes gateway: unreachable")

// ErrGatewayUnauthorized marks a server that has authentication enabled. Hermes
// only injects `__HERMES_SESSION_TOKEN__` into the root page for loopback /
// insecure serves; a gated serve answers 401/403 or a token-less page. A gated
// dial recovers through the native PKCE credential + ws-ticket flow when a
// CredentialSource is injected; otherwise this error is surfaced as-is.
var ErrGatewayUnauthorized = errors.New("hermes gateway: authentication required")

// ErrGatewayProtocol marks a reachable server that did not speak the expected
// handshake (e.g. the root page carried no session token).
var ErrGatewayProtocol = errors.New("hermes gateway: unexpected protocol")

// ErrGatewayClosed marks a connection the server closed cleanly: the turn it
// carried is over.
var ErrGatewayClosed = errors.New("hermes gateway: connection closed")

// ErrGatewayLost marks a connection that died abnormally (reset, restart, a
// half-open socket). Distinct from ErrGatewayClosed so callers can tell "the
// server went away" from "this turn ended".
var ErrGatewayLost = errors.New("hermes gateway: connection lost")

// Session is the per-turn gateway contract. Production is *gatewaySession; tests
// inject a fake through SessionFactory. The Runtime owns the Session for exactly
// one turn and closes it when the turn ends.
type Session interface {
	// Create opens a fresh gateway session and returns (liveSID, storedKey).
	Create(ctx context.Context, cwd string) (liveSID, storedKey string, err error)
	// Resume reopens a stored session and returns its new live runtime id.
	Resume(ctx context.Context, storedKey string) (liveSID string, err error)
	// Usage snapshots the gateway's cumulative session usage (pre-submit baseline).
	Usage(ctx context.Context, liveSID string) (*provider.Usage, error)
	// Submit starts a turn; it returns once the gateway accepted it.
	Submit(ctx context.Context, liveSID, text string) error
	// Interrupt asks the gateway to stop the in-flight turn (idempotent).
	Interrupt(ctx context.Context, liveSID string) error
	// Events streams decoded event frames; it closes when the connection ends.
	Events() <-chan Event
	// Err reports the terminal read/connection error after Events closes.
	Err() error
	// Close tears the connection down.
	Close(ctx context.Context) error
}

// SessionFactory dials and readies a gateway connection.
type SessionFactory func(ctx context.Context, req sessionSpec) (Session, error)

// sessionSpec is the launch input the Runtime hands the factory. It is a small
// value (not agentruntime.RunRequest) so the factory has no reverse dependency
// on the run loop.
type sessionSpec struct {
	// URL is the canonical `http(s)://host:port` of the running `hermes serve`.
	URL string
	// AuthProvider is the persisted provider hint for a gated serve (may be empty).
	AuthProvider string
	// Credentials supplies the bearer token for a gated serve. nil means the dial
	// only supports the loopback `?token=` path.
	Credentials CredentialSource
}

// webSocketDialer is the injectable dial seam (gorilla's *websocket.Dialer in
// production, a scripted fake in tests).
type webSocketDialer interface {
	DialContext(ctx context.Context, urlStr string, requestHeader http.Header) (*websocket.Conn, *http.Response, error)
}

const (
	gatewayEventsBuffer  = 64
	gatewayReadyTimeout  = 60 * time.Second
	gatewayHTTPTimeout   = 15 * time.Second
	gatewayDialTimeout   = 15 * time.Second
	gatewayWSMessageSize = 8 * 1024 * 1024
)

// hermesSessionTokenRe extracts the loopback token Hermes injects into the root
// page: `window.__HERMES_SESSION_TOKEN__="<token>"`.
var hermesSessionTokenRe = regexp.MustCompile(`__HERMES_SESSION_TOKEN__\s*=\s*"([^"]+)"`)

// gatewaySession owns one WebSocket connection to a `hermes serve` and speaks
// newline-delimited JSON-RPC 2.0 over it. The codec (rpcConn) is transport
// agnostic; this type only moves bytes and classifies transport errors.
type gatewaySession struct {
	conn  *websocket.Conn
	codec *rpcConn

	closeOnce sync.Once
	done      chan struct{}
}

// defaultSessionFactory dials a gateway connection and waits for gateway.ready.
func defaultSessionFactory(ctx context.Context, spec sessionSpec) (Session, error) {
	sess, err := dialGatewayWithAuth(ctx, spec.URL, spec.AuthProvider, spec.Credentials, nil, nil)
	if err != nil {
		return nil, err
	}
	if err := sess.WaitReady(ctx); err != nil {
		_ = sess.Close(context.Background())
		return nil, err
	}
	return sess, nil
}

// dialGatewayWithAuth dials a `hermes serve`. It first tries the loopback token
// path; a gated serve (no token in the page) falls back to the bearer/ws-ticket
// flow when creds is non-nil. The returned session is connected but not
// necessarily ready; callers wait on WaitReady.
func dialGatewayWithAuth(ctx context.Context, baseURL, provider string, creds CredentialSource, dialer webSocketDialer, client *http.Client) (*gatewaySession, error) {
	parsed, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil || parsed.Host == "" {
		return nil, fmt.Errorf("%w: invalid server URL %q", ErrGatewayUnreachable, baseURL)
	}
	scheme := "ws"
	if parsed.Scheme == "https" || parsed.Scheme == "wss" {
		scheme = "wss"
	}

	token, tokenErr := fetchSessionToken(ctx, baseURL, client)
	var wsURL string
	switch {
	case tokenErr == nil:
		wsURL = scheme + "://" + parsed.Host + "/api/ws?token=" + url.QueryEscape(token)
	case errors.Is(tokenErr, ErrGatewayUnauthorized) && creds != nil:
		ticket, ticketErr := gatedWSTicket(ctx, baseURL, provider, creds, client)
		if ticketErr != nil {
			return nil, ticketErr
		}
		wsURL = scheme + "://" + parsed.Host + "/api/ws?ticket=" + url.QueryEscape(ticket)
	default:
		return nil, tokenErr
	}

	if dialer == nil {
		dialer = &websocket.Dialer{HandshakeTimeout: gatewayDialTimeout}
	}
	dialCtx, cancel := context.WithTimeout(ctx, gatewayDialTimeout)
	defer cancel()
	conn, resp, err := dialer.DialContext(dialCtx, wsURL, nil)
	if err != nil {
		if resp != nil && resp.Body != nil {
			_ = resp.Body.Close()
		}
		if resp != nil && (resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden) {
			return nil, fmt.Errorf("%w: websocket upgrade returned %s", ErrGatewayUnauthorized, resp.Status)
		}
		return nil, fmt.Errorf("%w: %v", ErrGatewayUnreachable, err)
	}
	conn.SetReadLimit(gatewayWSMessageSize)

	s := &gatewaySession{
		conn: conn,
		done: make(chan struct{}),
	}
	s.codec = newRPCConn(func(line []byte) error {
		return conn.WriteMessage(websocket.TextMessage, line)
	})
	go s.readLoop()
	return s, nil
}

// gatedWSTicket mints a ws-ticket from the injected credential source. An
// access token the server rejects is refreshed exactly once before giving up;
// a still-rejected ticket is reported as an expired login so the UI can say
// "log in again" instead of a generic failure.
func gatedWSTicket(ctx context.Context, baseURL, provider string, creds CredentialSource, client *http.Client) (string, error) {
	access, err := creds.AccessToken(ctx, baseURL, provider)
	if err != nil {
		return "", err
	}
	ticket, err := FetchWSTicket(ctx, baseURL, access, client)
	if errors.Is(err, ErrAccessTokenRejected) {
		creds.Invalidate(baseURL)
		if access, err = creds.AccessToken(ctx, baseURL, provider); err != nil {
			return "", err
		}
		ticket, err = FetchWSTicket(ctx, baseURL, access, client)
	}
	if err != nil {
		if errors.Is(err, ErrAccessTokenRejected) {
			return "", fmt.Errorf("%w: ws-ticket rejected even after refresh", ErrLoginExpired)
		}
		return "", err
	}
	return ticket, nil
}

// fetchSessionToken GETs the Hermes root page and extracts the loopback session
// token. Failures are classified so the UI can say "the server has auth enabled"
// instead of a generic connection error.
func fetchSessionToken(ctx context.Context, baseURL string, client *http.Client) (string, error) {
	if client == nil {
		client = &http.Client{Timeout: gatewayHTTPTimeout}
	}
	root := strings.TrimRight(strings.TrimSpace(baseURL), "/") + "/"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, root, nil)
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrGatewayUnreachable, err)
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrGatewayUnreachable, err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))

	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return "", fmt.Errorf("%w: server returned %s", ErrGatewayUnauthorized, resp.Status)
	}
	if m := hermesSessionTokenRe.FindSubmatch(body); len(m) == 2 {
		return string(m[1]), nil
	}
	// A gated `hermes serve` never puts the token in the page: a live one
	// redirects "/" to /login and serves the sign-in form there, and a headless
	// one answers 404. Both mean "auth provider configured", so the caller can
	// take the credential path instead of reporting a broken protocol.
	if strings.Contains(string(body), "__HERMES_AUTH_REQUIRED__=true") || resp.StatusCode == http.StatusNotFound || isHTML(resp) {
		return "", fmt.Errorf("%w: the server has an auth provider configured", ErrGatewayUnauthorized)
	}
	return "", fmt.Errorf("%w: no session token at %s", ErrGatewayProtocol, root)
}

// isHTML reports whether the response is an HTML document (the sign-in page a
// gated serve answers with after following its "/" redirect).
func isHTML(resp *http.Response) bool {
	return strings.Contains(strings.ToLower(resp.Header.Get("Content-Type")), "text/html")
}

// readLoop pumps WebSocket messages into the codec. A single message may carry
// several newline-delimited JSON frames, so each message is split by newline.
func (s *gatewaySession) readLoop() {
	defer close(s.done)
	for {
		_, message, err := s.conn.ReadMessage()
		if err != nil {
			s.codec.failConnection(s.classifyReadError(err))
			return
		}
		for _, line := range strings.Split(string(message), "\n") {
			if err := s.codec.handleLine([]byte(line)); err != nil {
				s.codec.failConnection(err)
				return
			}
		}
	}
}

// classifyReadError distinguishes an explicit Close (the turn is over) from a
// clean server close and an abnormal connection loss.
func (s *gatewaySession) classifyReadError(err error) error {
	if s.codec.isClosed() {
		return errSessionClosed
	}
	return classifyReadError(err)
}

func classifyReadError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, io.EOF) || errors.Is(err, net.ErrClosed) ||
		strings.Contains(err.Error(), "use of closed network connection") {
		return fmt.Errorf("%w: %v", ErrGatewayClosed, err)
	}
	if websocket.IsCloseError(err,
		websocket.CloseNormalClosure,
		websocket.CloseGoingAway,
		websocket.CloseNoStatusReceived,
	) {
		return fmt.Errorf("%w: %v", ErrGatewayClosed, err)
	}
	return fmt.Errorf("%w: %v", ErrGatewayLost, err)
}

func (s *gatewaySession) WaitReady(ctx context.Context) error { return s.codec.WaitReady(ctx) }
func (s *gatewaySession) Events() <-chan Event                { return s.codec.Events() }
func (s *gatewaySession) Err() error                          { return s.codec.Err() }

// Close signals the close first, then tears the socket down. Order matters: a
// racing write must observe "closed" (see errSessionClosed), never a raw socket
// error that callers would have to match by string.
func (s *gatewaySession) Close(ctx context.Context) error {
	s.closeOnce.Do(func() {
		s.codec.signalClose()
		_ = s.conn.Close()
	})
	select {
	case <-s.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *gatewaySession) Create(ctx context.Context, cwd string) (string, string, error) {
	params := map[string]any{"cols": 80}
	if strings.TrimSpace(cwd) != "" {
		params["cwd"] = cwd
	}
	result, err := s.codec.call(ctx, "session.create", params)
	if err != nil {
		return "", "", err
	}
	var r struct {
		SessionID       string `json:"session_id"`
		StoredSessionID string `json:"stored_session_id"`
	}
	if err := json.Unmarshal(result, &r); err != nil {
		return "", "", fmt.Errorf("hermes gateway: decode session.create: %w", err)
	}
	if strings.TrimSpace(r.SessionID) == "" {
		return "", "", errors.New("hermes gateway: session.create returned no session_id")
	}
	stored := strings.TrimSpace(r.StoredSessionID)
	if stored == "" {
		stored = strings.TrimSpace(r.SessionID)
	}
	return strings.TrimSpace(r.SessionID), stored, nil
}

func (s *gatewaySession) Resume(ctx context.Context, storedKey string) (string, error) {
	result, err := s.codec.call(ctx, "session.resume", map[string]any{"session_id": strings.TrimSpace(storedKey)})
	if err != nil {
		return "", err
	}
	var r struct {
		SessionID string `json:"session_id"`
	}
	if err := json.Unmarshal(result, &r); err != nil {
		return "", fmt.Errorf("hermes gateway: decode session.resume: %w", err)
	}
	if strings.TrimSpace(r.SessionID) == "" {
		return "", errors.New("hermes gateway: session.resume returned no session_id")
	}
	return strings.TrimSpace(r.SessionID), nil
}

func (s *gatewaySession) Usage(ctx context.Context, liveSID string) (*provider.Usage, error) {
	result, err := s.codec.call(ctx, "session.usage", map[string]any{"session_id": liveSID})
	if err != nil {
		return nil, err
	}
	var u usagePayload
	if err := json.Unmarshal(result, &u); err != nil {
		return nil, err
	}
	return mapUsage(u), nil
}

func (s *gatewaySession) Submit(ctx context.Context, liveSID, text string) error {
	_, err := s.codec.call(ctx, "prompt.submit", map[string]any{"session_id": liveSID, "text": text})
	return err
}

func (s *gatewaySession) Interrupt(ctx context.Context, liveSID string) error {
	_, err := s.codec.call(ctx, "session.interrupt", map[string]any{"session_id": liveSID})
	return err
}
