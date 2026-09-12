package portforward

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/agentre-hub/agentre/internal/model/entity/port_forward_entity"
	"github.com/agentre-hub/agentre/internal/repository/port_forward_repo/mock_port_forward_repo"
	"github.com/agentre-hub/agentre/pkg/wire/agentrewire"
	"github.com/agentre-hub/agentre/pkg/wire/rpcerror"
	"github.com/agentre-hub/agentre/pkg/wire/wirelimits"
)

// 本文件测的是**一条转发流真的把字节搬过去了**:目标是一个跑在本机环回上的真服务
// (httptest / 一个裸 TCP 监听),拨号走生产那一条 DialLoopback,读的是真 socket。
//
// 三条不许踩的线,每一条都有对应的用例:
//
//   - 分片**一块都不许丢**。terminal.* 的流控队列满了就丢最老的一块再插一行
//     [--- output throttled ---];终端上少几行还看得懂,HTTP 响应体丢一块就是文件
//     损坏。所以断言是「拼起来与原始字节逐字相同」,而不是「大致收到了」。
//   - 背压**不许靠阻塞**。生产者停的是「从本机 socket 读」这一件事,不是把通知顶在
//     写锁上 —— protorpc 的写锁全连接互斥,顶住它等于让同一条连接上所有别的会话一起
//     停(硬不变量 2)。
//   - 升级路径上 Connection / Upgrade 必须**送得到上游**。逐跳头那张表默认剥掉
//     Upgrade,照搬就会让 101 永远上不去。

// ---------- 测试脚手架 ----------

// notification 是收到的一条通知,按种类摊平 —— 用例真正要断言的是**次序**。
type notification struct {
	kind    string
	head    *agentrewire.PortForwardResponseNotification
	data    []byte
	closed  *agentrewire.PortForwardClosedNotification
	revoked *agentrewire.PortForwardRevokedNotification
}

type recorder struct {
	mu     sync.Mutex
	events []notification
	// hold 不为 nil 时,每一条通知都卡在这里不返回 —— 模拟一个写不动的对端。
	hold chan struct{}
}

func newRecorder() *recorder { return &recorder{} }

func (r *recorder) notify(n *agentrewire.RpcNotification) error {
	r.mu.Lock()
	hold := r.hold
	r.mu.Unlock()
	if hold != nil {
		<-hold
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	switch payload := n.GetPayload().(type) {
	case *agentrewire.RpcNotification_PortForwardResponse:
		r.events = append(r.events, notification{kind: "head", head: payload.PortForwardResponse})
	case *agentrewire.RpcNotification_PortForwardData:
		r.events = append(r.events, notification{kind: "data", data: payload.PortForwardData.GetData()})
	case *agentrewire.RpcNotification_PortForwardClosed:
		r.events = append(r.events, notification{kind: "closed", closed: payload.PortForwardClosed})
	case *agentrewire.RpcNotification_PortForwardRevoked:
		r.events = append(r.events, notification{kind: "revoked", revoked: payload.PortForwardRevoked})
	}
	return nil
}

// stall 让此后的每一条通知都卡住,返回的函数放行(用例收尾时调,免得留下挂死的 goroutine)。
func (r *recorder) stall() func() {
	hold := make(chan struct{})
	r.mu.Lock()
	r.hold = hold
	r.mu.Unlock()
	var once sync.Once
	return func() { once.Do(func() { close(hold) }) }
}

func (r *recorder) snapshot() []notification {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]notification(nil), r.events...)
}

func (r *recorder) dataBytes() []byte {
	var out []byte
	for _, event := range r.snapshot() {
		if event.kind == "data" {
			out = append(out, event.data...)
		}
	}
	return out
}

func (r *recorder) closedEvent() *agentrewire.PortForwardClosedNotification {
	for _, event := range r.snapshot() {
		if event.kind == "closed" {
			return event.closed
		}
	}
	return nil
}

func (r *recorder) waitClosed(t *testing.T) *agentrewire.PortForwardClosedNotification {
	t.Helper()
	require.Eventually(t, func() bool { return r.closedEvent() != nil }, 10*time.Second, time.Millisecond,
		"每一种断开都必须有一条收尾通知,不留悬挂的流")
	return r.closedEvent()
}

