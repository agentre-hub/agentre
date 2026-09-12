package diff_test

import (
	"strings"
	"testing"

	. "github.com/smartystreets/goconvey/convey"
	"github.com/stretchr/testify/assert"

	"github.com/agentre-hub/agentre/internal/pkg/diff"
)

func TestFromClaudeCodeEdit(t *testing.T) {
	Convey("Edit · 单行替换", t, func() {
		old := "line A\nline B\nline C\n"
		new_ := "line A\nline B-new\nline C\n"

		p := diff.FromClaudeCodeEdit("/abs/foo.go", old, new_, false)

		So(p.Files, ShouldHaveLength, 1)
		f := p.Files[0]
		So(f.Path, ShouldEqual, "/abs/foo.go")
		So(f.Kind, ShouldEqual, diff.KindModified)
		So(f.Plus, ShouldEqual, 1)
		So(f.Minus, ShouldEqual, 1)
		So(f.Hunks, ShouldHaveLength, 1)

		lines := f.Hunks[0].Lines
		// 上下文 1 行 + 1 del + 1 add + 上下文 1 行
		assert.Equal(t, diff.OpContext, lines[0].Op)
		assert.Equal(t, "line A", lines[0].Text)
		assert.Equal(t, diff.OpDel, lines[1].Op)
		assert.Equal(t, "line B", lines[1].Text)
		assert.Equal(t, diff.OpAdd, lines[2].Op)
		assert.Equal(t, "line B-new", lines[2].Text)
		assert.Equal(t, diff.OpContext, lines[3].Op)
		assert.Equal(t, "line C", lines[3].Text)
	})

	Convey("Edit · old == new(无变更)", t, func() {
		p := diff.FromClaudeCodeEdit("/x.go", "same\n", "same\n", false)
		So(p.Files, ShouldHaveLength, 1)
		So(p.Files[0].Plus, ShouldEqual, 0)
		So(p.Files[0].Minus, ShouldEqual, 0)
		So(p.Files[0].Hunks, ShouldHaveLength, 0)
	})

	Convey("Edit · 纯插入(old 短 new 长)", t, func() {
		p := diff.FromClaudeCodeEdit("/x.go", "a\n", "a\nb\nc\n", false)
		f := p.Files[0]
		So(f.Plus, ShouldEqual, 2)
		So(f.Minus, ShouldEqual, 0)
		So(f.Hunks[0].Lines, ShouldHaveLength, 3)
	})

	Convey("Edit · 纯删除", t, func() {
		p := diff.FromClaudeCodeEdit("/x.go", "a\nb\nc\n", "a\n", false)
		f := p.Files[0]
		So(f.Plus, ShouldEqual, 0)
		So(f.Minus, ShouldEqual, 2)
	})

	Convey("Edit · CRLF 行尾照样能对齐", t, func() {
		p := diff.FromClaudeCodeEdit("/x.go", "a\r\nb\r\n", "a\r\nb-new\r\n", false)
		f := p.Files[0]
		So(f.Plus, ShouldEqual, 1)
		So(f.Minus, ShouldEqual, 1)
	})

	Convey("Edit · unicode 字符正常", t, func() {
		p := diff.FromClaudeCodeEdit("/x.go", "你好\n", "你好世界\n", false)
		f := p.Files[0]
		So(f.Plus, ShouldEqual, 1)
		So(f.Minus, ShouldEqual, 1)
	})

	Convey("Edit · 空 old + 非空 new", t, func() {
		p := diff.FromClaudeCodeEdit("/x.go", "", "a\nb\n", false)
		f := p.Files[0]
		So(f.Plus, ShouldEqual, 2)
		So(f.Minus, ShouldEqual, 0)
	})
}

func TestReplaceAll(t *testing.T) {
	Convey("Edit · replaceAll=true 透传到 File.ReplaceAll", t, func() {
		p := diff.FromClaudeCodeEdit("/x.go", "a", "b", true)
		So(p.Files[0].ReplaceAll, ShouldBeTrue)
	})
}

