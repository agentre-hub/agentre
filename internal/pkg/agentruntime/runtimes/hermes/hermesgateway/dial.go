// Package hermesgateway is the side-effect-free transport handshake of a running
// `hermes serve`: it resolves how to authenticate (the loopback session token or
// a ws-ticket minted from a stored login), opens the WebSocket, classifies the
// failures, and probes a serve up to `gateway.ready`.
//
// It registers no runtime, so a host that must not advertise the Hermes runtime
// (agentred) can still test a connection. The runtime package builds its
// JSON-RPC session on top of Dial.
package hermesgateway

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/gorilla/websocket"

	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/runtimes/hermes/hermesauth"
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

// CredentialSource supplies a bearer access token for a gated `hermes serve`.
//
// The transport never touches a secret store itself: each host wires an
// implementation backed by its own store, tests inject a scripted fake.
type CredentialSource interface {
	// AccessToken returns a valid bearer token for baseURL. provider is the
	// optional persisted provider name. It returns hermesauth.ErrLoginRequired
	// when no credential is stored and hermesauth.ErrLoginExpired when the
	// stored refresh token has been rejected.
	AccessToken(ctx context.Context, baseURL, provider string) (string, error)
	// Invalidate drops the cached access token for baseURL so the next
	// AccessToken refreshes it.
	Invalidate(baseURL string)
}

// WebSocketDialer is the injectable dial seam (gorilla's *websocket.Dialer in
// production, a scripted fake in tests).
type WebSocketDialer interface {
	DialContext(ctx context.Context, urlStr string, requestHeader http.Header) (*websocket.Conn, *http.Response, error)
}

const (
	// ReadyTimeout bounds the wait for `gateway.ready` after the upgrade.
	ReadyTimeout   = 60 * time.Second
	httpTimeout    = 15 * time.Second
	dialTimeout    = 15 * time.Second
	wsMessageBytes = 8 * 1024 * 1024
)

// sessionTokenRe extracts the loopback token Hermes injects into the root page:
// `window.__HERMES_SESSION_TOKEN__="<token>"`.
var sessionTokenRe = regexp.MustCompile(`__HERMES_SESSION_TOKEN__\s*=\s*"([^"]+)"`)

// DialRequest is one WebSocket dial of a serve.
type DialRequest struct {
	// URL is the canonical `http(s)://host:port` of the running serve.
	URL string
	// AuthProvider is the persisted provider hint for a gated serve (may be empty).
	AuthProvider string
	// Credentials supplies the bearer token for a gated serve. nil means the dial
	// only supports the loopback `?token=` path.
	Credentials CredentialSource
	// Dialer and HTTPClient are test seams; nil uses the production defaults.
	Dialer     WebSocketDialer
	HTTPClient *http.Client
}

// Dial opens the WebSocket of a `hermes serve`. It first tries the loopback
// token path; a gated serve (no token in the page) falls back to the
// bearer/ws-ticket flow when Credentials is non-nil. The returned connection is
// upgraded but not necessarily ready: `gateway.ready` is the caller's to await.
func Dial(ctx context.Context, req DialRequest) (*websocket.Conn, error) {
	baseURL := req.URL
	parsed, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil || parsed.Host == "" {
		return nil, fmt.Errorf("%w: invalid server URL %q", ErrGatewayUnreachable, baseURL)
	}
	scheme := "ws"
	if parsed.Scheme == "https" || parsed.Scheme == "wss" {
		scheme = "wss"
	}

	token, tokenErr := FetchSessionToken(ctx, baseURL, req.HTTPClient)
	var wsURL string
	switch {
	case tokenErr == nil:
		wsURL = scheme + "://" + parsed.Host + "/api/ws?token=" + url.QueryEscape(token)
	case errors.Is(tokenErr, ErrGatewayUnauthorized) && req.Credentials != nil:
		ticket, ticketErr := gatedWSTicket(ctx, baseURL, req.AuthProvider, req.Credentials, req.HTTPClient)
		if ticketErr != nil {
			return nil, ticketErr
		}
		wsURL = scheme + "://" + parsed.Host + "/api/ws?ticket=" + url.QueryEscape(ticket)
	default:
		return nil, tokenErr
	}

	dialer := req.Dialer
	if dialer == nil {
		dialer = &websocket.Dialer{HandshakeTimeout: dialTimeout}
	}
	dialCtx, cancel := context.WithTimeout(ctx, dialTimeout)
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
	conn.SetReadLimit(wsMessageBytes)
	return conn, nil
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
	ticket, err := hermesauth.FetchWSTicket(ctx, baseURL, access, client)
	if errors.Is(err, hermesauth.ErrAccessTokenRejected) {
		creds.Invalidate(baseURL)
		if access, err = creds.AccessToken(ctx, baseURL, provider); err != nil {
			return "", err
		}
		ticket, err = hermesauth.FetchWSTicket(ctx, baseURL, access, client)
	}
	if err != nil {
		if errors.Is(err, hermesauth.ErrAccessTokenRejected) {
			return "", fmt.Errorf("%w: ws-ticket rejected even after refresh", hermesauth.ErrLoginExpired)
		}
		return "", err
	}
	return ticket, nil
}

// FetchSessionToken GETs the Hermes root page and extracts the loopback session
// token. Failures are classified so the UI can say "the server has auth enabled"
// instead of a generic connection error.
func FetchSessionToken(ctx context.Context, baseURL string, client *http.Client) (string, error) {
	if client == nil {
		client = &http.Client{Timeout: httpTimeout}
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
	if m := sessionTokenRe.FindSubmatch(body); len(m) == 2 {
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

// ClassifyReadError distinguishes a clean server close from an abnormal
// connection loss.
func ClassifyReadError(err error) error {
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
