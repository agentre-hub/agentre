package protorpc

import (
	"crypto/tls"
	"io"
	"net"
	"sync"
	"time"
)

// tlsHandshakeRecord is the first byte of every TLS connection (the record
// type of the ClientHello). No HTTP request line starts with it, so one byte
// is enough to tell a wss client from a ws client on the same port.
const tlsHandshakeRecord = 0x16

// protocolSniffListener hands http.Server plain connections and TLS
// connections from one TCP listener. A TLS connection is returned as a
// *tls.Conn so http.Server performs the handshake under its own timeouts and
// fills Request.TLS exactly as ServeTLS would.
//
// Reading the first byte happens off the Accept path: a client that connects
// and sends nothing only holds its own goroutine until sniffTimeout, never the
// clients behind it.
type protocolSniffListener struct {
	net.Listener
	tlsConfig    *tls.Config
	sniffTimeout time.Duration
	conns        chan net.Conn
	errs         chan error
	done         chan struct{}
	closeOnce    sync.Once
	mu           sync.Mutex
	// pending holds connections whose first byte has not arrived, so Close can
	// release them instead of leaving them open until sniffTimeout. nil once
	// the listener is closed.
	pending map[net.Conn]struct{}
}

func newProtocolSniffListener(inner net.Listener, tlsConfig *tls.Config, sniffTimeout time.Duration) *protocolSniffListener {
	listener := &protocolSniffListener{
		Listener:     inner,
		tlsConfig:    tlsConfig,
		sniffTimeout: sniffTimeout,
		conns:        make(chan net.Conn),
		errs:         make(chan error),
		done:         make(chan struct{}),
		pending:      map[net.Conn]struct{}{},
	}
	go listener.acceptLoop()
	return listener
}

func (l *protocolSniffListener) Accept() (net.Conn, error) {
	select {
	case conn := <-l.conns:
		return conn, nil
	case err := <-l.errs:
		return nil, err
	case <-l.done:
		return nil, net.ErrClosed
	}
}

func (l *protocolSniffListener) Close() error {
	err := net.ErrClosed
	l.closeOnce.Do(func() {
		close(l.done)
		err = l.Listener.Close()
		l.mu.Lock()
		for conn := range l.pending {
			_ = conn.Close()
		}
		l.pending = nil
		l.mu.Unlock()
	})
	return err
}

func (l *protocolSniffListener) acceptLoop() {
	for {
		conn, err := l.Listener.Accept()
		if err != nil {
			// The caller of Accept decides whether the error is worth a retry
			// (http.Server backs off on temporary errors); when it gives up it
			// closes this listener, which is what ends the loop. The unbuffered
			// channel keeps a failing Accept from spinning while nobody reads.
			select {
			case l.errs <- err:
				continue
			case <-l.done:
				return
			}
		}
		if !l.track(conn) {
			_ = conn.Close()
			return
		}
		go l.classify(conn)
	}
}

func (l *protocolSniffListener) classify(conn net.Conn) {
	first := make([]byte, 1)
	_ = conn.SetReadDeadline(time.Now().Add(l.sniffTimeout))
	_, err := io.ReadFull(conn, first)
	_ = conn.SetReadDeadline(time.Time{})
	l.untrack(conn)
	if err != nil {
		_ = conn.Close()
		return
	}
	var accepted net.Conn = &replayConn{Conn: conn, prefix: first}
	if first[0] == tlsHandshakeRecord {
		accepted = tls.Server(accepted, l.tlsConfig)
	}
	select {
	case l.conns <- accepted:
	case <-l.done:
		_ = accepted.Close()
	}
}

func (l *protocolSniffListener) track(conn net.Conn) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.pending == nil {
		return false
	}
	l.pending[conn] = struct{}{}
	return true
}

func (l *protocolSniffListener) untrack(conn net.Conn) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.pending, conn)
}

// replayConn gives back the byte consumed to classify the connection before
// reading on from the socket.
type replayConn struct {
	net.Conn
	prefix []byte
}

func (c *replayConn) Read(p []byte) (int, error) {
	if len(c.prefix) > 0 {
		n := copy(p, c.prefix)
		c.prefix = c.prefix[n:]
		return n, nil
	}
	return c.Conn.Read(p)
}
