package protorpc

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/require"
)

// keepaliveConnStub 实现 websocket 连接里与保活有关的那几个方法(gorilla 的
// *websocket.Conn 全都有),用来观察本层设了哪些期限、发了哪些控制帧。
type keepaliveConnStub struct {
	mu            sync.Mutex
	reads         chan message
	writes        []message
	controls      []message
	readDeadlines []time.Time
	writeDeadline []time.Time
	writeErr      error
	pong          func(string) error
	closed        bool
}

func newKeepaliveConnStub() *keepaliveConnStub {
	return &keepaliveConnStub{reads: make(chan message, 8)}
}

func (c *keepaliveConnStub) ReadMessage() (int, []byte, error) {
	m, ok := <-c.reads
	if !ok {
		return 0, nil, errors.New("closed")
	}
	return m.kind, m.data, nil
}

func (c *keepaliveConnStub) WriteMessage(kind int, data []byte) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.writeErr != nil {
		return c.writeErr
	}
	c.writes = append(c.writes, message{kind: kind, data: append([]byte(nil), data...)})
	return nil
}

func (c *keepaliveConnStub) WriteControl(kind int, data []byte, _ time.Time) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.controls = append(c.controls, message{kind: kind, data: append([]byte(nil), data...)})
	return nil
}

func (c *keepaliveConnStub) SetReadDeadline(t time.Time) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.readDeadlines = append(c.readDeadlines, t)
	return nil
}

func (c *keepaliveConnStub) SetWriteDeadline(t time.Time) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.writeDeadline = append(c.writeDeadline, t)
	return nil
}

func (c *keepaliveConnStub) SetPongHandler(h func(string) error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.pong = h
}

func (c *keepaliveConnStub) SetReadLimit(int64) {}

func (c *keepaliveConnStub) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.closed = true
	return nil
}

func (c *keepaliveConnStub) snapshot() (writes, controls []message, readDeadlines, writeDeadlines []time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]message(nil), c.writes...), append([]message(nil), c.controls...),
		append([]time.Time(nil), c.readDeadlines...), append([]time.Time(nil), c.writeDeadline...)
}

func (c *keepaliveConnStub) firePong() error {
	c.mu.Lock()
	h := c.pong
	c.mu.Unlock()
	if h == nil {
		return errors.New("no pong handler installed")
	}
	return h("")
}

// 一个不再读的对端(合盖、进程卡死)会把发送缓冲填满,而 WriteMessage 没有期限时
// 就永久阻塞 —— 它持着连接的写锁,这条连接上其余会话的应答、通知和 cancel 一起堵死。
// Given 一条支持期限的 websocket,When 写一帧,Then 必须先给这次写压上期限。
func TestWebSocketFrameConn_GivenAConnThatSupportsDeadlines_WhenWritingAFrame_ThenTheWriteIsBounded(t *testing.T) {
	ws := newKeepaliveConnStub()
	conn := newWebSocketFrameConn(ws)
	defer func() { _ = conn.Close() }()

	require.NoError(t, conn.WriteFrame([]byte{1}))

	_, _, _, writeDeadlines := ws.snapshot()
	require.Len(t, writeDeadlines, 1)
	require.WithinDuration(t, time.Now().Add(defaultWriteTimeout), writeDeadlines[0], 2*time.Second)
}

// 写失败(期限到点就是这个形态)说明这条连接已经不可用了。等读循环自己发现要等到
// 读期限到点,而在飞的调用方全都挂在 transport.Done() 上 —— 就地收口,让它们立刻醒。
func TestWebSocketFrameConn_GivenAWriteThatFails_WhenWritingAFrame_ThenTheConnectionIsFinishedAtOnce(t *testing.T) {
	ws := newKeepaliveConnStub()
	ws.writeErr = errors.New("i/o timeout")
	conn := newWebSocketFrameConn(ws)

	require.Error(t, conn.WriteFrame([]byte{1}))

	select {
	case <-conn.Done():
	case <-time.After(time.Second):
		t.Fatal("a failed write must finish the connection so callers waiting on Done wake up")
	}
}

