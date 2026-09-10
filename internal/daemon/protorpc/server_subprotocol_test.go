package protorpc

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Given a client that offers no subprotocol (an older agentre, or a browser
// poking at the port), When it upgrades, Then the 426 body has to name the
// subprotocol this endpoint speaks and what to do about it — a bare "protobuf
// subprotocol required" tells an operator staring at `make agentred-deploy`
// skew nothing at all.
func TestLANServer_GivenClientWithoutTheSubprotocol_WhenUpgrading_ThenExplains426(t *testing.T) {
	server := NewLANServer(LANOpts{Host: "127.0.0.1", Registry: NewRegistry()})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = server.Run(ctx) }()
	require.Eventually(t, func() bool { return server.Addr() != "" }, 5*time.Second, 5*time.Millisecond)

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+server.Addr()+"/rpc", nil)
	require.NoError(t, err)
	request.Header.Set("Connection", "Upgrade")
	request.Header.Set("Upgrade", "websocket")
	request.Header.Set("Sec-WebSocket-Version", "13")
	request.Header.Set("Sec-WebSocket-Key", "dGhlIHNhbXBsZSBub25jZQ==")

	response, err := http.DefaultClient.Do(request)
	require.NoError(t, err)
	defer func() { _ = response.Body.Close() }()
	body, err := io.ReadAll(response.Body)
	require.NoError(t, err)

	assert.Equal(t, http.StatusUpgradeRequired, response.StatusCode)
	assert.Contains(t, string(body), Subprotocol)
	assert.Contains(t, strings.ToLower(string(body)), "upgrade")
}
