package relaytransport

import (
	"context"
	"errors"
	"fmt"
	"log"
	"math/rand/v2"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
)

const (
	defaultHubHeartbeatInterval = 15 * time.Second
	defaultHubRetryInitial      = time.Second
	defaultHubRetryMax          = time.Minute
	hubFrameBuffer              = 64
)

// ErrHubUnavailable reports that the relay is not currently connected. Callers
// must wait for OnDial before retrying; HubLink reconnects in the background.
var ErrHubUnavailable = errors.New("rpc: hub relay unavailable")

// ErrHubUnresolved reports that there is no relay endpoint to dial yet, because
// the daemon has no account. It is not a failure: Run backs off and retries, so
// a login that lands later is picked up without restarting the process.
var ErrHubUnresolved = errors.New("rpc: relay endpoint not resolved yet")

// ErrRelayCredentialRejected reports that the relay answered the handshake with
// 401. Unlike every other dial failure this one never heals by itself: the device
// has been deauthorized, or the account it is claimed to no longer exists on the
// server (a rebuilt database is the usual cause — the numeric user/device ids the
// credential names are simply gone). Only a human can fix it, so it must not read
// like transient network trouble.
var ErrRelayCredentialRejected = errors.New("rpc: relay rejected this daemon's account credential")

// ErrRelayGoingAway reports that the account server closed this link on purpose
// because the replica is shutting down (WebSocket 1001, Going Away).
//
// It is deliberately not a failure. The replica that just left is already out of
// the load balancer, so redialing lands on a live one — the right move is to
// reconnect at once. Compounding the backoff instead would turn one rolling
// update into a minute of apparent offline time per machine, while nothing was
// ever broken. A 1006 (the wire really dropped) keeps compounding; telling the
// two apart is the entire point of the server writing that close frame.
var ErrRelayGoingAway = errors.New("rpc: relay replica is shutting down")

// HubFrame is one raw WebSocket frame arriving from the relay. The future hub
// multiplexer owns interpreting its payload and must select on Frames instead
// of reading the underlying socket directly.
type HubFrame struct {
	MessageType int
	Payload     []byte
}

// HubLinkOptions configures a daemon-owned outbound relay connection.
type HubLinkOptions struct {
	// Endpoint is the relay path this link dials, appended to ServerURL the same
	// way hubEndpoint always has. It defaults to "/v1/relay/daemon" — the path
	// used both by agentred's own outbound link and by a desktop registering
	// itself as an addressable target (internal/peer.Inbound).
	//
	// server_svc's resident relay-client connection (decision 13: browser and
	// desktop merge their account signal into their relay **client** link) sets
	// this to "/v1/relay/client" instead — same transport, same envelope, same
	// Multiplexer, different server-side route.
	Endpoint  string
	ServerURL string
	// ServerURLProvider re-resolves the account server base URL at every dial,
	// the same way AccessTokenProvider re-resolves the token. When set it takes
	// precedence over the static ServerURL field.
	//
	// It returns "" while the daemon has no account to connect to, and Run then
	// stays in its retry loop instead of treating the link as nonexistent: the
	// daemon may have started before `agentred login` ran, and login is a
	// separate process that writes state.json and exits — nothing would come
	// back to build the link.
	ServerURLProvider func() string
	AccessToken       string
	// AccessTokenProvider re-resolves the bearer token at every dial. When set
	// it takes precedence over the static AccessToken field. The daemon uses it
	// to hand HubLink the freshest refreshed access token across reconnects
	// without the link itself knowing how tokens are renewed (R4/R14): a token
	// that expires mid-connection is replaced by the fresh one on the next dial.
	AccessTokenProvider func() string

	HeartbeatInterval time.Duration
	RetryInitial      time.Duration
	RetryMax          time.Duration

	// MaxFrameBytes 是单帧载荷的读上限,0 取 defaultMaxFrameBytes。
	//
	// 中继帧是账号服务器转发过来的**另一台设备**的字节,不是本机可信输入:没有
	// 上限,对面说多大这边就分配多大。直连那条 WebSocket 一直是有界的
	// (protorpc.MaxFrameBytes),这一条必须同样有界,而且两处该是同一个数 ——
	// 所以由同时看得见两边的 daemon 在装配时把它传进来,而不是让本包反向去
	// import 上层的 protorpc。
	MaxFrameBytes int64

	// RetryWait is the clock seam for retry tests. Production waits for delay
	// or context cancellation; a test can advance a fake clock immediately.
	RetryWait func(context.Context, time.Duration) error
	// Random produces [0,1] jitter. A test supplies a stable value.
	Random func() float64

	// Logf is the log sink, defaulting to log.Printf. Same seam as
	// credentialRefresher.logf, so a test can read what the link reports.
	Logf func(format string, args ...any)

	// OnDial and OnDisconnect let the future multiplexer observe link lifecycle.
	// Hooks must return promptly because they run on the link's goroutine.
	OnDial       func()
	OnDisconnect func(error)
}

