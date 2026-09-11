package auth

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"

	"github.com/agentre-hub/agentre/pkg/wire/rpcerror"
)

const (
	introspectPath = "/v1/credentials/introspect"
	// introspectionCacheTTL 是一次成功核验被沿用的时长(H2):同一枚凭据 60 秒内再次
	// 握手不再访问 server。它同时是 server 侧撤销在这台接收方上生效的最长延迟。
	introspectionCacheTTL   = 60 * time.Second
	defaultIntrospectWait   = 10 * time.Second
	introspectResponseLimit = 1 << 20
)

// Introspection is the account server's verdict on a presented account
// credential: whose it is and which peer presented it. Mode C takes the peer
// identity from here, never from the request body (decision 8).
type Introspection struct {
	AccountID       string
	DeviceID        int64
	Kind            string
	PeerFingerprint string
}

// HTTPDoer is the Introspector's HTTP port. agentred supplies a plain client;
// a host that already owns an authenticated server client can supply its own.
type HTTPDoer interface {
	Do(*http.Request) (*http.Response, error)
}

// IntrospectorOptions wires an Introspector to its host. The receiver's own
// identity comes in through AccessToken and RefreshCredential — the
// Introspector never stores or persists a credential itself.
type IntrospectorOptions struct {
	HTTP HTTPDoer
	// ServerURL resolves the account server base URL at each verification.
	ServerURL func() string
	// AccessToken resolves the receiver's own freshest device access token.
	AccessToken func() string
	// RefreshCredential refreshes the receiver's own credential once, after the
	// server rejected it with HTTP 401. An error wrapping ErrReceiverNotReady
	// means the refresh was permanently rejected; any other error is transient.
	RefreshCredential func(context.Context) error
	Now               func() time.Time
	// Timeout bounds each round trip to the account server; no answer within it
	// counts as unreachable. Zero means defaultIntrospectWait.
	Timeout time.Duration
}

var (
	// ErrCredentialInvalid rejects a presented credential the server did not
	// vouch for (unknown, expired, revoked, another account). It is today's
	// "credential rejected" shape: -32001.
	ErrCredentialInvalid = &rpcerror.Error{Code: rpcerror.CodeUnauthorized, Message: "account credential invalid"}
	// ErrAccountServerUnreachable means the credential could not be verified
	// because the account server did not answer (H3): -32007, retryable.
	ErrAccountServerUnreachable = rpcerror.ErrAccountServerUnreachable
	// ErrReceiverNotReady means the receiver itself cannot verify anyone: it is
	// not logged in, or its own credential is rejected and cannot be refreshed
	// (H4). Same -32001 shape as the former "missing verification material".
	ErrReceiverNotReady = &rpcerror.Error{Code: rpcerror.CodeUnauthorized, Message: "account credential rejected: receiver not ready"}
)

// AccountVerifier verifies a presented account credential. AuthHandlers
// depends on this interface; Introspector is the production implementation.
type AccountVerifier interface {
	Verify(ctx context.Context, token string) (Introspection, error)
}

// Introspector verifies account credentials online against agentre-server's
// POST /v1/credentials/introspect, authenticating as the receiver. Successful
// verdicts are cached for 60 seconds (never past the credential's own expiry),
// keyed by the credential's SHA-256 digest; failures are never cached.
// Concurrent verifications of one credential share a single round trip.
type Introspector struct {
	http        HTTPDoer
	serverURL   func() string
	accessToken func() string
	refresh     func(context.Context) error
	now         func() time.Time
	timeout     time.Duration

	mu       sync.Mutex
	cache    map[[sha256.Size]byte]cachedIntrospection
	inflight map[[sha256.Size]byte]*introspectCall
}

type cachedIntrospection struct {
	result    Introspection
	expiresAt time.Time
}

type introspectCall struct {
	done   chan struct{}
	result Introspection
	err    error
}

// NewIntrospector constructs an Introspector from its host wiring.
func NewIntrospector(opts IntrospectorOptions) *Introspector {
	introspector := &Introspector{
		http:        opts.HTTP,
		serverURL:   opts.ServerURL,
		accessToken: opts.AccessToken,
		refresh:     opts.RefreshCredential,
		now:         opts.Now,
		timeout:     opts.Timeout,
		cache:       map[[sha256.Size]byte]cachedIntrospection{},
		inflight:    map[[sha256.Size]byte]*introspectCall{},
	}
	if introspector.http == nil {
		introspector.http = http.DefaultClient
	}
	if introspector.now == nil {
		introspector.now = time.Now
	}
	if introspector.timeout <= 0 {
		introspector.timeout = defaultIntrospectWait
	}
	return introspector
}

