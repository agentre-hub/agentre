//go:build windows

package procscan

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

// 经 PEB 读目标进程的 ppid / 环境标记 / 命令行 / cwd：
// NtQueryInformationProcess(ProcessBasicInformation) → PEB 地址 → ReadProcessMemory 读 PEB
// → ProcessParameters（RTL_USER_PROCESS_PARAMETERS）→ 再读 Environment / CommandLine / CurrentDirectory。
//
// 只支持与本进程同位宽的目标：64 位宿主读 64 位进程、32 位宿主读 32 位进程。
// 位宽不同（64 位宿主遇到 WOW64 的 32 位进程，或反之）直接跳过——读到的标记为空，不会被认领。

// nativeIs64 在 64 位构建上为 1，32 位上为 0。
const nativeIs64 = unsafe.Sizeof(uintptr(0)) / 8

// 本机位宽对应的偏移；下面的编译期断言把它们与 x/sys/windows 的结构体逐一对照，
// 任何一个对不上（差值非 0 或下溢）都会让 GOOS=windows 构建失败。
const (
	nativeProcParams = nativeIs64*pebProcParams64 + (1-nativeIs64)*pebProcParams32
	nativeCurDir     = nativeIs64*upCurDir64 + (1-nativeIs64)*upCurDir32
	nativeCmdLine    = nativeIs64*upCmdLine64 + (1-nativeIs64)*upCmdLine32
	nativeEnv        = nativeIs64*upEnv64 + (1-nativeIs64)*upEnv32
	nativeEnvSize    = nativeIs64*upEnvSize64 + (1-nativeIs64)*upEnvSize32
)

var (
	_ = [1]struct{}{}[unsafe.Offsetof(windows.PEB{}.ProcessParameters)-nativeProcParams]
	_ = [1]struct{}{}[unsafe.Offsetof(windows.RTL_USER_PROCESS_PARAMETERS{}.CurrentDirectory)+unsafe.Offsetof(windows.CURDIR{}.DosPath)-nativeCurDir]
	_ = [1]struct{}{}[unsafe.Offsetof(windows.RTL_USER_PROCESS_PARAMETERS{}.CommandLine)-nativeCmdLine]
	_ = [1]struct{}{}[unsafe.Offsetof(windows.RTL_USER_PROCESS_PARAMETERS{}.Environment)-nativeEnv]
	_ = [1]struct{}{}[unsafe.Offsetof(windows.RTL_USER_PROCESS_PARAMETERS{}.EnvironmentSize)-nativeEnvSize]
	_ = [1]struct{}{}[unsafe.Offsetof(windows.NTUnicodeString{}.Buffer)-unsafe.Sizeof(uintptr(0))]
	_ = [1]struct{}{}[unsafe.Sizeof(windows.PROCESS_BASIC_INFORMATION{})-unsafe.Sizeof(processBasicInformation{})]
)

// processBasicInformation 与 PROCESS_BASIC_INFORMATION 同布局，但全用 uintptr：
// 目标进程的 PEB 地址不能放进 Go 的指针类型字段，否则 GC 可能把它当成堆指针检查。
type processBasicInformation struct {
	ExitStatus                   uintptr
	PebBaseAddress               uintptr
	AffinityMask                 uintptr
	BasePriority                 uintptr
	UniqueProcessID              uintptr
	InheritedFromUniqueProcessID uintptr
}

// maxEnvBytes 是环境块读取上限，防止被异常的 EnvironmentSize 拖去读一大块内存。
const maxEnvBytes = 4 << 20

// pebInfo 是从 PEB 读出的内容；未带标记的进程不读命令行与 cwd。
type pebInfo struct {
	PPID    int
	Marker  string
	CmdLine string
	Cwd     string
}

// readPEBInfo 读 h（需 PROCESS_QUERY_LIMITED_INFORMATION|PROCESS_VM_READ）的 PEB；任一步失败返回 false。
func readPEBInfo(h windows.Handle) (pebInfo, bool) {
	if !sameBitness(h) {
		return pebInfo{}, false
	}
	var pbi processBasicInformation
	// #nosec G103 -- NtQueryInformationProcess 要求传出参数指针；pbi 是全 uintptr 的本地结构。
	if err := windows.NtQueryInformationProcess(h, windows.ProcessBasicInformation,
		unsafe.Pointer(&pbi), uint32(unsafe.Sizeof(pbi)), nil); err != nil {
		return pebInfo{}, false
	}
	layout := pebLayout32
	if nativeIs64 == 1 {
		layout = pebLayout64
	}
	peb, ok := readMem(h, uint64(pbi.PebBaseAddress), layout.pebSize())
	if !ok {
		return pebInfo{}, false
	}
	paramsAddr, ok := layout.paramsAddr(peb)
	if !ok {
		return pebInfo{}, false
	}
	raw, ok := readMem(h, paramsAddr, layout.paramsSize())
	if !ok {
		return pebInfo{}, false
	}
	up, ok := layout.userParams(raw)
	if !ok {
		return pebInfo{}, false
	}
	info := pebInfo{PPID: int(pbi.InheritedFromUniqueProcessID)}
	// EnvironmentSize 为 0（读不到大小）时不猜长度，视为没有标记。
	if up.EnvSize > 0 {
		env, ok := readMem(h, up.Env, int(min(up.EnvSize, maxEnvBytes)))
		if ok {
			info.Marker = envBlockMarker(env)
		}
	}
	if info.Marker == "" {
		return info, true
	}
	info.CmdLine = readRemoteString(h, up.CmdLine)
	info.Cwd = cleanDosCwd(readRemoteString(h, up.Cwd))
	return info, true
}

// sameBitness 判断目标进程与本进程是否同为 WOW64（即同位宽）。
func sameBitness(h windows.Handle) bool {
	var self, target bool
	if err := windows.IsWow64Process(windows.CurrentProcess(), &self); err != nil {
		return false
	}
	if err := windows.IsWow64Process(h, &target); err != nil {
		return false
	}
	return self == target
}

// readRemoteString 读目标进程里的一段 UTF-16 字符串，读不到时为空。
func readRemoteString(h windows.Handle, s remoteString) string {
	if s.Len == 0 || s.Addr == 0 {
		return ""
	}
	b, ok := readMem(h, s.Addr, int(s.Len))
	if !ok {
		return ""
	}
	return decodeUTF16LE(b)
}

// readMem 从目标进程 addr 处读 n 字节；地址为 0、读取失败或读不满都返回 false。
func readMem(h windows.Handle, addr uint64, n int) ([]byte, bool) {
	if addr == 0 || n <= 0 {
		return nil, false
	}
	buf := make([]byte, n)
	var read uintptr
	if err := windows.ReadProcessMemory(h, uintptr(addr), &buf[0], uintptr(n), &read); err != nil || int(read) != n {
		return nil, false
	}
	return buf, true
}
