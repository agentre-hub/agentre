package daemon

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/require"

	"github.com/agentre-hub/agentre/internal/daemon/client"
	"github.com/agentre-hub/agentre/pkg/wire/agentrewire"
)

func TestDaemonRunServesOnlyBinaryProtobufWebSocket(t *testing.T) {
	dir, err := os.MkdirTemp("", "ard-proto-lan")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	d, err := New(Options{DataDir: dir, LANHost: "127.0.0.1", LANPort: 0})
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	serveErr := make(chan error, 1)
	go func() { serveErr <- d.Run(ctx) }()
	require.Eventually(t, func() bool {
		d.mu.RLock()
		defer d.mu.RUnlock()
		return d.lan != nil && d.lan.Addr() != ""
	}, 2*time.Second, 10*time.Millisecond)

	pair := readLocalPair(t, d)
	code, _ := pair["code"].(string)
	d.mu.RLock()
	url := d.lan.URL()
	d.mu.RUnlock()
	protobufClient, err := client.DialProtobuf(t.Context(), client.Options{URL: url})
	require.NoError(t, err)
	t.Cleanup(func() { _ = protobufClient.Close() })
	paired, err := protobufClient.AuthPair(t.Context(), &agentrewire.AuthPairRequest{
		Code: code, DeviceName: "protobuf desktop", DeviceFingerprint: "sha256:protobuf-desktop",
	})
	require.NoError(t, err)
	require.NotEmpty(t, paired.GetDeviceToken())

	unsupportedDialer := *websocket.DefaultDialer
	unsupportedDialer.Subprotocols = []string{"unsupported-protocol"}
	unsupported, response, err := unsupportedDialer.DialContext(t.Context(), url, nil)
	if unsupported != nil {
		_ = unsupported.Close()
	}
	require.Error(t, err)
	require.NotNil(t, response)
	t.Cleanup(func() { _ = response.Body.Close() })
	require.Equal(t, 426, response.StatusCode)

	cancel()
	require.NoError(t, <-serveErr)
}
