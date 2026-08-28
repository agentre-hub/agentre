package chat_svc

import (
	"testing"

	"github.com/cago-frame/agents/agent/blocks"
	. "github.com/smartystreets/goconvey/convey"

	chatblocks "github.com/agentre-hub/agentre/internal/service/chat_svc/blocks"
	"github.com/agentre-hub/agentre/internal/service/chat_svc/turn"
)

// §1.6 ContentBlock 顺序约束 — characterization tests
//
// 关键 pin (turn.Accumulator):
//   - thinking 按时间顺序穿插:同段内 thinking 在 text 前(与流序一致),工具循环
//     里后几轮的 thinking 出现在对应 tool_result 之后 —— 不再整体抬到 index 0
//   - text delta 在 AddToolUse / AddBlock 时 flush(否则 tool_use 后面文字会黏前面文字)
//   - text delta 在 AddToolResult 时 *不* flush(tool_use→tool_result 之间一般无穿插)
func TestCharacterization_BlockOrder_ThinkingInterleaved(t *testing.T) {
	Convey("§1.6 Finalize() thinking 按时间顺序穿插,后一轮 thinking 在 tool_result 之后", t, func() {
		acc := turn.New()
		acc.AddThinking("thought1")
		acc.AddText("hello ")
		acc.AddToolUse(&blocks.ToolUseBlock{ID: "tu-1", Name: "X"}, "")
		acc.AddToolResult(&blocks.ToolResultBlock{ToolUseID: "tu-1"})
		acc.AddThinking("thought2")
		acc.AddText("world")

		final := acc.Finalize()
		So(len(final), ShouldEqual, 6)
		// index 0 是 round 1 的 thinking(它本就是最早的内容)
		_, isThink0 := final[0].(*blocks.ThinkingBlock)
		So(isThink0, ShouldBeTrue)
		// round 2 的 thinking 在 tool_result 之后,不再抬到最顶。
		think2, isThink4 := final[4].(*blocks.ThinkingBlock)
		So(isThink4, ShouldBeTrue)
		So(think2.Text, ShouldEqual, "thought2")
	})
}

func TestCharacterization_BlockOrder_TextFlushOnToolUse(t *testing.T) {
	Convey("§1.6 text delta 遇 tool_use flush", t, func() {
		acc := turn.New()
		acc.AddText("before ")
		acc.AddToolUse(&blocks.ToolUseBlock{ID: "tu-1", Name: "X"}, "")

		final := acc.Finalize()
		So(final, ShouldHaveLength, 2)
		txt, _ := final[0].(*blocks.TextBlock)
		So(txt, ShouldNotBeNil)
		So(txt.Text, ShouldEqual, "before ")
	})
}

func TestCharacterization_BlockOrder_TextNotFlushedOnToolResult(t *testing.T) {
	Convey("§1.6 text delta 遇 tool_result 不 flush", t, func() {
		acc := turn.New()
		acc.AddToolUse(&blocks.ToolUseBlock{ID: "tu-1", Name: "X"}, "")
		acc.AddText("intermixed")
		acc.AddToolResult(&blocks.ToolResultBlock{ToolUseID: "tu-1"})

		final := acc.Finalize()
		// 期望顺序: [ToolUseBlock, ToolResultBlock, TextBlock]
		So(final, ShouldHaveLength, 3)
		_, isText := final[2].(*blocks.TextBlock)
		So(isText, ShouldBeTrue)
	})
}

func TestCharacterization_BlockOrder_AddBlockFlushesText(t *testing.T) {
	Convey("§1.6 AddBlock(自定义 block) 与 tool_use 同样先 flush text", t, func() {
		acc := turn.New()
		acc.AddText("hi ")
		acc.AddBlock(chatblocks.UserAskBlock{RequestID: "r-1"}, "")

		final := acc.Finalize()
		So(final, ShouldHaveLength, 2)
		txt, _ := final[0].(*blocks.TextBlock)
		So(txt, ShouldNotBeNil)
		So(txt.Text, ShouldEqual, "hi ")
	})
}
