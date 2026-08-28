package openclawgateway

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/cago-frame/cago/pkg/logger"
	"github.com/gorilla/websocket"
	"go.uber.org/zap"
)

const (
	clientID   = "cli"
	clientMode = "cli"
	clientRole = "operator"
)

type webSocketDialer interface {
	DialContext(context.Context, string, http.Header) (*websocket.Conn, *http.Response, error)
}

type Client struct {
	config Config
	dialer webSocketDialer

	startMu sync.Mutex
	started bool
	ctx     context.Context
	cancel  context.CancelFunc

	connMu sync.RWMutex
	conn   *websocket.Conn
	closed bool

	writeMu sync.Mutex

	pendingMu sync.Mutex
	pending   map[string]chan pendingResponse
	nextID    atomic.Uint64

	ready  chan Hello
	events chan Event
	gaps   chan EventGap
	errors chan error

	closeOnce sync.Once
}

func NewClient(config Config) (*Client, error) {
	normalizedURL, err := normalizeGatewayURL(config.URL)
	if err != nil {
		return nil, err
	}
	if config.Identity == nil {
		return nil, fmt.Errorf("openclaw gateway device identity is required")
	}
	config.URL = normalizedURL
	if strings.TrimSpace(config.ClientVersion) == "" {
		config.ClientVersion = "unknown"
	}
	if strings.TrimSpace(config.Platform) == "" {
		config.Platform = runtime.GOOS
	}
	if len(config.RequiredScopes) == 0 {
		config.RequiredScopes = append([]string(nil), RequiredOperatorScopes...)
	} else {
		config.RequiredScopes = append([]string(nil), config.RequiredScopes...)
	}
	if config.HandshakeTimeout <= 0 {
		config.HandshakeTimeout = 15 * time.Second
	}
	if config.RequestTimeout <= 0 {
		config.RequestTimeout = 30 * time.Second
	}
	if config.ReconnectInitial <= 0 {
		config.ReconnectInitial = time.Second
	}
	if config.ReconnectMax <= 0 {
		config.ReconnectMax = 30 * time.Second
	}
	if config.ReconnectMax < config.ReconnectInitial {
		config.ReconnectMax = config.ReconnectInitial
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	return &Client{
		config:  config,
		dialer:  websocket.DefaultDialer,
		pending: make(map[string]chan pendingResponse),
		ready:   make(chan Hello, 8),
		events:  make(chan Event, 1024),
		gaps:    make(chan EventGap, 32),
		errors:  make(chan error, 32),
	}, nil
}

func (c *Client) Start(ctx context.Context) (Hello, error) {
	c.startMu.Lock()
	defer c.startMu.Unlock()
	if c.started {
		return Hello{}, fmt.Errorf("openclaw gateway client already started")
	}
	c.started = true

	conn, hello, challengeSeq, err := c.dialAndHandshake(ctx)
	if err != nil {
		return Hello{}, err
	}
	c.ctx, c.cancel = context.WithCancel(context.Background())
	c.setConnection(conn)
	c.publishReady(hello)
	go c.supervise(conn, challengeSeq)
	return hello, nil
}

func (c *Client) Ready() <-chan Hello   { return c.ready }
func (c *Client) Events() <-chan Event  { return c.events }
func (c *Client) Gaps() <-chan EventGap { return c.gaps }
func (c *Client) Errors() <-chan error  { return c.errors }

func (c *Client) Call(ctx context.Context, method string, params, out any) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, c.config.RequestTimeout)
		defer cancel()
	}

	c.connMu.RLock()
	conn := c.conn
	closed := c.closed
	c.connMu.RUnlock()
	if conn == nil || closed {
		return ErrDisconnected
	}

	id := fmt.Sprintf("agentre-%d", c.nextID.Add(1))
	responseCh := make(chan pendingResponse, 1)
	c.pendingMu.Lock()
	c.pending[id] = responseCh
	c.pendingMu.Unlock()
	defer c.removePending(id)

	frame := requestFrame{Type: "req", ID: id, Method: method, Params: params}
	if raw, marshalErr := json.Marshal(frame); marshalErr == nil {
		logger.Default().Debug("openclawgateway.Client.Call: raw frame", zap.ByteString("frame", raw))
	}
	c.writeMu.Lock()
	c.connMu.RLock()
	current := c.conn
	c.connMu.RUnlock()
	if current != conn {
		c.writeMu.Unlock()
		return ErrDisconnected
	}
	err := conn.WriteJSON(frame)
	c.writeMu.Unlock()
	if err != nil {
		_ = conn.Close()
		return ErrDisconnected
	}

	select {
	case response := <-responseCh:
		if response.err != nil {
			return response.err
		}
		if out == nil || len(response.payload) == 0 || string(response.payload) == "null" {
			return nil
		}
		if err := json.Unmarshal(response.payload, out); err != nil {
			return fmt.Errorf("decode openclaw gateway %s response: %w", method, err)
		}
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-c.clientDone():
		return ErrDisconnected
	}
}

