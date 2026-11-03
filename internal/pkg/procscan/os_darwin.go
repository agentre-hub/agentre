//go:build darwin

package procscan

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"

	"golang.org/x/sys/unix"
)

const lsofPath = "/usr/sbin/lsof"

func init() {
	platformOS = func() OS { return &darwinOS{} }
}

// darwinOS 用 sysctl 枚举进程、读 argv / 环境，用 lsof 取监听端口与 cwd（纯 Go，无 CGO）。
type darwinOS struct{}

// Snapshot 返回当前用户的全部进程；procargs2 读不到（EPERM 等）的进程被跳过。
// 端口用一次 lsof，cwd 再用一次 lsof 且只查带标记的进程；lsof 缺失或失败时二者留空。
func (d *darwinOS) Snapshot(ctx context.Context) ([]Proc, error) {
	kps, err := unix.SysctlKinfoProcSlice("kern.proc.uid", os.Getuid())
	if err != nil {
		return nil, err
	}
	procs := make([]Proc, 0, len(kps))
	var marked []int
	for i := range kps {
		p, ok := procFromKinfo(&kps[i])
		if !ok {
			continue
		}
		procs = append(procs, p)
		if p.Marker != "" {
			marked = append(marked, p.PID)
		}
	}
	ports := parseLsofPorts(runLsof(ctx, "-nP", "-F", "pcn", "-a", "-u", strconv.Itoa(os.Getuid()), "-iTCP", "-sTCP:LISTEN"))
	cwds := lsofCwds(ctx, marked)
	for i := range procs {
		procs[i].Ports = ports[procs[i].PID]
		procs[i].Cwd = cwds[procs[i].PID]
	}
	return procs, nil
}

// Inspect 只做单进程的 sysctl 读取（身份核对用，不跑 lsof）。
func (d *darwinOS) Inspect(_ context.Context, pid int) (Proc, bool, error) {
	kp, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if err != nil {
		// 进程已退出：内核对正在退出 / 已不存在的 pid 会返回 ESRCH、ENOENT 或 EIO。
		if errors.Is(err, unix.ESRCH) || errors.Is(err, unix.ENOENT) || errors.Is(err, unix.EIO) {
			return Proc{}, false, nil
		}
		return Proc{}, false, err
	}
	if int(kp.Proc.P_pid) != pid {
		return Proc{}, false, nil
	}
	p, ok := procFromKinfo(kp)
	if !ok {
		return Proc{}, false, nil
	}
	return p, true, nil
}

// Terminate 只对单个 pid 发 SIGTERM / SIGKILL，进程已不存在视为成功。
func (d *darwinOS) Terminate(pid int, force bool) error {
	sig := syscall.SIGTERM
	if force {
		sig = syscall.SIGKILL
	}
	if err := syscall.Kill(pid, sig); err != nil && !errors.Is(err, syscall.ESRCH) {
		return err
	}
	return nil
}

// procFromKinfo 由 KinfoProc 与 procargs2 构造 Proc；procargs2 读不到时 ok=false。
func procFromKinfo(kp *unix.KinfoProc) (Proc, bool) {
	pid := int(kp.Proc.P_pid)
	if pid <= 0 {
		return Proc{}, false
	}
	buf, err := unix.SysctlRaw("kern.procargs2", pid)
	if err != nil {
		return Proc{}, false
	}
	argv, marker, err := parseProcArgs(buf)
	if err != nil {
		return Proc{}, false
	}
	st := kp.Proc.P_starttime
	return Proc{
		PID:     pid,
		PPID:    int(kp.Eproc.Ppid),
		StartMs: int64(st.Sec)*1000 + int64(st.Usec)/1000,
		Argv:    argv,
		Marker:  marker,
	}, true
}

// lsofCwds 用一次 lsof 取一批 pid 的工作目录。
func lsofCwds(ctx context.Context, pids []int) map[int]string {
	if len(pids) == 0 {
		return nil
	}
	ids := make([]string, len(pids))
	for i, p := range pids {
		ids[i] = strconv.Itoa(p)
	}
	return parseLsofCwd(runLsof(ctx, "-nP", "-F", "pfn", "-a", "-d", "cwd", "-p", strings.Join(ids, ",")))
}

// runLsof 运行 lsof 并返回标准输出；lsof 缺失或失败返回 nil。
// 查不到匹配项时 lsof 退出码为 1 但输出仍有效，所以只要有输出就采用。
func runLsof(ctx context.Context, args ...string) []byte {
	out, err := exec.CommandContext(ctx, lsofPath, args...).Output() //nolint:gosec // 固定路径的 lsof，参数由本包拼出
	if err != nil && len(out) == 0 {
		return nil
	}
	return out
}