// defaultMaxFrameBytes 是直连那条 WebSocket 的载荷预算(protorpc.MaxFrameBytes)
// 加一个信封头 —— 中继这条线上收到的是服务端套过信封的载荷,不是裸载荷。
// 这里写字面量而不是 import:relaytransport 是传输层,不该反向依赖它上面的 RPC 层;
// 生产装配由 daemon 显式传参,两处同源由 daemon 那侧的用例守。
const defaultMaxFrameBytes int64 = 10<<20 + MaxEnvelopeBytes

func (l *HubLink) maxFrameBytes() int64 {
	if l.opts.MaxFrameBytes > 0 {
		return l.opts.MaxFrameBytes
	}
	return defaultMaxFrameBytes
}

// HubLink maintains the daemon's one outbound WebSocket to the account server.
// It intentionally has no virtual-channel or RPC-envelope knowledge: the
// caller layers that protocol on Send and Frames.
type HubLink struct {
	opts HubLinkOptions

	mu      sync.RWMutex
	conn    *websocket.Conn
	writeMu sync.Mutex
	frames  chan HubFrame

	lifecycleMu        sync.RWMutex
	lifecycleListeners []hubLifecycleListener
}

type hubLifecycleListener struct {
	onDial       func()
	onDisconnect func(error)
}

// NewHubLink creates an outbound relay manager. Run starts its lifetime.
func NewHubLink(opts HubLinkOptions) *HubLink {
	if opts.Endpoint == "" {
		opts.Endpoint = defaultHubEndpoint
	}
	if opts.HeartbeatInterval <= 0 {
		opts.HeartbeatInterval = defaultHubHeartbeatInterval
	}
	if opts.RetryInitial <= 0 {
		opts.RetryInitial = defaultHubRetryInitial
	}
	if opts.RetryMax <= 0 {
		opts.RetryMax = defaultHubRetryMax
	}
	if opts.RetryMax < opts.RetryInitial {
		opts.RetryMax = opts.RetryInitial
	}
	if opts.RetryWait == nil {
		opts.RetryWait = waitForHubRetry
	}
	if opts.Random == nil {
		opts.Random = rand.Float64
	}
	if opts.Logf == nil {
		opts.Logf = log.Printf
	}
	return &HubLink{opts: opts, frames: make(chan HubFrame, hubFrameBuffer)}
}

