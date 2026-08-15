package rpc

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLAN_GivenOverlappingConnections_WhenSecondRegistersSameMethod_ThenEachKeepsPrivateHandler(t *testing.T) {
	bootstrap := NewRegistry()
	bootstrap.Register("static.identity", func(context.Context, json.RawMessage) (any, error) {
		return "bootstrap", nil
	})

	accepted := make(chan *Conn, 2)
	var nextID atomic.Int32
	srv := NewLANServer(LANOpts{
		Host:     "127.0.0.1",
		Port:     0,
		Registry: bootstrap,
		OnConn: func(c *Conn) {
			identity := fmt.Sprintf("connection-%d", nextID.Add(1))
			c.Registry().Register("connection.identity", func(context.Context, json.RawMessage) (any, error) {
				return identity, nil
			})
			accepted <- c
		},
	})
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go func() { _ = srv.Run(ctx) }()
	require.Eventually(t, func() bool { return srv.Addr() != "" }, time.Second, 10*time.Millisecond)

	clientA := dialLANClient(t, srv.URL())
	serverConnA := <-accepted
	assert.JSONEq(t, `"bootstrap"`, string(callLANRPC(t, clientA, 1, "static.identity").Result))
	assert.JSONEq(t, `"connection-1"`, string(callLANRPC(t, clientA, 2, "connection.identity").Result))

	clientB := dialLANClient(t, srv.URL())
	serverConnB := <-accepted
	assert.JSONEq(t, `"bootstrap"`, string(callLANRPC(t, clientB, 1, "static.identity").Result))
	assert.JSONEq(t, `"connection-1"`, string(callLANRPC(t, clientA, 3, "connection.identity").Result))
	assert.JSONEq(t, `"connection-2"`, string(callLANRPC(t, clientB, 2, "connection.identity").Result))

	_, err := bootstrap.Dispatch(context.Background(), "connection.identity", nil)
	require.ErrorIs(t, err, ErrMethodNotFound, "OnConn must not mutate the bootstrap registry")

	require.NoError(t, clientA.Close())
	require.Eventually(t, func() bool {
		select {
		case <-serverConnA.Done():
			return true
		default:
			return false
		}
	}, time.Second, 10*time.Millisecond)
	select {
	case <-serverConnB.Done():
		t.Fatal("closing connection A must not close connection B")
	default:
	}
	assert.JSONEq(t, `"connection-2"`, string(callLANRPC(t, clientB, 3, "connection.identity").Result))
}

func dialLANClient(t *testing.T, rawURL string) *websocket.Conn {
	t.Helper()
	c, hsResp, err := websocket.DefaultDialer.Dial(rawURL, nil)
	require.NoError(t, err)
	if hsResp != nil {
		_ = hsResp.Body.Close()
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}

func callLANRPC(t *testing.T, c *websocket.Conn, id int, method string) Frame {
	t.Helper()
	require.NoError(t, c.WriteJSON(Frame{
		JSONRPC: "2.0",
		ID:      json.RawMessage(fmt.Sprintf("%d", id)),
		Method:  method,
	}))
	var resp Frame
	require.NoError(t, c.ReadJSON(&resp))
	require.Nil(t, resp.Error)
	return resp
}

func TestLAN_ServerAcceptsWS(t *testing.T) {
	reg := NewRegistry()
	reg.Register("ping", func(ctx context.Context, p json.RawMessage) (any, error) { return "pong", nil })

	srv := NewLANServer(LANOpts{
		Host:     "127.0.0.1",
		Port:     0, // ephemeral
		Registry: reg,
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = srv.Run(ctx) }()
	require.Eventually(t, func() bool { return srv.Addr() != "" }, time.Second, 10*time.Millisecond)

	u := url.URL{Scheme: "ws", Host: srv.Addr(), Path: "/rpc"}
	c, hsResp, err := websocket.DefaultDialer.Dial(u.String(), nil)
	require.NoError(t, err)
	if hsResp != nil {
		_ = hsResp.Body.Close()
	}
	defer func() { _ = c.Close() }()

	require.NoError(t, c.WriteJSON(map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "ping",
	}))
	var resp Frame
	require.NoError(t, c.ReadJSON(&resp))
	assert.Equal(t, `"pong"`, string(resp.Result))
}

// TestLAN_ServeCtxNotCanceledAfterUpgrade pins the fix for
// transport_lan.go:74 — Serve must NOT inherit r.Context(), since
// net/http cancels the request ctx as soon as the HTTP handler
// returns (which happens immediately after upgrader.Upgrade
// hijacks the connection). If that ctx leaked into Serve, every
// subsequent RPC handler would see context.Canceled on entry,
// breaking chat.start in the first message of a new session.
func TestLAN_ServeCtxNotCanceledAfterUpgrade(t *testing.T) {
	reg := NewRegistry()
	ctxSeen := make(chan error, 1)
	reg.Register("probe", func(ctx context.Context, _ json.RawMessage) (any, error) {
		ctxSeen <- ctx.Err()
		return "ok", nil
	})
	srv := NewLANServer(LANOpts{Host: "127.0.0.1", Port: 0, Registry: reg})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = srv.Run(ctx) }()
	require.Eventually(t, func() bool { return srv.Addr() != "" }, time.Second, 10*time.Millisecond)

	u := url.URL{Scheme: "ws", Host: srv.Addr(), Path: "/rpc"}
	c, hsResp, err := websocket.DefaultDialer.Dial(u.String(), nil)
	require.NoError(t, err)
	if hsResp != nil {
		_ = hsResp.Body.Close()
	}
	defer func() { _ = c.Close() }()

	// Give net/http a beat to drop the request ctx — the bug
	// surfaces because Upgrade hijacks the conn and the HTTP
	// handler returns, after which the request ctx is canceled
	// even though the WS read loop keeps running.
	time.Sleep(100 * time.Millisecond)

	require.NoError(t, c.WriteJSON(map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "probe",
	}))
	var resp Frame
	require.NoError(t, c.ReadJSON(&resp))

	select {
	case got := <-ctxSeen:
		assert.NoError(t, got, "Serve ctx must outlive the HTTP request that upgraded it")
	case <-time.After(time.Second):
		t.Fatal("probe handler never fired")
	}
}