// declaredRepo 是一份「这几个端口已声明且已启用」的声明集。仓储一律走 mock,不连库。
func declaredRepo(t *testing.T, ports ...int) *mock_port_forward_repo.MockPortForwardRepo {
	t.Helper()
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)
	repo := mock_port_forward_repo.NewMockPortForwardRepo(ctrl)
	declared := make(map[int]bool, len(ports))
	for _, port := range ports {
		declared[port] = true
	}
	repo.EXPECT().FindByPort(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, port int) (*port_forward_entity.PortForward, error) {
			if !declared[port] {
				return nil, nil
			}
			return &port_forward_entity.PortForward{ID: int64(port), Port: port, Enabled: true}, nil
		}).AnyTimes()
	return repo
}

func newStreams(t *testing.T, notify Notifier, dial Dialer, ports ...int) *Streams {
	t.Helper()
	gate := NewHandlers(Options{Repo: declaredRepo(t, ports...), Dial: dial})
	streams := NewStreams(StreamOptions{Gate: gate, Notify: notify})
	t.Cleanup(streams.CloseAll)
	return streams
}

// countingConn 记下这条流真的从本机 socket 读走了多少字节,以及它有没有被关掉。
// 背压的判据只能落在这里 —— 「宿主收到多少」证不了设备侧停没停手。
type countingConn struct {
	net.Conn
	read   *atomic.Int64
	closed *atomic.Bool
}

func (c countingConn) Read(p []byte) (int, error) {
	n, err := c.Conn.Read(p)
	c.read.Add(int64(n))
	return n, err
}

func (c countingConn) Close() error {
	c.closed.Store(true)
	return c.Conn.Close()
}

func countingDialer() (Dialer, *atomic.Int64, *atomic.Bool) {
	read := &atomic.Int64{}
	closed := &atomic.Bool{}
	return func(ctx context.Context, port int) (net.Conn, error) {
		conn, err := DialLoopback(ctx, port)
		if err != nil {
			return nil, err
		}
		return countingConn{Conn: conn, read: read, closed: closed}, nil
	}, read, closed
}

func listenerPort(t *testing.T, addr string) int {
	t.Helper()
	_, portText, err := net.SplitHostPort(addr)
	require.NoError(t, err)
	var port int
	_, err = fmt.Sscanf(portText, "%d", &port)
	require.NoError(t, err)
	return port
}

func serverPort(t *testing.T, server *httptest.Server) int {
	t.Helper()
	return listenerPort(t, server.Listener.Addr().String())
}

// hangingServer 起一个「回了响应头就不再往下写」的本机服务:用来看一条**还开着**的
// 流被关掉时会发生什么。handler 卡在 hang 上,清理顺序(LIFO)保证它先被放行,再关服务器
// —— 否则 httptest.Server.Close 会等这次请求等到天荒地老。
func hangingServer(t *testing.T) (port int, release func()) {
	t.Helper()
	hang := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("head"))
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
		<-hang
	}))
	t.Cleanup(server.Close)
	var once sync.Once
	closer := func() { once.Do(func() { close(hang) }) }
	t.Cleanup(closer)
	return serverPort(t, server), closer
}

// patternBody 造一段可逐字比对的响应体。刻意不是随机数据:用例要断言的是
// 「第 n 个字节还是第 n 个字节」,乱序 / 丢块 / 重复都必须当场被认出来。
func patternBody(size int) []byte {
	out := make([]byte, size)
	for i := range out {
		out[i] = byte(i*7 + i/251)
	}
	return out
}

// ---------- 目标 1:大响应分片带回,字节逐字一致,一块都不丢 ----------

