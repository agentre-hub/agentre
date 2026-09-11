package auth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/agentre-hub/agentre/pkg/wire/rpcerror"
)

const introspectSuccessBody = `{"code":0,"msg":"ok","data":{"account_id":"42","device_id":7,"kind":"desktop","peer_fingerprint":"sha256:peer","expires_in":900}}`

type introspectClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *introspectClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *introspectClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// introspectRig 是 Introspector 的外部边界:一台假的 agentre-server、接收方自己的
// 访问令牌与刷新入口,以及一只可拨动的钟。
type introspectRig struct {
	server       *httptest.Server
	calls        atomic.Int32
	refreshCalls atomic.Int32
	clock        *introspectClock

	mu       sync.Mutex
	ownToken string
	refresh  func(context.Context) error
}

func (r *introspectRig) setOwnToken(token string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.ownToken = token
}

func (r *introspectRig) currentOwnToken() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.ownToken
}

func setupIntrospectorTest(t *testing.T, handler http.HandlerFunc, tune ...func(*IntrospectorOptions)) (*Introspector, *introspectRig) {
	t.Helper()
	rig := &introspectRig{clock: &introspectClock{now: time.Unix(1_760_000_000, 0)}, ownToken: "own-token"}
	rig.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rig.calls.Add(1)
		handler(w, r)
	}))
	t.Cleanup(rig.server.Close)
	options := IntrospectorOptions{
		HTTP:        rig.server.Client(),
		ServerURL:   func() string { return rig.server.URL },
		AccessToken: rig.currentOwnToken,
		RefreshCredential: func(ctx context.Context) error {
			rig.refreshCalls.Add(1)
			rig.mu.Lock()
			refresh := rig.refresh
			rig.mu.Unlock()
			if refresh == nil {
				return errors.New("refresh not configured")
			}
			return refresh(ctx)
		},
		Now:     rig.clock.Now,
		Timeout: time.Second,
	}
	for _, apply := range tune {
		apply(&options)
	}
	return NewIntrospector(options), rig
}

func answer(status int, body string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}
}

func assertRejectedWith(t *testing.T, err error, sentinel *rpcerror.Error) {
	t.Helper()
	require.ErrorIs(t, err, sentinel)
	var rpcErr *rpcerror.Error
	require.ErrorAs(t, err, &rpcErr)
	assert.Equal(t, sentinel.Code, rpcErr.Code)
}

func TestIntrospector_GivenAValidCredential_WhenVerifying_ThenPostsItWithTheReceiversOwnBearerAndReturnsTheServersIdentity(t *testing.T) {
	introspector, rig := setupIntrospectorTest(t, func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPost, r.Method)
		assert.Equal(t, "/v1/credentials/introspect", r.URL.Path)
		assert.Equal(t, "Bearer own-token", r.Header.Get("Authorization"), "the receiver authenticates with its own device token")
		assert.Equal(t, "application/json", r.Header.Get("Content-Type"))
		var body map[string]string
		assert.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		assert.Equal(t, map[string]string{"token": "presented-credential"}, body)
		answer(http.StatusOK, introspectSuccessBody)(w, r)
	})

	got, err := introspector.Verify(context.Background(), "presented-credential")

	require.NoError(t, err)
	assert.Equal(t, Introspection{AccountID: "42", DeviceID: 7, Kind: "desktop", PeerFingerprint: "sha256:peer"}, got)
	assert.Equal(t, int32(1), rig.calls.Load())
}

func TestIntrospector_GivenASuccessfulVerification_WhenTheSameCredentialReturnsWithin60Seconds_ThenTheServerIsNotContactedAgain(t *testing.T) {
	introspector, rig := setupIntrospectorTest(t, answer(http.StatusOK, introspectSuccessBody))
	ctx := context.Background()

	_, err := introspector.Verify(ctx, "credential-a")
	require.NoError(t, err)
	rig.clock.Advance(59 * time.Second)
	cached, err := introspector.Verify(ctx, "credential-a")
	require.NoError(t, err)
	assert.Equal(t, "sha256:peer", cached.PeerFingerprint, "a cache hit returns the previous verification result")
	assert.Equal(t, int32(1), rig.calls.Load(), "within 60s the same credential must not reach the server")

	_, err = introspector.Verify(ctx, "credential-b")
	require.NoError(t, err)
	assert.Equal(t, int32(2), rig.calls.Load(), "a different credential is never answered from another credential's cache entry")

	rig.clock.Advance(2 * time.Second)
	_, err = introspector.Verify(ctx, "credential-a")
	require.NoError(t, err)
	assert.Equal(t, int32(3), rig.calls.Load(), "after 60s the credential must be verified online again")
}