// Run connects until ctx is canceled, and returns nil for that shutdown. A
// relay failure is intentionally not returned to the daemon: it is logged,
// backed off, and retried while LAN work and local sessions continue
// independently.
//
// The single exception is a retry clock that fails for a reason other than
// shutdown (see stopRetrying): backing off has become impossible, so Run stops
// and returns that error instead of looking like a clean exit. A caller that
// discards Run's error therefore keeps a relay that is permanently down and
// silent except for the log line.
func (l *HubLink) Run(ctx context.Context) error {
	failures := 0
	// 「还没有账号可连」是稳态而不是反复发生的故障：LAN-only 的 daemon 可以一直不
	// 登录，而退避封顶 60s —— 每轮记一行就是每天上千行噪声，真正的失败会被淹掉。
	// 只在进入这个状态时记一次，离开它（连上了，或换成别的失败）时复位。
	unresolvedLogged := false
	// 401 同理，而且更该压制：它是个**永久**状态（要人来 unclaim + login），退避封顶
	// 60s 也照样是每天上千行。说清楚一次，比重复一千遍有用。
	rejectedLogged := false
	for {
		if ctx.Err() != nil {
			// Shutdown is Run's normal termination, not a relay failure.
			return nil //nolint:nilerr // ctx cancellation ends Run cleanly: there is nothing left to dial or report
		}
		conn, err := l.dial(ctx)
		if err != nil {
			if ctx.Err() != nil {
				// The dial was aborted by our own shutdown, so the daemon must not
				// see it as a relay failure: no log, no backoff, no retry.
				return nil //nolint:nilerr // this dial error is the shutdown surfacing through the dialer
			}
			if errors.Is(err, ErrHubUnresolved) {
				// 未认领不是故障：说成 dial failed 会把「这台机器还没登录」误导成
				// 「登录了但连不上」，而这两者的处置完全不同。
				if !unresolvedLogged {
					l.opts.Logf("rpc.HubLink: no account to connect to yet; waiting for login")
					unresolvedLogged = true
				}
			} else if errors.Is(err, ErrRelayCredentialRejected) {
				unresolvedLogged = false
				if !rejectedLogged {
					l.opts.Logf("rpc.HubLink: the server rejected this daemon's credential (HTTP 401); " +
						"retrying cannot fix it — this device was deauthorized, or the account it is claimed " +
						"to no longer exists. Stop the daemon, then run `agentred unclaim` and `agentred login`.")
					rejectedLogged = true
				}
			} else {
				unresolvedLogged = false
				rejectedLogged = false
				l.opts.Logf("rpc.HubLink: relay dial failed; retrying: %v", err)
			}
			if err := l.wait(ctx, failures); err != nil {
				return l.stopRetrying(ctx, err)
			}
			failures++
			continue
		}

		l.setConn(conn)
		l.notifyDial()
		var renewed bool
		err, renewed = l.serve(ctx, conn)
		l.clearConn(conn)
		_ = conn.Close()
		if errors.Is(err, ErrRelayGoingAway) {
			// 服务端主动让位:立刻重拨,别把它记成一次故障。
			renewed = true
		}
		if ctx.Err() != nil {
			l.notifyDisconnect(ctx.Err())
			// The read loop ended because we are shutting down; listeners already
			// have ctx.Err(), and Run itself exits clean.
			return nil //nolint:nilerr // shutdown, not a relay failure: the serve error is the cancellation surfacing
		}
		if renewed {
			failures = 0
		}
		// 连上过就说明凭据当时是好的：两个抑制位都复位，之后再出现同样的状态要重新说一次。
		unresolvedLogged = false
		rejectedLogged = false
		l.notifyDisconnect(err)
		l.opts.Logf("rpc.HubLink: relay disconnected; retrying: %v", err)
		if err := l.wait(ctx, failures); err != nil {
			return l.stopRetrying(ctx, err)
		}
		failures++
	}
}

// stopRetrying classifies why a backoff wait ended the reconnect loop. A wait
// that ends because ctx was canceled is Run's normal shutdown, so it exits nil.
// Any other wait failure means the retry clock itself is broken: swallowing it
// would leave the relay permanently down with nobody told, so it propagates.
func (l *HubLink) stopRetrying(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return nil //nolint:nilerr // the wait error is the shutdown surfacing through the clock seam; Run's contract is a clean nil exit
	}
	l.opts.Logf("rpc.HubLink: relay retry clock failed; giving up: %v", err)
	return fmt.Errorf("relay retry wait: %w", err)
}

// Send writes one raw relay frame. It does not queue while disconnected so the
// future multiplexer can fail or retry each virtual-channel operation honestly.
func (l *HubLink) Send(messageType int, payload []byte) error {
	l.mu.RLock()
	conn := l.conn
	l.mu.RUnlock()
	if conn == nil {
		return ErrHubUnavailable
	}
	if err := l.writeMessage(conn, messageType, payload); err != nil {
		_ = conn.Close()
		return fmt.Errorf("write relay frame: %w", err)
	}
	return nil
}

// Receive returns the raw inbound frame stream. It remains open across a
// reconnect; callers use lifecycle hooks or their parent context to delimit a
// particular physical connection.
func (l *HubLink) Receive() <-chan HubFrame { return l.frames }

// Connected reports whether the relay currently has a physical WebSocket.
func (l *HubLink) Connected() bool {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.conn != nil
}

