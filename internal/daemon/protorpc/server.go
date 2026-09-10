package protorpc

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

type LANOpts struct {
	Host, TLSCertFile, TLSKeyFile string
	Port                          int
	Registry                      *Registry
	OnConn                        func(*Conn)
}

type LANServer struct {
	opts     LANOpts
	mu       sync.Mutex
	listener net.Listener
	server   *http.Server
}

func NewLANServer(opts LANOpts) *LANServer { return &LANServer{opts: opts} }

func (s *LANServer) Run(ctx context.Context) error {
	if (s.opts.TLSCertFile == "") != (s.opts.TLSKeyFile == "") {
		return errors.New("tls: both certificate and key are required")
	}
	listener, err := net.Listen("tcp", fmt.Sprintf("%s:%d", s.opts.Host, s.opts.Port))
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.listener = listener
	s.mu.Unlock()
	upgrader := websocket.Upgrader{Subprotocols: []string{Subprotocol}, CheckOrigin: func(*http.Request) bool { return true }}
	mux := http.NewServeMux()
	mux.HandleFunc("/rpc", func(writer http.ResponseWriter, request *http.Request) {
		matched := false
		for _, offered := range strings.Split(request.Header.Get("Sec-WebSocket-Protocol"), ",") {
			if strings.TrimSpace(offered) == Subprotocol {
				matched = true
				break
			}
		}
		if !matched {
			// The body is the only thing a caller that cannot negotiate the
			// subprotocol ever sees, so it names the protocol and the remedy:
			// on the desktop side this is folded into
			// client.ErrPeerProtocolUnsupported.
			http.Error(writer, "this endpoint speaks only the \""+Subprotocol+
				"\" WebSocket subprotocol; upgrade agentred and agentre to the same release so both ends speak it",
				http.StatusUpgradeRequired)
			return
		}
		ws, upgradeErr := upgrader.Upgrade(writer, request, nil)
		if upgradeErr != nil {
			return
		}
		registry := s.opts.Registry
		if registry == nil {
			registry = NewRegistry()
		}
		conn := NewConn(NewWebSocketFrameConn(ws), registry.Clone())
		if s.opts.OnConn != nil {
			s.opts.OnConn(conn)
		}
		go conn.Serve(ctx)
	})
	s.server = &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer cancel()
		_ = s.server.Shutdown(shutdownCtx)
	}()
	if s.opts.TLSCertFile != "" {
		if _, err := tls.LoadX509KeyPair(s.opts.TLSCertFile, s.opts.TLSKeyFile); err != nil {
			return fmt.Errorf("tls: %w", err)
		}
		err = s.server.ServeTLS(listener, s.opts.TLSCertFile, s.opts.TLSKeyFile)
	} else {
		err = s.server.Serve(listener)
	}
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

func (s *LANServer) Addr() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.listener == nil {
		return ""
	}
	return s.listener.Addr().String()
}
func (s *LANServer) URL() string {
	scheme := "ws"
	if s.opts.TLSCertFile != "" {
		scheme = "wss"
	}
	return fmt.Sprintf("%s://%s/rpc", scheme, s.Addr())
}

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
	for _, current := range hosts {
		urls = append(urls, s.urlFor(net.JoinHostPort(current, port)))
	}
	return urls
}

func (s *LANServer) urlFor(address string) string {
	scheme := "ws"
	if s.opts.TLSCertFile != "" {
		scheme = "wss"
	}
	return fmt.Sprintf("%s://%s/rpc", scheme, address)
}

func isWildcardHost(host string) bool {
	if host == "" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsUnspecified()
}

type lanInterface struct {
	flags net.Flags
	addrs []net.Addr
}

func localInterfaces() []lanInterface {
	interfaces, err := net.Interfaces()
	if err != nil {
		return nil
	}
	result := make([]lanInterface, 0, len(interfaces))
	for _, current := range interfaces {
		addresses, err := current.Addrs()
		if err != nil {
			continue
		}
		result = append(result, lanInterface{flags: current.Flags, addrs: addresses})
	}
	return result
}

func routableHosts(interfaces []lanInterface) []string {
	var ipv4, ipv6 []string
	seen := make(map[string]bool)
	for _, current := range interfaces {
		if current.flags&net.FlagUp == 0 || current.flags&net.FlagLoopback != 0 {
			continue
		}
		for _, address := range current.addrs {
			ip := addressIP(address)
			if !isRoutableIP(ip) || seen[ip.String()] {
				continue
			}
			seen[ip.String()] = true
			if ip.To4() != nil {
				ipv4 = append(ipv4, ip.String())
			} else {
				ipv6 = append(ipv6, ip.String())
			}
		}
	}
	return append(ipv4, ipv6...)
}

func addressIP(address net.Addr) net.IP {
	switch value := address.(type) {
	case *net.IPNet:
		return value.IP
	case *net.IPAddr:
		return value.IP
	default:
		return nil
	}
}

func isRoutableIP(ip net.IP) bool {
	return ip != nil && !ip.IsLoopback() && !ip.IsLinkLocalUnicast() && !ip.IsLinkLocalMulticast() && ip.IsGlobalUnicast()
}
