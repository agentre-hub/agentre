package relaytransport

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Connect 是一次性客户端（agrctl）用的单条物理连接：拨一次、不在后台重拨，拨号失败
// 原样交回，连上之后断线由 done 告知调用方，由调用方决定要不要再 Connect。
func TestHubLink_GivenConnect_WhenTheRelayAcceptsThenDrops_ThenItReportsBothAndNeverRedials(t *testing.T) {
	var attempts atomic.Int32
	drop := make(chan struct{})
	upgrader := websocket.Upgrader{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts.Add(1)
		assert.Equal(t, "/v1/relay/client", r.URL.Path)
		ws, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer func() { _ = ws.Close() }()
		require.NoError(t, ws.WriteMessage(websocket.BinaryMessage, []byte("hello")))
		<-drop
	}))
	t.Cleanup(server.Close)

	dialed := make(chan struct{}, 1)
	link := NewHubLink(HubLinkOptions{Endpoint: "/v1/relay/client", ServerURL: server.URL, AccessToken: "t",
		Logf: func(string, ...any) {}})
	link.AddLifecycleListener(func() { dialed <- struct{}{} }, nil)

	done, err := link.Connect(context.Background())
	require.NoError(t, err)
	assert.True(t, link.Connected())
	<-dialed
	select {
	case frame := <-link.Receive():
		assert.Equal(t, "hello", string(frame.Payload))
	case <-time.After(5 * time.Second):
		t.Fatal("no frame")
	}

	close(drop)
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("drop not reported")
	}
	assert.False(t, link.Connected())
	time.Sleep(50 * time.Millisecond)
	assert.Equal(t, int32(1), attempts.Load(), "Connect never redials on its own")
}

func TestHubLink_GivenConnect_WhenTheRelayRejectsTheCredential_ThenItReturnsTheRejectionAtOnce(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	t.Cleanup(server.Close)
	link := NewHubLink(HubLinkOptions{Endpoint: "/v1/relay/client", ServerURL: server.URL, AccessToken: "bad"})
	_, err := link.Connect(context.Background())
	assert.True(t, errors.Is(err, ErrRelayCredentialRejected), "err = %v", err)
	assert.False(t, link.Connected())
}
