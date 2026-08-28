package transcriptimport

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	. "github.com/smartystreets/goconvey/convey"
)

// TestResolveLocator 钉住根内定位符解析这条**安全边界**。定位符来自前端,不可信;
// 三个读取器此前各抄一份,其中 codex 那份已经漏了 root == "" 这道防护 —— 判据只留
// 一处,漏一道防护就是所有后端一起漏,也就一定会有人在这里看见红。
func TestResolveLocator(t *testing.T) {
	root := t.TempDir()

	Convey("Given 一个转录根目录", t, func() {
		Convey("When 定位符指向根内, Then 还原成根下的绝对路径", func() {
			abs, err := ResolveLocator(root, Locator("-tmp-fixture/session.jsonl"))
			So(err, ShouldBeNil)
			So(abs, ShouldEqual, filepath.Join(root, "-tmp-fixture", "session.jsonl"))
		})

		Convey("When 定位符用斜杠写多级路径, Then 按本机分隔符还原", func() {
			abs, err := ResolveLocator(root, Locator("sessions/2026/08/20/rollout.jsonl"))
			So(err, ShouldBeNil)
			So(abs, ShouldEqual, filepath.Join(root, "sessions", "2026", "08", "20", "rollout.jsonl"))
		})

		Convey("When 定位符用 .. 逃出根目录, Then 拒绝", func() {
			_, err := ResolveLocator(root, Locator("../../../etc/passwd"))
			So(err, ShouldNotBeNil)
			So(err, ShouldWrap, ErrLocatorEscapes)
		})

		Convey("When 定位符只用一层 .. 逃出根目录, Then 同样拒绝", func() {
			_, err := ResolveLocator(root, Locator(".."))
			So(err, ShouldNotBeNil)
		})

		Convey("When 定位符先下钻再逃出根目录, Then 拒绝(Clean 之后仍在根外)", func() {
			_, err := ResolveLocator(root, Locator("projects/../../outside/session.jsonl"))
			So(err, ShouldNotBeNil)
			So(err, ShouldWrap, ErrLocatorEscapes)
		})

		Convey("When 定位符是绝对路径, Then 拒绝", func() {
			_, err := ResolveLocator(root, Locator("/etc/passwd"))
			So(err, ShouldNotBeNil)
			So(err, ShouldWrap, ErrLocatorInvalid)
		})

		Convey("When 定位符是空串或当前目录, Then 拒绝", func() {
			_, err := ResolveLocator(root, Locator(""))
			So(err, ShouldWrap, ErrLocatorInvalid)
			_, err = ResolveLocator(root, Locator("."))
			So(err, ShouldWrap, ErrLocatorInvalid)
		})
	})

	// 根目录取不到时(拿不到 HOME 之类)必须先于任何路径运算拒绝:否则 root == ""
	// 会让 filepath.Join 把定位符还原成**进程当前目录**下的路径,根内判断随之失效。
	Convey("Given 根目录取不到, When 解析任何定位符, Then 拒绝", t, func() {
		_, err := ResolveLocator("", Locator("session.jsonl"))
		So(err, ShouldWrap, ErrLocatorRootUnavailable)
	})
}

// TestNewRecordScanner 单行上限要够大:内联 tool_result 能到兆级,按 bufio 默认的
// 64KiB 上限会在长行处直接停扫,表现是转录从中间被截断而不报错。
func TestNewRecordScanner(t *testing.T) {
	Convey("Given 一行远超 bufio 默认上限的记录", t, func() {
		long := strings.Repeat("x", 300<<10)
		sc := NewRecordScanner(strings.NewReader(long + "\n第二行\n"))

		Convey("When 逐行扫描, Then 长行完整读出且后续行照常", func() {
			So(sc.Scan(), ShouldBeTrue)
			So(len(sc.Text()), ShouldEqual, len(long))
			So(sc.Scan(), ShouldBeTrue)
			So(sc.Text(), ShouldEqual, "第二行")
			So(sc.Scan(), ShouldBeFalse)
			So(sc.Err(), ShouldBeNil)
		})
	})
}

func TestFirstLine(t *testing.T) {
	Convey("Given 一段用户正文", t, func() {
		Convey("When 有换行, Then 只取首行并去掉两端空白", func() {
			So(FirstLine("  第一行  \n第二行"), ShouldEqual, "第一行")
		})
		Convey("When 没有换行, Then 整段去空白", func() {
			So(FirstLine("  只有一行 "), ShouldEqual, "只有一行")
		})
		Convey("When 是空串, Then 还是空串", func() {
			So(FirstLine(""), ShouldBeBlank)
		})
	})
}

