package blocks

import (
	"testing"

	cagoblocks "github.com/cago-frame/agents/agent/blocks"
	. "github.com/smartystreets/goconvey/convey"

	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
)

func TestSubagentStateBlock(t *testing.T) {
	Convey("SubagentStateBlock 类型/Audience/round-trip", t, func() {
		b := &SubagentStateBlock{
			ParentToolCallID: "task-1",
			Status:           "running",
			ToolUses:         2,
			Model:            "claude-haiku-4-5-20251001",
			Mode:             "parallel",
			Runs: []agentruntime.SubagentRun{
				{ID: "run-0", Index: 0, Task: "inspect", Status: "completed", Model: "model-a"},
				{ID: "run-1", Index: 1, Task: "test", Status: "running"},
			},
			NestedToolCallIDs: []string{"n-1", "n-2"},
		}
		So(b.Type(), ShouldEqual, "subagent_state")
		So(b.Audience(), ShouldEqual, cagoblocks.ToUI)

		sb, err := cagoblocks.Encode(b)
		So(err, ShouldBeNil)
		decoded, err := cagoblocks.Decode(sb)
		So(err, ShouldBeNil)
		got, ok := decoded.(SubagentStateBlock)
		So(ok, ShouldBeTrue)
		So(got.NestedToolCallIDs, ShouldHaveLength, 2)
		// R6:模型须随累计态一起持久化/replay。
		So(got.Model, ShouldEqual, "claude-haiku-4-5-20251001")
		So(got.Mode, ShouldEqual, "parallel")
		So(got.Runs, ShouldHaveLength, 2)
		So(got.Runs[0].Model, ShouldEqual, "model-a")
		So(got.Runs[1].Status, ShouldEqual, "running")
	})
}

func TestSubagentStateBlock_AgrctlFields(t *testing.T) {
	Convey("agrctl 绑定的 overlay:归属 / 子会话 / isolated / 子任务最终文本随块往返", t, func() {
		b := &SubagentStateBlock{
			ParentToolCallID: "sh-1",
			Kind:             agentruntime.SubagentKindAgrctl,
			Origin:           agentruntime.SubagentOriginAgrctl,
			ChildSessionID:   77,
			Isolated:         true,
			Status:           "completed",
			Summary:          "Background command completed (exit code 0)",
			FinalText:        "子任务的结论",
		}
		sb, err := cagoblocks.Encode(b)
		So(err, ShouldBeNil)
		decoded, err := cagoblocks.Decode(sb)
		So(err, ShouldBeNil)
		got, ok := decoded.(SubagentStateBlock)
		So(ok, ShouldBeTrue)
		So(got, ShouldResemble, *b)
	})
}
