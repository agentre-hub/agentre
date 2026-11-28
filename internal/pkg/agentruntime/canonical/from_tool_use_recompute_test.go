package canonical

import (
	"testing"

	. "github.com/smartystreets/goconvey/convey"
)

// 持久化后重算(FromToolUse)与实时识别必须同口径:这里钉住 Hermes 与 ACP 两家。
func TestFromToolUse_HermesFileTools(t *testing.T) {
	Convey("Hermes 落库的 write_file / patch 重算时被识别", t, func() {
		Convey("write_file → FileWrite", func() {
			c, ok := FromToolUse("write_file", map[string]any{"path": "/wt/a.go", "content": "x\n"})
			So(ok, ShouldBeTrue)
			So(c.(FileWrite).Path, ShouldEqual, "/wt/a.go")
		})
		Convey("write_file 没有 content → 不识别", func() {
			_, ok := FromToolUse("write_file", map[string]any{"path": "/wt/a.go"})
			So(ok, ShouldBeFalse)
		})
		Convey("patch mode=replace → FileEdit", func() {
			c, ok := FromToolUse("patch", map[string]any{
				"mode": "replace", "path": "/wt/b.go", "old_string": "a", "new_string": "b",
			})
			So(ok, ShouldBeTrue)
			So(c.(FileEdit).Files[0].Path, ShouldEqual, "/wt/b.go")
		})
		Convey("patch 其它 mode → 不识别", func() {
			_, ok := FromToolUse("patch", map[string]any{
				"mode": "append", "path": "/wt/b.go", "old_string": "a", "new_string": "b",
			})
			So(ok, ShouldBeFalse)
		})
	})
}
