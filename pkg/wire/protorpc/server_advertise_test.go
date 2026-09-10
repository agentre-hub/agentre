package protorpc

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestProtobufLANAdvertiseURLsKeepsExplicitHost(t *testing.T) {
	server := NewLANServer(LANOpts{Host: "127.0.0.1", Port: 0, Registry: NewRegistry()})
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go func() { _ = server.Run(ctx) }()
	require.Eventually(t, func() bool { return server.Addr() != "" }, time.Second, 10*time.Millisecond)

	require.Equal(t, []string{"ws://" + server.Addr() + "/rpc"}, server.AdvertiseURLs())
}

func TestProtobufLANAdvertiseURLsNeverPublishesWildcardOrLoopback(t *testing.T) {
	for _, host := range []string{"0.0.0.0", ""} {
		server := NewLANServer(LANOpts{Host: host, Port: 0, Registry: NewRegistry()})
		ctx, cancel := context.WithCancel(context.Background())
		go func() { _ = server.Run(ctx) }()
		require.Eventually(t, func() bool { return server.Addr() != "" }, time.Second, 10*time.Millisecond)
		for _, advertised := range server.AdvertiseURLs() {
			parsed, err := url.Parse(advertised)
			require.NoError(t, err)
			ip := net.ParseIP(parsed.Hostname())
			require.NotNil(t, ip)
			assert.False(t, ip.IsUnspecified(), "advertised endpoint must not point the peer at itself")
			assert.False(t, ip.IsLoopback(), "wildcard bind must not advertise a local-only address")
		}
		cancel()
	}
}

func TestProtobufLANRoutableHostsFiltersUnsafeAddressesAndPrefersIPv4(t *testing.T) {
	hosts := routableHosts([]lanInterface{
		{flags: net.FlagUp | net.FlagLoopback, addrs: []net.Addr{parseCIDR(t, "127.0.0.1/8"), parseCIDR(t, "::1/128")}},
		{flags: 0, addrs: []net.Addr{parseCIDR(t, "10.9.9.9/24")}},
		{flags: net.FlagUp, addrs: []net.Addr{
			parseCIDR(t, "169.254.7.7/16"), parseCIDR(t, "fe80::1/64"),
			parseCIDR(t, "192.168.1.9/24"), parseCIDR(t, "fd00::1/64"),
		}},
		{flags: net.FlagUp, addrs: []net.Addr{parseCIDR(t, "192.168.1.9/24")}},
	})

	require.Equal(t, []string{"192.168.1.9", "fd00::1"}, hosts)
}

func TestProtobufLANRoutableHostsReturnsEmptyWithoutPeerAddress(t *testing.T) {
	hosts := routableHosts([]lanInterface{
		{flags: net.FlagUp | net.FlagLoopback, addrs: []net.Addr{parseCIDR(t, "127.0.0.1/8")}},
		{flags: 0, addrs: []net.Addr{parseCIDR(t, "192.168.1.9/24")}},
	})
	require.Empty(t, hosts)
}

func TestProtobufLANTLSConfigurationAndAdvertisedScheme(t *testing.T) {
	t.Run("given only a certificate, then startup fails", func(t *testing.T) {
		server := NewLANServer(LANOpts{Host: "127.0.0.1", Port: 0, TLSCertFile: "/no/such/cert.pem", Registry: NewRegistry()})
		err := server.Run(context.Background())
		require.Error(t, err)
		assert.Contains(t, err.Error(), "tls")
	})

	t.Run("given a valid pair, then URLs use wss", func(t *testing.T) {
		certFile, keyFile := writeProtobufSelfSignedPair(t, "127.0.0.1")
		server := NewLANServer(LANOpts{Host: "127.0.0.1", Port: 0, TLSCertFile: certFile, TLSKeyFile: keyFile, Registry: NewRegistry()})
		ctx, cancel := context.WithCancel(context.Background())
		t.Cleanup(cancel)
		go func() { _ = server.Run(ctx) }()
		require.Eventually(t, func() bool { return server.Addr() != "" }, time.Second, 10*time.Millisecond)
		assert.True(t, strings.HasPrefix(server.URL(), "wss://"))
		require.Equal(t, []string{"wss://" + server.Addr() + "/rpc"}, server.AdvertiseURLs())
	})
}

func parseCIDR(t *testing.T, value string) net.Addr {
	t.Helper()
	ip, network, err := net.ParseCIDR(value)
	require.NoError(t, err)
	network.IP = ip
	return network
}

func writeProtobufSelfSignedPair(t *testing.T, host string) (string, string) {
	t.Helper()
	privateKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	template := x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: host}, NotBefore: time.Now().Add(-time.Minute), NotAfter: time.Now().Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, IPAddresses: []net.IP{net.ParseIP(host)}}
	der, err := x509.CreateCertificate(rand.Reader, &template, &template, &privateKey.PublicKey, privateKey)
	require.NoError(t, err)
	cert := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyBytes, err := x509.MarshalECPrivateKey(privateKey)
	require.NoError(t, err)
	key := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyBytes})
	dir := t.TempDir()
	certPath, keyPath := filepath.Join(dir, "cert.pem"), filepath.Join(dir, "key.pem")
	require.NoError(t, os.WriteFile(certPath, cert, 0o600))
	require.NoError(t, os.WriteFile(keyPath, key, 0o600))
	return certPath, keyPath
}