// daemon 默认监听通配地址(cmd/agentred/run.go 的 0.0.0.0),Go 对通配监听走双栈,
// listener.Addr() 报回来的是 "[::]:7456"。这串东西被 /local/pair 当作 listenURLs 交给
// `agentred pair` 打印,用户照引导粘进桌面端的配对表单 —— 桌面端于是去拨 [::]:7456,
// 那是桌面端**自己那台机器**。所以对外广播的地址不能是通配地址。
func TestLAN_GivenWildcardBind_WhenAdvertisingURLs_ThenHostsAreDialableByAPeer(t *testing.T) {
	for _, host := range []string{"0.0.0.0", ""} {
		srv := NewLANServer(LANOpts{Host: host, Port: 0, Registry: NewRegistry()})
		ctx, cancel := context.WithCancel(context.Background())
		go func() { _ = srv.Run(ctx) }()
		require.Eventually(t, func() bool { return srv.Addr() != "" }, time.Second, 10*time.Millisecond)

		urls := srv.AdvertiseURLs()
		require.NotEmpty(t, urls, "bind %q: this host has a routable interface, so pairing must advertise one", host)
		for _, raw := range urls {
			assertDialableByPeer(t, raw)
		}
		cancel()
	}
}

// 用户显式指定了 --host 就照他说的广播:他可能指的是一个 NAT 后的转发入口 / 一张只在
// 某个网段可见的网卡,daemon 没有资格替他改写。
func TestLAN_GivenExplicitHostBind_WhenAdvertisingURLs_ThenKeepsThatHost(t *testing.T) {
	srv := NewLANServer(LANOpts{Host: "127.0.0.1", Port: 0, Registry: NewRegistry()})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = srv.Run(ctx) }()
	require.Eventually(t, func() bool { return srv.Addr() != "" }, time.Second, 10*time.Millisecond)

	assert.Equal(t, []string{"ws://" + srv.Addr() + "/rpc"}, srv.AdvertiseURLs())
}

