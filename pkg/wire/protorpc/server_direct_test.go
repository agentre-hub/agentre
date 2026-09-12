package protorpc

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/agentre-hub/agentre/pkg/wire/agentrewire"
)

func TestProtobufLANDirectCertificateServesWSAndWSSOnOnePort(t *testing.T) {
	certFile, keyFile := writeProtobufSelfSignedPair(t, "127.0.0.1")
	certificate, err := tls.LoadX509KeyPair(certFile, keyFile)
	require.NoError(t, err)
	certPEM := readProtobufFile(t, certFile)
	server := startProtobufLANServer(t, LANOpts{Host: "127.0.0.1", Port: 0, DirectCertificate: &certificate, Registry: pingRegistry()})

	t.Run("given a direct certificate, then ws and wss both complete an RPC on the same port", func(t *testing.T) {
		require.NoError(t, pingOverLAN(t, server.URL(), nil))
		require.Len(t, server.DirectURLs(), 1)
		require.NoError(t, pingOverLAN(t, server.DirectURLs()[0], trustingProtobufTLS(t, certPEM)))
	})

	t.Run("given a client that never sends a byte, then other ws and wss clients are still accepted", func(t *testing.T) {
		silent, err := net.Dial("tcp", server.Addr())
		require.NoError(t, err)
		t.Cleanup(func() { _ = silent.Close() })

		require.NoError(t, pingOverLAN(t, server.URL(), nil))
		require.Len(t, server.DirectURLs(), 1)
		require.NoError(t, pingOverLAN(t, server.DirectURLs()[0], trustingProtobufTLS(t, certPEM)))
	})

	t.Run("then advertised addresses stay ws, direct addresses are wss on the same port and the served leaf is exposed", func(t *testing.T) {
		assert.Equal(t, "ws://"+server.Addr()+"/rpc", server.URL())
		assert.Equal(t, []string{"ws://" + server.Addr() + "/rpc"}, server.AdvertiseURLs())
		assert.Equal(t, []string{"wss://" + server.Addr() + "/rpc"}, server.DirectURLs())
		assert.Equal(t, certPEM, server.CertificatePEM())
	})
}

func TestProtobufLANConfiguredCertificateKeepsTLSOnlyListening(t *testing.T) {
	certFile, keyFile := writeProtobufSelfSignedPair(t, "127.0.0.1")
	certPEM := readProtobufFile(t, certFile)
	server := startProtobufLANServer(t, LANOpts{Host: "127.0.0.1", Port: 0, TLSCertFile: certFile, TLSKeyFile: keyFile, Registry: pingRegistry()})

	wssURL := "wss://" + server.Addr() + "/rpc"
	assert.Equal(t, []string{wssURL}, server.AdvertiseURLs())
	assert.Equal(t, []string{wssURL}, server.DirectURLs(), "auto-direct pins the configured certificate")
	assert.Equal(t, certPEM, server.CertificatePEM())
	require.NoError(t, pingOverLAN(t, wssURL, trustingProtobufTLS(t, certPEM)))
	require.Error(t, pingOverLAN(t, "ws://"+server.Addr()+"/rpc", nil), "a configured certificate keeps the port TLS-only")
}

func TestProtobufLANWithoutCertificateOffersNoDirectAddress(t *testing.T) {
	server := startProtobufLANServer(t, LANOpts{Host: "127.0.0.1", Port: 0, Registry: pingRegistry()})

	assert.Empty(t, server.DirectURLs())
	assert.Empty(t, server.CertificatePEM())
}

func TestProtobufLANDirectURLsNeverPublishWildcardOrLoopback(t *testing.T) {
	certFile, keyFile := writeProtobufSelfSignedPair(t, "127.0.0.1")
	certificate, err := tls.LoadX509KeyPair(certFile, keyFile)
	require.NoError(t, err)
	server := startProtobufLANServer(t, LANOpts{Host: "0.0.0.0", Port: 0, DirectCertificate: &certificate, Registry: pingRegistry()})

	require.Len(t, server.DirectURLs(), len(server.AdvertiseURLs()))
	for _, direct := range server.DirectURLs() {
		parsed, err := url.Parse(direct)
		require.NoError(t, err)
		assert.Equal(t, "wss", parsed.Scheme)
		ip := net.ParseIP(parsed.Hostname())
		require.NotNil(t, ip)
		assert.False(t, ip.IsUnspecified())
		assert.False(t, ip.IsLoopback())
	}
}

func TestProtobufLANRejectsConfiguredAndDirectCertificateTogether(t *testing.T) {
	certFile, keyFile := writeProtobufSelfSignedPair(t, "127.0.0.1")
	certificate, err := tls.LoadX509KeyPair(certFile, keyFile)
	require.NoError(t, err)
	server := NewLANServer(LANOpts{Host: "127.0.0.1", Port: 0, TLSCertFile: certFile, TLSKeyFile: keyFile, DirectCertificate: &certificate, Registry: NewRegistry()})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	err = server.Run(ctx)
	require.Error(t, err, "Run must refuse the configuration instead of serving until the context ends")
	assert.Contains(t, err.Error(), "tls")
	assert.Empty(t, server.Addr(), "a rejected TLS configuration must not leave a listener behind")
}

