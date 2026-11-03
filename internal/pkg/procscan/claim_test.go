package procscan

import (
	"strings"
	"testing"
	"unicode/utf8"

	. "github.com/smartystreets/goconvey/convey"
)

const testConv = "conv-1"

func p(pid, ppid int, marker string, ports ...uint16) Proc {
	return Proc{PID: pid, PPID: ppid, StartMs: int64(pid) * 10, Argv: []string{"cmd", "a"}, Marker: marker, Ports: ports}
}

func pids(l []Listed) []int {
	out := make([]int, 0, len(l))
	for _, x := range l {
		out = append(out, x.PID)
	}
	return out
}

func TestClaim(t *testing.T) {
	Convey("Claim 认领规则", t, func() {
		cli := map[int]struct{}{10: {}}
		Convey("监听端口的带标记进程被列出，且带端口与命令行", func() {
			snap := []Proc{p(10, 1, testConv), p(11, 10, testConv, 3000)}
			got := Claim(snap, testConv, cli)
			So(pids(got), ShouldResemble, []int{11})
			So(got[0].Ports, ShouldResemble, []uint16{3000})
			So(got[0].Command, ShouldEqual, "cmd a")
			So(got[0].StartedAtMs, ShouldEqual, 110)
		})
		Convey("CLI 的轮内前台子进程不列", func() {
			So(Claim([]Proc{p(10, 1, testConv), p(11, 10, testConv)}, testConv, cli), ShouldBeEmpty)
		})
		Convey("已脱离根（父进程不带标记）被列出", func() {
			snap := []Proc{p(20, 1, ""), p(21, 20, testConv)}
			So(pids(Claim(snap, testConv, cli)), ShouldResemble, []int{21})
		})
		Convey("父进程不在快照里也算已脱离", func() {
			So(pids(Claim([]Proc{p(21, 999, testConv)}, testConv, cli)), ShouldResemble, []int{21})
		})
		Convey("过继给宿主（PID 1）的孤儿照常列出", func() {
			snap := []Proc{p(1, 0, ""), p(30, 1, testConv)}
			So(pids(Claim(snap, testConv, cli)), ShouldResemble, []int{30})
		})
		Convey("已脱离进程的子进程不列，除非它监听", func() {
			snap := []Proc{p(30, 1, testConv), p(31, 30, testConv), p(32, 30, testConv, 80)}
			So(pids(Claim(snap, testConv, cli)), ShouldResemble, []int{30, 32})
		})
		Convey("登记的 CLI 即便在监听或已脱离也不列", func() {
			So(Claim([]Proc{p(10, 1, testConv, 8080)}, testConv, cli), ShouldBeEmpty)
		})
		Convey("标记不符或为空的进程不认领", func() {
			snap := []Proc{p(40, 1, "other", 80), p(41, 1, "", 81)}
			So(Claim(snap, testConv, cli), ShouldBeEmpty)
			So(Claim(snap, "", cli), ShouldBeEmpty)
		})
		Convey("nil cliPIDs 不报错；输出按启动时间再 PID 排序", func() {
			a, b := p(51, 1, testConv), p(50, 1, testConv)
			a.StartMs, b.StartMs = 5, 5
			c := p(52, 1, testConv)
			c.StartMs = 1
			So(pids(Claim([]Proc{a, b, c}, testConv, nil)), ShouldResemble, []int{52, 50, 51})
		})
		Convey("超长命令行被截断", func() {
			x := p(60, 1, testConv)
			x.Argv = []string{strings.Repeat("x", 2000)}
			So(len(Claim([]Proc{x}, testConv, nil)[0].Command), ShouldBeLessThanOrEqualTo, 512)
		})
		// 远程会话的命令行走 proto3 string,不合法的 UTF-8 会让整个列表编码失败、这一节消失。
		Convey("截断不切开多字节字符，输出始终是合法 UTF-8", func() {
			x := p(61, 1, testConv)
			x.Argv = []string{strings.Repeat("x", 511) + "中文参数"}
			got := Claim([]Proc{x}, testConv, nil)[0].Command
			So(utf8.ValidString(got), ShouldBeTrue)
			So(len(got), ShouldBeLessThanOrEqualTo, 512)
			So(got, ShouldEqual, strings.Repeat("x", 511))
		})
		Convey("argv 里的非法 UTF-8 字节被替换掉", func() {
			x := p(62, 1, testConv)
			x.Argv = []string{"node", "a\xffb"}
			So(utf8.ValidString(Claim([]Proc{x}, testConv, nil)[0].Command), ShouldBeTrue)
		})
	})
}
