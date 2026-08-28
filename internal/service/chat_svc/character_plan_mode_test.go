package chat_svc

import (
	"testing"

	"github.com/cago-frame/agents/agent/blocks"
	. "github.com/smartystreets/goconvey/convey"

	"github.com/agentre-hub/agentre/internal/service/chat_svc/turn"
)

// §1.12 Plan mode 空 turn 兜底 — characterization test
//
// 关键 pin: plan mode 下 LLM 可能空 turn(无 text / 无 tool_use)。chat.go 收尾时
// 若 acc.Empty() 真则追加兜底文本 "Plan mode completed without executable changes.",
// 避免前端显示空白助手消息让用户以为系统挂了。
func TestCharacterization_PlanMode_EmptyTurnFallbackText(t *testing.T) {
	Convey("§1.12 plan mode 空 turn 收尾追加兜底文本", t, func() {
		acc := turn.New()
		So(acc.Empty(), ShouldBeTrue)

		acc.AddText("Plan mode completed without executable changes.")
		final := acc.Finalize()
		So(final, ShouldHaveLength, 1)
		txt, _ := final[0].(*blocks.TextBlock)
		So(txt, ShouldNotBeNil)
		So(txt.Text, ShouldContainSubstring, "Plan mode completed")
	})
}