func startProtobufLANServer(t *testing.T, opts LANOpts) *LANServer {
	t.Helper()
	server := NewLANServer(opts)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- server.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("LAN server did not stop")
		}
	})
	require.Eventually(t, func() bool { return server.Addr() != "" }, time.Second, 10*time.Millisecond)
	return server
}

func pingRegistry() *Registry {
	registry := NewRegistry()
	RegisterMethod(registry, uint32(agentrewire.RpcMethod_RPC_METHOD_HEALTH_PING),
		func() *agentrewire.HealthPingRequest { return &agentrewire.HealthPingRequest{} },
		func(context.Context, *agentrewire.HealthPingRequest) (*agentrewire.HealthPingResponse, error) {
			return &agentrewire.HealthPingResponse{InstanceUuid: "lan"}, nil
		})
	return registry
}

// pingOverLAN performs a real WebSocket upgrade and one RPC round trip, so a
// listener that mangles the first byte or the TLS stream fails here rather
// than only at the upgrade.
func pingOverLAN(t *testing.T, target string, tlsConfig *tls.Config) error {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	dialer := websocket.Dialer{Subprotocols: []string{Subprotocol}, TLSClientConfig: tlsConfig, HandshakeTimeout: 3 * time.Second}
	ws, response, err := dialer.DialContext(ctx, target, nil)
	if response != nil {
		_ = response.Body.Close()
	}
	if err != nil {
		return err
	}
	conn := NewConn(NewWebSocketFrameConn(ws), NewRegistry())
	defer func() { _ = ws.Close() }()
	go conn.Serve(ctx)
	result, err := CallMethod(ctx, conn, uint32(agentrewire.RpcMethod_RPC_METHOD_HEALTH_PING), &agentrewire.HealthPingRequest{}, func() *agentrewire.HealthPingResponse { return &agentrewire.HealthPingResponse{} })
	if err != nil {
		return err
	}
	if result.GetInstanceUuid() != "lan" {
		return fmt.Errorf("unexpected ping answer %q", result.GetInstanceUuid())
	}
	return nil
}

func trustingProtobufTLS(t *testing.T, certPEM string) *tls.Config {
	t.Helper()
	pool := x509.NewCertPool()
	require.True(t, pool.AppendCertsFromPEM([]byte(certPEM)))
	return &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: pool}
}

func readProtobufFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path) //nolint:gosec // G304: path is the t.TempDir fixture this test just wrote
	require.NoError(t, err)
	return strings.TrimSpace(string(data)) + "\n"
}

// 跑在 NAT 后面(容器网桥、端口映射)的 daemon,从自己网卡上只看得见对端够不着的
// 地址,更无从知道宿主把端口映射到了哪个号上。AdvertiseAddr 就是运维把「对外该用
// 哪个地址」直接说出来 —— 自动直连与手工配对给的是同一个地址,只是方案不同。
func TestProtobufLANURLsPublishTheConfiguredAdvertiseAddress(t *testing.T) {
	certFile, keyFile := writeProtobufSelfSignedPair(t, "127.0.0.1")
	certificate, err := tls.LoadX509KeyPair(certFile, keyFile)
	require.NoError(t, err)
	server := startProtobufLANServer(t, LANOpts{
		Host: "0.0.0.0", Port: 0, DirectCertificate: &certificate,
		AdvertiseAddr: "203.0.113.7:9443", Registry: pingRegistry(),
	})

	assert.Equal(t, []string{"wss://203.0.113.7:9443/rpc"}, server.DirectURLs(),
		"the configured address replaces the interface addresses, it does not join them")
	assert.Equal(t, []string{"ws://203.0.113.7:9443/rpc"}, server.AdvertiseURLs(),
		"what a person pastes into a peer is the same address, on the scheme this port advertises")
}

func TestProtobufLANAdvertiseAddressWithoutAPortKeepsTheListeningPort(t *testing.T) {
	certFile, keyFile := writeProtobufSelfSignedPair(t, "127.0.0.1")
	certificate, err := tls.LoadX509KeyPair(certFile, keyFile)
	require.NoError(t, err)

	for name, address := range map[string]string{"IPv4": "203.0.113.7", "IPv6": "fd00::5", "host name": "agentred.example"} {
		t.Run(name, func(t *testing.T) {
			server := startProtobufLANServer(t, LANOpts{
				Host: "0.0.0.0", Port: 0, DirectCertificate: &certificate,
				AdvertiseAddr: address, Registry: pingRegistry(),
			})
			_, port, err := net.SplitHostPort(server.Addr())
			require.NoError(t, err)

			require.Len(t, server.DirectURLs(), 1)
			parsed, err := url.Parse(server.DirectURLs()[0])
			require.NoError(t, err)
			assert.Equal(t, "wss", parsed.Scheme)
			assert.Equal(t, address, parsed.Hostname())
			assert.Equal(t, port, parsed.Port(), "an address without a port keeps the port this server listens on")
		})
	}
}

func TestProtobufLANAdvertiseAddressOffersNothingWithoutACertificate(t *testing.T) {
	server := startProtobufLANServer(t, LANOpts{
		Host: "0.0.0.0", Port: 0, AdvertiseAddr: "203.0.113.7:9443", Registry: pingRegistry(),
	})

	assert.Empty(t, server.DirectURLs(), "there is no certificate to pin, so there is nothing to offer")
	assert.Empty(t, server.CertificatePEM())
}
