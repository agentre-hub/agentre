package rpc

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestHubLink_GivenRelayDropsAndRecovers_WhenRunning_ThenReconnectsRenewsAndExchangesFrames
// covers the daemon-owned part of R14/R20: a failed dial and a dropped outbound
// connection retry with backoff, the replacement sends pings that renew the server
// TTL, and the raw frame seam remains available to the future multiplexer.
func TestHubLink_GivenRelayDropsAndRecovers_WhenRunning_ThenReconnectsRenewsAndExchangesFrames(t *testing.T) {
	var attempts atomic.Int32
	connected := make(chan int, 2)
	pings := make(chan struct{}, 1)
	upgrader := websocket.Upgrader{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/v1/relay/daemon", r.URL.Path)
		if got := r.Header.Get("Authorization"); got != "Bearer device-access-token" {
			t.Errorf("Authorization = %q, want device bearer token", got)
		}

		attempt := int(attempts.Add(1))
		if attempt == 1 {
			http.Error(w, "temporarily unavailable", http.StatusServiceUnavailable)
			return
		}

		ws, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer func() { _ = ws.Close() }()
		connected <- attempt
		if attempt == 2 {
			return // force a post-dial disconnect; the next connection must re-register.
		}

		ws.SetPingHandler(func(appData string) error {
			select {
			case pings <- struct{}{}:
			default:
			}
			return ws.WriteControl(websocket.PongMessage, []byte(appData), time.Now().Add(time.Second))
		})
		for {
			messageType, payload, readErr := ws.ReadMessage()
			if readErr != nil {
				return
			}
			if messageType == websocket.BinaryMessage && string(payload) == "outbound" {
				if err := ws.WriteMessage(websocket.BinaryMessage, []byte("inbound")); err != nil {
					return
				}
			}
		}
	}))
	t.Cleanup(server.Close)

	retryDelays := make(chan time.Duration, 2)
	dialed := make(chan struct{}, 2)
	disconnected := make(chan error, 1)
	link := NewHubLink(HubLinkOptions{
		ServerURL:         server.URL,
		AccessToken:       "device-access-token",
		HeartbeatInterval: 10 * time.Millisecond,
		RetryInitial:      time.Second,
		RetryMax:          4 * time.Second,
		RetryWait: func(_ context.Context, delay time.Duration) error {
			retryDelays <- delay
			return nil // deterministic test clock: advance every retry immediately.
		},
		Random: func() float64 { return 1 },
		OnDial: func() {
			dialed <- struct{}{}
		},
		OnDisconnect: func(err error) {
			disconnected <- err
		},
	})
	ctx, cancel := context.WithCancel(context.Background())
	runDone := make(chan error, 1)
	go func() { runDone <- link.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		require.NoError(t, <-runDone)
	})

	require.Equal(t, 2, <-connected, "the first successful dial must be registered")
	require.Equal(t, 3, <-connected, "a dropped connection must be re-established")
	assert.Equal(t, time.Second, <-retryDelays, "failed dials begin at the configured backoff")
	assert.Equal(t, 2*time.Second, <-retryDelays, "an immediately dropped connection compounds the reconnect backoff")
	<-dialed
	<-dialed
	require.Error(t, <-disconnected, "the drop must be observable to the multiplexer seam")

	require.NoError(t, link.Send(websocket.BinaryMessage, []byte("outbound")))
	select {
	case frame := <-link.Receive():
		assert.Equal(t, websocket.BinaryMessage, frame.MessageType)
		assert.Equal(t, []byte("inbound"), frame.Payload)
	case <-time.After(time.Second):
		t.Fatal("relay response did not reach the hub frame seam")
	}
	select {
	case <-pings:
	case <-time.After(time.Second):
		t.Fatal("hub link did not send a heartbeat to renew the relay registration")
	}
}

