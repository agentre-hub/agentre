package procscan

// 本文件是 Windows 实现用到的纯字节解析：不带 build tag、不碰系统调用，
// 这样固定样本单测在 darwin / linux CI 上也能跑。系统调用胶水在 os_windows.go / peb_windows.go。

import (
	"encoding/binary"
	"errors"
	"slices"
	"strings"
	"unicode/utf16"
)

// mibTCPStateListen 是 MIB_TCP_STATE_LISTEN。
const mibTCPStateListen = 2

// MIB_TCPROW_OWNER_PID：dwState, dwLocalAddr, dwLocalPort, dwRemoteAddr, dwRemotePort, dwOwningPid。
const (
	tcp4RowSize   = 24
	tcp4StateOff  = 0
	tcp4PortOff   = 8
	tcp4PIDOff    = 20
	tcpTableHdrSz = 4 // dwNumEntries；行内全是 DWORD / 字节数组，无额外对齐填充
)

// MIB_TCP6ROW_OWNER_PID：ucLocalAddr[16], dwLocalScopeId, dwLocalPort, ucRemoteAddr[16],
// dwRemoteScopeId, dwRemotePort, dwState, dwOwningPid。
const (
	tcp6RowSize  = 56
	tcp6PortOff  = 20
	tcp6StateOff = 48
	tcp6PIDOff   = 52
)

// errShortTCPTable 表示表头声明的行数超出了缓冲区。
var errShortTCPTable = errors.New("procscan: truncated tcp table")

// addTCP4Listeners 把 AF_INET 的 TCP_TABLE_OWNER_PID_LISTENER 表并入 ports（pid → 端口）。
func addTCP4Listeners(ports map[int][]uint16, table []byte) error {
	return addTCPListeners(ports, table, tcp4RowSize, tcp4StateOff, tcp4PortOff, tcp4PIDOff)
}

// addTCP6Listeners 同 addTCP4Listeners，处理 AF_INET6 的表。
func addTCP6Listeners(ports map[int][]uint16, table []byte) error {
	return addTCPListeners(ports, table, tcp6RowSize, tcp6StateOff, tcp6PortOff, tcp6PIDOff)
}

func addTCPListeners(ports map[int][]uint16, table []byte, rowSize, stateOff, portOff, pidOff int) error {
	if len(table) < tcpTableHdrSz {
		return errShortTCPTable
	}
	n := int(binary.LittleEndian.Uint32(table))
	rows := table[tcpTableHdrSz:]
	if n < 0 || n > len(rows)/rowSize {
		return errShortTCPTable
	}
	for i := 0; i < n; i++ {
		row := rows[i*rowSize : (i+1)*rowSize]
		if binary.LittleEndian.Uint32(row[stateOff:]) != mibTCPStateListen {
			continue
		}
		// dwLocalPort 的低两字节是网络字节序（大端）的端口号。
		port := binary.BigEndian.Uint16(row[portOff:])
		pid := int(binary.LittleEndian.Uint32(row[pidOff:]))
		if !slices.Contains(ports[pid], port) {
			ports[pid] = append(ports[pid], port)
			slices.Sort(ports[pid])
		}
	}
	return nil
}

// envBlockMarker 从 UTF-16LE 环境块（K=V\0K=V\0\0）里只取出 AGENTRE_SESSION 的值。
// Windows 变量名大小写不敏感；以 = 开头的盘符 cwd 项（=C:=C:\x）名字里那个 = 不算分隔符。
// 其余变量只在本函数内过一遍，不保存、不返回。
func envBlockMarker(block []byte) string {
	for len(block) >= 2 {
		end := 0
		for end+1 < len(block) && (block[end] != 0 || block[end+1] != 0) {
			end += 2
		}
		if end == 0 {
			return "" // 空项即块尾
		}
		// end > 0 保证 entry 非空；首字符可能就是 =（盘符 cwd 项），从第二个字符起找分隔符。
		entry := decodeUTF16LE(block[:end])
		if name, value, ok := strings.Cut(entry[1:], "="); ok && strings.EqualFold(entry[:1]+name, MarkerEnv) {
			return value
		}
		block = block[min(end+2, len(block)):]
	}
	return ""
}

// decodeUTF16LE 解码 UTF-16LE 字节；结尾多出的单个字节忽略。
func decodeUTF16LE(b []byte) string {
	u := make([]uint16, len(b)/2)
	for i := range u {
		u[i] = binary.LittleEndian.Uint16(b[2*i:])
	}
	return string(utf16.Decode(u))
}

