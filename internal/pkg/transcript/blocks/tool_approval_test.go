package blocks

import (
	"testing"

	cagoblocks "github.com/cago-frame/agents/agent/blocks"
	. "github.com/smartystreets/goconvey/convey"
)

func TestToolApprovalBlock_TypeAndAudience(t *testing.T) {
	Convey("ToolApprovalBlock 类型 + Audience", t, func() {
		b := ToolApprovalBlock{}
		So(b.Type(), ShouldEqual, "tool_approval")
		So(b.Audience(), ShouldEqual, cagoblocks.ToUI)
	})
}

func TestToolApprovalBlock_FactoryRoundTrip(t *testing.T) {
	Convey("ToolApprovalBlock Encode/Decode round-trip", t, func() {
		b := &ToolApprovalBlock{
			ToolKey:   "org",
			RequestID: "org-req-1",
			ToolName:  "org_invite",
			ToolInput: map[string]any{"user_id": "u-42"},
			Status:    "pending",
			Result:    "",
		}
		sb, err := cagoblocks.Encode(b)
		So(err, ShouldBeNil)
		So(sb.Type, ShouldEqual, "tool_approval")

		decoded, err := cagoblocks.Decode(sb)
		So(err, ShouldBeNil)
		got, ok := decoded.(ToolApprovalBlock)
		So(ok, ShouldBeTrue)
		So(got.ToolKey, ShouldEqual, "org")
		So(got.RequestID, ShouldEqual, "org-req-1")
		So(got.ToolName, ShouldEqual, "org_invite")
		So(got.ToolInput["user_id"], ShouldEqual, "u-42")
		So(got.Status, ShouldEqual, "pending")
	})
}
