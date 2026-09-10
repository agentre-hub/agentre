package protorpc

import (
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

const Subprotocol = "agentre-protobuf"

// MaxFrameBytes 是一条 RPC **载荷**的上限,整条链路共用这一个数:直连的这一条、
// 服务端中继的两个端点(agentre-server 的 relayws.MaxPayloadBytes)、以及 daemon
// 的中继链路(那一条另加一个信封头)。
//
// 三处曾经不同源(这里与中继链路 16 MiB、服务端 10 MiB),后果不是「大一点的请求
// 失败了」:超限时 gorilla 回 1009 并让读循环出错,于是**整条物理连接**被拆掉,
// 而中继链路上跑着那台机器的全部虚拟通道,所有会话一起断线重连。取小的那个数。
const MaxFrameBytes int64 = 10 << 20

var ErrNonBinaryFrame = errors.New("protorpc: websocket frame is not binary")

// defaultPingInterval / defaultWriteTimeout 是这条 WebSocket 自己的活性预算。
//
// 半开连接(Wi-Fi 掉了、NAT 回收了映射、笔记本合盖)不会给任何一端 FIN:读永远
// 不返回,Done() 不触发,重连状态机因此根本不启动,在飞的调用方全挂着。TCP
// keepalive 兜得住「对面机器没了」,兜不住「对面进程还活着但卡死了」——那种情况
// 内核照样回 ACK。所以活性判断必须由本层自己做。
//
// 读期限取 2 倍心跳间隔:丢一个 ping 不算断线,连丢两个才算。这两个数与中继链路
// (relaytransport/hub.go)一致 —— 同一条物理链路上两套不同的判活节奏只会让排障
// 时对不上账。
//
// 写期限兜的是另一半:对端不再读时发送缓冲会填满,没有期限的 WriteMessage 就永久
// 阻塞,而它持着连接的写锁 —— 这条连接上其余会话的应答、通知、cancel 一起堵死。
// 30 秒远长于任何正常一帧(上限 10 MiB)的发送时间,又保证卡死一定会被判成断线。
const (
	defaultPingInterval = 15 * time.Second
	defaultWriteTimeout = 30 * time.Second
)

type websocketMessages interface {
	ReadMessage() (int, []byte, error)
	WriteMessage(int, []byte) error
	Close() error
}

type websocketFrameConn struct {
	conn         websocketMessages
	done         chan struct{}
	once         sync.Once
	readTimeout  time.Duration
	writeTimeout time.Duration
}

// websocketKeepalive 是保活要用到的那几个方法。gorilla 的 *websocket.Conn 全都有;
// 单元测试里的替身可以只实现读写,那时本层退化成没有保活的老行为(与既有测试兼容)。
type websocketKeepalive interface {
	SetReadDeadline(time.Time) error
	SetPongHandler(func(string) error)
	WriteControl(int, []byte, time.Time) error
}

type websocketWriteDeadline interface{ SetWriteDeadline(time.Time) error }

type PayloadChannel interface {
	ReadPayload() ([]byte, error)
	WritePayload([]byte) error
	Close() error
	Done() <-chan struct{}
}

type payloadFrameConn struct{ channel PayloadChannel }

func NewPayloadFrameConn(channel PayloadChannel) FrameConn {
	return &payloadFrameConn{channel: channel}
}
func (c *payloadFrameConn) ReadFrame() ([]byte, error)      { return c.channel.ReadPayload() }
func (c *payloadFrameConn) WriteFrame(payload []byte) error { return c.channel.WritePayload(payload) }
func (c *payloadFrameConn) Close() error                    { return c.channel.Close() }
func (c *payloadFrameConn) Done() <-chan struct{}           { return c.channel.Done() }

func NewWebSocketFrameConn(conn *websocket.Conn) FrameConn { return newWebSocketFrameConn(conn) }
func newWebSocketFrameConn(conn websocketMessages) *websocketFrameConn {
	return newWebSocketFrameConnWith(conn, defaultPingInterval, defaultWriteTimeout)
}

func newWebSocketFrameConnWith(conn websocketMessages, pingInterval, writeTimeout time.Duration) *websocketFrameConn {
	if limiter, ok := conn.(interface{ SetReadLimit(int64) }); ok {
		limiter.SetReadLimit(MaxFrameBytes)
	}
	c := &websocketFrameConn{
		conn:         conn,
		done:         make(chan struct{}),
		readTimeout:  2 * pingInterval,
		writeTimeout: writeTimeout,
	}
	if keepalive, ok := conn.(websocketKeepalive); ok && pingInterval > 0 {
		_ = c.extendReadDeadline()
		// pong 与「收到任何一帧」是对端还活着的两种等价证据,都续期。
		keepalive.SetPongHandler(func(string) error { return c.extendReadDeadline() })
		go c.ping(keepalive, pingInterval)
	}
	return c
}

// ping 按间隔发心跳。它写的是控制帧(gorilla 允许它与 WriteMessage 并发),所以一次
// 卡住的数据写不会连心跳一起堵掉。
func (c *websocketFrameConn) ping(keepalive websocketKeepalive, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-c.done:
			return
		case <-ticker.C:
			if err := keepalive.WriteControl(websocket.PingMessage, nil, time.Now().Add(interval)); err != nil {
				// 连 ping 都发不出去 = 这条连接完了,就地收口叫醒等待方。
				_ = c.Close()
				return
			}
		}
	}
}

func (c *websocketFrameConn) extendReadDeadline() error {
	keepalive, ok := c.conn.(websocketKeepalive)
	if !ok || c.readTimeout <= 0 {
		return nil
	}
	return keepalive.SetReadDeadline(time.Now().Add(c.readTimeout))
}

func (c *websocketFrameConn) ReadFrame() ([]byte, error) {
	kind, payload, err := c.conn.ReadMessage()
	if err != nil {
		c.markDone()
		return nil, err
	}
	_ = c.extendReadDeadline()
	if kind != websocket.BinaryMessage {
		return nil, fmt.Errorf("%w: message type %d", ErrNonBinaryFrame, kind)
	}
	return payload, nil
}
func (c *websocketFrameConn) WriteFrame(payload []byte) error {
	if deadliner, ok := c.conn.(websocketWriteDeadline); ok && c.writeTimeout > 0 {
		if err := deadliner.SetWriteDeadline(time.Now().Add(c.writeTimeout)); err != nil {
			return err
		}
	}
	if err := c.conn.WriteMessage(websocket.BinaryMessage, payload); err != nil {
		// 写失败(期限到点就是这个形态)说明这条连接已经不可用。等读循环自己发现
		// 要多等一个读期限,而在飞的调用方全挂在 Done() 上 —— 就地收口。
		_ = c.Close()
		return err
	}
	return nil
}
func (c *websocketFrameConn) Close() error          { err := c.conn.Close(); c.markDone(); return err }
func (c *websocketFrameConn) Done() <-chan struct{} { return c.done }
func (c *websocketFrameConn) markDone()             { c.once.Do(func() { close(c.done) }) }
