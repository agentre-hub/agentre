package hermes

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"

	"github.com/cago-frame/agents/provider"
	"github.com/gorilla/websocket"

	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/runtimes/hermes/hermesgateway"
)

// The transport handshake lives in the side-effect-free hermesgateway package
// so a host that must not register this runtime can still probe a serve. The
// sentinels are the same values, so errors.Is matches across both packages.
var (
	ErrGatewayUnreachable  = hermesgateway.ErrGatewayUnreachable
	ErrGatewayUnauthorized = hermesgateway.ErrGatewayUnauthorized
	ErrGatewayProtocol     = hermesgateway.ErrGatewayProtocol
	ErrGatewayClosed       = hermesgateway.ErrGatewayClosed
	ErrGatewayLost         = hermesgateway.ErrGatewayLost
)

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
	// RespondServerRequest answers a server->client request (delivered on Events
	// as EventServerRequest) with result under its id; each id is answered once.
	RespondServerRequest(id string, result any) error
	// RejectServerRequest answers a server->client request with an error: the
	// host cannot answer it and Hermes withdraws it.
	RejectServerRequest(id string) error
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
type webSocketDialer = hermesgateway.WebSocketDialer

const (
	gatewayEventsBuffer = 64
	gatewayReadyTimeout = hermesgateway.ReadyTimeout
)

// gatewaySession owns one WebSocket connection to a `hermes serve` and speaks
// newline-delimited JSON-RPC 2.0 over it. The codec (rpcConn) is transport
// agnostic; this type only moves bytes and classifies transport errors.
type gatewaySession struct {
	conn  *websocket.Conn
	codec *rpcConn

	closeOnce sync.Once
	done      chan struct{}
}

// defaultSessionFactory dials a gateway connection, waits for gateway.ready and
// declares that this connection answers server->client requests.
func defaultSessionFactory(ctx context.Context, spec sessionSpec) (Session, error) {
	sess, err := dialGatewayWithAuth(ctx, spec.URL, spec.AuthProvider, spec.Credentials, nil, nil)
	if err != nil {
		return nil, err
	}
	if err := sess.WaitReady(ctx); err != nil {
		_ = sess.Close(context.Background())
		return nil, err
	}
	if err := sess.declareServerRequests(ctx); err != nil {
		_ = sess.Close(context.Background())
		return nil, err
	}
	return sess, nil
}

// declareServerRequests sends client.capabilities once per connection. Hermes
// sends approval / clarify / ... requests only to a connection that did; without
// it every such request is treated as unanswerable.
func (s *gatewaySession) declareServerRequests(ctx context.Context) error {
	if _, err := s.codec.call(ctx, "client.capabilities", map[string]any{"server_requests": true}); err != nil {
		return fmt.Errorf("hermes gateway: client.capabilities: %w", err)
	}
	return nil
}

// dialGatewayWithAuth dials a `hermes serve` (see hermesgateway.Dial) and wraps
// the upgraded connection in the JSON-RPC session. The returned session is
// connected but not necessarily ready; callers wait on WaitReady.
func dialGatewayWithAuth(ctx context.Context, baseURL, provider string, creds CredentialSource, dialer webSocketDialer, client *http.Client) (*gatewaySession, error) {
	conn, err := hermesgateway.Dial(ctx, hermesgateway.DialRequest{
		URL: baseURL, AuthProvider: provider, Credentials: creds, Dialer: dialer, HTTPClient: client,
	})
	if err != nil {
		return nil, err
	}
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

// fetchSessionToken reads the loopback session token; see
// hermesgateway.FetchSessionToken.
func fetchSessionToken(ctx context.Context, baseURL string, client *http.Client) (string, error) {
	return hermesgateway.FetchSessionToken(ctx, baseURL, client)
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

func classifyReadError(err error) error { return hermesgateway.ClassifyReadError(err) }

func (s *gatewaySession) WaitReady(ctx context.Context) error { return s.codec.WaitReady(ctx) }
func (s *gatewaySession) Events() <-chan Event                { return s.codec.Events() }
func (s *gatewaySession) Err() error                          { return s.codec.Err() }

func (s *gatewaySession) RespondServerRequest(id string, result any) error {
	return s.codec.RespondServerRequest(id, result)
}

func (s *gatewaySession) RejectServerRequest(id string) error {
	return s.codec.RejectServerRequest(id)
}

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