// TestHubLink_GivenRetryClockFailsOutsideShutdown_WhenTheRelayDialFails_ThenRunReportsIt
// pins the one way the reconnect loop is allowed to end without a shutdown: the
// retry clock itself failed. Returning nil there would leave the relay
// permanently down while the daemon believes Run finished normally — the
// silently-dead reconnect loop this transport exists to avoid.
func TestHubLink_GivenRetryClockFailsOutsideShutdown_WhenTheRelayDialFails_ThenRunReportsIt(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "temporarily unavailable", http.StatusServiceUnavailable)
	}))
	t.Cleanup(server.Close)

	brokenClock := errors.New("retry clock stopped")
	link := NewHubLink(HubLinkOptions{
		ServerURL:   server.URL,
		AccessToken: "device-access-token",
		RetryWait: func(context.Context, time.Duration) error {
			return brokenClock // not a cancellation: the clock seam itself is broken.
		},
	})

	runDone := make(chan error, 1)
	go func() { runDone <- link.Run(context.Background()) }()
	select {
	case err := <-runDone:
		require.ErrorIs(t, err, brokenClock,
			"a retry wait that failed for a reason other than shutdown must reach the daemon, not look like a clean exit")
	case <-time.After(2 * time.Second):
		t.Fatal("Run never returned after the retry clock failed")
	}
}

// TestHubLink_GivenShutdown_WhenItArrivesBeforeDuringOrBetweenDials_ThenRunExitsCleanly
// covers the degraded outcome each //nolint:nilerr in Run preserves: once ctx is
// canceled the swallowed error is only the shutdown surfacing through the dialer
// or the retry clock, so Run reports success and stops — it must not retry, and
// the daemon must not see shutdown as a relay failure.
func TestHubLink_GivenShutdown_WhenItArrivesBeforeDuringOrBetweenDials_ThenRunExitsCleanly(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		http.Error(w, "temporarily unavailable", http.StatusServiceUnavailable)
	}))
	t.Cleanup(server.Close)

	t.Run("already canceled before the first dial", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		link := NewHubLink(HubLinkOptions{
			ServerURL: server.URL,
			RetryWait: func(context.Context, time.Duration) error { return nil },
			OnDial:    func() { t.Error("a canceled link must not dial the relay") },
		})
		before := requests.Load()
		require.NoError(t, link.Run(ctx))
		assert.Equal(t, before, requests.Load(), "a canceled link must not reach the relay at all")
	})

	t.Run("canceled while the dial is in flight", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		t.Cleanup(cancel)
		var retries atomic.Int32
		link := NewHubLink(HubLinkOptions{
			ServerURL: server.URL,
			// The provider runs at the start of every dial (see HubLinkOptions):
			// canceling here lands the shutdown inside the dial deterministically.
			AccessTokenProvider: func() string {
				cancel()
				return "device-access-token"
			},
			RetryWait: func(context.Context, time.Duration) error {
				retries.Add(1)
				return nil
			},
			OnDial: func() { t.Error("a dial aborted by shutdown must not count as connected") },
		})
		runDone := make(chan error, 1)
		go func() { runDone <- link.Run(ctx) }()
		select {
		case err := <-runDone:
			require.NoError(t, err, "a dial aborted by our own shutdown is not a relay failure")
		case <-time.After(2 * time.Second):
			t.Fatal("Run never returned after the dial was aborted by shutdown")
		}
		assert.Zero(t, retries.Load(), "a dial aborted by shutdown must not be backed off and retried")
	})

	t.Run("canceled while backing off between dials", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		t.Cleanup(cancel)
		link := NewHubLink(HubLinkOptions{
			ServerURL: server.URL,
			// Production's waitForHubRetry reports cancellation exactly this way.
			RetryWait: func(waitCtx context.Context, _ time.Duration) error {
				cancel()
				<-waitCtx.Done()
				return waitCtx.Err()
			},
		})
		runDone := make(chan error, 1)
		go func() { runDone <- link.Run(ctx) }()
		select {
		case err := <-runDone:
			require.NoError(t, err, "a backoff ended by shutdown is not a relay failure")
		case <-time.After(2 * time.Second):
			t.Fatal("Run never returned after the backoff was canceled")
		}
	})
}

