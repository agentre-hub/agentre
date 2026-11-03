package procscan

import (
	"encoding/binary"
	"testing"
	"unicode/utf16"

	. "github.com/smartystreets/goconvey/convey"
)

// utf16le 把字符串编码成 UTF-16LE 字节（不带结尾 NUL）。
func utf16le(s string) []byte {
	var b []byte
	for _, u := range utf16.Encode([]rune(s)) {
		b = binary.LittleEndian.AppendUint16(b, u)
	}
	return b
}

// envBlock 按 Windows 环境块布局拼样本：每项 K=V\0，整体再以一个 \0 结尾。
func envBlock(entries ...string) []byte {
	var b []byte
	for _, e := range entries {
		b = append(b, utf16le(e)...)
		b = append(b, 0, 0)
	}
	return append(b, 0, 0)
}

// tcp4Row 拼一行 MIB_TCPROW_OWNER_PID（6 个 DWORD）；端口按网络字节序放在 DWORD 低两字节。
func tcp4Row(state uint32, port uint16, pid uint32) []byte {
	b := binary.LittleEndian.AppendUint32(nil, state)
	b = append(b, 127, 0, 0, 1)                    // dwLocalAddr
	b = append(b, byte(port>>8), byte(port), 0, 0) // dwLocalPort
	b = append(b, 0, 0, 0, 0, 0, 0, 0, 0)          // dwRemoteAddr / dwRemotePort
	return binary.LittleEndian.AppendUint32(b, pid)
}

// tcp6Row 拼一行 MIB_TCP6ROW_OWNER_PID（56 字节）。
func tcp6Row(state uint32, port uint16, pid uint32) []byte {
	b := append([]byte(nil), make([]byte, 16)...)   // ucLocalAddr
	b = binary.LittleEndian.AppendUint32(b, 0)      // dwLocalScopeId
	b = append(b, byte(port>>8), byte(port), 0, 0)  // dwLocalPort
	b = append(b, make([]byte, 16+4+4)...)          // ucRemoteAddr / scope / port
	b = binary.LittleEndian.AppendUint32(b, state)  // dwState
	return binary.LittleEndian.AppendUint32(b, pid) // dwOwningPid
}

func tcpTable(rows ...[]byte) []byte {
	b := binary.LittleEndian.AppendUint32(nil, uint32(len(rows)))
	for _, r := range rows {
		b = append(b, r...)
	}
	return b
}

// putUnicodeString 在 buf 的 off 处写一个 UNICODE_STRING{Length, MaximumLength, Buffer}。
func putUnicodeString(buf []byte, off int, ptrSize int, length uint16, addr uint64) {
	binary.LittleEndian.PutUint16(buf[off:], length)
	binary.LittleEndian.PutUint16(buf[off+2:], length+2)
	putPtr(buf, off+ptrSize, ptrSize, addr)
}

func putPtr(buf []byte, off, ptrSize int, v uint64) {
	if ptrSize == 8 {
		binary.LittleEndian.PutUint64(buf[off:], v)
		return
	}
	binary.LittleEndian.PutUint32(buf[off:], uint32(v))
}

func TestEnvBlockMarker(t *testing.T) {
	Convey("环境块 UTF-16 解析只取 AGENTRE_SESSION", t, func() {
		Convey("标记在中间时取出它的值", func() {
			b := envBlock("=C:=C:\\work", "PATH=C:\\Windows", "SECRET=hunter2", "AGENTRE_SESSION=conv-1", "TEMP=C:\\t")
			So(envBlockMarker(b), ShouldEqual, "conv-1")
		})
		Convey("变量名大小写不敏感（Windows 语义），值里含 = 原样保留", func() {
			So(envBlockMarker(envBlock("agentre_session=a=b")), ShouldEqual, "a=b")
		})
		Convey("非 ASCII 值按 UTF-16 正确解码", func() {
			So(envBlockMarker(envBlock("AGENTRE_SESSION=会话-1")), ShouldEqual, "会话-1")
		})
		Convey("没有标记时为空，前缀相同的变量不误中", func() {
			So(envBlockMarker(envBlock("PATH=x", "AGENTRE_SESSION_X=no", "XAGENTRE_SESSION=no")), ShouldEqual, "")
		})
		Convey("截断的块（无结尾 NUL、奇数长度）不越界，已读完的项照常匹配", func() {
			b := envBlock("AGENTRE_SESSION=conv-2")
			So(envBlockMarker(b[:len(b)-2]), ShouldEqual, "conv-2")
			So(envBlockMarker(append(utf16le("AGENTRE_SESSION=conv-3"), 0x41)), ShouldEqual, "conv-3")
			So(envBlockMarker(nil), ShouldEqual, "")
		})
		Convey("块内第一个空项即结束，后面的垃圾不再解析", func() {
			b := append(envBlock("PATH=x"), envBlock("AGENTRE_SESSION=late")...)
			So(envBlockMarker(b), ShouldEqual, "")
		})
	})
}