// AddLifecycleListener registers a transport observer without replacing the
// composition-root callbacks supplied in HubLinkOptions. A listener added
// after a successful dial is immediately told about that live connection.
func (l *HubLink) AddLifecycleListener(onDial func(), onDisconnect func(error)) {
	l.lifecycleMu.Lock()
	defer l.lifecycleMu.Unlock()
	l.lifecycleListeners = append(l.lifecycleListeners, hubLifecycleListener{
		onDial:       onDial,
		onDisconnect: onDisconnect,
	})
	if onDial != nil && l.Connected() {
		onDial()
	}
}

func (l *HubLink) notifyDial() {
	if l.opts.OnDial != nil {
		l.opts.OnDial()
	}
	l.lifecycleMu.RLock()
	listeners := append([]hubLifecycleListener(nil), l.lifecycleListeners...)
	l.lifecycleMu.RUnlock()
	for _, listener := range listeners {
		if listener.onDial != nil {
			listener.onDial()
		}
	}
}

func (l *HubLink) notifyDisconnect(err error) {
	if l.opts.OnDisconnect != nil {
		l.opts.OnDisconnect(err)
	}
	l.lifecycleMu.RLock()
	listeners := append([]hubLifecycleListener(nil), l.lifecycleListeners...)
	l.lifecycleMu.RUnlock()
	for _, listener := range listeners {
		if listener.onDisconnect != nil {
			listener.onDisconnect(err)
		}
	}
}

func (l *HubLink) dial(ctx context.Context) (*websocket.Conn, error) {
	serverURL := l.opts.ServerURL
	if l.opts.ServerURLProvider != nil {
		serverURL = l.opts.ServerURLProvider()
	}
	if serverURL == "" {
		return nil, ErrHubUnresolved
	}
	endpoint, err := hubEndpoint(serverURL, l.opts.Endpoint)
	if err != nil {
		return nil, err
	}
	token := l.opts.AccessToken
	if l.opts.AccessTokenProvider != nil {
		token = l.opts.AccessTokenProvider()
	}
	headers := make(http.Header)
	headers.Set("Authorization", "Bearer "+token)
	conn, resp, err := websocket.DefaultDialer.DialContext(ctx, endpoint, headers)
	// The handshake response body is a no-op reader on both outcomes (gorilla
	// replaces it before returning); closing it keeps the HTTP contract explicit.
	if resp != nil && resp.Body != nil {
		_ = resp.Body.Close()
	}
	if err != nil {
		// gorilla 把任何非 101 都压成 ErrBadHandshake，状态码只在 resp 上。401 与
		// 「连不上」必须分开：前者重试到天荒地老也不会成功。
		if resp != nil && resp.StatusCode == http.StatusUnauthorized {
			return nil, ErrRelayCredentialRejected
		}
		return nil, fmt.Errorf("dial relay websocket: %w", err)
	}
	// 超限时 gorilla 回一个 1009 关闭帧并让 ReadMessage 报错,读循环因此走既有的
	// 断连重拨路径 —— 这正是想要的:超限说明对面要么坏了要么不怀好意,继续按帧
	// 边界读下去没有意义。
	conn.SetReadLimit(l.maxFrameBytes())
	return conn, nil
}

func (l *HubLink) serve(ctx context.Context, conn *websocket.Conn) (error, bool) {
	readDeadline := 2 * l.opts.HeartbeatInterval
	if err := conn.SetReadDeadline(time.Now().Add(readDeadline)); err != nil {
		return fmt.Errorf("set relay read deadline: %w", err), false
	}
	var renewed atomic.Bool
	conn.SetPongHandler(func(string) error {
		renewed.Store(true)
		return conn.SetReadDeadline(time.Now().Add(readDeadline))
	})

	stopHeartbeat := make(chan struct{})
	defer close(stopHeartbeat)
	go func() {
		select {
		case <-ctx.Done():
			_ = conn.Close()
		case <-stopHeartbeat:
		}
	}()
	go l.heartbeat(ctx, conn, stopHeartbeat)

	for {
		messageType, payload, err := conn.ReadMessage()
		if err != nil {
			if websocket.IsCloseError(err, websocket.CloseGoingAway) {
				return fmt.Errorf("%w: %v", ErrRelayGoingAway, err), renewed.Load()
			}
			return fmt.Errorf("read relay frame: %w", err), renewed.Load()
		}
		frame := HubFrame{MessageType: messageType, Payload: append([]byte(nil), payload...)}
		select {
		case l.frames <- frame:
		case <-ctx.Done():
			return ctx.Err(), renewed.Load()
		default:
			_ = conn.Close()
			return errors.New("relay frame consumer is not keeping up"), renewed.Load()
		}
	}
}

