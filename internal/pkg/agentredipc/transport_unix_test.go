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

	info, err := os.Stat(unixSocketPath(dataDir))
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

	assert.FileExists(t, unixSocketPath(dataDir), "后起的 daemon 的 socket 不该被先起的那个收尾删掉")
	client := &http.Client{Transport: &http.Transport{DialContext: DialContext(dataDir)}}
	response, err := client.Get("http://daemon/local/status")
	require.NoError(t, err, "本机 IPC 必须仍然拨得通")
	defer func() { _ = response.Body.Close() }()
	body, err := io.ReadAll(response.Body)
	require.NoError(t, err)
	assert.JSONEq(t, `{"ok":true}`, string(body))
}

// 上一台 daemon 的收尾与新 daemon 的启动是**并发**的:ipc.go 里那个
// `<-ctx.Done(); srv.Shutdown(...)` 的 goroutine 没人 join,它完全可能晚到新进程
// 已经建好 socket 之后才被调度。
//
// 上面那条用例只覆盖顺序发生的接管。交错发生时,「先 stat 看看还是不是我造的、
// 再删」这两个系统调用中间就是窗口:stat 看到的还是自己那一个,删下去的却已经是
// 后继者刚 bind 上的那一个。命中之后有两种表现 —— 后继者的 Listen 直接以
// ENOENT 失败(它建完还要 stat 一次),或者它的 socket 建成之后凭空消失,此后本机
// IPC 全部失联而两个 daemon 都毫无察觉。
//
// 这条用例靠重复逼近那个窗口,是概率性的:修复前本机 3000 轮内必现。
func TestGivenAStaleListenerClosingWhileTheNextOneStartsWhenTheyRaceThenTheLiveSocketSurvives(t *testing.T) {
	const rounds = 3000
	for round := range rounds {
		dataDir, err := os.MkdirTemp("", "agentred-ipc-race-")
		require.NoError(t, err)

		stale, err := Listen(dataDir)
		require.NoError(t, err)
		closed := make(chan struct{})
		go func() {
			defer close(closed)
			_ = stale.Close()
		}()

		current, err := Listen(dataDir)
		require.NoErrorf(t, err, "第 %d 轮:上一台 daemon 的收尾把新 daemon 的启动搞挂了", round)
		<-closed
		assert.FileExistsf(t, unixSocketPath(dataDir),
			"第 %d 轮:活着的 socket 被上一台 daemon 的收尾删掉了", round)

		_ = current.Close()
		_ = os.RemoveAll(dataDir)
	}
}