func TestFromUnifiedDiffString(t *testing.T) {
	Convey("Codex · 标准 hunk", t, func() {
		raw := strings.Join([]string{
			"@@ -10,3 +10,4 @@",
			" ctx-before",
			"-old-line",
			"+new-line",
			"+extra",
			" ctx-after",
		}, "\n")

		p, err := diff.FromUnifiedDiffString("repo/foo.go", diff.KindModified, raw)
		So(err, ShouldBeNil)
		So(p.Files, ShouldHaveLength, 1)
		f := p.Files[0]
		So(f.Path, ShouldEqual, "repo/foo.go")
		So(f.Plus, ShouldEqual, 2)
		So(f.Minus, ShouldEqual, 1)
		So(f.Hunks, ShouldHaveLength, 1)
		So(f.Hunks[0].OldStart, ShouldEqual, 10)
		So(f.Hunks[0].NewStart, ShouldEqual, 10)
		So(f.Hunks[0].Lines, ShouldHaveLength, 5)
	})

	Convey("Codex · 多 hunk", t, func() {
		raw := strings.Join([]string{
			"@@ -1,1 +1,1 @@",
			"-a",
			"+A",
			"@@ -10,1 +10,1 @@",
			"-b",
			"+B",
		}, "\n")
		p, err := diff.FromUnifiedDiffString("x.go", diff.KindModified, raw)
		So(err, ShouldBeNil)
		So(p.Files[0].Hunks, ShouldHaveLength, 2)
		So(p.Files[0].Plus, ShouldEqual, 2)
		So(p.Files[0].Minus, ShouldEqual, 2)
	})

	Convey("Codex · 空 diff 字符串", t, func() {
		p, err := diff.FromUnifiedDiffString("x.go", diff.KindModified, "")
		So(err, ShouldBeNil)
		So(p.Files[0].Hunks, ShouldHaveLength, 0)
		So(p.Files[0].Plus, ShouldEqual, 0)
	})

	Convey("Codex · 不合法 hunk header 报错", t, func() {
		_, err := diff.FromUnifiedDiffString("x.go", diff.KindModified, "@@ garbage @@\n+x\n")
		So(err, ShouldNotBeNil)
	})
}

func TestTruncation(t *testing.T) {
	Convey("Edit · 超过 MaxLinesPerFile 时 Truncated=true", t, func() {
		var oldB, newB strings.Builder
		for i := 0; i < 250; i++ {
			oldB.WriteString("line\n")
			newB.WriteString("line-new\n")
		}
		p := diff.FromClaudeCodeEdit("/x.go", oldB.String(), newB.String(), false)
		So(p.Files[0].Truncated, ShouldBeTrue)
		So(len(p.Files[0].Hunks[0].Lines), ShouldBeLessThanOrEqualTo, diff.MaxLinesPerFile)
	})

	Convey("Edit · 正好 MaxLinesPerFile 不截断", t, func() {
		var oldB, newB strings.Builder
		for i := 0; i < 100; i++ {
			oldB.WriteString("a\n")
			newB.WriteString("b\n")
		}
		p := diff.FromClaudeCodeEdit("/x.go", oldB.String(), newB.String(), false)
		// 100 del + 100 add = 200 lines = MaxLinesPerFile, 不截断
		So(p.Files[0].Truncated, ShouldBeFalse)
		So(len(p.Files[0].Hunks[0].Lines), ShouldEqual, 200)
	})
}

func TestFromEdit(t *testing.T) {
	Convey("FromEdit · 单 file 单 hunk", t, func() {
		input := map[string]any{
			"file_path":  "/x.go",
			"old_string": "a\n",
			"new_string": "b\n",
		}
		p := diff.FromEdit(input)
		So(p.Files, ShouldHaveLength, 1)
		So(p.Files[0].Path, ShouldEqual, "/x.go")
	})
}

func TestFromMultiEdit(t *testing.T) {
	Convey("FromMultiEdit · 串联多 edits 进单 File", t, func() {
		input := map[string]any{
			"file_path": "/x.go",
			"edits": []any{
				map[string]any{"old_string": "a", "new_string": "A"},
				map[string]any{"old_string": "b", "new_string": "B"},
			},
		}
		p := diff.FromMultiEdit(input)
		So(p.Files, ShouldHaveLength, 1)
		So(p.Files[0].Plus+p.Files[0].Minus, ShouldBeGreaterThan, 0)
	})
}

