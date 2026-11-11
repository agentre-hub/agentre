package memory_entity

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/cago-frame/cago/pkg/utils/httputils"
	"github.com/smartystreets/goconvey/convey"

	"github.com/agentre-hub/agentre/internal/pkg/code"
)

func codeOf(err error) int {
	var e *httputils.Error
	if errors.As(err, &e) {
		return e.Code
	}
	return 0
}

func TestCheckContent(t *testing.T) {
	ctx := context.Background()
	convey.Convey("记忆内容检查", t, func() {
		convey.Convey("摘要 200 字、详情 4000 字恰好在上限内时通过", func() {
			convey.So(CheckContent(ctx, strings.Repeat("记", SummaryMaxChars), strings.Repeat("详", DetailMaxChars)), convey.ShouldBeNil)
		})
		convey.Convey("详情可以多行", func() {
			convey.So(CheckContent(ctx, "用 pnpm", "第一行\n第二行\t缩进"), convey.ShouldBeNil)
		})
		convey.Convey("摘要为空被拒", func() {
			convey.So(codeOf(CheckContent(ctx, "", "x")), convey.ShouldEqual, code.MemorySummaryRequired)
		})
		convey.Convey("摘要超过 200 字被拒", func() {
			convey.So(codeOf(CheckContent(ctx, strings.Repeat("记", SummaryMaxChars+1), "")), convey.ShouldEqual, code.MemorySummaryTooLong)
		})
		convey.Convey("摘要含换行被拒", func() {
			convey.So(codeOf(CheckContent(ctx, "a\nb", "")), convey.ShouldEqual, code.MemorySummaryMultiline)
			convey.So(codeOf(CheckContent(ctx, "a\rb", "")), convey.ShouldEqual, code.MemorySummaryMultiline)
		})
		convey.Convey("详情超过 4000 字被拒", func() {
			convey.So(codeOf(CheckContent(ctx, "a", strings.Repeat("详", DetailMaxChars+1))), convey.ShouldEqual, code.MemoryDetailTooLong)
		})
		convey.Convey("不可见字符被拒（摘要或详情）", func() {
			for _, s := range []string{"a\u200bb", "a\u202eb", "\ufeffa", "a\u2060b", "a\x00b", "a\u00adb"} {
				convey.So(codeOf(CheckContent(ctx, s, "")), convey.ShouldEqual, code.MemoryInvisibleChars)
				convey.So(codeOf(CheckContent(ctx, "ok", s)), convey.ShouldEqual, code.MemoryInvisibleChars)
			}
		})
		convey.Convey("明显的凭据格式被拒", func() {
			for _, s := range []string{
				"-----BEGIN OPENSSH PRIVATE KEY-----",
				"-----BEGIN RSA PRIVATE KEY-----",
				"key is sk-ant-api03-abcdefghijklmnopqrstuvwx",
				"token ghp_abcdefghijklmnopqrstuvwxyz0123456789",
				"github_pat_11ABCDEFG0123456789_abcdefghij",
				"AKIAIOSFODNN7EXAMPLE",
				"xoxb-123456789012-abcdefghijkl",
				"glpat-abcdefghij0123456789",
				"AIzaSyA1234567890abcdefghijklmnopqrstuv",
			} {
				convey.So(codeOf(CheckContent(ctx, "ok", s)), convey.ShouldEqual, code.MemorySensitiveContent)
			}
			convey.So(codeOf(CheckContent(ctx, "用 sk-ant-api03-abcdefghijklmnopqrstuvwx 调用", "")), convey.ShouldEqual, code.MemorySensitiveContent)
		})
		convey.Convey("形似前缀但不是凭据的普通文字通过", func() {
			convey.So(CheckContent(ctx, "用 sk-learn 训练；task-runner 走 ghp 目录", ""), convey.ShouldBeNil)
		})
	})
}