// TestLastRecordTime 回读文件尾取最后一条带时间戳的记录。
func TestLastRecordTime(t *testing.T) {
	recordTime := func(line []byte) time.Time {
		var rec struct {
			Timestamp string `json:"timestamp"`
		}
		if json.Unmarshal(line, &rec) != nil || rec.Timestamp == "" {
			return time.Time{}
		}
		ts, err := time.Parse(time.RFC3339Nano, rec.Timestamp)
		if err != nil {
			return time.Time{}
		}
		return ts
	}
	fallback := time.Date(2026, 8, 26, 1, 0, 0, 0, time.UTC)
	open := func(t *testing.T, body string) *os.File {
		t.Helper()
		path := filepath.Join(t.TempDir(), "session.jsonl")
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		f, err := os.Open(path) // #nosec G304 -- 测试自建的临时文件
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = f.Close() })
		return f
	}

	Convey("Given 一份转录文件", t, func() {
		Convey("When 末尾有带时间戳的记录, Then 取最后一条的时间", func() {
			f := open(t, `{"timestamp":"2026-08-26T01:00:00Z"}`+"\n"+`{"timestamp":"2026-08-26T01:03:08Z"}`+"\n")
			So(LastRecordTime(f, fallback, recordTime).UTC().Format(time.RFC3339), ShouldEqual, "2026-08-26T01:03:08Z")
		})

		Convey("When 末尾是坏行, Then 跳过它继续往前找", func() {
			f := open(t, `{"timestamp":"2026-08-26T01:03:08Z"}`+"\n{not json\n")
			So(LastRecordTime(f, fallback, recordTime).UTC().Format(time.RFC3339), ShouldEqual, "2026-08-26T01:03:08Z")
		})

		Convey("When 一条时间戳都没有, Then 退回 fallback 而不谎报现在", func() {
			f := open(t, "{not json\n{\"type\":\"x\"}\n")
			So(LastRecordTime(f, fallback, recordTime), ShouldEqual, fallback)
		})

		Convey("When 文件为空, Then 退回 fallback", func() {
			f := open(t, "")
			So(LastRecordTime(f, fallback, recordTime), ShouldEqual, fallback)
		})

		Convey("When 文件比回读窗口大, Then 只看尾部那一段", func() {
			body := `{"timestamp":"2026-08-26T00:00:00Z","pad":"` + strings.Repeat("p", ScanTailBytes) + `"}` + "\n" +
				`{"timestamp":"2026-08-26T02:00:00Z"}` + "\n"
			f := open(t, body)
			So(LastRecordTime(f, fallback, recordTime).UTC().Format(time.RFC3339), ShouldEqual, "2026-08-26T02:00:00Z")
		})
	})
}

// TestBuildGaps 缺口按固定次序声明,计数为 0 的种类不出现(UI 拿它渲染提示,
// 出一条 Count=0 的缺口等于凭空吓人一跳)。
func TestBuildGaps(t *testing.T) {
	Convey("Given 各类缺口计数", t, func() {
		Convey("When 四类都非零, Then 按 思维/子代理/未闭合/坏行 的次序声明", func() {
			gaps := BuildGaps(1, 2, 3, 4)
			So(gaps, ShouldResemble, []Gap{
				{Kind: GapThinkingUnavailable, Count: 1},
				{Kind: GapSubagentInternals, Count: 4},
				{Kind: GapUnclosedToolCall, Count: 3},
				{Kind: GapUnparsableRecords, Count: 2},
			})
		})

		Convey("When 某一类为 0, Then 它不出现在声明里", func() {
			gaps := BuildGaps(0, 0, 2, 0)
			So(gaps, ShouldResemble, []Gap{{Kind: GapUnclosedToolCall, Count: 2}})
		})

		Convey("When 全为 0, Then 一条缺口都不出", func() {
			So(BuildGaps(0, 0, 0, 0), ShouldBeNil)
		})
	})
}

// TestWalkChain 从叶子沿父指针回溯出正序的链。
func TestWalkChain(t *testing.T) {
	type node struct{ parent string }
	parentOf := func(n node) string { return n.parent }

	Convey("Given 一份带分支的记录集合", t, func() {
		nodes := map[string]node{
			"a": {parent: ""},
			"b": {parent: "a"},
			"c": {parent: "b"},
			// 被抛弃的分支:同样挂在 b 上,但不在叶子回溯路径上。
			"x": {parent: "b"},
		}

		Convey("When 从叶子回溯, Then 得到正序的链且不含被抛弃的分支", func() {
			So(WalkChain(nodes, "c", parentOf), ShouldResemble, []string{"a", "b", "c"})
		})

		Convey("When 叶子不存在, Then 得到空链而不是 panic", func() {
			So(WalkChain(nodes, "nope", parentOf), ShouldBeEmpty)
		})

		Convey("When 叶子是空串, Then 得到空链", func() {
			So(WalkChain(nodes, "", parentOf), ShouldBeEmpty)
		})

		Convey("When 父指针指向链外, Then 到断点为止", func() {
			So(WalkChain(map[string]node{"b": {parent: "gone"}}, "b", parentOf), ShouldResemble, []string{"b"})
		})
	})

	Convey("Given 父指针成环(理论上不该有), When 回溯, Then 不死循环", t, func() {
		cyclic := map[string]node{"a": {parent: "b"}, "b": {parent: "a"}}
		chain := WalkChain(cyclic, "a", parentOf)
		So(len(chain), ShouldEqual, 2)
	})
}
