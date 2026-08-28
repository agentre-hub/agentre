package chat_svc

import (
	"testing"

	. "github.com/smartystreets/goconvey/convey"

	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/canonical"
	"github.com/agentre-hub/agentre/internal/pkg/code"
)

func TestPlanActionDecision(t *testing.T) {
	Convey("planActionDecision", t, func() {
		Convey("nil / 空 SessionID / 空 ActionID → InvalidParameter", func() {
			_, ec := planActionDecision(nil)
			So(ec, ShouldEqual, code.InvalidParameter)

			_, ec = planActionDecision(&ResolvePlanActionRequest{ActionID: canonical.PlanActionIDRefine})
			So(ec, ShouldEqual, code.InvalidParameter)

			_, ec = planActionDecision(&ResolvePlanActionRequest{SessionID: 1, ActionID: "  "})
			So(ec, ShouldEqual, code.InvalidParameter)
		})

		Convey("plan.approve.bypass_permissions → AnswerToolPermission(allow,bypassPermissions)", func() {
			d, ec := planActionDecision(&ResolvePlanActionRequest{
				SessionID: 1, RequestID: "r", ActionID: canonical.PlanActionIDApproveBypassPermissions,
			})
			So(ec, ShouldEqual, 0)
			So(d.answerPermission, ShouldNotBeNil)
			So(d.send, ShouldBeNil)
			So(d.answerPermission.Allow, ShouldBeTrue)
			So(d.answerPermission.TargetPermissionMode, ShouldEqual, "bypassPermissions")
			So(d.answerPermission.RequestID, ShouldEqual, "r")
		})

		Convey("plan.approve.accept_edits → acceptEdits", func() {
			d, _ := planActionDecision(&ResolvePlanActionRequest{
				SessionID: 1, RequestID: "r", ActionID: canonical.PlanActionIDApproveAcceptEdits,
			})
			So(d.answerPermission.TargetPermissionMode, ShouldEqual, "acceptEdits")
		})

		Convey("plan.approve.manual → default(不接力)", func() {
			d, _ := planActionDecision(&ResolvePlanActionRequest{
				SessionID: 1, RequestID: "r", ActionID: canonical.PlanActionIDApproveManual,
			})
			So(d.answerPermission.TargetPermissionMode, ShouldEqual, "default")
		})

		Convey("plan.approve.* 缺 RequestID → InvalidParameter", func() {
			_, ec := planActionDecision(&ResolvePlanActionRequest{
				SessionID: 1, ActionID: canonical.PlanActionIDApproveBypassPermissions,
			})
			So(ec, ShouldEqual, code.InvalidParameter)
		})

		Convey("plan.approve.unknown_suffix → ChatPlanActionUnknown", func() {
			_, ec := planActionDecision(&ResolvePlanActionRequest{
				SessionID: 1, RequestID: "r", ActionID: "plan.approve.weirdmode",
			})
			So(ec, ShouldEqual, code.ChatPlanActionUnknown)
		})

		Convey("plan.refine + requestID → AnswerToolPermission(deny, denyReason=feedback)", func() {
			d, _ := planActionDecision(&ResolvePlanActionRequest{
				SessionID: 1, RequestID: "r", ActionID: canonical.PlanActionIDRefine, Feedback: "  脱缰一些  ",
			})
			So(d.answerPermission.Allow, ShouldBeFalse)
			So(d.answerPermission.DenyReason, ShouldEqual, "脱缰一些")
		})

		Convey("plan.execute → Send(默认文案, mode=default)", func() {
			d, _ := planActionDecision(&ResolvePlanActionRequest{
				SessionID: 9, ActionID: canonical.PlanActionIDExecute,
			})
			So(d.send, ShouldNotBeNil)
			So(d.answerPermission, ShouldBeNil)
			So(d.send.Text, ShouldEqual, "Implement the plan.")
			So(d.send.PermissionMode, ShouldEqual, "default")
			So(d.send.SessionID, ShouldEqual, 9)
			So(d.allowPlanWaiting, ShouldBeTrue)
		})

		Convey("plan.refine 无 requestID 且带 feedback → Send(feedback, plan)", func() {
			d, _ := planActionDecision(&ResolvePlanActionRequest{
				SessionID: 9, ActionID: canonical.PlanActionIDRefine, Feedback: "再细一些",
			})
			So(d.send.Text, ShouldEqual, "再细一些")
			So(d.send.PermissionMode, ShouldEqual, "plan")
			So(d.allowPlanWaiting, ShouldBeTrue)
		})

		Convey("plan.refine 无 requestID 且空 feedback → 默认文案", func() {
			d, _ := planActionDecision(&ResolvePlanActionRequest{
				SessionID: 9, ActionID: canonical.PlanActionIDRefine,
			})
			So(d.send.Text, ShouldEqual, "继续完善上述计划。")
		})

		Convey("未知 actionID → ChatPlanActionUnknown", func() {
			_, ec := planActionDecision(&ResolvePlanActionRequest{
				SessionID: 1, ActionID: "weird.action",
			})
			So(ec, ShouldEqual, code.ChatPlanActionUnknown)
		})
	})
}