// Given 本机服务回一段远大于分片的响应体,When 宿主经转发流消费它,Then 响应头先到、
// body 分成多块按序到达、拼起来与原始字节逐字相同,且中途没有任何丢弃 / 节流标记。
func TestStreamOpen_GivenALargeResponse_WhenTheHostConsumesIt_ThenEveryByteComesBackInOrder(t *testing.T) {
	body := patternBody(1 << 20)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		_, _ = w.Write(body)
	}))
	t.Cleanup(server.Close)
	port := serverPort(t, server)

	rec := newRecorder()
	streams := newStreams(t, rec.notify, DialLoopback, port)

	opened, err := streams.Open(context.Background(), &agentrewire.PortForwardOpenRequest{
		StreamId: "s1", Port: uint32(port), Method: http.MethodGet, Path: "/big",
	})
	require.NoError(t, err)
	require.Equal(t, "s1", opened.GetStreamId())

	closed := rec.waitClosed(t)
	assert.Equal(t, "eof", closed.GetReason())
	assert.Zero(t, closed.GetCode(), "正常收尾不带领域错误码")

	events := rec.snapshot()
	require.Equal(t, "head", events[0].kind, "响应头必须先于任何一块 body")
	assert.EqualValues(t, http.StatusOK, events[0].head.GetStatus())
	assert.Equal(t, []string{"application/octet-stream"},
		events[0].head.GetHeaders()["Content-Type"].GetValues())
	assert.False(t, events[0].head.GetUpgraded())

	var chunks int
	for _, event := range events {
		if event.kind == "data" {
			chunks++
			assert.NotContains(t, string(event.data), "throttled",
				"HTTP 响应体不许出现任何丢弃 / 节流标记 —— 丢一块就是文件损坏")
		}
	}
	assert.GreaterOrEqual(t, chunks, 2, "远大于分片的响应体必须分成多块带回,而不是整体缓冲")
	assert.True(t, bytes.Equal(body, rec.dataBytes()), "拼起来必须与本机服务发出的字节逐字相同")
}

// ---------- 目标 2:真背压 —— 停止从本机 socket 读,而不是丢块、也不是顶住 Notify ----------

// Given 一条窗口很小的转发流与一段远大于窗口的响应体,When 宿主收下分片却迟迟不回 ack,
// Then 设备侧**停止从本机 socket 读**(读走的字节不超过窗口),而不是继续读进内存或丢块;
// 宿主补上 ack 之后,剩下的字节一个不少地跟上来。
func TestStreamBackpressure_GivenAConsumerThatDoesNotAck_WhenTheWindowFills_ThenTheDeviceStopsReadingTheLocalSocket(t *testing.T) {
	const window = 16 << 10
	body := patternBody(1 << 20)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(body)
	}))
	t.Cleanup(server.Close)
	port := serverPort(t, server)

	rec := newRecorder()
	dial, read, _ := countingDialer()
	streams := newStreams(t, rec.notify, dial, port)

	_, err := streams.Open(context.Background(), &agentrewire.PortForwardOpenRequest{
		StreamId: "s1", Port: uint32(port), Method: http.MethodGet, Path: "/big",
		WindowBytes: window,
	})
	require.NoError(t, err)

	// 窗口一满就该停手。等它读满,再多给一段时间证明它**没有**继续读。
	//
	// 上界是「窗口 + 一次 bufio 填充」:应答头是从同一条 socket 上读进来的,读它那一
	// 次会顺带把缓冲区填满(bufio 默认 4 KiB),这几千字节与信用无关。要紧的是这个数
	// 与 1 MiB 的响应体差着两个数量级 —— 设备侧确实停在窗口上,而不是把整段读进内存。
	const bufioFill = 4 << 10
	require.Eventually(t, func() bool { return read.Load() >= window }, 5*time.Second, time.Millisecond)
	time.Sleep(200 * time.Millisecond)
	assert.LessOrEqual(t, read.Load(), int64(window+bufioFill),
		"未确认量已达窗口,设备侧必须停止从本机 socket 读 —— 让内核的 TCP 窗口把压力还给被转发的那个服务")
	assert.Nil(t, rec.closedEvent(), "停读不是结束:这条流还活着,只是不再往前取")

	// 补 ack:一路把「已消费到第几个字节」累计回报,直到收尾。
	go func() {
		for {
			if rec.closedEvent() != nil {
				return
			}
			_, _ = streams.Ack(context.Background(), &agentrewire.PortForwardAckRequest{
				StreamId: "s1", ConsumedBytes: uint64(len(rec.dataBytes())),
			})
			time.Sleep(time.Millisecond)
		}
	}()

	rec.waitClosed(t)
	assert.True(t, bytes.Equal(body, rec.dataBytes()), "背压期间一个字节都不许丢")
}

