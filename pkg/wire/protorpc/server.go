package protorpc

import (
	"context"
	"crypto/tls"
	"encoding/pem"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// lanReadHeaderTimeout bounds how long one LAN client may take to start
// talking: the request headers, the TLS handshake and, beside a direct
// certificate, the first byte that tells ws from wss.
const lanReadHeaderTimeout = 10 * time.Second

type LANOpts struct {
	Host, TLSCertFile, TLSKeyFile string
	Port                          int
	// DirectCertificate is served to clients that open the LAN port with a TLS
	// handshake, while plain ws keeps working on the same port and stays the
	// advertised scheme. It is the host's own certificate for automatic direct
	// connections when no certificate was configured, so it cannot be combined
	// with TLSCertFile/TLSKeyFile.
	DirectCertificate *tls.Certificate
	Registry          *Registry
	OnConn            func(*Conn)
}

type LANServer struct {
	opts     LANOpts
	mu       sync.Mutex
	listener net.Listener
	server   *http.Server
	certPEM  string
}

func NewLANServer(opts LANOpts) *LANServer { return &LANServer{opts: opts} }

func (s *LANServer) Run(ctx context.Context) error {
	certificate, err := s.loadCertificate()
	if err != nil {
		return err
	}
	listener, err := net.Listen("tcp", fmt.Sprintf("%s:%d", s.opts.Host, s.opts.Port))
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.listener = listener
	if certificate != nil {
		s.certPEM = string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certificate.Certificate[0]}))
	}
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
	s.server = &http.Server{Handler: mux, ReadHeaderTimeout: lanReadHeaderTimeout}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer cancel()
		_ = s.server.Shutdown(shutdownCtx)
	}()
	if s.opts.TLSCertFile != "" {
		// The pair loaded above is the one served, so CertificatePEM names exactly
		// the leaf a client sees even if the files change while running.
		s.server.TLSConfig = &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{*certificate}}
		err = s.server.ServeTLS(listener, "", "")
	} else if certificate != nil {
		err = s.server.Serve(newProtocolSniffListener(listener,
			&tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{*certificate}}, lanReadHeaderTimeout))
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
	return lanURL(s.advertisedScheme(), s.Addr())
}

// AdvertiseURLs lists the addresses a person pastes into a peer: wss only when
// a certificate was configured, ws otherwise — including when DirectCertificate
// is served beside it.
func (s *LANServer) AdvertiseURLs() []string {
	return s.peerURLs(s.advertisedScheme())
}

// DirectURLs lists the wss addresses of this same port that an automatic
// direct connection pins CertificatePEM against. It is empty while no
// certificate is served, and follows AdvertiseURLs' host selection.
func (s *LANServer) DirectURLs() []string {
	if s.CertificatePEM() == "" {
		return nil
	}
	return s.peerURLs("wss")
}

// CertificatePEM is the PEM of the leaf certificate this server presents to
// TLS clients, configured or direct; empty when it serves no TLS.
func (s *LANServer) CertificatePEM() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.certPEM
}

func (s *LANServer) loadCertificate() (*tls.Certificate, error) {
	if (s.opts.TLSCertFile == "") != (s.opts.TLSKeyFile == "") {
		return nil, errors.New("tls: both certificate and key are required")
	}
	switch {
	case s.opts.TLSCertFile != "" && s.opts.DirectCertificate != nil:
		return nil, errors.New("tls: a configured certificate and a direct certificate cannot be combined")
	case s.opts.TLSCertFile != "":
		certificate, err := tls.LoadX509KeyPair(s.opts.TLSCertFile, s.opts.TLSKeyFile)
		if err != nil {
			return nil, fmt.Errorf("tls: %w", err)
		}
		return &certificate, nil
	case s.opts.DirectCertificate != nil:
		if len(s.opts.DirectCertificate.Certificate) == 0 {
			return nil, errors.New("tls: direct certificate has no leaf")
		}
		return s.opts.DirectCertificate, nil
	default:
		return nil, nil
	}
}

func (s *LANServer) advertisedScheme() string {
	if s.opts.TLSCertFile != "" {
		return "wss"
	}
	return "ws"
}

func (s *LANServer) peerURLs(scheme string) []string {
	host, port, err := net.SplitHostPort(s.Addr())
	if err != nil {
		return nil
	}
	if !isWildcardHost(host) {
		return []string{lanURL(scheme, net.JoinHostPort(host, port))}
	}
	hosts := routableHosts(localInterfaces())
	urls := make([]string, 0, len(hosts))
	for _, current := range hosts {
		urls = append(urls, lanURL(scheme, net.JoinHostPort(current, port)))
	}
	return urls
}

func lanURL(scheme, address string) string {
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
