package blocks

import (
	"testing"

	cagoblocks "github.com/cago-frame/agents/agent/blocks"
	. "github.com/smartystreets/goconvey/convey"
)

func TestToolPermissionBlock_TypeAndAudience(t *testing.T) {
	Convey("ToolPermissionBlock 类型 + Audience", t, func() {
		b := ToolPermissionBlock{}
		So(b.Type(), ShouldEqual, "tool_permission")
		So(b.Audience(), ShouldEqual, cagoblocks.ToUI)
	})
}

func TestToolPermissionBlock_FactoryRoundTrip(t *testing.T) {
	Convey("ToolPermissionBlock Encode/Decode round-trip", t, func() {
		b := &ToolPermissionBlock{
			RequestID:  "perm-1",
			ToolCallID: "tu-1",
			ToolName:   "Bash",
			ToolInput:  map[string]any{"command": "ls"},
			Resolved:   true,
			Allowed:    true,
			DenyReason: "",
		}
		sb, err := cagoblocks.Encode(b)
		So(err, ShouldBeNil)
		So(sb.Type, ShouldEqual, "tool_permission")

		decoded, err := cagoblocks.Decode(sb)
		So(err, ShouldBeNil)
		got, ok := decoded.(ToolPermissionBlock)
		So(ok, ShouldBeTrue)
		So(got.RequestID, ShouldEqual, "perm-1")
		So(got.Allowed, ShouldBeTrue)
	})
}
