package rpc

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// Subprotocol is the canonical WebSocket subprotocol advertised by the
// daemon and matched by the client.
const Subprotocol = "agentred-jsonrpc.v1"

// websocketFrameConn adapts the LAN WebSocket transport to FrameConn.
type websocketFrameConn struct {
	conn      *websocket.Conn
	done      chan struct{}
	closeOnce sync.Once
}

// NewWebSocketFrameConn returns the FrameConn implementation for a LAN
// WebSocket connection.
func NewWebSocketFrameConn(conn *websocket.Conn) FrameConn {
	return &websocketFrameConn{conn: conn, done: make(chan struct{})}
}

func (c *websocketFrameConn) WriteFrame(f Frame) error { return c.conn.WriteJSON(f) }

func (c *websocketFrameConn) ReadFrame(f *Frame) error {
	err := c.conn.ReadJSON(f)
	if err != nil {
		c.markDone()
	}
	return err
}

func (c *websocketFrameConn) Close() error {
	err := c.conn.Close()
	c.markDone()
	return err
}

func (c *websocketFrameConn) Done() <-chan struct{} { return c.done }

func (c *websocketFrameConn) markDone() { c.closeOnce.Do(func() { close(c.done) }) }

// frameConnFor keeps existing direct WebSocket callers working while all new
// Conn construction crosses the FrameConn boundary.
func frameConnFor(transport any) FrameConn {
	switch conn := transport.(type) {
	case nil:
		return newDisconnectedFrameConn()
	case FrameConn:
		return conn
	case *websocket.Conn:
		return NewWebSocketFrameConn(conn)
	default:
		panic(fmt.Sprintf("rpc: unsupported frame transport %T", transport))
	}
}

// LANOpts configures the LAN-mode transport.
type LANOpts struct {
	Host        string
	Port        int
	TLSCertFile string
	TLSKeyFile  string
	// Registry contains immutable/bootstrap handlers. The LAN accept path
	// snapshots it into a private registry for each connection.
	Registry *Registry
	// OnConn is invoked with that private registry so daemon.go can attach
	// connection-owned handlers before Serve starts.
	OnConn func(*Conn)
}

// LANServer accepts WebSocket connections at /rpc and runs one *Conn per
// peer through a private snapshot of the bootstrap registry.
type LANServer struct {
	opts LANOpts

	mu       sync.Mutex
	listener net.Listener
	srv      *http.Server
}

// NewLANServer creates a new LANServer with the given options.
func NewLANServer(opts LANOpts) *LANServer { return &LANServer{opts: opts} }

// Run starts the server and blocks until ctx is canceled or a fatal error
// occurs. It returns nil if the server was shut down cleanly.
func (s *LANServer) Run(ctx context.Context) error {
	if (s.opts.TLSCertFile == "") != (s.opts.TLSKeyFile == "") {
		return fmt.Errorf("tls: both --tls-cert and --tls-key must be set or neither")
	}
	addr := fmt.Sprintf("%s:%d", s.opts.Host, s.opts.Port)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.listener = ln
	s.mu.Unlock()

	mux := http.NewServeMux()
	upgrader := websocket.Upgrader{
		Subprotocols: []string{Subprotocol},
		CheckOrigin:  func(r *http.Request) bool { return true },
	}
	mux.HandleFunc("/rpc", func(w http.ResponseWriter, r *http.Request) {
		ws, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		c := NewConn(NewWebSocketFrameConn(ws), s.opts.Registry.Clone())
		if s.opts.OnConn != nil {
			s.opts.OnConn(c)
		}
		// 用 Run(ctx) 的 daemon 主 ctx，不用 r.Context() —— 后者在 hijack
		// 后 handler 一返回就被 net/http cancel，从而让 Serve 派发的所有
		// chat.start 等 RPC handler 一开 ctx 就是 context.Canceled。
		go c.Serve(ctx)
	})
	s.srv = &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}
	//nolint:gosec // G118: shutdown must use a fresh ctx since the request ctx is already canceled
	go func() {
		<-ctx.Done()
		_ = s.srv.Shutdown(context.Background())
	}()
	if s.opts.TLSCertFile != "" {
		// Validate the cert/key pair early for clean errors.
		if _, err := tls.LoadX509KeyPair(s.opts.TLSCertFile, s.opts.TLSKeyFile); err != nil {
			return fmt.Errorf("tls: %w", err)
		}
		err = s.srv.ServeTLS(ln, s.opts.TLSCertFile, s.opts.TLSKeyFile)
	} else {
		err = s.srv.Serve(ln)
	}
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