// Verify returns the server's verdict on token, or ErrCredentialInvalid,
// ErrAccountServerUnreachable or ErrReceiverNotReady.
func (i *Introspector) Verify(ctx context.Context, token string) (Introspection, error) {
	if token == "" {
		return Introspection{}, ErrCredentialInvalid
	}
	key := sha256.Sum256([]byte(token))

	i.mu.Lock()
	if hit, ok := i.cache[key]; ok && i.now().Before(hit.expiresAt) {
		i.mu.Unlock()
		return hit.result, nil
	}
	if call, ok := i.inflight[key]; ok {
		i.mu.Unlock()
		select {
		case <-call.done:
			return call.result, call.err
		case <-ctx.Done():
			return Introspection{}, ErrAccountServerUnreachable
		}
	}
	call := &introspectCall{done: make(chan struct{})}
	i.inflight[key] = call
	i.mu.Unlock()

	// 这一次往返由同一枚凭据的所有并发握手共享,所以不随发起它的那条握手取消;
	// 每次请求各自受 timeout 约束。刷新自己的凭据尤其不能被半路取消:server 已经
	// 轮换、本地却没来得及落盘的 refresh token 会让这台接收方永久掉线。
	var ttl time.Duration
	call.result, ttl, call.err = i.introspect(context.WithoutCancel(ctx), token)

	i.mu.Lock()
	delete(i.inflight, key)
	if call.err == nil && ttl > 0 {
		i.storeLocked(key, call.result, ttl)
	}
	i.mu.Unlock()
	close(call.done)
	return call.result, call.err
}

func (i *Introspector) storeLocked(key [sha256.Size]byte, result Introspection, ttl time.Duration) {
	now := i.now()
	for candidate, entry := range i.cache {
		if !now.Before(entry.expiresAt) {
			delete(i.cache, candidate)
		}
	}
	i.cache[key] = cachedIntrospection{result: result, expiresAt: now.Add(ttl)}
}

// introspect performs one verification: a round trip with the receiver's own
// token and, if the server rejects that token, one refresh and one retry.
func (i *Introspector) introspect(ctx context.Context, token string) (Introspection, time.Duration, error) {
	serverURL := ""
	if i.serverURL != nil {
		serverURL = strings.TrimRight(strings.TrimSpace(i.serverURL()), "/")
	}
	ownToken := i.currentAccessToken()
	if serverURL == "" || ownToken == "" {
		return Introspection{}, 0, ErrReceiverNotReady
	}

	status, payload, err := i.post(ctx, serverURL, ownToken, token)
	if err == nil && status == http.StatusUnauthorized {
		if refreshErr := i.refreshOwnCredential(ctx); refreshErr != nil {
			return Introspection{}, 0, refreshErr
		}
		if ownToken = i.currentAccessToken(); ownToken == "" {
			return Introspection{}, 0, ErrReceiverNotReady
		}
		status, payload, err = i.post(ctx, serverURL, ownToken, token)
		if err == nil && status == http.StatusUnauthorized {
			logger.Ctx(ctx).Warn("auth.Introspector.Verify: account server still rejects the receiver's refreshed credential")
			return Introspection{}, 0, ErrReceiverNotReady
		}
	}
	if err != nil {
		logger.Ctx(ctx).Warn("auth.Introspector.Verify: account server unreachable", zap.Error(err))
		return Introspection{}, 0, ErrAccountServerUnreachable
	}
	return i.verdict(ctx, status, payload)
}

// refreshOwnCredential maps the host's refresh outcome onto the handshake's
// vocabulary: permanently rejected → not ready, anything else → unreachable.
func (i *Introspector) refreshOwnCredential(ctx context.Context) error {
	if i.refresh == nil {
		logger.Ctx(ctx).Warn("auth.Introspector.Verify: receiver credential rejected and no refresh is available")
		return ErrReceiverNotReady
	}
	err := i.refresh(ctx)
	switch {
	case err == nil:
		return nil
	case errors.Is(err, ErrReceiverNotReady):
		logger.Ctx(ctx).Warn("auth.Introspector.Verify: receiver credential rejected and its refresh was refused", zap.Error(err))
		return ErrReceiverNotReady
	default:
		logger.Ctx(ctx).Warn("auth.Introspector.Verify: receiver credential refresh failed transiently", zap.Error(err))
		return ErrAccountServerUnreachable
	}
}