// 半开连接(Wi-Fi 掉了、NAT 回收了映射)下 ReadFrame 永远不返回:Done() 不触发,
// 重连状态机不启动,在飞的调用方永远等下去。Given 一条 websocket,When 建立连接,
// Then 本层必须自己发心跳并给读设期限,而不是指望对端或 TCP keepalive。
func TestWebSocketFrameConn_GivenAQuietConnection_WhenTheKeepaliveIntervalPasses_ThenItPingsAndBoundsReads(t *testing.T) {
	ws := newKeepaliveConnStub()
	conn := newWebSocketFrameConnWith(ws, 40*time.Millisecond, time.Second)
	defer func() { _ = conn.Close() }()

	_, _, readDeadlines, _ := ws.snapshot()
	require.NotEmpty(t, readDeadlines, "建立连接时就要设读期限,不能等第一条帧")
	require.WithinDuration(t, time.Now().Add(80*time.Millisecond), readDeadlines[0], time.Second)

	require.Eventually(t, func() bool {
		_, controls, _, _ := ws.snapshot()
		return len(controls) >= 2
	}, 2*time.Second, 10*time.Millisecond, "静默的连接必须按心跳间隔持续发 ping")
	_, controls, _, _ := ws.snapshot()
	require.Equal(t, websocket.PingMessage, controls[0].kind)
}

// 对端还活着的两种证据都要顶用:它答了 pong,或者它发来了任何一帧。
func TestWebSocketFrameConn_GivenPeerActivity_WhenAPongOrAFrameArrives_ThenTheReadDeadlineIsExtended(t *testing.T) {
	ws := newKeepaliveConnStub()
	conn := newWebSocketFrameConnWith(ws, time.Hour, time.Second) // 不让 ping 循环干扰计数
	defer func() { _ = conn.Close() }()
	_, _, initial, _ := ws.snapshot()
	require.Len(t, initial, 1)

	require.NoError(t, ws.firePong())
	_, _, afterPong, _ := ws.snapshot()
	require.Len(t, afterPong, 2, "pong 必须续期")

	ws.reads <- message{kind: websocket.BinaryMessage, data: []byte{9}}
	got, err := conn.ReadFrame()
	require.NoError(t, err)
	require.Equal(t, []byte{9}, got)
	_, _, afterRead, _ := ws.snapshot()
	require.Len(t, afterRead, 3, "收到一帧同样是活着的证据,必须续期")
}

// 上面几条用替身钉的是「本层发了什么」。这一条用**真的 websocket** 钉住那件真正要命
// 的事:读期限是本次改动新加的,ping/pong 只要有一环不成立,每一条空闲连接都会在
// 读期限到点时被自己杀掉 —— 一个比原 bug 更严重的回归。
//
// Given 一条真连接与一个只做常规读、靠 gorilla 默认 ping handler 自动回 pong 的对端,
// When 连接空闲超过好几个读期限,Then 它必须还活着。
func TestWebSocketFrameConn_GivenARealIdleConnection_WhenSeveralReadDeadlinesPass_ThenItStaysAlive(t *testing.T) {
	upgrader := websocket.Upgrader{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		peer, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer func() { _ = peer.Close() }()
		for { // 只读:gorilla 的默认 ping handler 会自动回 pong
			if _, _, err := peer.ReadMessage(); err != nil {
				return
			}
		}
	}))
	defer server.Close()

	ws, resp, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http"), nil)
	require.NoError(t, err)
	if resp != nil && resp.Body != nil {
		_ = resp.Body.Close()
	}
	// 心跳 60ms → 读期限 120ms;空闲 600ms = 五个读期限。
	conn := newWebSocketFrameConnWith(ws, 60*time.Millisecond, time.Second)
	defer func() { _ = conn.Close() }()

	readErr := make(chan error, 1)
	go func() { _, err := conn.ReadFrame(); readErr <- err }()

	select {
	case err := <-readErr:
		t.Fatalf("空闲连接被自己的读期限杀掉了: %v", err)
	case <-time.After(600 * time.Millisecond):
	}
	require.NoError(t, conn.WriteFrame([]byte{1}), "连接仍然可写")
}