func (c *Client) Close() {
	c.closeOnce.Do(func() {
		c.connMu.Lock()
		c.closed = true
		conn := c.conn
		c.conn = nil
		c.connMu.Unlock()
		if c.cancel != nil {
			c.cancel()
		}
		if conn != nil {
			_ = conn.Close()
		}
		c.failPending(ErrDisconnected)
	})
}

func (c *Client) supervise(conn *websocket.Conn, challengeSeq int64) {
	backoff := c.config.ReconnectInitial
	for {
		err := c.readLoop(conn, challengeSeq)
		c.clearConnection(conn)
		c.failPending(ErrDisconnected)
		if c.isClosed() || c.ctx.Err() != nil {
			return
		}
		c.publishError(err)

		for {
			if !waitContext(c.ctx, backoff) {
				return
			}
			newConn, hello, newChallengeSeq, dialErr := c.dialAndHandshake(c.ctx)
			if dialErr == nil {
				conn = newConn
				challengeSeq = newChallengeSeq
				c.setConnection(conn)
				c.publishReady(hello)
				backoff = c.config.ReconnectInitial
				break
			}
			c.publishError(dialErr)
			backoff *= 2
			if backoff > c.config.ReconnectMax {
				backoff = c.config.ReconnectMax
			}
		}
	}
}

func (c *Client) readLoop(conn *websocket.Conn, lastSeq int64) error {
	for {
		var frame gatewayFrame
		if err := conn.ReadJSON(&frame); err != nil {
			return err
		}
		if raw, marshalErr := json.Marshal(frame); marshalErr == nil {
			logger.Default().Debug("openclawgateway.Client.readLoop: raw frame", zap.ByteString("frame", raw))
		}
		switch frame.Type {
		case "res":
			c.routeResponse(frame)
		case "event":
			if frame.Seq > 0 {
				if frame.Seq <= lastSeq {
					continue
				}
				if lastSeq > 0 && frame.Seq > lastSeq+1 {
					if !c.publishGap(EventGap{Expected: lastSeq + 1, Received: frame.Seq}) {
						return c.ctx.Err()
					}
				}
				lastSeq = frame.Seq
			}
			if !c.publishEvent(Event{Name: frame.Event, Payload: frame.Payload, Seq: frame.Seq}) {
				return c.ctx.Err()
			}
		}
	}
}