func (i *Introspector) currentAccessToken() string {
	if i.accessToken == nil {
		return ""
	}
	return i.accessToken()
}

func (i *Introspector) post(ctx context.Context, serverURL, ownToken, token string) (int, []byte, error) {
	body, err := json.Marshal(map[string]string{"token": token})
	if err != nil {
		return 0, nil, err
	}
	requestCtx, cancel := context.WithTimeout(ctx, i.timeout)
	defer cancel()
	request, err := http.NewRequestWithContext(requestCtx, http.MethodPost, serverURL+introspectPath, bytes.NewReader(body))
	if err != nil {
		return 0, nil, err
	}
	request.Header.Set("Authorization", "Bearer "+ownToken)
	request.Header.Set("Content-Type", "application/json")
	response, err := i.http.Do(request)
	if err != nil {
		return 0, nil, err
	}
	defer func() { _ = response.Body.Close() }()
	payload, err := io.ReadAll(io.LimitReader(response.Body, introspectResponseLimit))
	if err != nil {
		return 0, nil, err
	}
	return response.StatusCode, payload, nil
}

type introspectResponse struct {
	AccountID       string `json:"account_id"`
	DeviceID        int64  `json:"device_id"`
	Kind            string `json:"kind"`
	PeerFingerprint string `json:"peer_fingerprint"`
	ExpiresIn       int64  `json:"expires_in"`
}

// verdict classifies an answered round trip: 2xx is the server vouching for
// the credential, 429 is the server refusing to answer at all (decision 17:
// rate-limited, not a verdict on the credential), any other 4xx is the
// server rejecting the credential, the rest is no usable answer.
func (i *Introspector) verdict(ctx context.Context, status int, payload []byte) (Introspection, time.Duration, error) {
	switch {
	case status >= http.StatusOK && status < http.StatusMultipleChoices:
		var answer introspectResponse
		if err := decodeIntrospectResponse(payload, &answer); err != nil || answer.AccountID == "" {
			// 语法上是 200、却不是契约的形状(中间设备塞回来的页面最常见):这不是 server
			// 对凭据的结论,当作没有应答,绝不当作「有效」或「无效」。
			logger.Ctx(ctx).Warn("auth.Introspector.Verify: account server answered outside the introspection contract",
				zap.Int("status", status))
			return Introspection{}, 0, ErrAccountServerUnreachable
		}
		if answer.PeerFingerprint == "" {
			// 决策 8:名不指人的凭据被拒,不回退到请求体。
			return Introspection{}, 0, ErrCredentialInvalid
		}
		ttl := introspectionCacheTTL
		if remaining := time.Duration(answer.ExpiresIn) * time.Second; answer.ExpiresIn > 0 && remaining < ttl {
			ttl = remaining
		}
		return Introspection{
			AccountID: answer.AccountID, DeviceID: answer.DeviceID,
			Kind: answer.Kind, PeerFingerprint: answer.PeerFingerprint,
		}, ttl, nil
	case status == http.StatusTooManyRequests:
		logger.Ctx(ctx).Warn("auth.Introspector.Verify: account server rate-limited the introspect request",
			zap.Int("status", status))
		return Introspection{}, 0, ErrAccountServerUnreachable
	case status >= http.StatusBadRequest && status < http.StatusInternalServerError:
		logger.Ctx(ctx).Info("auth.Introspector.Verify: account server rejected the presented credential",
			zap.Int("status", status))
		return Introspection{}, 0, ErrCredentialInvalid
	default:
		logger.Ctx(ctx).Warn("auth.Introspector.Verify: account server returned no usable answer",
			zap.Int("status", status))
		return Introspection{}, 0, ErrAccountServerUnreachable
	}
}

// decodeIntrospectResponse accepts both the raw contract body and cago's
// {code, msg, data} envelope.
func decodeIntrospectResponse(payload []byte, target *introspectResponse) error {
	var envelope struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(payload, &envelope); err != nil {
		return err
	}
	if len(envelope.Data) != 0 && string(envelope.Data) != "null" {
		return json.Unmarshal(envelope.Data, target)
	}
	return json.Unmarshal(payload, target)
}