func TestIntrospector_GivenTheCredentialExpiresBeforeTheCacheWindow_ThenTheCachedSuccessEndsWithTheCredential(t *testing.T) {
	introspector, rig := setupIntrospectorTest(t, answer(http.StatusOK,
		`{"code":0,"msg":"ok","data":{"account_id":"42","device_id":7,"kind":"desktop","peer_fingerprint":"sha256:peer","expires_in":10}}`))
	ctx := context.Background()

	_, err := introspector.Verify(ctx, "short-lived")
	require.NoError(t, err)
	rig.clock.Advance(11 * time.Second)
	_, err = introspector.Verify(ctx, "short-lived")
	require.NoError(t, err)

	assert.Equal(t, int32(2), rig.calls.Load(), "a cached success must not outlive the credential the server said expires sooner")
}

func TestIntrospector_GivenTheServerRejectsTheCredential_WhenItIsPresentedAgain_ThenTheFailureWasNotCached(t *testing.T) {
	introspector, rig := setupIntrospectorTest(t, answer(http.StatusBadRequest, `{"code":10401,"msg":"credential invalid"}`))
	ctx := context.Background()

	_, err := introspector.Verify(ctx, "rejected")
	assertRejectedWith(t, err, ErrCredentialInvalid)
	_, err = introspector.Verify(ctx, "rejected")
	assertRejectedWith(t, err, ErrCredentialInvalid)

	assert.Equal(t, int32(2), rig.calls.Load(), "failures are never cached")
	assert.Equal(t, int32(0), rig.refreshCalls.Load(), "a rejected presented credential says nothing about the receiver's own token")
}

func TestIntrospector_GivenAnAnswerWithoutAPeerFingerprint_ThenRejectsAsCredentialInvalid(t *testing.T) {
	introspector, _ := setupIntrospectorTest(t, answer(http.StatusOK,
		`{"code":0,"msg":"ok","data":{"account_id":"42","device_id":7,"kind":"desktop","peer_fingerprint":"","expires_in":900}}`))

	_, err := introspector.Verify(context.Background(), "names-nobody")

	assertRejectedWith(t, err, ErrCredentialInvalid)
}

func TestIntrospector_GivenTheAccountServerCannotAnswer_ThenReturnsAccountServerUnreachable(t *testing.T) {
	cases := []struct {
		name    string
		handler http.HandlerFunc
		close   bool
	}{
		{name: "server error", handler: answer(http.StatusBadGateway, `bad gateway`)},
		{name: "connection refused", handler: answer(http.StatusOK, introspectSuccessBody), close: true},
		{name: "no answer before the timeout", handler: func(w http.ResponseWriter, r *http.Request) {
			select {
			case <-r.Context().Done():
			case <-time.After(2 * time.Second):
			}
		}},
		{name: "a 200 that is not the contract's answer", handler: answer(http.StatusOK, `{}`)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			introspector, rig := setupIntrospectorTest(t, tc.handler, func(o *IntrospectorOptions) {
				o.Timeout = 100 * time.Millisecond
			})
			if tc.close {
				rig.server.Close()
			}

			_, err := introspector.Verify(context.Background(), "credential")

			assertRejectedWith(t, err, ErrAccountServerUnreachable)
			assert.Equal(t, rpcerror.CodeAccountServerUnreachable, ErrAccountServerUnreachable.Code)
		})
	}
}

func TestIntrospector_GivenTheServerWasUnreachable_WhenItRecovers_ThenTheFailureWasNotCached(t *testing.T) {
	var attempts atomic.Int32
	introspector, _ := setupIntrospectorTest(t, func(w http.ResponseWriter, r *http.Request) {
		if attempts.Add(1) == 1 {
			answer(http.StatusServiceUnavailable, `unavailable`)(w, r)
			return
		}
		answer(http.StatusOK, introspectSuccessBody)(w, r)
	})
	ctx := context.Background()

	_, err := introspector.Verify(ctx, "credential")
	assertRejectedWith(t, err, ErrAccountServerUnreachable)
	got, err := introspector.Verify(ctx, "credential")

	require.NoError(t, err)
	assert.Equal(t, "42", got.AccountID)
}

func TestIntrospector_GivenTheReceiversOwnTokenIsRejected_WhenRefreshSucceeds_ThenRetriesOnceWithTheFreshToken(t *testing.T) {
	introspector, rig := setupIntrospectorTest(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer fresh-token" {
			answer(http.StatusUnauthorized, `{"code":401,"msg":"unauthorized"}`)(w, r)
			return
		}
		answer(http.StatusOK, introspectSuccessBody)(w, r)
	})
	rig.refresh = func(context.Context) error {
		rig.setOwnToken("fresh-token")
		return nil
	}

	got, err := introspector.Verify(context.Background(), "presented")

	require.NoError(t, err)
	assert.Equal(t, "sha256:peer", got.PeerFingerprint)
	assert.Equal(t, int32(1), rig.refreshCalls.Load())
	assert.Equal(t, int32(2), rig.calls.Load(), "one rejected attempt plus one retry with the refreshed token")
}

