//go:build !windows

package agentredipc

import (
	"context"
	"io"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGivenUnixDataDirectoryWhenServingLocalHTTPThenSocketModeAndRoundTripStayCompatible(t *testing.T) {
	dataDir, err := os.MkdirTemp("", "agentred-ipc-")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(dataDir) })
	listener, err := Listen(dataDir)
	require.NoError(t, err)
	t.Cleanup(func() { _ = listener.Close() })

	info, err := os.Stat(UnixSocketPath(dataDir))
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())

	server := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{"ok":true}`))
		}),
		ReadHeaderTimeout: time.Second,
	}
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() { _ = server.Shutdown(context.Background()) })

	client := &http.Client{Transport: &http.Transport{DialContext: DialContext(dataDir)}}
	response, err := client.Get("http://daemon/local/status")
	require.NoError(t, err)
	defer func() { _ = response.Body.Close() }()
	body, err := io.ReadAll(response.Body)
	require.NoError(t, err)
	assert.JSONEq(t, `{"ok":true}`, string(body))
}

// 同一个数据目录先后起两个 daemon(安装器原地升级、崩溃重拉)时,先起的那个监听
// 收尾时必须认得出路径上的 socket 已经不是它造的那一个 —— 否则它会把后起的进程
// 刚建好的 socket 删掉,此后 agrctl 与本机 IPC 全部失联,而 daemon 自己毫无察觉。
func TestGivenSocketTakenOverByASecondListenerWhenTheFirstOneClosesThenTheLiveSocketSurvives(t *testing.T) {
	dataDir, err := os.MkdirTemp("", "agentred-ipc-takeover-")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(dataDir) })

	previous, err := Listen(dataDir)
	require.NoError(t, err)

	current, err := Listen(dataDir)
	require.NoError(t, err)
	t.Cleanup(func() { _ = current.Close() })
	server := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{"ok":true}`))
		}),
		ReadHeaderTimeout: time.Second,
	}
	go func() { _ = server.Serve(current) }()
	t.Cleanup(func() { _ = server.Shutdown(context.Background()) })

	// 先起的那个 daemon 这时才收尾。
	require.NoError(t, previous.Close())

	assert.FileExists(t, UnixSocketPath(dataDir), "后起的 daemon 的 socket 不该被先起的那个收尾删掉")
	client := &http.Client{Transport: &http.Transport{DialContext: DialContext(dataDir)}}
	response, err := client.Get("http://daemon/local/status")
	require.NoError(t, err, "本机 IPC 必须仍然拨得通")
	defer func() { _ = response.Body.Close() }()
	body, err := io.ReadAll(response.Body)
	require.NoError(t, err)
	assert.JSONEq(t, `{"ok":true}`, string(body))
}
