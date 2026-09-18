//go:build !windows

package agentredipc

import (
	"context"
	"fmt"
	"net"
	"os"
)

// Endpoint returns the unchanged Unix-domain socket path.
func Endpoint(dataDir string) string {
	return unixSocketPath(dataDir)
}

// Listen creates the current-user-only Unix-domain socket used by agentred.
// Binding replaces whatever stale socket sits on the path; closing the listener
// leaves the file behind on purpose (see the comment at SetUnlinkOnClose).
func Listen(dataDir string) (net.Listener, error) {
	path := Endpoint(dataDir)
	_ = os.Remove(path)
	listener, err := net.Listen("unix", path)
	if err != nil {
		return nil, err
	}
	unixListener, ok := listener.(*net.UnixListener)
	if !ok {
		_ = listener.Close()
		return nil, fmt.Errorf("agentredipc: unexpected %T listening on %s", listener, path)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		_ = listener.Close()
		return nil, err
	}
	// 收尾一律不删这个文件 —— net 包那个无条件的 unlink-on-close 要关掉,我们自己
	// 也不补一个「先 stat 看看还是不是我的、再删」的版本。
	//
	// 同一个数据目录先后起两个 daemon(安装器原地升级、崩溃重拉)时,先起的那个收尾
	// 时路径上可能已经是后起的那个 socket,删下去就把活着的那个删掉了 —— 此后本机
	// IPC 全部失联,而两个 daemon 都毫无察觉(net 包在 UnixListener.close 里自己写明
	// 了这个竞争)。而 stat 与 remove 是两个系统调用,中间就是窗口:两台 daemon 的
	// 收尾与启动一交错,stat 看到的还是自己那一个,删下去的已经是后继者刚 bind 上的
	// 那一个。回归锚在 transport_unix_test.go 的
	// TestGivenAStaleListenerClosingWhileTheNextOneStarts...(修复前几轮内必现)。
	//
	// 路径上的陈旧文件由下一次 Listen 上面那行 os.Remove 清掉:那是唯一该删它的
	// 时刻 —— 删的正是自己即将取代的那一个,而且紧接着就 bind。代价是干净退出之后
	// socket 文件留在磁盘上,客户端拨它拿到的是 connection refused 而不是
	// no such file;两者都是「没在跑」,没有人靠这个文件在不在判定存活。
	unixListener.SetUnlinkOnClose(false)
	return unixListener, nil
}

// DialContext returns an HTTP transport dialer for the local Unix socket.
func DialContext(dataDir string) func(context.Context, string, string) (net.Conn, error) {
	path := Endpoint(dataDir)
	return func(ctx context.Context, _, _ string) (net.Conn, error) {
		var dialer net.Dialer
		return dialer.DialContext(ctx, "unix", path)
	}
}