// 过滤规则:关掉的网卡、回环、link-local 都不是对端拨得通的地址;同一个地址在多张网卡上
// 出现只报一次;IPv4 排在前面,用户粘的通常是第一条。
func TestRoutableHosts_GivenMixedInterfaces_ThenKeepsOnlyWhatAPeerCanDial(t *testing.T) {
	hosts := routableHosts([]lanInterface{
		{flags: net.FlagUp | net.FlagLoopback, addrs: []net.Addr{cidr(t, "127.0.0.1/8"), cidr(t, "::1/128")}},
		{flags: 0, addrs: []net.Addr{cidr(t, "10.9.9.9/24")}}, // down
		{flags: net.FlagUp, addrs: []net.Addr{
			cidr(t, "169.254.7.7/16"), // IPv4 link-local
			cidr(t, "fe80::1/64"),     // IPv6 link-local
			cidr(t, "192.168.1.9/24"), // 正常的局域网地址
			cidr(t, "fd00::1/64"),     // ULA,局域网内可路由
		}},
		{flags: net.FlagUp, addrs: []net.Addr{cidr(t, "192.168.1.9/24")}}, // 重复
	})

	assert.Equal(t, []string{"192.168.1.9", "fd00::1"}, hosts)
}

// 一个可路由地址都没有时返回空:此时**不能**回退到 bind 地址 —— 那正是让用户粘错的那串
// 东西。空列表让上层(agentred pair)明确告诉用户加 --host,而不是发一个必然拨不通的地址。
func TestRoutableHosts_GivenNothingRoutable_ThenReturnsEmptyRatherThanBindAddress(t *testing.T) {
	hosts := routableHosts([]lanInterface{
		{flags: net.FlagUp | net.FlagLoopback, addrs: []net.Addr{cidr(t, "127.0.0.1/8")}},
		{flags: 0, addrs: []net.Addr{cidr(t, "192.168.1.9/24")}},
	})

	assert.Empty(t, hosts)
}

func cidr(t *testing.T, s string) net.Addr {
	t.Helper()
	ip, ipnet, err := net.ParseCIDR(s)
	require.NoError(t, err)
	ipnet.IP = ip
	return ipnet
}

func assertDialableByPeer(t *testing.T, raw string) {
	t.Helper()
	u, err := url.Parse(raw)
	require.NoError(t, err)
	ip := net.ParseIP(u.Hostname())
	require.NotNil(t, ip, "advertised %q must carry an IP a peer can dial", raw)
	assert.False(t, ip.IsUnspecified(), "advertised %q points a peer at itself, not at this daemon", raw)
	assert.False(t, ip.IsLoopback(), "advertised %q is only reachable from this box", raw)
}

func TestLAN_TLSMisconfigFails(t *testing.T) {
	srv := NewLANServer(LANOpts{
		Host:        "127.0.0.1",
		Port:        0,
		TLSCertFile: "/no/such/cert.pem",
		// TLSKeyFile intentionally missing -> mismatched pair
		Registry: NewRegistry(),
	})
	err := srv.Run(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "tls")
}

func TestLAN_WithTempTLS(t *testing.T) {
	certFile, keyFile := writeSelfSignedPair(t, "127.0.0.1")
	reg := NewRegistry()
	reg.Register("ping", func(ctx context.Context, p json.RawMessage) (any, error) { return "pong", nil })

	srv := NewLANServer(LANOpts{
		Host:        "127.0.0.1",
		Port:        0,
		TLSCertFile: certFile,
		TLSKeyFile:  keyFile,
		Registry:    reg,
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = srv.Run(ctx) }()
	require.Eventually(t, func() bool { return srv.Addr() != "" }, time.Second, 10*time.Millisecond)
	assert.True(t, strings.HasPrefix(srv.URL(), "wss://"))
	// Verified end-to-end via the daemon integration TLS test (T22).
}

// writeSelfSignedPair writes a self-signed ECDSA cert + key into t.TempDir()
// and returns their paths. Used by this transport test and (later) reused
// by the daemon-level TLS sub-tests.
func writeSelfSignedPair(t *testing.T, host string) (certPath, keyPath string) {
	t.Helper()
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)

	tmpl := x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: host},
		NotBefore:    time.Now().Add(-time.Minute),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IPAddresses:  []net.IP{net.ParseIP(host)},
	}
	der, err := x509.CreateCertificate(rand.Reader, &tmpl, &tmpl, &priv.PublicKey, priv)
	require.NoError(t, err)

	dir := t.TempDir()
	certPath = filepath.Join(dir, "cert.pem")
	keyPath = filepath.Join(dir, "key.pem")

	certBytes := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	require.NotNil(t, certBytes)
	require.NoError(t, os.WriteFile(certPath, certBytes, 0o600))

	kb, err := x509.MarshalECPrivateKey(priv)
	require.NoError(t, err)
	keyBytes := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: kb})
	require.NotNil(t, keyBytes)
	require.NoError(t, os.WriteFile(keyPath, keyBytes, 0o600))
	return
}
