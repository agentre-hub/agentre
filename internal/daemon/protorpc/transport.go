package protorpc

import (
	"errors"
	"fmt"
	"sync"

	"github.com/gorilla/websocket"
)

const Subprotocol = "agentre-protobuf"
const MaxFrameBytes int64 = 16 << 20

var ErrNonBinaryFrame = errors.New("protorpc: websocket frame is not binary")

type websocketMessages interface {
	ReadMessage() (int, []byte, error)
	WriteMessage(int, []byte) error
	Close() error
}

type websocketFrameConn struct {
	conn websocketMessages
	done chan struct{}
	once sync.Once
}

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
	if limiter, ok := conn.(interface{ SetReadLimit(int64) }); ok {
		limiter.SetReadLimit(MaxFrameBytes)
	}
	return &websocketFrameConn{conn: conn, done: make(chan struct{})}
}
func (c *websocketFrameConn) ReadFrame() ([]byte, error) {
	kind, payload, err := c.conn.ReadMessage()
	if err != nil {
		c.markDone()
		return nil, err
	}
	if kind != websocket.BinaryMessage {
		return nil, fmt.Errorf("%w: message type %d", ErrNonBinaryFrame, kind)
	}
	return payload, nil
}
func (c *websocketFrameConn) WriteFrame(payload []byte) error {
	return c.conn.WriteMessage(websocket.BinaryMessage, payload)
}
func (c *websocketFrameConn) Close() error          { err := c.conn.Close(); c.markDone(); return err }
func (c *websocketFrameConn) Done() <-chan struct{} { return c.done }
func (c *websocketFrameConn) markDone()             { c.once.Do(func() { close(c.done) }) }