// Given 一条通知怎么也写不出去的连接(对端不取),When 同一条连接上还有别的方法被调用,
// Then 它们照常应答 —— 转发流的背压不许落在 Notify 上。
//
// protorpc 的写锁是全连接互斥、通知在读循环里同步派发:一条流把 Notify 顶住,顶死的是
// 整条连接上所有别的会话(硬不变量 2)。所以生产者必须在**自己的 goroutine** 里发通知,
// 且不许在发通知期间握着任何 open / write / close / ack 要用的锁。
func TestStreamBackpressure_GivenANotifierThatNeverReturns_WhenOtherMethodsAreCalled_ThenTheyStillAnswer(t *testing.T) {
	body := patternBody(1 << 20)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(body)
	}))
	t.Cleanup(server.Close)
	port := serverPort(t, server)

	rec := newRecorder()
	release := rec.stall()
	t.Cleanup(release)
	streams := newStreams(t, rec.notify, DialLoopback, port)

	_, err := streams.Open(context.Background(), &agentrewire.PortForwardOpenRequest{
		StreamId: "stuck", Port: uint32(port), Method: http.MethodGet, Path: "/big",
	})
	require.NoError(t, err)

	answered := make(chan struct{})
	go func() {
		defer close(answered)
		ctx := context.Background()
		_, _ = streams.Ack(ctx, &agentrewire.PortForwardAckRequest{StreamId: "stuck", ConsumedBytes: 1})
		_, _ = streams.Open(ctx, &agentrewire.PortForwardOpenRequest{
			StreamId: "second", Port: uint32(port), Method: http.MethodGet, Path: "/big",
		})
		_, _ = streams.Close(ctx, &agentrewire.PortForwardCloseRequest{StreamId: "stuck"})
	}()

	select {
	case <-answered:
	case <-time.After(5 * time.Second):
		t.Fatal("一条卡住的流把同一条连接上别的方法一起顶死了 —— 背压不许落在 Notify 上")
	}
}

// ---------- 目标 3:101 之后双向纯字节 ----------

// upgradeEcho 起一个裸 TCP 监听:读完请求头就回 101,此后原样回显收到的每一个字节。
// 用真 socket 而不是 httptest 是因为 101 之后不再是 HTTP,net/http 没有立场处理它。
func upgradeEcho(t *testing.T) (port int, head func() string) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = listener.Close() })

	var mu sync.Mutex
	var request string
	go func() {
		conn, acceptErr := listener.Accept()
		if acceptErr != nil {
			return
		}
		defer func() { _ = conn.Close() }()
		reader := bufio.NewReader(conn)
		var builder strings.Builder
		for {
			line, readErr := reader.ReadString('\n')
			if readErr != nil {
				return
			}
			builder.WriteString(line)
			if line == "\r\n" {
				break
			}
		}
		mu.Lock()
		request = builder.String()
		mu.Unlock()
		if _, writeErr := io.WriteString(conn, "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: abc\r\n\r\n"); writeErr != nil {
			return
		}
		_, _ = io.Copy(conn, reader)
	}()

	return listenerPort(t, listener.Addr().String()), func() string {
		mu.Lock()
		defer mu.Unlock()
		return request
	}
}