// Addr returns the bound "host:port" after Run starts listening.
func (s *LANServer) Addr() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.listener == nil {
		return ""
	}
	return s.listener.Addr().String()
}

// URL returns the ws[s]:// endpoint of the bound address itself. It is what a
// caller on this same host dials; it is NOT what a peer should be handed —
// a wildcard bind reports back as "[::]:7456" here. Use AdvertiseURLs for that.
func (s *LANServer) URL() string {
	return s.urlFor(s.Addr())
}

// AdvertiseURLs returns the ws[s]://host:port/rpc endpoints a desktop elsewhere
// on the LAN can actually dial.
//
// The daemon listens on a wildcard address by default (0.0.0.0, see
// cmd/agentred/run.go), and Go serves a wildcard bind dual-stack, so
// listener.Addr() reports "[::]:7456". Handing that to a peer is worse than
// useless: "[::]" resolves on the *peer's* box, so the desktop ends up dialing
// itself and reports a connection error that says nothing about the address
// being wrong. A wildcard bind is therefore expanded into this host's own
// routable addresses, while an explicit --host bind is passed through untouched
// (the operator may be naming a forwarding entrypoint we have no business
// rewriting).
//
// When nothing routable turns up the result is empty **on purpose** — the caller
// must tell the user to re-run with --host rather than quietly advertise the
// bind address that started this whole problem.
func (s *LANServer) AdvertiseURLs() []string {
	host, port, err := net.SplitHostPort(s.Addr())
	if err != nil {
		return nil
	}
	if !isWildcardHost(host) {
		return []string{s.urlFor(net.JoinHostPort(host, port))}
	}
	hosts := routableHosts(localInterfaces())
	urls := make([]string, 0, len(hosts))
	for _, h := range hosts {
		urls = append(urls, s.urlFor(net.JoinHostPort(h, port)))
	}
	return urls
}

func (s *LANServer) urlFor(hostPort string) string {
	scheme := "ws"
	if s.opts.TLSCertFile != "" {
		scheme = "wss"
	}
	return fmt.Sprintf("%s://%s/rpc", scheme, hostPort)
}

// isWildcardHost reports whether the bind host names every local address rather
// than one of them — "", "0.0.0.0" and "::" all land here.
func isWildcardHost(host string) bool {
	if host == "" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsUnspecified()
}

// lanInterface is the slice of a net.Interface that address selection needs.
// net.Interface.Addrs() is a method that hits the OS, so the filtering rules
// only stay testable if they take the flags and the addresses as plain data.
type lanInterface struct {
	flags net.Flags
	addrs []net.Addr
}

// localInterfaces snapshots this host's interfaces. An interface whose
// addresses cannot be read is skipped rather than failing the whole lookup —
// one unreadable interface must not cost the user every other address.
func localInterfaces() []lanInterface {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil
	}
	out := make([]lanInterface, 0, len(ifaces))
	for _, iface := range ifaces {
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		out = append(out, lanInterface{flags: iface.Flags, addrs: addrs})
	}
	return out
}

// routableHosts picks the addresses a peer on the LAN can dial back on: down
// and loopback interfaces are out, so are loopback / link-local / multicast /
// unspecified addresses. Private RFC 1918 and ULA addresses stay — on a LAN
// they are the normal case. IPv4 comes first because that is the entry users
// paste, and duplicates (the same address on several interfaces) collapse.
func routableHosts(ifaces []lanInterface) []string {
	var v4, v6 []string
	seen := make(map[string]bool)
	for _, iface := range ifaces {
		if iface.flags&net.FlagUp == 0 || iface.flags&net.FlagLoopback != 0 {
			continue
		}
		for _, addr := range iface.addrs {
			ip := addrIP(addr)
			if !isRoutableIP(ip) || seen[ip.String()] {
				continue
			}
			seen[ip.String()] = true
			if ip.To4() != nil {
				v4 = append(v4, ip.String())
			} else {
				v6 = append(v6, ip.String())
			}
		}
	}
	return append(v4, v6...)
}

func addrIP(addr net.Addr) net.IP {
	switch a := addr.(type) {
	case *net.IPNet:
		return a.IP
	case *net.IPAddr:
		return a.IP
	default:
		return nil
	}
}

func isRoutableIP(ip net.IP) bool {
	if ip == nil || ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() {
		return false
	}
	// IsGlobalUnicast additionally rules out the unspecified and multicast
	// addresses while keeping the private ranges a LAN actually runs on.
	return ip.IsGlobalUnicast()
}