func TestIntrospector_GivenTheReceiversOwnTokenIsRejected_WhenTheRetryIsStillRejected_ThenReceiverNotReadyAfterExactlyOneRefresh(t *testing.T) {
	introspector, rig := setupIntrospectorTest(t, answer(http.StatusUnauthorized, `{"code":401,"msg":"unauthorized"}`))
	rig.refresh = func(context.Context) error {
		rig.setOwnToken("fresh-token")
		return nil
	}

	_, err := introspector.Verify(context.Background(), "presented")

	assertRejectedWith(t, err, ErrReceiverNotReady)
	assert.Equal(t, int32(1), rig.refreshCalls.Load(), "the receiver refreshes its own credential at most once per verification")
	assert.Equal(t, int32(2), rig.calls.Load())
}

func TestIntrospector_GivenTheReceiversOwnTokenIsRejected_WhenRefreshIsRejected_ThenReceiverNotReady(t *testing.T) {
	introspector, rig := setupIntrospectorTest(t, answer(http.StatusUnauthorized, `{"code":401,"msg":"unauthorized"}`))
	rig.refresh = func(context.Context) error {
		return fmt.Errorf("%w: refresh grant rejected", ErrReceiverNotReady)
	}

	_, err := introspector.Verify(context.Background(), "presented")

	assertRejectedWith(t, err, ErrReceiverNotReady)
	assert.Equal(t, int32(1), rig.calls.Load(), "no retry once the refresh itself was rejected")
}

func TestIntrospector_GivenTheReceiversOwnTokenIsRejected_WhenRefreshFailsTransiently_ThenAccountServerUnreachable(t *testing.T) {
	introspector, rig := setupIntrospectorTest(t, answer(http.StatusUnauthorized, `{"code":401,"msg":"unauthorized"}`))
	rig.refresh = func(context.Context) error {
		return errors.New("refresh request: connection refused")
	}

	_, err := introspector.Verify(context.Background(), "presented")

	assertRejectedWith(t, err, ErrAccountServerUnreachable)
	assert.Equal(t, int32(1), rig.calls.Load())
}

func TestIntrospector_GivenNothingToVerifyWith_ThenRejectsWithoutContactingTheServer(t *testing.T) {
	t.Run("the receiver holds no access token", func(t *testing.T) {
		introspector, rig := setupIntrospectorTest(t, answer(http.StatusOK, introspectSuccessBody))
		rig.setOwnToken("")

		_, err := introspector.Verify(context.Background(), "presented")

		assertRejectedWith(t, err, ErrReceiverNotReady)
		assert.Zero(t, rig.calls.Load())
	})
	t.Run("the peer presented no credential", func(t *testing.T) {
		introspector, rig := setupIntrospectorTest(t, answer(http.StatusOK, introspectSuccessBody))

		_, err := introspector.Verify(context.Background(), "")

		assertRejectedWith(t, err, ErrCredentialInvalid)
		assert.Zero(t, rig.calls.Load())
	})
}

func TestIntrospector_GivenConcurrentHandshakesWithTheSameCredential_ThenOneServerRoundTripAnswersAll(t *testing.T) {
	entered := make(chan struct{}, 8)
	release := make(chan struct{})
	introspector, rig := setupIntrospectorTest(t, func(w http.ResponseWriter, r *http.Request) {
		entered <- struct{}{}
		<-release
		answer(http.StatusOK, introspectSuccessBody)(w, r)
	})

	const handshakes = 5
	results := make([]Introspection, handshakes)
	errs := make([]error, handshakes)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		results[0], errs[0] = introspector.Verify(context.Background(), "shared")
	}()
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		close(release)
		t.Fatal("the first handshake never reached the account server")
	}
	for i := 1; i < handshakes; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			results[i], errs[i] = introspector.Verify(context.Background(), "shared")
		}(i)
	}
	// 让后来的握手在第一次往返还挂着时就到达;它们若各自去问 server,计数会超过 1。
	time.Sleep(100 * time.Millisecond)
	close(release)
	wg.Wait()

	for i := range handshakes {
		require.NoError(t, errs[i])
		assert.Equal(t, "sha256:peer", results[i].PeerFingerprint)
	}
	assert.Equal(t, int32(1), rig.calls.Load(), "concurrent handshakes with one credential share one server round trip")
}
