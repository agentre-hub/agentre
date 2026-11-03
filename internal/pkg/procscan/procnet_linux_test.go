//go:build linux

package procscan

import (
	"testing"

	. "github.com/smartystreets/goconvey/convey"
)

func TestParseStat(t *testing.T) {
	Convey("解析 /proc/<pid>/stat", t, func() {
		Convey("comm 含空格与括号时仍按最后一个右括号切分", func() {
			// 第 22 个字段 starttime = 123456
			line := "4242 (my (weird) app) S 1 4242 4242 0 -1 4194560 100 0 0 0 1 2 0 0 20 0 3 0 123456 1000 50 18446744073709551615"
			st, err := parseStat(line)
			So(err, ShouldBeNil)
			So(st.State, ShouldEqual, 'S')
			So(st.PPID, ShouldEqual, 1)
			So(st.StartTicks, ShouldEqual, uint64(123456))
		})
		Convey("僵尸进程状态为 Z", func() {
			st, err := parseStat("7 (x) Z 1 7 7 0 -1 0 0 0 0 0 0 0 0 0 20 0 1 0 99 0 0")
			So(err, ShouldBeNil)
			So(st.State, ShouldEqual, 'Z')
		})
		Convey("字段不足或没有括号返回错误", func() {
			_, err := parseStat("4242 (x) S 1 2")
			So(err, ShouldNotBeNil)
			_, err = parseStat("garbage")
			So(err, ShouldNotBeNil)
		})
	})
}

func TestStartMs(t *testing.T) {
	Convey("启动时间 = btime + ticks/100 秒", t, func() {
		So(startMs(1700000000, 250), ShouldEqual, int64(1700000002500))
	})
}

func TestParseBtime(t *testing.T) {
	Convey("从 /proc/stat 取 btime", t, func() {
		b, err := parseBtime([]byte("cpu  1 2 3\nbtime 1700000000\nprocesses 5\n"))
		So(err, ShouldBeNil)
		So(b, ShouldEqual, int64(1700000000))
		_, err = parseBtime([]byte("cpu 1\n"))
		So(err, ShouldNotBeNil)
	})
}

func TestMarkerFromEnviron(t *testing.T) {
	Convey("只保留 AGENTRE_SESSION，其余环境变量丢弃", t, func() {
		blob := []byte("PATH=/usr/bin\x00SECRET=hunter2\x00AGENTRE_SESSION=conv-1\x00HOME=/root\x00")
		So(markerFromEnviron(blob), ShouldEqual, "conv-1")
		So(markerFromEnviron([]byte("PATH=/bin\x00")), ShouldEqual, "")
		So(markerFromEnviron([]byte("XAGENTRE_SESSION=no\x00")), ShouldEqual, "")
		So(markerFromEnviron(nil), ShouldEqual, "")
	})
}

func TestParseCmdline(t *testing.T) {
	Convey("cmdline 按 NUL 切分", t, func() {
		So(parseCmdline([]byte("node\x00server.js\x00--port\x003000\x00")), ShouldResemble, []string{"node", "server.js", "--port", "3000"})
		So(parseCmdline(nil), ShouldBeEmpty)
	})
}

const sampleTCP = `  sl  local_address rem_address   st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode
   0: 0100007F:0BB8 00000000:0000 0A 00000000:00000000 00:00000000 00000000  1000        0 11111 1 0000000000000000 100 0 0 10 0
   1: 0100007F:1F90 0100007F:D2F0 01 00000000:00000000 00:00000000 00000000  1000        0 22222 1 0000000000000000 100 0 0 10 0
   2: 00000000:0050 00000000:0000 0A 00000000:00000000 00:00000000 00000000     0        0 33333 1 0000000000000000 100 0 0 10 0
`

const sampleTCP6 = `  sl  local_address                         remote_address                        st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode
   0: 00000000000000000000000001000000:1F91 00000000000000000000000000000000:0000 0A 00000000:00000000 00:00000000 00000000  1000        0 44444 1 0000000000000000 100 0 0 10 0
   1: 00000000000000000000000001000000:1F92 00000000000000000000000001000000:9999 06 00000000:00000000 00:00000000 00000000  1000        0 55555 1 0000000000000000 100 0 0 10 0
`

func TestParseNetTCP(t *testing.T) {
	Convey("只取 LISTEN(0A) 行，映射 inode → 端口", t, func() {
		got := parseNetTCP([]byte(sampleTCP))
		So(got, ShouldResemble, map[uint64]uint16{11111: 3000, 33333: 80})
	})
	Convey("tcp6 同样解析，非 LISTEN 行忽略", t, func() {
		got := parseNetTCP([]byte(sampleTCP6))
		So(got, ShouldResemble, map[uint64]uint16{44444: 8081})
	})
	Convey("坏行与空输入不 panic", t, func() {
		So(parseNetTCP([]byte("header\nbad line\n   0: zzzz 0A\n")), ShouldBeEmpty)
		So(parseNetTCP(nil), ShouldBeEmpty)
	})
}

func TestParseSocketLink(t *testing.T) {
	Convey("fd 链接 socket:[inode]", t, func() {
		ino, ok := parseSocketLink("socket:[12345]")
		So(ok, ShouldBeTrue)
		So(ino, ShouldEqual, uint64(12345))
		for _, s := range []string{"pipe:[1]", "/dev/null", "socket:[x]", "socket:[]", ""} {
			_, ok = parseSocketLink(s)
			So(ok, ShouldBeFalse)
		}
	})
}