// pebLayout 是 PEB 与 RTL_USER_PROCESS_PARAMETERS 里本包用到的字段偏移，随目标进程指针宽度不同。
// 64 位偏移由 peb_windows.go 在 amd64 / arm64 上对照 x/sys/windows 的结构体做编译期校验，
// 32 位偏移在 386 上同样校验。
type pebLayout struct {
	ptrSize int
	// procParams 是 PEB.ProcessParameters 的偏移。
	procParams int
	// 以下是 RTL_USER_PROCESS_PARAMETERS 内的偏移：CurrentDirectory.DosPath、CommandLine
	// 两个 UNICODE_STRING，Environment 指针，EnvironmentSize。
	curDir, cmdLine, env, envSize int
}

const (
	pebProcParams64 = 0x20
	upCurDir64      = 0x38
	upCmdLine64     = 0x70
	upEnv64         = 0x80
	upEnvSize64     = 0x3f0

	pebProcParams32 = 0x10
	upCurDir32      = 0x24
	upCmdLine32     = 0x40
	upEnv32         = 0x48
	upEnvSize32     = 0x290
)

var (
	pebLayout64 = pebLayout{ptrSize: 8, procParams: pebProcParams64, curDir: upCurDir64, cmdLine: upCmdLine64, env: upEnv64, envSize: upEnvSize64}
	pebLayout32 = pebLayout{ptrSize: 4, procParams: pebProcParams32, curDir: upCurDir32, cmdLine: upCmdLine32, env: upEnv32, envSize: upEnvSize32}
)

// remoteString 是目标进程地址空间里的一段 UTF-16 字符串（Len 为字节数）。
type remoteString struct {
	Addr uint64
	Len  uint16
}

// userParams 是从 RTL_USER_PROCESS_PARAMETERS 取出的远端地址，内容需再读一次目标进程内存。
type userParams struct {
	Cwd, CmdLine remoteString
	Env, EnvSize uint64
}

// pebSize 是读 PEB 时需要的字节数（到 ProcessParameters 为止）。
func (l pebLayout) pebSize() int { return l.procParams + l.ptrSize }

// paramsSize 是读 RTL_USER_PROCESS_PARAMETERS 时需要的字节数（到 EnvironmentSize 为止）。
func (l pebLayout) paramsSize() int { return l.envSize + l.ptrSize }

// paramsAddr 从 PEB 字节里取 ProcessParameters 指针。
func (l pebLayout) paramsAddr(peb []byte) (uint64, bool) {
	if len(peb) < l.pebSize() {
		return 0, false
	}
	return l.ptr(peb, l.procParams), true
}

// userParams 从 RTL_USER_PROCESS_PARAMETERS 字节里取 cwd / 命令行 / 环境块的位置。
func (l pebLayout) userParams(b []byte) (userParams, bool) {
	if len(b) < l.paramsSize() {
		return userParams{}, false
	}
	return userParams{
		Cwd:     l.unicodeString(b, l.curDir),
		CmdLine: l.unicodeString(b, l.cmdLine),
		Env:     l.ptr(b, l.env),
		EnvSize: l.ptr(b, l.envSize),
	}, true
}

// unicodeString 读 UNICODE_STRING{Length u16, MaximumLength u16, [pad], Buffer ptr}。
func (l pebLayout) unicodeString(b []byte, off int) remoteString {
	return remoteString{Len: binary.LittleEndian.Uint16(b[off:]), Addr: l.ptr(b, off+l.ptrSize)}
}

func (l pebLayout) ptr(b []byte, off int) uint64 {
	if l.ptrSize == 8 {
		return binary.LittleEndian.Uint64(b[off:])
	}
	return uint64(binary.LittleEndian.Uint32(b[off:]))
}

// filetimeEpochDelta 是 1601-01-01 到 1970-01-01 之间的 100ns 个数。
const filetimeEpochDelta = 116444736000000000

// filetimeToUnixMs 把 FILETIME（1601 起的 100ns 计数）换算成 unix 毫秒。
func filetimeToUnixMs(ft uint64) int64 {
	return (int64(ft) - filetimeEpochDelta) / 10000
}

// cleanDosCwd 去掉 CurrentDirectory.DosPath 结尾的反斜杠（C:\x\ → C:\x），盘符根 C:\ 保留。
func cleanDosCwd(s string) string {
	if len(s) > 3 && strings.HasSuffix(s, `\`) {
		return strings.TrimSuffix(s, `\`)
	}
	return s
}
