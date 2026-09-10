package capability

import (
	"testing"

	. "github.com/smartystreets/goconvey/convey"
)

func TestCapabilitiesHas(t *testing.T) {
	Convey("Capabilities.Has 读 bool 集合", t, func() {
		c := Capabilities{Set: map[Capability]bool{CapSteer: true, CapAbort: false}}
		So(c.Has(CapSteer), ShouldBeTrue)
		So(c.Has(CapAbort), ShouldBeFalse)
		So(c.Has(CapForkSession), ShouldBeFalse) // not in map
	})

	Convey("nil Set safely returns false", t, func() {
		var c Capabilities
		So(c.Has(CapSteer), ShouldBeFalse)
	})
}

func TestPermissionModeMeta(t *testing.T) {
	Convey("PermissionModeMeta 结构化字段", t, func() {
		c := Capabilities{
			Set: map[Capability]bool{CapSetPermission: true},
			PermissionModeMeta: PermissionModeMeta{
				AllowedModes:         []string{"default", "plan"},
				DefaultMode:          "default",
				SwitchableDuringTurn: false,
			},
		}
		So(c.PermissionModeMeta.AllowedModes, ShouldContain, "plan")
		So(c.PermissionModeMeta.DefaultMode, ShouldEqual, "default")
		So(c.PermissionModeMeta.SwitchableDuringTurn, ShouldBeFalse)
		So(c.Has(CapSetPermission), ShouldBeTrue)
	})
}

func TestCapabilityWireStrings(t *testing.T) {
	Convey("Capability wire 字符串值固定 (前端依赖)", t, func() {
		So(string(CapSteer), ShouldEqual, "steer")
		So(string(CapCancelSteer), ShouldEqual, "cancel_steer")
		So(string(CapDrainSteer), ShouldEqual, "drain_steer")
		So(string(CapAbort), ShouldEqual, "abort")
		So(string(CapStopBackgroundTask), ShouldEqual, "stop_background_task")
		So(string(CapSetPermission), ShouldEqual, "set_permission_mode")
		So(string(CapAnswerUserAsk), ShouldEqual, "answer_user_ask")
		So(string(CapToolPermission), ShouldEqual, "tool_permission_gate")
		So(string(CapForkSession), ShouldEqual, "fork_session")
		So(string(CapReportContextWindow), ShouldEqual, "report_context_window")
		So(string(CapCompact), ShouldEqual, "compact")
		So(string(CapGoal), ShouldEqual, "goal")
		// reasoning_effort:composer 的思考力度选择器据此渲染/隐藏
		// (spec 2026-09-01 决策 6),前端 Capability 联合类型逐字对齐这个值。
		So(string(CapReasoningEffort), ShouldEqual, "reasoning_effort")
	})
}
