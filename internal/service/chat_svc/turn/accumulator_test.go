package turn

import (
	"testing"

	cagoblocks "github.com/cago-frame/agents/agent/blocks"
	. "github.com/smartystreets/goconvey/convey"

	"github.com/agentre-hub/agentre/internal/service/chat_svc/blocks"
)

func TestAccumulator_AddBlockAndMutate(t *testing.T) {
	Convey("Mutate[B] 找到原 block 并 patch (UserAskBlock)", t, func() {
		acc := New()
		acc.AddBlock(&blocks.UserAskBlock{RequestID: "r-1"}, "user_ask:r-1")

		ok := Mutate[blocks.UserAskBlock](acc, "user_ask:r-1", func(b *blocks.UserAskBlock) {
			b.Answered = true
			b.Answers = []blocks.AskAnswerDTO{{QuestionIndex: 0, Labels: []string{"A"}}}
		})
		So(ok, ShouldBeTrue)

		final := acc.Finalize()
		So(final, ShouldHaveLength, 1)
		patched, isPtr := final[0].(*blocks.UserAskBlock)
		So(isPtr, ShouldBeTrue)
		So(patched.Answered, ShouldBeTrue)
		So(patched.Answers, ShouldHaveLength, 1)
	})
}

func TestAccumulator_MutateUnknownKeyReturnsFalse(t *testing.T) {
	Convey("Mutate 未知 key 返 false", t, func() {
		acc := New()
		ok := Mutate[blocks.UserAskBlock](acc, "missing", func(_ *blocks.UserAskBlock) {})
		So(ok, ShouldBeFalse)
	})
}

func TestAccumulator_TextFlushOnAddBlock(t *testing.T) {
	Convey("AddBlock 先 flush textBuf 再 push 新 block", t, func() {
		acc := New()
		acc.AddText("hi ")
		acc.AddBlock(&blocks.UserAskBlock{RequestID: "r"}, "user_ask:r")
		acc.AddText("done")

		final := acc.Finalize()
		So(final, ShouldHaveLength, 3)
		_, isText0 := final[0].(*cagoblocks.TextBlock)
		So(isText0, ShouldBeTrue)
		_, isAsk1 := final[1].(*blocks.UserAskBlock)
		So(isAsk1, ShouldBeTrue)
		_, isText2 := final[2].(*cagoblocks.TextBlock)
		So(isText2, ShouldBeTrue)
	})
}

func TestAccumulator_ThinkingFlushBeforeTextOnBlock(t *testing.T) {
	Convey("同段内 flush 时 thinking 先于 text,不再全堆 index 0", t, func() {
		acc := New()
		acc.AddText("text")
		acc.AddThinking("thought")
		acc.AddBlock(&blocks.UserAskBlock{RequestID: "r"}, "")

		final := acc.Finalize()
		// 顺序: [thinking, text, ask] —— 同段 thinking 在 text 前(与流序一致),
		// 但不是因为"插到 index 0",而是本段 flush 顺序本就 thinking 在前。
		_, isThink := final[0].(*cagoblocks.ThinkingBlock)
		So(isThink, ShouldBeTrue)
		_, isText := final[1].(*cagoblocks.TextBlock)
		So(isText, ShouldBeTrue)
	})
}

func TestAccumulator_MultiRoundThinkingInterleaved(t *testing.T) {
	Convey("工具循环里后几轮 thinking 按时间顺序穿插在 tool_result 之后", t, func() {
		acc := New()
		// round 1: thinking → text → tool_use → tool_result
		acc.AddThinking("thought1")
		acc.AddText("text1")
		acc.AddToolUse(&cagoblocks.ToolUseBlock{ID: "tu-1", Name: "Bash"}, "")
		acc.AddToolResult(&cagoblocks.ToolResultBlock{ToolUseID: "tu-1"})
		// round 2: thinking → text
		acc.AddThinking("thought2")
		acc.AddText("text2")

		final := acc.Finalize()
		So(final, ShouldHaveLength, 6)
		_, isThink0 := final[0].(*cagoblocks.ThinkingBlock)
		So(isThink0, ShouldBeTrue)
		_, isText1 := final[1].(*cagoblocks.TextBlock)
		So(isText1, ShouldBeTrue)
		_, isUse2 := final[2].(*cagoblocks.ToolUseBlock)
		So(isUse2, ShouldBeTrue)
		_, isRes3 := final[3].(*cagoblocks.ToolResultBlock)
		So(isRes3, ShouldBeTrue)
		// round 2 的 thinking 出现在 tool_result 之后,不再抬到最顶。
		think2, isThink4 := final[4].(*cagoblocks.ThinkingBlock)
		So(isThink4, ShouldBeTrue)
		So(think2.Text, ShouldEqual, "thought2")
		_, isText5 := final[5].(*cagoblocks.TextBlock)
		So(isText5, ShouldBeTrue)
	})
}

