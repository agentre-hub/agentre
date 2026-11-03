//go:build linux

package procscan

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"syscall"
)

// 纯 Go 读 /proc，不依赖 lsof（agentred 容器镜像里没有）。
func init() {
	platformOS = func() OS { return &linuxOS{root: "/proc"} }
}

// linuxOS 基于 procfs 实现 OS；root 便于指向别的挂载点。
type linuxOS struct {
	root string
}

// Snapshot 枚举当前 uid 的全部进程；读不到（权限 / 已退出）的条目直接跳过。
func (l *linuxOS) Snapshot(ctx context.Context) ([]Proc, error) {
	entries, err := os.ReadDir(l.root)
	if err != nil {
		return nil, err
	}
	btime, err := l.btime()
	if err != nil {
		return nil, err
	}
	listening := l.listeningInodes()
	uid := uint32(os.Getuid())

	var out []Proc
	for _, e := range entries {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		pid, err := strconv.Atoi(e.Name())
		if err != nil || pid <= 0 {
			continue
		}
		p, ok := l.read(pid, btime, uid, listening)
		if ok {
			out = append(out, p)
		}
	}
	return out, nil
}

// Inspect 返回单个进程（身份核对用，不读监听表）；不存在、已成僵尸或不属于当前用户时第二个返回值为 false。
func (l *linuxOS) Inspect(ctx context.Context, pid int) (Proc, bool, error) {
	if err := ctx.Err(); err != nil {
		return Proc{}, false, err
	}
	btime, err := l.btime()
	if err != nil {
		return Proc{}, false, err
	}
	p, ok := l.read(pid, btime, uint32(os.Getuid()), nil)
	return p, ok, nil
}

// Terminate 只对单个 pid 发信号，绝不碰进程组；进程已不在视为成功。
func (l *linuxOS) Terminate(pid int, force bool) error {
	sig := syscall.SIGTERM
	if force {
		sig = syscall.SIGKILL
	}
	if err := syscall.Kill(pid, sig); err != nil && !errors.Is(err, syscall.ESRCH) {
		return err
	}
	return nil
}

func (l *linuxOS) btime() (int64, error) {
	b, err := os.ReadFile(filepath.Join(l.root, "stat"))
	if err != nil {
		return 0, err
	}
	return parseBtime(b)
}

// listeningInodes 合并 tcp 与 tcp6 的 LISTEN socket（inode → 端口）。
func (l *linuxOS) listeningInodes() map[uint64]uint16 {
	out := map[uint64]uint16{}
	for _, name := range []string{"net/tcp", "net/tcp6"} {
		b, err := os.ReadFile(filepath.Join(l.root, name)) //nolint:gosec // 路径来自固定的 procfs 根与常量名
		if err != nil {
			continue
		}
		for ino, port := range parseNetTCP(b) {
			out[ino] = port
		}
	}
	return out
}

// read 读单个进程；任何一步读不到（含环境）都返回 false。
// 只有带标记的进程才去扫 fd 找监听端口，其余进程不付这笔开销。
func (l *linuxOS) read(pid int, btime int64, uid uint32, listening map[uint64]uint16) (Proc, bool) {
	dir := filepath.Join(l.root, strconv.Itoa(pid))
	var st syscall.Stat_t
	if err := syscall.Stat(dir, &st); err != nil || st.Uid != uid {
		return Proc{}, false
	}
	raw, err := os.ReadFile(filepath.Join(dir, "stat")) //nolint:gosec // procfs 路径，pid 来自枚举的数字目录
	if err != nil {
		return Proc{}, false
	}
	info, err := parseStat(string(raw))
	if err != nil || info.State == 'Z' || info.State == 'X' {
		return Proc{}, false
	}
	env, err := os.ReadFile(filepath.Join(dir, "environ")) //nolint:gosec // 同上
	if err != nil {
		return Proc{}, false
	}
	cmdline, _ := os.ReadFile(filepath.Join(dir, "cmdline")) //nolint:gosec // 同上
	cwd, _ := os.Readlink(filepath.Join(dir, "cwd"))
	p := Proc{
		PID:     pid,
		PPID:    info.PPID,
		StartMs: startMs(btime, info.StartTicks),
		Argv:    parseCmdline(cmdline),
		Cwd:     cwd,
		Marker:  markerFromEnviron(env),
	}
	if p.Marker != "" {
		p.Ports = portsOf(dir, listening)
	}
	return p, true
}

// portsOf 读 <dir>/fd 下的 socket 链接，映射到监听端口（去重、升序）。
func portsOf(dir string, listening map[uint64]uint16) []uint16 {
	if len(listening) == 0 {
		return nil
	}
	fds, err := os.ReadDir(filepath.Join(dir, "fd"))
	if err != nil {
		return nil
	}
	seen := map[uint16]bool{}
	var ports []uint16
	for _, fd := range fds {
		target, err := os.Readlink(filepath.Join(dir, "fd", fd.Name()))
		if err != nil {
			continue
		}
		ino, ok := parseSocketLink(target)
		if !ok {
			continue
		}
		if port, ok := listening[ino]; ok && !seen[port] {
			seen[port] = true
			ports = append(ports, port)
		}
	}
	sort.Slice(ports, func(i, j int) bool { return ports[i] < ports[j] })
	return ports
}