// TestHubLink_GivenAccessTokenProvider_WhenRunning_ThenEachDialReResolvesTheToken
// pins the R4/R14 seam that lets the daemon own state while HubLink stays
// transport-only: the provider is consulted at every dial, so an access token
// that expires mid-connection is replaced by the fresh one on the next reconnect.
func TestHubLink_GivenAccessTokenProvider_WhenRunning_ThenEachDialReResolvesTheToken(t *testing.T) {
	var (
		mu          sync.Mutex
		authHeaders []string
	)
	upgrader := websocket.Upgrader{}
	conns := make(chan *websocket.Conn, 4)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		authHeaders = append(authHeaders, r.Header.Get("Authorization"))
		mu.Unlock()

		ws, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		conns <- ws
		// Keep the connection open until the test (or the peer) closes it.
		for {
			if _, _, err := ws.ReadMessage(); err != nil {
				return
			}
		}
	}))
	t.Cleanup(server.Close)

	var token atomic.Value
	token.Store("token-1")
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	link := NewHubLink(HubLinkOptions{
		ServerURL:           server.URL,
		AccessToken:         "token-0", // stale static fallback; the provider must win
		AccessTokenProvider: func() string { return token.Load().(string) },
		HeartbeatInterval:   100 * time.Millisecond,
		RetryInitial:        time.Second,
		RetryMax:            4 * time.Second,
		RetryWait:           func(context.Context, time.Duration) error { return nil },
		Random:              func() float64 { return 1 },
	})
	runDone := make(chan error, 1)
	go func() { runDone <- link.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		require.NoError(t, <-runDone)
	})

	var first *websocket.Conn
	select {
	case first = <-conns:
	case <-time.After(2 * time.Second):
		t.Fatal("first relay dial never happened")
	}
	mu.Lock()
	assert.Equal(t, "Bearer token-1", authHeaders[0], "the provider value is used for the first dial")
	mu.Unlock()

	// Rotate the token while the first connection is still up, then drop it:
	// the reconnect must re-resolve the provider and dial with the fresh token.
	token.Store("token-2")
	_ = first.Close()
	select {
	case <-conns:
	case <-time.After(2 * time.Second):
		t.Fatal("relay did not reconnect after the connection was dropped")
	}
	mu.Lock()
	assert.Equal(t, "Bearer token-2", authHeaders[1], "each dial must re-resolve the token provider")
	mu.Unlock()
}

// TestHubLink_GivenServerURLProvider_WhenTheDaemonIsNotClaimedYet_ThenItWaitsAndConnectsAfterLogin
// 覆盖「先起服务、后 agentred login」这条路径：daemon 启动时还没被认领，链路必须留在
// 重试循环里等，而不是当作「不存在」——login 是另一个进程，它写完 state.json 就退出，
// 没有任何东西会回来把链路建起来。
func TestHubLink_GivenServerURLProvider_WhenTheDaemonIsNotClaimedYet_ThenItWaitsAndConnectsAfterLogin(t *testing.T) {
	upgrader := websocket.Upgrader{}
	conns := make(chan *websocket.Conn, 4)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ws, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		conns <- ws
		for {
			if _, _, err := ws.ReadMessage(); err != nil {
				return
			}
		}
	}))
	t.Cleanup(server.Close)

	var serverURL atomic.Value
	serverURL.Store("") // 还没认领：解析不出端点
	var resolved atomic.Int64

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	link := NewHubLink(HubLinkOptions{
		ServerURLProvider: func() string {
			resolved.Add(1)
			return serverURL.Load().(string)
		},
		AccessTokenProvider: func() string { return "token-1" },
		HeartbeatInterval:   100 * time.Millisecond,
		RetryInitial:        time.Second,
		RetryMax:            4 * time.Second,
		RetryWait:           func(context.Context, time.Duration) error { return nil },
		Random:              func() float64 { return 1 },
	})
	runDone := make(chan error, 1)
	go func() { runDone <- link.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		require.NoError(t, <-runDone)
	})

	// 未认领期间：Run 必须还活着并反复重新解析，而不是退出或空连一气。
	require.Eventually(t, func() bool { return resolved.Load() >= 3 }, 2*time.Second, 10*time.Millisecond,
		"未认领时 Run 应当留在重试循环里反复重新解析端点")
	select {
	case <-conns:
		t.Fatal("端点解析不出来时不该发起连接")
	default:
	}

	// 另一个进程完成了 login。
	serverURL.Store(server.URL)
	select {
	case <-conns:
	case <-time.After(2 * time.Second):
		t.Fatal("认领之后同一个 Run 循环应当连上去，不需要重启进程")
	}
}