// Given 被转发的服务回 101,When 宿主此后往这条流写字节,Then 升级这件事独立于状态码
// 可判定、升级头原样送到了上游,而且两个方向搬的都是未经解释的纯字节。
func TestStreamUpgrade_GivenTheUpstreamSwitchesProtocols_WhenBytesFlowBothWays_ThenNeitherDirectionIsInterpreted(t *testing.T) {
	port, head := upgradeEcho(t)

	rec := newRecorder()
	streams := newStreams(t, rec.notify, DialLoopback, port)

	_, err := streams.Open(context.Background(), &agentrewire.PortForwardOpenRequest{
		StreamId: "ws", Port: uint32(port), Method: http.MethodGet, Path: "/hmr",
		Headers: map[string]*agentrewire.HeaderValues{
			"Connection":            {Values: []string{"Upgrade"}},
			"Upgrade":               {Values: []string{"websocket"}},
			"Sec-Websocket-Key":     {Values: []string{"dGhlIHNhbXBsZSBub25jZQ=="}},
			"Sec-Websocket-Version": {Values: []string{"13"}},
		},
	})
	require.NoError(t, err)

	require.Eventually(t, func() bool {
		for _, event := range rec.snapshot() {
			if event.kind == "head" {
				return true
			}
		}
		return false
	}, 5*time.Second, time.Millisecond)

	events := rec.snapshot()
	require.Equal(t, "head", events[0].kind)
	assert.EqualValues(t, http.StatusSwitchingProtocols, events[0].head.GetStatus())
	assert.True(t, events[0].head.GetUpgraded(),
		"「此后不再是 HTTP」必须独立于状态码可判定,消费者据它切换自己那一侧的行为")
	assert.Equal(t, []string{"abc"}, events[0].head.GetHeaders()["Sec-Websocket-Accept"].GetValues(),
		"握手应答头要原样带回,否则宿主没法完成与浏览器那一侧的升级")

	// 升级请求头必须真的送到了上游 —— 逐跳头那张表默认剥掉 Upgrade,照搬会让 101 永远上不去。
	requestHead := head()
	assert.Contains(t, strings.ToLower(requestHead), "upgrade: websocket")
	assert.Contains(t, strings.ToLower(requestHead), "connection: upgrade")
	assert.Contains(t, requestHead, "dGhlIHNhbXBsZSBub25jZQ==")

	frame := []byte{0x81, 0x03, 'h', 'm', 'r', 0x00, 0xff, 0x0d, 0x0a}
	_, err = streams.Write(context.Background(), &agentrewire.PortForwardWriteRequest{
		StreamId: "ws", Data: frame,
	})
	require.NoError(t, err)

	require.Eventually(t, func() bool { return bytes.Equal(frame, rec.dataBytes()) }, 5*time.Second, time.Millisecond,
		"101 之后两个方向都只搬字节:写进去什么,回显回来的就该是什么")
}

// ---------- 目标 4:端口上没有服务 → 专用失败 ----------

// Given 一个已声明但没有任何服务在监听的端口,When 宿主开转发流,Then 回的是
// NoListener 这一个专用码,而不是一句笼统的失败。
//
// 「等那台机器回来」与「去把服务起起来」是用户要做的两件不同的事,折进一个笼统失败
// 就等于让用户去猜。
func TestStreamOpen_GivenNothingListeningOnTheDeclaredPort_WhenOpening_ThenNoListenerIsReported(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	port := listenerPort(t, listener.Addr().String())
	require.NoError(t, listener.Close())

	rec := newRecorder()
	streams := newStreams(t, rec.notify, DialLoopback, port)

	opened, err := streams.Open(context.Background(), &agentrewire.PortForwardOpenRequest{
		StreamId: "dead", Port: uint32(port), Method: http.MethodGet, Path: "/",
	})

	assert.Nil(t, opened)
	assert.Equal(t, int32(rpcerror.CodePortForwardNoListener), code(t, err))
	assert.Nil(t, rec.closedEvent(), "open 失败的流从没存在过,不该有收尾通知")
}

// ---------- 目标 5:close 与连接断开都关掉底层 conn ----------

