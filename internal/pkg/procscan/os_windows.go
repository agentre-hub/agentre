//go:build windows

package procscan

import (
	"context"
	"errors"
	"unsafe"

	"golang.org/x/sys/windows"
)

func init() {
	platformOS = func() OS { return &windowsOS{} }
}

// windowsOS 用 Toolhelp32 枚举进程、经 PEB 读环境标记 / 命令行 / cwd、
// 用 GetExtendedTcpTable 取监听端口（纯 Go，无 CGO，不起子进程）。
//
// 已知限制：本进程未提权时，同一用户下以管理员身份运行的进程打不开，会被跳过；
// 与本进程位宽不同的进程（WOW64）同样跳过，见 peb_windows.go。
type windowsOS struct{}

const (
	// stillActive 是 GetExitCodeProcess 对仍在运行的进程给出的 STILL_ACTIVE。
	stillActive = 259
	// openQueryAccess 是读时间、令牌与 PEB 需要的最小权限。
	openQueryAccess = windows.PROCESS_QUERY_LIMITED_INFORMATION | windows.PROCESS_VM_READ
	// tcpTableOwnerPIDListener 是 TCP_TABLE_CLASS 的 TCP_TABLE_OWNER_PID_LISTENER。
	tcpTableOwnerPIDListener = 3
)

var (
	modIphlpapi             = windows.NewLazySystemDLL("iphlpapi.dll")
	procGetExtendedTCPTable = modIphlpapi.NewProc("GetExtendedTcpTable")
)

// Snapshot 返回与本进程同一用户的全部进程；打不开、令牌用户不同或 PEB 读不到的进程被跳过。
// 端口表取不到时端口留空（与 darwin 的 lsof 失败同口径），不让整个扫描失败。
func (w *windowsOS) Snapshot(ctx context.Context) ([]Proc, error) {
	self, err := currentUserSID()
	if err != nil {
		return nil, err
	}
	pids, err := processIDs()
	if err != nil {
		return nil, err
	}
	ports, _ := listeningPorts()
	procs := make([]Proc, 0, len(pids))
	for _, pid := range pids {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		p, ok := inspectPID(pid, self)
		if !ok {
			continue
		}
		p.Ports = ports[p.PID]
		procs = append(procs, p)
	}
	return procs, nil
}

// Inspect 读单个进程（身份核对用，不取端口表）。
func (w *windowsOS) Inspect(_ context.Context, pid int) (Proc, bool, error) {
	self, err := currentUserSID()
	if err != nil {
		return Proc{}, false, err
	}
	p, ok := inspectPID(uint32(pid), self)
	if !ok {
		return Proc{}, false, nil
	}
	return p, true, nil
}

// Terminate 用 TerminateProcess 直接结束单个 pid。Windows 没有可投递给任意进程的
// 「请自行退出」信号，所以 force 与否都是直接终止；Scanner.Kill 的 TERM 宽限轮询会立刻看到它退出。
// 进程已不存在视为成功。
func (w *windowsOS) Terminate(pid int, _ bool) error {
	h, err := windows.OpenProcess(windows.PROCESS_TERMINATE|windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		if errors.Is(err, windows.ERROR_INVALID_PARAMETER) {
			return nil // pid 已不存在
		}
		return err
	}
	defer windows.CloseHandle(h) //nolint:errcheck // 只读句柄的关闭失败无可补救
	if !alive(h) {
		return nil
	}
	if err := windows.TerminateProcess(h, 1); err != nil && alive(h) {
		return err
	}
	return nil
}

// inspectPID 打开 pid 并读出 Proc；进程已退出、不是同一用户或读不到 PEB 时返回 false。
func inspectPID(pid uint32, self *windows.SID) (Proc, bool) {
	if pid == 0 {
		return Proc{}, false
	}
	h, err := windows.OpenProcess(openQueryAccess, false, pid)
	if err != nil {
		return Proc{}, false
	}
	defer windows.CloseHandle(h) //nolint:errcheck // 只读句柄的关闭失败无可补救
	// 句柄还被别人持有时，已退出的进程对象仍能打开，要看退出码。
	if !alive(h) || !sameUser(h, self) {
		return Proc{}, false
	}
	var created, exited, kernel, user windows.Filetime
	if err := windows.GetProcessTimes(h, &created, &exited, &kernel, &user); err != nil {
		return Proc{}, false
	}
	info, ok := readPEBInfo(h)
	if !ok {
		return Proc{}, false
	}
	p := Proc{
		PID:     int(pid),
		PPID:    info.PPID,
		StartMs: filetimeToUnixMs(uint64(created.HighDateTime)<<32 | uint64(created.LowDateTime)),
		Cwd:     info.Cwd,
		Marker:  info.Marker,
	}
	// Windows 的命令行本来就是一整串，不再拆 argv。
	if info.CmdLine != "" {
		p.Argv = []string{info.CmdLine}
	}
	return p, true
}