func TestFromPiEdit(t *testing.T) {
	Convey("FromPiEdit · path + edits[oldText/newText] 串联进单 File", t, func() {
		input := map[string]any{
			"path": "/x.go",
			"edits": []any{
				map[string]any{"oldText": "a\n", "newText": "A\n"},
				map[string]any{"oldText": "b\n", "newText": "B\n"},
			},
		}
		p := diff.FromPiEdit(input)
		So(p.Files, ShouldHaveLength, 1)
		So(p.Files[0].Path, ShouldEqual, "/x.go")
		So(p.Files[0].Kind, ShouldEqual, diff.KindModified)
		So(p.Files[0].Plus, ShouldEqual, 2)
		So(p.Files[0].Minus, ShouldEqual, 2)
		So(p.Files[0].Hunks, ShouldHaveLength, 2)
	})

	Convey("FromPiEdit · edits 缺失仍返单 File(0 hunks)", t, func() {
		p := diff.FromPiEdit(map[string]any{"path": "/x.go"})
		So(p.Files, ShouldHaveLength, 1)
		So(p.Files[0].Hunks, ShouldBeEmpty)
	})

	Convey("FromPiEdit · claudecode 键名不串味(old_string 不被认)", t, func() {
		p := diff.FromPiEdit(map[string]any{
			"path":  "/x.go",
			"edits": []any{map[string]any{"old_string": "a", "new_string": "A"}},
		})
		So(p.Files, ShouldHaveLength, 1)
		So(p.Files[0].Hunks, ShouldBeEmpty)
	})
}

func TestFromFileChange(t *testing.T) {
	Convey("FromFileChange · Codex 多文件返多 File", t, func() {
		input := map[string]any{
			"type": "fileChange",
			"changes": []any{
				map[string]any{
					"path": "a.go",
					"kind": "update",
					"diff": "@@ -1,1 +1,1 @@\n-a\n+A\n",
				},
				map[string]any{
					"path": "b.go",
					"kind": "add",
					"diff": "@@ -0,0 +1,1 @@\n+B\n",
				},
			},
		}
		p, ok := diff.FromFileChange(input)
		So(ok, ShouldBeTrue)
		So(p.Files, ShouldHaveLength, 2)
		So(p.Files[0].Kind, ShouldEqual, diff.KindModified)
		So(p.Files[1].Kind, ShouldEqual, diff.KindCreated)
	})

	Convey("FromFileChange · changes 为空返 false", t, func() {
		_, ok := diff.FromFileChange(map[string]any{"type": "fileChange"})
		So(ok, ShouldBeFalse)
	})

	Convey("FromFileChange · Codex add kind object treats diff as new file content", t, func() {
		p, ok := diff.FromFileChange(map[string]any{
			"changes": []any{
				map[string]any{
					"path": "/tmp/hello",
					"kind": map[string]any{"type": "add"},
					"diff": "Hello\n",
				},
			},
		})

		So(ok, ShouldBeTrue)
		So(p.Files, ShouldHaveLength, 1)
		f := p.Files[0]
		So(f.Path, ShouldEqual, "/tmp/hello")
		So(f.Kind, ShouldEqual, diff.KindCreated)
		So(f.Plus, ShouldEqual, 1)
		So(f.Minus, ShouldEqual, 0)
		So(f.Hunks, ShouldHaveLength, 1)
		So(f.Hunks[0].OldStart, ShouldEqual, 0)
		So(f.Hunks[0].OldLines, ShouldEqual, 0)
		So(f.Hunks[0].NewStart, ShouldEqual, 1)
		So(f.Hunks[0].NewLines, ShouldEqual, 1)
		So(f.Hunks[0].Lines, ShouldHaveLength, 1)
		So(f.Hunks[0].Lines[0].Op, ShouldEqual, diff.OpAdd)
		So(f.Hunks[0].Lines[0].New, ShouldEqual, 1)
		So(f.Hunks[0].Lines[0].Text, ShouldEqual, "Hello")
	})

	Convey("FromFileChange · Codex delete kind object treats diff as old file content", t, func() {
		p, ok := diff.FromFileChange(map[string]any{
			"changes": []any{
				map[string]any{
					"path": "/tmp/old",
					"kind": map[string]any{"type": "delete"},
					"diff": "old\n",
				},
			},
		})

		So(ok, ShouldBeTrue)
		So(p.Files, ShouldHaveLength, 1)
		f := p.Files[0]
		So(f.Kind, ShouldEqual, diff.KindDeleted)
		So(f.Plus, ShouldEqual, 0)
		So(f.Minus, ShouldEqual, 1)
		So(f.Hunks, ShouldHaveLength, 1)
		So(f.Hunks[0].OldStart, ShouldEqual, 1)
		So(f.Hunks[0].OldLines, ShouldEqual, 1)
		So(f.Hunks[0].NewStart, ShouldEqual, 0)
		So(f.Hunks[0].NewLines, ShouldEqual, 0)
		So(f.Hunks[0].Lines[0].Op, ShouldEqual, diff.OpDel)
		So(f.Hunks[0].Lines[0].Old, ShouldEqual, 1)
		So(f.Hunks[0].Lines[0].Text, ShouldEqual, "old")
	})
}