// Given 一条开着的转发流,When 宿主主动关闭它,Then 设备到本机服务的那条连接被关掉,
// 并且这条流有一条收尾通知;此后再指向它的调用得到 StreamNotFound。
func TestStreamClose_GivenAnOpenStream_WhenTheHostCloses_ThenTheUpstreamConnectionIsClosed(t *testing.T) {
	port, _ := hangingServer(t)

	rec := newRecorder()
	dial, _, closed := countingDialer()
	streams := newStreams(t, rec.notify, dial, port)

	_, err := streams.Open(context.Background(), &agentrewire.PortForwardOpenRequest{
		StreamId: "s1", Port: uint32(port), Method: http.MethodGet, Path: "/hang",
	})
	require.NoError(t, err)

	_, err = streams.Close(context.Background(), &agentrewire.PortForwardCloseRequest{StreamId: "s1"})
	require.NoError(t, err)

	require.Eventually(t, closed.Load, 5*time.Second, time.Millisecond,
		"关流必须关掉设备到本机服务的那条连接,不留悬挂的流")
	rec.waitClosed(t)

	_, err = streams.Ack(context.Background(), &agentrewire.PortForwardAckRequest{StreamId: "s1"})
	assert.Equal(t, int32(rpcerror.CodePortForwardStreamNotFound), code(t, err))
}

// Given 一条开着的转发流,When 承载它的那条连接断了(CloseAll),Then 到本机服务的连接
// 一并被关掉 —— 流的生命周期挂在连接上,连接没了不留悬挂的流。
func TestStreamCloseAll_GivenTheCarryingConnectionGoesAway_WhenStreamsAreTornDown_ThenEveryUpstreamConnectionIsClosed(t *testing.T) {
	port, _ := hangingServer(t)

	rec := newRecorder()
	dial, _, closed := countingDialer()
	streams := newStreams(t, rec.notify, dial, port)

	_, err := streams.Open(context.Background(), &agentrewire.PortForwardOpenRequest{
		StreamId: "s1", Port: uint32(port), Method: http.MethodGet, Path: "/hang",
	})
	require.NoError(t, err)

	streams.CloseAll()

	require.Eventually(t, closed.Load, 5*time.Second, time.Millisecond,
		"连接断开时每一条流都要收尾")
	require.NotNil(t, rec.waitClosed(t))
}

// Given 一个从没 open 过的流号,When 宿主拿它调 write / close / ack,Then 三个都回
// StreamNotFound —— 这是调用方状态机落后了一步,与「这次访问不被允许」必须分得开。
func TestStreamMethods_GivenAnUnknownStreamID_WhenCalled_ThenStreamNotFoundIsReported(t *testing.T) {
	rec := newRecorder()
	streams := newStreams(t, rec.notify, DialLoopback)
	ctx := context.Background()

	_, err := streams.Write(ctx, &agentrewire.PortForwardWriteRequest{StreamId: "ghost", Data: []byte("x")})
	assert.Equal(t, int32(rpcerror.CodePortForwardStreamNotFound), code(t, err))
	_, err = streams.Close(ctx, &agentrewire.PortForwardCloseRequest{StreamId: "ghost"})
	assert.Equal(t, int32(rpcerror.CodePortForwardStreamNotFound), code(t, err))
	_, err = streams.Ack(ctx, &agentrewire.PortForwardAckRequest{StreamId: "ghost"})
	assert.Equal(t, int32(rpcerror.CodePortForwardStreamNotFound), code(t, err))
}

// 分片与窗口都必须远低于载荷硬顶:超限拆掉的是整条物理连接,那台机器上所有会话一起重连。
func TestStreamBudget_GivenTheWireLimits_ThenChunksLeaveRoomUnderTheHardCap(t *testing.T) {
	assert.Less(t, wirelimits.PortForwardChunkBytes, wirelimits.MaxPayloadBytes/8)
	assert.Less(t, wirelimits.PortForwardChunkBytes, wirelimits.PortForwardWindowBytes)
}

// ---------- 目标 4:1xx 是中间应答,不是最终应答 ----------