func TestOwnerValid(t *testing.T) {
	convey.Convey("归属合法性", t, func() {
		convey.So(Owner{Kind: OwnerGlobal}.Valid(), convey.ShouldBeTrue)
		convey.So(Owner{Kind: OwnerGlobal, ID: 3}.Valid(), convey.ShouldBeFalse)
		convey.So(Owner{Kind: OwnerDepartment, ID: 3}.Valid(), convey.ShouldBeTrue)
		convey.So(Owner{Kind: OwnerAgent, ID: 0}.Valid(), convey.ShouldBeFalse)
		convey.So(Owner{Kind: OwnerProject, ID: 9}.Valid(), convey.ShouldBeTrue)
		convey.So(Owner{Kind: "team", ID: 9}.Valid(), convey.ShouldBeFalse)
	})
}

func TestActorValid(t *testing.T) {
	convey.Convey("写入者合法性", t, func() {
		convey.So(UserActor().Valid(), convey.ShouldBeTrue)
		convey.So(AIActor(3, 7).Valid(), convey.ShouldBeTrue)
		convey.So(AIActor(0, 7).Valid(), convey.ShouldBeFalse)
		convey.So(AIActor(3, 0).Valid(), convey.ShouldBeFalse)
		convey.So(Actor{Kind: "robot"}.Valid(), convey.ShouldBeFalse)
	})
}

func TestMemoryLifecycle(t *testing.T) {
	agent := Owner{Kind: OwnerAgent, ID: 5}
	ai := AIActor(5, 70)
	convey.Convey("一条记忆的状态变化", t, func() {
		m := NewMemory(agent, "用 pnpm", "", ai, 100)
		convey.So(m.Owner(), convey.ShouldResemble, agent)
		convey.So(m.State, convey.ShouldEqual, StateNormal)
		convey.So(m.Creator(), convey.ShouldResemble, ai)
		convey.So(m.LastChange(), convey.ShouldResemble, Change{Action: ActionCreated, Actor: ai, At: 100})
		convey.So(m.HasPrevious(), convey.ShouldBeFalse)

		convey.Convey("修改保留上一版，记下谁改的", func() {
			changed := m.Edit("用 pnpm 10", "锁文件在根目录", UserActor(), 200)
			convey.So(changed, convey.ShouldBeTrue)
			convey.So(m.Summary, convey.ShouldEqual, "用 pnpm 10")
			convey.So(m.PrevSummary, convey.ShouldEqual, "用 pnpm")
			convey.So(m.PrevDetail, convey.ShouldEqual, "")
			convey.So(m.HasPrevious(), convey.ShouldBeTrue)
			convey.So(m.LastChange(), convey.ShouldResemble, Change{Action: ActionEdited, Actor: UserActor(), At: 200})
			convey.So(m.Creator(), convey.ShouldResemble, ai)

			convey.Convey("再改一次只保留紧邻的上一版", func() {
				m.Edit("用 pnpm 11", "锁文件在根目录", ai, 300)
				convey.So(m.PrevSummary, convey.ShouldEqual, "用 pnpm 10")
				convey.So(m.PrevDetail, convey.ShouldEqual, "锁文件在根目录")
			})

			convey.Convey("恢复上一版是对调，再恢复一次回到修改后", func() {
				convey.So(m.RestorePrevious(UserActor(), 300), convey.ShouldBeTrue)
				convey.So(m.Summary, convey.ShouldEqual, "用 pnpm")
				convey.So(m.Detail, convey.ShouldEqual, "")
				convey.So(m.PrevSummary, convey.ShouldEqual, "用 pnpm 10")
				convey.So(m.PrevDetail, convey.ShouldEqual, "锁文件在根目录")
				convey.So(m.LastChange().Action, convey.ShouldEqual, ActionRestored)
				m.RestorePrevious(UserActor(), 400)
				convey.So(m.Summary, convey.ShouldEqual, "用 pnpm 10")
			})
		})

		convey.Convey("内容未变的修改不覆盖上一版", func() {
			convey.So(m.Edit("用 pnpm", "", UserActor(), 200), convey.ShouldBeFalse)
			convey.So(m.HasPrevious(), convey.ShouldBeFalse)
			convey.So(m.ChangedAt, convey.ShouldEqual, 100)
		})

		convey.Convey("没有上一版时无法恢复", func() {
			convey.So(m.RestorePrevious(UserActor(), 200), convey.ShouldBeFalse)
			convey.So(m.Summary, convey.ShouldEqual, "用 pnpm")
		})

		convey.Convey("归档与恢复都记为变更", func() {
			m.Archive(ai, 200)
			convey.So(m.IsArchived(), convey.ShouldBeTrue)
			convey.So(m.LastChange().Action, convey.ShouldEqual, ActionArchived)
			m.Unarchive(UserActor(), 300)
			convey.So(m.IsArchived(), convey.ShouldBeFalse)
			convey.So(m.LastChange(), convey.ShouldResemble, Change{Action: ActionUnarchived, Actor: UserActor(), At: 300})
		})

		convey.Convey("移层改归属并记为变更", func() {
			m.MoveTo(GlobalOwner(), UserActor(), 200)
			convey.So(m.Owner(), convey.ShouldResemble, GlobalOwner())
			convey.So(m.LastChange().Action, convey.ShouldEqual, ActionMoved)
		})
	})
}