func (c *Client) dialAndHandshake(ctx context.Context) (*websocket.Conn, Hello, int64, error) {
	handshakeCtx, cancel := context.WithTimeout(ctx, c.config.HandshakeTimeout)
	defer cancel()
	conn, dialResponse, err := c.dialer.DialContext(handshakeCtx, c.config.URL, nil)
	if err != nil {
		if dialResponse != nil && dialResponse.Body != nil {
			_ = dialResponse.Body.Close()
		}
		return nil, Hello{}, 0, fmt.Errorf("connect openclaw gateway: %w", err)
	}
	fail := func(err error) (*websocket.Conn, Hello, int64, error) {
		_ = conn.Close()
		return nil, Hello{}, 0, err
	}
	deadline, _ := handshakeCtx.Deadline()
	_ = conn.SetReadDeadline(deadline)
	_ = conn.SetWriteDeadline(deadline)

	var challenge gatewayFrame
	if err := conn.ReadJSON(&challenge); err != nil {
		return fail(fmt.Errorf("read openclaw connect challenge: %w", err))
	}
	if raw, marshalErr := json.Marshal(challenge); marshalErr == nil {
		logger.Default().Debug("openclawgateway.Client.dialAndHandshake: raw frame", zap.ByteString("frame", raw))
	}
	if challenge.Type != "event" || challenge.Event != "connect.challenge" {
		return fail(fmt.Errorf("openclaw gateway first frame is not connect.challenge"))
	}
	var challengePayload struct {
		Nonce string `json:"nonce"`
		TS    int64  `json:"ts"`
	}
	if err := json.Unmarshal(challenge.Payload, &challengePayload); err != nil || strings.TrimSpace(challengePayload.Nonce) == "" {
		return fail(fmt.Errorf("invalid openclaw connect challenge"))
	}

	signedAt := c.config.Now().UnixMilli()
	proof, err := c.config.Identity.proof(
		clientID, clientMode, clientRole, c.config.RequiredScopes, signedAt,
		c.config.Token, challengePayload.Nonce, c.config.Platform, c.config.DeviceFamily,
	)
	if err != nil {
		return fail(err)
	}
	connectParams := struct {
		MinProtocol int `json:"minProtocol"`
		MaxProtocol int `json:"maxProtocol"`
		Client      struct {
			ID           string `json:"id"`
			DisplayName  string `json:"displayName"`
			Version      string `json:"version"`
			Platform     string `json:"platform"`
			DeviceFamily string `json:"deviceFamily,omitempty"`
			Mode         string `json:"mode"`
		} `json:"client"`
		Role   string      `json:"role"`
		Scopes []string    `json:"scopes"`
		Device deviceProof `json:"device"`
		Auth   struct {
			Token string `json:"token,omitempty"`
		} `json:"auth,omitempty"`
	}{
		MinProtocol: ProtocolVersion,
		MaxProtocol: ProtocolVersion,
		Role:        clientRole,
		Scopes:      append([]string(nil), c.config.RequiredScopes...),
		Device:      proof,
	}
	connectParams.Client.ID = clientID
	connectParams.Client.DisplayName = "AgentRE"
	connectParams.Client.Version = c.config.ClientVersion
	connectParams.Client.Platform = strings.ToLower(strings.TrimSpace(c.config.Platform))
	connectParams.Client.DeviceFamily = strings.ToLower(strings.TrimSpace(c.config.DeviceFamily))
	connectParams.Client.Mode = clientMode
	connectParams.Auth.Token = c.config.Token

	requestID := fmt.Sprintf("agentre-connect-%d", c.nextID.Add(1))
	connectFrame := requestFrame{Type: "req", ID: requestID, Method: "connect", Params: connectParams}
	if raw, marshalErr := json.Marshal(connectFrame); marshalErr == nil {
		logger.Default().Debug("openclawgateway.Client.dialAndHandshake: raw frame", zap.ByteString("frame", raw))
	}
	if err := conn.WriteJSON(connectFrame); err != nil {
		return fail(fmt.Errorf("write openclaw connect request: %w", err))
	}
	var response gatewayFrame
	if err := conn.ReadJSON(&response); err != nil {
		return fail(fmt.Errorf("read openclaw connect response: %w", err))
	}
	if raw, marshalErr := json.Marshal(response); marshalErr == nil {
		logger.Default().Debug("openclawgateway.Client.dialAndHandshake: raw frame", zap.ByteString("frame", raw))
	}
	if response.Type != "res" || response.ID != requestID {
		return fail(fmt.Errorf("invalid openclaw connect response"))
	}
	if !response.OK {
		return fail(c.rpcError(response.Error))
	}
	var hello Hello
	if err := json.Unmarshal(response.Payload, &hello); err != nil {
		return fail(fmt.Errorf("decode openclaw hello: %w", err))
	}
	if hello.Type != "hello-ok" || hello.Protocol != ProtocolVersion {
		return fail(fmt.Errorf("%w: negotiated %d, required %d", ErrProtocolMismatch, hello.Protocol, ProtocolVersion))
	}
	granted := make(map[string]struct{}, len(hello.Auth.Scopes))
	for _, scope := range hello.Auth.Scopes {
		granted[scope] = struct{}{}
	}
	for _, required := range c.config.RequiredScopes {
		if _, ok := granted[required]; !ok {
			return fail(fmt.Errorf("%w: %s", ErrRequiredScopeMissing, required))
		}
	}
	_ = conn.SetReadDeadline(time.Time{})
	_ = conn.SetWriteDeadline(time.Time{})
	return conn, hello, challenge.Seq, nil
}

