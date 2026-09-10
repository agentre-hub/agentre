package chat_svc

import (
	"testing"

	"github.com/cago-frame/agents/agent/blocks"
	. "github.com/smartystreets/goconvey/convey"

	"github.com/agentre-hub/agentre/internal/service/chat_svc/turn"
)

// §1.2 孤儿事件丢弃 — characterization tests
//
// 关键 pin:
//  1. acc.HasToolUse(id) 对未知 id 返 false —— dispatcher 据此过滤孤儿 ToolResult,
//     避免脏 tool_result 写库 / 推送幽灵 tool 卡。
//  2. attachToolResultMeta 找不到匹配 ToolCallID 时静默丢弃 ToolResultMeta —— 不在
//     transcript 里追加单独的 meta block 污染前端。
func TestCharacterization_Orphan_ToolResultDropped(t *testing.T) {
	Convey("§1.2 ToolResult 找不到配对 tool_use 时 acc 不收", t, func() {
		acc := turn.New()

		Convey("孤儿 tool_result HasToolUse 返 false", func() {
			So(acc.HasToolUse("orphan-id"), ShouldBeFalse)
		})

		Convey("正常 tool_use → tool_result 配对正确", func() {
			acc.AddToolUse(&blocks.ToolUseBlock{ID: "tu-1", Name: "X"}, "")
			So(acc.HasToolUse("tu-1"), ShouldBeTrue)
			acc.AddToolResult(&blocks.ToolResultBlock{
				ToolUseID: "tu-1",
				Content:   []blocks.ContentBlock{blocks.TextBlock{Text: "ok"}},
			})
			So(acc.Finalize(), ShouldHaveLength, 2)
		})
	})
}

// ToolResultMetaBlock 已删除 —— meta 现在由 raw tool_result Meta 字节透传
// (StreamToolResult 事件的 toolResultMeta 字段),不再独立 block,因此孤儿场景无意义。