func TestSplitByBudget(t *testing.T) {
	mk := func(id int64, summary string, pinned bool, changedAt int64) *Memory {
		return &Memory{ID: id, Summary: summary, Pinned: pinned, ChangedAt: changedAt, State: StateNormal}
	}
	ids := func(ms []*Memory) []int64 {
		out := make([]int64, 0, len(ms))
		for _, m := range ms {
			out = append(out, m.ID)
		}
		return out
	}
	convey.Convey("按预算取舍", t, func() {
		convey.Convey("置顶在前，其余按最近修改，取到预算为止", func() {
			entries := []*Memory{
				mk(1, strings.Repeat("a", 40), false, 100),
				mk(2, strings.Repeat("b", 40), true, 10),
				mk(3, strings.Repeat("c", 40), false, 300),
				mk(4, strings.Repeat("d", 40), false, 200),
			}
			carried, over := SplitByBudget(entries, 120)
			convey.So(ids(carried), convey.ShouldResemble, []int64{2, 3, 4})
			convey.So(ids(over), convey.ShouldResemble, []int64{1})
		})
		convey.Convey("按字数而不是字节计", func() {
			carried, over := SplitByBudget([]*Memory{mk(1, strings.Repeat("记", 10), false, 1)}, 10)
			convey.So(ids(carried), convey.ShouldResemble, []int64{1})
			convey.So(over, convey.ShouldBeEmpty)
		})
		convey.Convey("第一条放不下之后，其后的较短条目也不带", func() {
			entries := []*Memory{
				mk(1, strings.Repeat("a", 8), false, 300),
				mk(2, strings.Repeat("b", 8), false, 200),
				mk(3, "c", false, 100),
			}
			carried, over := SplitByBudget(entries, 10)
			convey.So(ids(carried), convey.ShouldResemble, []int64{1})
			convey.So(ids(over), convey.ShouldResemble, []int64{2, 3})
		})
		convey.Convey("同一修改时间按 id 新的在前", func() {
			carried, _ := SplitByBudget([]*Memory{mk(1, "a", false, 5), mk(2, "b", false, 5)}, 100)
			convey.So(ids(carried), convey.ShouldResemble, []int64{2, 1})
		})
		convey.Convey("不改动入参的顺序", func() {
			entries := []*Memory{mk(1, "a", false, 1), mk(2, "b", false, 2)}
			SplitByBudget(entries, 100)
			convey.So(ids(entries), convey.ShouldResemble, []int64{1, 2})
		})
	})
}
