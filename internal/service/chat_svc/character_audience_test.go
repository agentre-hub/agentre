package chat_svc

import (
	"testing"

	"github.com/cago-frame/agents/agent/blocks"
	. "github.com/smartystreets/goconvey/convey"

	chatblocks "github.com/agentre-hub/agentre/internal/service/chat_svc/blocks"
)

// §1.5 Audience 边界 — characterization tests
//
// 关键 pin: agentre 自定义控制 block 必须 Audience=ToUI(不能让 LLM 看到自己的
// 控制平面 metadata,否则模型把它当上下文回写、导致 hallucination 与 turn loop)。
// cago 自带 block 走 ToAll(默认全可见)。
func TestCharacterization_Audience_BlockBoundaries(t *testing.T) {
	Convey("§1.5 Audience 边界 (LLM 不能看 agentre UI block)", t, func() {
		Convey("cago 自带 block: text/tool_use/tool_result = ToAll; thinking = ToLLM only", func() {
			// drift from plan §1.5: ThinkingBlock 在 cago 里只 ToLLM —— 思考过程不
			// 默认进 RenderForDisplay。agentre 前端要显示思考是通过自家投影路径
			// (toolUseToChatBlock 兄弟) 拿到的。
			So(blocks.TextBlock{}.Audience(), ShouldEqual, blocks.ToAll)
			So(blocks.ThinkingBlock{}.Audience(), ShouldEqual, blocks.ToLLM)
			So(blocks.ToolUseBlock{}.Audience(), ShouldEqual, blocks.ToAll)
			So(blocks.ToolResultBlock{}.Audience(), ShouldEqual, blocks.ToAll)
		})

		Convey("agentre 自定义控制 block = ToUI", func() {
			So(chatblocks.UserAskBlock{}.Audience(), ShouldEqual, blocks.ToUI)
			So(chatblocks.ToolPermissionBlock{}.Audience(), ShouldEqual, blocks.ToUI)
			So(PlanBlock{}.Audience(), ShouldEqual, blocks.ToUI)
		})
	})
}