// alive 判断进程对象是否仍在运行。
func alive(h windows.Handle) bool {
	var code uint32
	return windows.GetExitCodeProcess(h, &code) == nil && code == stillActive
}

// currentUserSID 返回本进程令牌的用户 SID。
func currentUserSID() (*windows.SID, error) {
	tu, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return nil, err
	}
	return tu.User.Sid.Copy()
}

// sameUser 判断 h 的令牌用户是否为 self；令牌打不开时视为不同。
func sameUser(h windows.Handle, self *windows.SID) bool {
	var tok windows.Token
	if err := windows.OpenProcessToken(h, windows.TOKEN_QUERY, &tok); err != nil {
		return false
	}
	defer tok.Close() //nolint:errcheck // 只读令牌的关闭失败无可补救
	tu, err := tok.GetTokenUser()
	if err != nil {
		return false
	}
	return tu.User.Sid.Equals(self)
}

// processIDs 用 Toolhelp32 快照列出全部 pid。
func processIDs() ([]uint32, error) {
	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return nil, err
	}
	defer windows.CloseHandle(snap) //nolint:errcheck // 快照句柄的关闭失败无可补救
	var e windows.ProcessEntry32
	e.Size = uint32(unsafe.Sizeof(e))
	var pids []uint32
	for err = windows.Process32First(snap, &e); err == nil; err = windows.Process32Next(snap, &e) {
		pids = append(pids, e.ProcessID)
	}
	if !errors.Is(err, windows.ERROR_NO_MORE_FILES) {
		return nil, err
	}
	return pids, nil
}

// listeningPorts 取 IPv4 + IPv6 的 LISTEN 表，返回 pid → 端口；某一族失败时其余结果照常返回。
func listeningPorts() (map[int][]uint16, error) {
	ports := map[int][]uint16{}
	errs := make([]error, 0, 2)
	for _, fam := range []struct {
		af  uint32
		add func(map[int][]uint16, []byte) error
	}{{windows.AF_INET, addTCP4Listeners}, {windows.AF_INET6, addTCP6Listeners}} {
		table, err := extendedTCPTable(fam.af)
		if err == nil {
			err = fam.add(ports, table)
		}
		errs = append(errs, err)
	}
	return ports, errors.Join(errs...)
}

// extendedTCPTable 调 GetExtendedTcpTable(TCP_TABLE_OWNER_PID_LISTENER) 取原始表字节；
// 缓冲区不够时按返回的大小重试（表在两次调用之间可能变大）。
func extendedTCPTable(af uint32) ([]byte, error) {
	if err := procGetExtendedTCPTable.Find(); err != nil {
		return nil, err
	}
	size := uint32(16 << 10)
	for range 4 {
		buf := make([]byte, size)
		// Win32 调用约定要求传指针；buf / size 是本函数持有的 Go 内存，转换写在实参里由 Call 保活。
		r, _, _ := procGetExtendedTCPTable.Call(
			uintptr(unsafe.Pointer(&buf[0])), // #nosec G103 -- 见上
			uintptr(unsafe.Pointer(&size)),   // #nosec G103 -- 见上
			0,                                // 不排序
			uintptr(af),
			tcpTableOwnerPIDListener,
			0,
		)
		switch errno := windows.Errno(r); errno {
		case 0:
			return buf, nil
		case windows.ERROR_INSUFFICIENT_BUFFER:
			size += 4 << 10 // 留一点余量给两次调用之间新增的行
		default:
			return nil, errno
		}
	}
	return nil, windows.ERROR_INSUFFICIENT_BUFFER
}
