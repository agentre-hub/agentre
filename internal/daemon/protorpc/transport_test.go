package protorpc

import (
	"errors"
	"testing"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/require"
)

type messageConn struct {
	reads     []message
	writes    []message
	closed    bool
	readLimit int64
}
type message struct {
	kind int
	data []byte
}

func (c *messageConn) ReadMessage() (int, []byte, error) {
	if len(c.reads) == 0 {
		return 0, nil, errors.New("done")
	}
	m := c.reads[0]
	c.reads = c.reads[1:]
	return m.kind, m.data, nil
}
func (c *messageConn) WriteMessage(kind int, data []byte) error {
	c.writes = append(c.writes, message{kind: kind, data: append([]byte(nil), data...)})
	return nil
}
func (c *messageConn) Close() error             { c.closed = true; return nil }
func (c *messageConn) SetReadLimit(limit int64) { c.readLimit = limit }

func TestWebSocketFrameConnUsesBinaryMessages(t *testing.T) {
	ws := &messageConn{reads: []message{{kind: websocket.BinaryMessage, data: []byte{0, 255}}}}
	conn := newWebSocketFrameConn(ws)
	require.Equal(t, MaxFrameBytes, ws.readLimit)
	require.NoError(t, conn.WriteFrame([]byte{1, 2}))
	require.Equal(t, websocket.BinaryMessage, ws.writes[0].kind)
	require.Equal(t, []byte{1, 2}, ws.writes[0].data)
	got, err := conn.ReadFrame()
	require.NoError(t, err)
	require.Equal(t, []byte{0, 255}, got)
}

func TestWebSocketFrameConnRejectsTextFrames(t *testing.T) {
	ws := &messageConn{reads: []message{{kind: websocket.TextMessage, data: []byte("{}")}}}
	conn := newWebSocketFrameConn(ws)
	_, err := conn.ReadFrame()
	require.ErrorIs(t, err, ErrNonBinaryFrame)
}