func TestAccumulator_HasToolUse(t *testing.T) {
	Convey("HasToolUse 兼容 pointer 与 value cago.ToolUseBlock", t, func() {
		acc := New()
		acc.AddToolUse(&cagoblocks.ToolUseBlock{ID: "tu-1", Name: "X"}, "tool_use:tu-1")
		So(acc.HasToolUse("tu-1"), ShouldBeTrue)
		So(acc.HasToolUse("nope"), ShouldBeFalse)
	})
}

func TestAccumulator_HasOpenToolUse(t *testing.T) {
	Convey("没有任何 tool_use 时不算有在途工具", t, func() {
		acc := New()
		acc.AddText("hi")
		So(acc.HasOpenToolUse(), ShouldBeFalse)
	})

	Convey("tool_use 还没等到 tool_result 时算在途", t, func() {
		acc := New()
		acc.AddToolUse(&cagoblocks.ToolUseBlock{ID: "tu-1", Name: "Bash"}, "tool_use:tu-1")
		So(acc.HasOpenToolUse(), ShouldBeTrue)
	})

	Convey("配对上 tool_result 后不再算在途", t, func() {
		acc := New()
		acc.AddToolUse(&cagoblocks.ToolUseBlock{ID: "tu-1", Name: "Bash"}, "tool_use:tu-1")
		acc.AddToolResult(&cagoblocks.ToolResultBlock{ToolUseID: "tu-1"})
		So(acc.HasOpenToolUse(), ShouldBeFalse)
	})

	Convey("并行工具:只要还有一个没配对就算在途", t, func() {
		acc := New()
		acc.AddToolUse(&cagoblocks.ToolUseBlock{ID: "tu-1", Name: "Bash"}, "tool_use:tu-1")
		acc.AddToolUse(&cagoblocks.ToolUseBlock{ID: "tu-2", Name: "Bash"}, "tool_use:tu-2")
		acc.AddToolResult(&cagoblocks.ToolResultBlock{ToolUseID: "tu-1"})
		So(acc.HasOpenToolUse(), ShouldBeTrue)
		acc.AddToolResult(&cagoblocks.ToolResultBlock{ToolUseID: "tu-2"})
		So(acc.HasOpenToolUse(), ShouldBeFalse)
	})

	Convey("subagent 内层 tool_use 不算外层在途(它的结果不走外层配对)", t, func() {
		acc := New()
		acc.AddToolUse(&blocks.NestedToolUseBlock{
			ID: "n-1", Name: "Bash", ParentToolCallID: "tu-parent",
		}, "tool_use:n-1")
		So(acc.HasOpenToolUse(), ShouldBeFalse)
	})
}

func TestAccumulator_AddToolResult_NoTextFlush(t *testing.T) {
	Convey("AddToolResult 不 flush textBuf(tool_use→tool_result 之间无文字)", t, func() {
		acc := New()
		acc.AddText("pending text")
		acc.AddToolResult(&cagoblocks.ToolResultBlock{ToolUseID: "tu-1"})
		// textBuf 仍未 flush
		final := acc.Finalize()
		// 顺序: tool_result, text(finalize flush)
		So(final, ShouldHaveLength, 2)
		_, isResult := final[0].(*cagoblocks.ToolResultBlock)
		So(isResult, ShouldBeTrue)
		_, isText := final[1].(*cagoblocks.TextBlock)
		So(isText, ShouldBeTrue)
	})
}

func TestAccumulator_Empty(t *testing.T) {
	Convey("Empty 反映 finalBlocks + bufs", t, func() {
		acc := New()
		So(acc.Empty(), ShouldBeTrue)
		acc.AddText("x")
		So(acc.Empty(), ShouldBeFalse)
	})
}

func TestAccumulator_Snapshot_DoesNotConsumeBufs(t *testing.T) {
	Convey("Snapshot 不消费 textBuf/thinkingBuf", t, func() {
		acc := New()
		acc.AddText("hi")
		acc.AddThinking("think")

		snap1 := acc.Snapshot()
		So(snap1, ShouldHaveLength, 2) // thinking + text

		// Snapshot 后 Finalize 仍能产出同样内容
		final := acc.Finalize()
		So(final, ShouldHaveLength, 2)
	})
}