func (c *Client) routeResponse(frame gatewayFrame) {
	c.pendingMu.Lock()
	responseCh := c.pending[frame.ID]
	c.pendingMu.Unlock()
	if responseCh == nil {
		return
	}
	response := pendingResponse{payload: frame.Payload}
	if !frame.OK {
		response.err = c.rpcError(frame.Error)
	}
	select {
	case responseCh <- response:
	default:
	}
}

func (c *Client) rpcError(raw *responseError) error {
	if raw == nil {
		return &RPCError{Code: "UNKNOWN", Message: "gateway rejected request"}
	}
	return &RPCError{
		Code:         raw.Code,
		Reason:       raw.Details.Reason,
		Message:      redact(raw.Message, c.config.Token),
		Retryable:    raw.Retryable,
		RetryAfterMs: raw.RetryAfterMs,
	}
}

func (c *Client) removePending(id string) {
	c.pendingMu.Lock()
	delete(c.pending, id)
	c.pendingMu.Unlock()
}

func (c *Client) failPending(err error) {
	c.pendingMu.Lock()
	pending := c.pending
	c.pending = make(map[string]chan pendingResponse)
	c.pendingMu.Unlock()
	for _, responseCh := range pending {
		select {
		case responseCh <- pendingResponse{err: err}:
		default:
		}
	}
}

func (c *Client) setConnection(conn *websocket.Conn) {
	c.connMu.Lock()
	c.conn = conn
	c.connMu.Unlock()
}

func (c *Client) clearConnection(conn *websocket.Conn) {
	c.connMu.Lock()
	if c.conn == conn {
		c.conn = nil
	}
	c.connMu.Unlock()
	_ = conn.Close()
}

func (c *Client) isClosed() bool {
	c.connMu.RLock()
	defer c.connMu.RUnlock()
	return c.closed
}

func (c *Client) clientDone() <-chan struct{} {
	if c.ctx == nil {
		return nil
	}
	return c.ctx.Done()
}

func (c *Client) publishReady(hello Hello) {
	select {
	case c.ready <- hello:
	default:
		select {
		case <-c.ready:
		default:
		}
		select {
		case c.ready <- hello:
		default:
		}
	}
}

func (c *Client) publishEvent(event Event) bool {
	select {
	case c.events <- event:
		return true
	case <-c.ctx.Done():
		return false
	}
}

func (c *Client) publishGap(gap EventGap) bool {
	select {
	case c.gaps <- gap:
		return true
	case <-c.ctx.Done():
		return false
	}
}

func (c *Client) publishError(err error) {
	if err == nil || c.ctx == nil {
		return
	}
	message := redact(err.Error(), c.config.Token)
	select {
	case c.errors <- errors.New(message):
	default:
	}
}

func normalizeGatewayURL(raw string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return "", fmt.Errorf("parse openclaw gateway URL: %w", err)
	}
	u.Scheme = strings.ToLower(u.Scheme)
	if (u.Scheme != "ws" && u.Scheme != "wss") || u.Hostname() == "" || u.Opaque != "" {
		return "", fmt.Errorf("openclaw gateway URL must be ws or wss with a host")
	}
	if u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" {
		return "", fmt.Errorf("openclaw gateway URL cannot contain credentials, query, or fragment")
	}
	hostname := strings.ToLower(strings.TrimSpace(u.Hostname()))
	loopback := hostname == "localhost"
	if ip := net.ParseIP(hostname); ip != nil {
		loopback = ip.IsLoopback()
	}
	if u.Scheme == "ws" && !loopback {
		return "", fmt.Errorf("plaintext openclaw gateway URL is limited to loopback")
	}
	if port := u.Port(); port != "" {
		u.Host = net.JoinHostPort(hostname, port)
	} else if strings.Contains(hostname, ":") {
		u.Host = "[" + hostname + "]"
	} else {
		u.Host = hostname
	}
	return u.String(), nil
}

func redact(value, secret string) string {
	if secret == "" {
		return value
	}
	return strings.ReplaceAll(value, secret, "[redacted]")
}

func waitContext(ctx context.Context, duration time.Duration) bool {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-timer.C:
		return true
	case <-ctx.Done():
		return false
	}
}
