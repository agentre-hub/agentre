package blocks

import (
	"testing"

	cagoblocks "github.com/cago-frame/agents/agent/blocks"
	. "github.com/smartystreets/goconvey/convey"
)

func TestExecApprovalBlockRoundTrip(t *testing.T) {
	Convey("Given a pending OpenClaw exec approval when persisted then dynamic decisions and safe command metadata survive replay", t, func() {
		block := &ExecApprovalBlock{
			ID: "approval-1", CommandText: "printf safe", CommandPreview: "printf safe",
			AllowedDecisions: []string{"allow-once", "deny"}, Host: "node", NodeID: "node-1",
			AgentID: "main", Status: "pending", CreatedAtMs: 10, ExpiresAtMs: 20,
		}
		So(block.Type(), ShouldEqual, "exec_approval")
		So(block.Audience(), ShouldEqual, cagoblocks.ToUI)
		encoded, err := cagoblocks.Encode(block)
		So(err, ShouldBeNil)
		decoded, err := cagoblocks.Decode(encoded)
		So(err, ShouldBeNil)
		got, ok := decoded.(ExecApprovalBlock)
		So(ok, ShouldBeTrue)
		So(got.AllowedDecisions, ShouldResemble, []string{"allow-once", "deny"})
		So(got.CommandText, ShouldEqual, "printf safe")
		So(got.Status, ShouldEqual, "pending")
	})
}