// Given 一个带 `Expect: 100-continue` 的 POST(curl 对超过 1KB 的请求体自动加这一格,
// 浏览器大上传同理),When 上游先回一条 `100 Continue` 再回真正的 `201`,Then 宿主
// 只收到**一条**响应头通知,而且它是那个 201 —— 连同它的响应头。
//
// 中间应答被当成最终应答的话,这条流会在读完那条空的 100 之后立刻以 eof 收尾:
// 上游真正的状态码、响应头与正文一个都到不了宿主,而不带 Expect 的同一个请求却是好的。
func TestStreamOpen_GivenExpect100Continue_WhenUpstreamSendsAnInterimResponse_ThenOnlyTheFinalResponseIsReported(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 读请求体这一步正是 net/http 发出 `100 Continue` 的时机。
		n, err := io.Copy(io.Discard, r.Body)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.Header().Set("X-Echo-Bytes", fmt.Sprint(n))
		w.WriteHeader(http.StatusCreated)
		_, _ = io.WriteString(w, "stored")
	}))
	t.Cleanup(server.Close)
	port := serverPort(t, server)

	body := patternBody(64 << 10)
	rec := newRecorder()
	streams := newStreams(t, rec.notify, DialLoopback, port)
	ctx := context.Background()

	_, err := streams.Open(ctx, &agentrewire.PortForwardOpenRequest{
		StreamId: "s1", Port: uint32(port), Method: http.MethodPost, Path: "/upload", HasBody: true,
		Headers: map[string]*agentrewire.HeaderValues{
			"Content-Length": {Values: []string{fmt.Sprint(len(body))}},
			"Expect":         {Values: []string{"100-continue"}},
		},
	})
	require.NoError(t, err)

	_, err = streams.Write(ctx, &agentrewire.PortForwardWriteRequest{StreamId: "s1", Data: body})
	require.NoError(t, err)
	_, err = streams.Write(ctx, &agentrewire.PortForwardWriteRequest{StreamId: "s1", Eof: true})
	require.NoError(t, err)

	closed := rec.waitClosed(t)
	assert.Equal(t, "eof", closed.GetReason())

	var heads []*agentrewire.PortForwardResponseNotification
	for _, event := range rec.snapshot() {
		if event.kind == "head" {
			heads = append(heads, event.head)
		}
	}
	require.Len(t, heads, 1, "一条流只该有一条响应头通知:100 Continue 是中间应答,不该占掉它")
	assert.EqualValues(t, http.StatusCreated, heads[0].GetStatus(),
		"宿主必须拿到上游真正的最终状态码,而不是那条中间的 100")
	assert.Equal(t, []string{fmt.Sprint(len(body))}, heads[0].GetHeaders()["X-Echo-Bytes"].GetValues(),
		"最终应答的响应头也必须一并到达")
	assert.False(t, heads[0].GetUpgraded())
	assert.Equal(t, "stored", string(rec.dataBytes()))
}

// interimFlood 起一个裸 TCP 监听:读完请求头就没完没了地回 `100 Continue`,一条最终
// 应答都不给。用真 socket 是因为 net/http 的服务端根本写不出这种应答。
func interimFlood(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = listener.Close() })

	go func() {
		conn, acceptErr := listener.Accept()
		if acceptErr != nil {
			return
		}
		defer func() { _ = conn.Close() }()
		for {
			if _, writeErr := io.WriteString(conn, "HTTP/1.1 100 Continue\r\n\r\n"); writeErr != nil {
				return
			}
		}
	}()

	return listenerPort(t, listener.Addr().String())
}

// Given 一个只会回 `100 Continue` 的本机服务,When 设备等它的最终应答,Then 这条流在
// 有限条中间应答之后以 upstream_error 收尾,而不是把这个 goroutine 永远拴在那里。
//
// 跳过中间应答是个循环,而它读的是一个不受本进程控制的 socket:没有上界的话,一个
// 坏掉(或有意为之)的本机服务就能让 agentred 里堆起永远退不出的 goroutine。
func TestStreamOpen_GivenAnUpstreamThatOnlySendsInterimResponses_ThenTheStreamGivesUp(t *testing.T) {
	port := interimFlood(t)

	rec := newRecorder()
	streams := newStreams(t, rec.notify, DialLoopback, port)

	_, err := streams.Open(context.Background(), &agentrewire.PortForwardOpenRequest{
		StreamId: "s1", Port: uint32(port), Method: http.MethodGet, Path: "/",
	})
	require.NoError(t, err)

	closed := rec.waitClosed(t)
	assert.Equal(t, "upstream_error", closed.GetReason())
	for _, event := range rec.snapshot() {
		assert.NotEqual(t, "head", event.kind, "一条中间应答都不该被当成响应头发出去")
	}
}