func (l *HubLink) heartbeat(ctx context.Context, conn *websocket.Conn, stop <-chan struct{}) {
	ticker := time.NewTicker(l.opts.HeartbeatInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-stop:
			return
		case <-ticker.C:
			if err := l.writeControl(conn, websocket.PingMessage, nil,
				time.Now().Add(l.opts.HeartbeatInterval)); err != nil {
				_ = conn.Close()
				return
			}
		}
	}
}

// relayWriter 是一次中继写要用到的全部能力。收窄成接口而不是 *websocket.Conn,
// 是为了让「这次写有没有期限」可被断言 —— 用真连接去验只能靠把内核发送缓冲塞满。
type relayWriter interface {
	SetWriteDeadline(time.Time) error
	WriteMessage(int, []byte) error
}

// writeTimeout 与 protorpc 的写期限同一个数(2 倍心跳间隔,默认 30s)。同一条物理
// 链路上两套判活节奏只会让排障时对不上账。
func (l *HubLink) writeTimeout() time.Duration { return 2 * l.opts.HeartbeatInterval }

func (l *HubLink) writeMessage(conn relayWriter, messageType int, payload []byte) error {
	l.writeMu.Lock()
	defer l.writeMu.Unlock()
	// 没有期限的写会永久堵在写锁里,连心跳 ping 都发不出去(writeControl 用的是同一
	// 把锁),这条链路上所有虚拟通道一起卡死且谁也发现不了。
	if timeout := l.writeTimeout(); timeout > 0 {
		if err := conn.SetWriteDeadline(time.Now().Add(timeout)); err != nil {
			return err
		}
	}
	return conn.WriteMessage(messageType, payload)
}

func (l *HubLink) writeControl(conn *websocket.Conn, messageType int, payload []byte, deadline time.Time) error {
	l.writeMu.Lock()
	defer l.writeMu.Unlock()
	return conn.WriteControl(messageType, payload, deadline)
}

func (l *HubLink) setConn(conn *websocket.Conn) {
	l.mu.Lock()
	l.conn = conn
	l.mu.Unlock()
}

func (l *HubLink) clearConn(conn *websocket.Conn) {
	l.mu.Lock()
	if l.conn == conn {
		l.conn = nil
	}
	l.mu.Unlock()
}

func (l *HubLink) wait(ctx context.Context, failures int) error {
	return l.opts.RetryWait(ctx, l.backoff(failures))
}

func (l *HubLink) backoff(failures int) time.Duration {
	delay := l.opts.RetryInitial
	for range failures {
		if delay >= l.opts.RetryMax/2 {
			delay = l.opts.RetryMax
			break
		}
		delay *= 2
	}
	jitter := l.opts.Random()
	if jitter < 0 {
		jitter = 0
	} else if jitter > 1 {
		jitter = 1
	}
	return time.Duration(float64(delay) * (0.5 + jitter/2))
}

func waitForHubRetry(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// defaultHubEndpoint is the path used by agentred's own outbound link and by a
// desktop registering itself as an addressable target (internal/peer.Inbound).
const defaultHubEndpoint = "/v1/relay/daemon"

func hubEndpoint(serverURL, path string) (string, error) {
	parsed, err := url.Parse(serverURL)
	if err != nil || parsed.Host == "" {
		return "", errors.New("relay server URL must be an http(s) base URL")
	}
	switch parsed.Scheme {
	case "http":
		parsed.Scheme = "ws"
	case "https":
		parsed.Scheme = "wss"
	default:
		return "", errors.New("relay server URL must be an http(s) base URL")
	}
	if path == "" {
		path = defaultHubEndpoint
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/") + path
	parsed.RawPath = ""
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return parsed.String(), nil
}
