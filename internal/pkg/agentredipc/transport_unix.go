//go:build !windows

package agentredipc

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
)

// Endpoint returns the unchanged Unix-domain socket path.
func Endpoint(dataDir string) string {
	return UnixSocketPath(dataDir)
}

// Listen creates the current-user-only Unix-domain socket used by agentred.
// The returned listener owns the socket file: closing it removes the socket,
// but only while the path still names the file this call created.
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
	// 收尾自己做,不用 net 包那个无条件的 unlink-on-close。同一个数据目录先后起两个
	// daemon(安装器原地升级、崩溃重拉)时,先起的那个收尾时路径上已经是后起的那个
	// socket,无条件 unlink 会把活着的那个删掉 —— 此后本机 IPC 全部失联,而两个
	// daemon 都毫无察觉。net 包在 UnixListener.close 里自己写明了这个竞争。
	unixListener.SetUnlinkOnClose(false)
	created, err := os.Stat(path)
	if err != nil {
		_ = listener.Close()
		_ = os.Remove(path)
		return nil, err
	}
	return &ownedListener{UnixListener: unixListener, path: path, created: created}, nil
}

// ownedListener 记着自己造出来的那个 socket 文件:收尾只删自己的那一个,别人的
// socket 不归它收。
type ownedListener struct {
	*net.UnixListener
	path    string
	created os.FileInfo
}

// Close 关掉监听,并只在路径仍指向自己造的那个 socket 时把它删掉。
func (l *ownedListener) Close() error {
	err := l.UnixListener.Close()
	current, statErr := os.Stat(l.path)
	if statErr != nil || !os.SameFile(l.created, current) {
		return err
	}
	if removeErr := os.Remove(l.path); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
		return errors.Join(err, removeErr)
	}
	return err
}

// DialContext returns an HTTP transport dialer for the local Unix socket.
func DialContext(dataDir string) func(context.Context, string, string) (net.Conn, error) {
	path := Endpoint(dataDir)
	return func(ctx context.Context, _, _ string) (net.Conn, error) {
		var dialer net.Dialer
		return dialer.DialContext(ctx, "unix", path)
	}
}