func TestDecodeUTF16LE(t *testing.T) {
	Convey("UTF-16LE 解码", t, func() {
		So(decodeUTF16LE(utf16le(`C:\工作\app`)), ShouldEqual, `C:\工作\app`)
		So(decodeUTF16LE(append(utf16le("ab"), 0x63)), ShouldEqual, "ab")
		So(decodeUTF16LE(nil), ShouldEqual, "")
	})
}

func TestTCPListeners(t *testing.T) {
	Convey("GetExtendedTcpTable 的 LISTENER 表解析", t, func() {
		Convey("IPv4 行：端口从网络字节序转换，按 pid 归并、排序、去重", func() {
			ports := map[int][]uint16{}
			err := addTCP4Listeners(ports, tcpTable(
				tcp4Row(mibTCPStateListen, 8080, 100),
				tcp4Row(mibTCPStateListen, 3000, 100),
				tcp4Row(mibTCPStateListen, 3000, 100),
				tcp4Row(mibTCPStateListen, 0x1234, 200),
			))
			So(err, ShouldBeNil)
			So(ports, ShouldResemble, map[int][]uint16{100: {3000, 8080}, 200: {0x1234}})
		})
		Convey("IPv6 行与 IPv4 同端口合并；非 LISTEN 状态的行被忽略", func() {
			ports := map[int][]uint16{}
			So(addTCP4Listeners(ports, tcpTable(tcp4Row(mibTCPStateListen, 5173, 7))), ShouldBeNil)
			So(addTCP6Listeners(ports, tcpTable(
				tcp6Row(mibTCPStateListen, 5173, 7),
				tcp6Row(mibTCPStateListen, 443, 7),
				tcp6Row(5, 9999, 8), // ESTABLISHED
			)), ShouldBeNil)
			So(ports, ShouldResemble, map[int][]uint16{7: {443, 5173}})
		})
		Convey("空表不产生任何端口", func() {
			ports := map[int][]uint16{}
			So(addTCP4Listeners(ports, tcpTable()), ShouldBeNil)
			So(ports, ShouldBeEmpty)
		})
		Convey("行数声明超过缓冲区时报错而不越界", func() {
			b := tcpTable(tcp4Row(mibTCPStateListen, 80, 1))
			binary.LittleEndian.PutUint32(b, 2)
			So(addTCP4Listeners(map[int][]uint16{}, b), ShouldNotBeNil)
			So(addTCP6Listeners(map[int][]uint16{}, []byte{1, 0}), ShouldNotBeNil)
		})
	})
}

func TestUserProcessParams(t *testing.T) {
	Convey("PEB / RTL_USER_PROCESS_PARAMETERS 按指针宽度取字段", t, func() {
		for _, l := range []pebLayout{pebLayout64, pebLayout32} {
			Convey(map[int]string{8: "64 位布局", 4: "32 位布局"}[l.ptrSize], func() {
				peb := make([]byte, l.procParams+l.ptrSize)
				putPtr(peb, l.procParams, l.ptrSize, 0x7ff0_1000)
				addr, ok := l.paramsAddr(peb)
				So(ok, ShouldBeTrue)
				So(addr, ShouldEqual, 0x7ff0_1000)
				_, ok = l.paramsAddr(peb[:len(peb)-1])
				So(ok, ShouldBeFalse)

				buf := make([]byte, l.paramsSize())
				putUnicodeString(buf, l.curDir, l.ptrSize, 20, 0x1111)
				putUnicodeString(buf, l.cmdLine, l.ptrSize, 40, 0x2222)
				putPtr(buf, l.env, l.ptrSize, 0x3333)
				putPtr(buf, l.envSize, l.ptrSize, 0x800)
				up, ok := l.userParams(buf)
				So(ok, ShouldBeTrue)
				So(up, ShouldResemble, userParams{
					Cwd:     remoteString{Addr: 0x1111, Len: 20},
					CmdLine: remoteString{Addr: 0x2222, Len: 40},
					Env:     0x3333,
					EnvSize: 0x800,
				})
				_, ok = l.userParams(buf[:len(buf)-1])
				So(ok, ShouldBeFalse)
			})
		}
	})
}

func TestWinHelpers(t *testing.T) {
	Convey("FILETIME 与 cwd 规整", t, func() {
		Convey("FILETIME（1601 起的 100ns）换算成 unix 毫秒", func() {
			So(filetimeToUnixMs(116444736000000000), ShouldEqual, 0)
			So(filetimeToUnixMs(116444736000000000+1_234_5678), ShouldEqual, 1234)
		})
		Convey("cwd 去掉结尾反斜杠，盘符根保留", func() {
			So(cleanDosCwd(`C:\work\app\`), ShouldEqual, `C:\work\app`)
			So(cleanDosCwd(`C:\`), ShouldEqual, `C:\`)
			So(cleanDosCwd(""), ShouldEqual, "")
		})
	})
}