// 未认领是稳态,不是反复发生的故障:LAN-only 的 daemon 可以一直不登录,而重试退避
// 封顶 60s —— 每轮都记一行就是每天一千多行噪声,还会把真正的失败淹掉。只在进入这个
// 状态时记一次。
func TestHubLink_GivenNoAccount_WhenRetryingForever_ThenSaysSoOnceNotEveryRound(t *testing.T) {
	var (
		mu    sync.Mutex
		lines []string
	)
	var rounds atomic.Int64

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	link := NewHubLink(HubLinkOptions{
		ServerURLProvider: func() string {
			rounds.Add(1)
			return "" // 一直没有账号
		},
		AccessTokenProvider: func() string { return "" },
		HeartbeatInterval:   100 * time.Millisecond,
		RetryInitial:        time.Second,
		RetryMax:            4 * time.Second,
		RetryWait:           func(context.Context, time.Duration) error { return nil },
		Random:              func() float64 { return 1 },
		Logf: func(format string, args ...any) {
			mu.Lock()
			defer mu.Unlock()
			lines = append(lines, fmt.Sprintf(format, args...))
		},
	})
	runDone := make(chan error, 1)
	go func() { runDone <- link.Run(ctx) }()

	require.Eventually(t, func() bool { return rounds.Load() >= 5 }, 2*time.Second, 10*time.Millisecond,
		"重试循环应当一直在跑")
	cancel()
	require.NoError(t, <-runDone)

	mu.Lock()
	defer mu.Unlock()
	require.Len(t, lines, 1, "转了 %d 轮，只该说一次「还没有账号可连」，实际记了 %d 行", rounds.Load(), len(lines))
	assert.Contains(t, lines[0], "no account")
}

// 401 与「连不上」是两种完全不同的处境，退避重试却长得一模一样。凭据被服务端拒绝时
// 重试永远不会成功——机器要么已被解除授权，要么它认领的那个账号在服务端已经不存在
// （例如库被重建）。这两种都只能靠人 unclaim 后重新 login，所以这一行必须把话说清楚，
// 而不是混在一串 "relay dial failed" 里让人对着一台恒离线的机器猜。
func TestHubLink_GivenServerRejectsTheCredential_ThenSaysItNeedsAFreshLoginNotJustRetrying(t *testing.T) {
	var (
		mu    sync.Mutex
		lines []string
	)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, `{"code":30304}`, http.StatusUnauthorized)
	}))
	t.Cleanup(server.Close)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	link := NewHubLink(HubLinkOptions{
		ServerURL:           server.URL,
		AccessTokenProvider: func() string { return "stale-token" },
		HeartbeatInterval:   100 * time.Millisecond,
		RetryInitial:        time.Millisecond,
		RetryMax:            2 * time.Millisecond,
		RetryWait:           func(context.Context, time.Duration) error { return nil },
		Random:              func() float64 { return 1 },
		Logf: func(format string, args ...any) {
			mu.Lock()
			defer mu.Unlock()
			lines = append(lines, fmt.Sprintf(format, args...))
		},
	})
	runDone := make(chan error, 1)
	go func() { runDone <- link.Run(ctx) }()

	require.Eventually(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		for _, l := range lines {
			if strings.Contains(l, "agentred login") {
				return true
			}
		}
		return false
	}, 2*time.Second, 10*time.Millisecond, "401 必须给出可执行的下一步，实际记了: %v", lines)

	cancel()
	require.NoError(t, <-runDone)
}
