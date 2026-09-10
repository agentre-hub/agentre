package view

import (
	"testing"

	. "github.com/smartystreets/goconvey/convey"

	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/canonical"
)

func TestFromCanonical_NilSafe(t *testing.T) {
	Convey("FromCanonical(nil) 返 nil", t, func() {
		So(FromCanonical(nil), ShouldBeNil)
	})
}

func TestFromCanonical_FileWrite(t *testing.T) {
	Convey("FileWrite 映射到 CanonicalDTO.FileWrite", t, func() {
		dto := FromCanonical(canonical.FileWrite{Path: "/p", Content: "c"})
		So(dto, ShouldNotBeNil)
		So(dto.Kind, ShouldEqual, canonical.KindFileWrite)
		So(dto.FileWrite, ShouldNotBeNil)
		So(dto.FileWrite.Path, ShouldEqual, "/p")
	})
}

func TestFromCanonical_PlanUpdate(t *testing.T) {
	Convey("PlanUpdate 映射", t, func() {
		dto := FromCanonical(canonical.PlanUpdate{Text: "## Plan"})
		So(dto.Kind, ShouldEqual, canonical.KindPlanUpdate)
		So(dto.PlanUpdate.Text, ShouldEqual, "## Plan")
	})

	Convey("PlanUpdate.Actions 透传", t, func() {
		dto := FromCanonical(canonical.PlanUpdate{
			Text: "## Plan",
			Actions: []canonical.PlanAction{
				{ID: "plan.execute", Kind: canonical.PlanActionApprove},
				{ID: "plan.refine", Kind: canonical.PlanActionRefine, RequiresFeedback: true},
			},
		})
		So(dto.PlanUpdate.Actions, ShouldHaveLength, 2)
		So(dto.PlanUpdate.Actions[0].ID, ShouldEqual, "plan.execute")
		So(dto.PlanUpdate.Actions[1].RequiresFeedback, ShouldBeTrue)
	})

}

func TestFromCanonical_PlanApprove(t *testing.T) {
	Convey("PlanApproveRequest.Actions 透传", t, func() {
		dto := FromCanonical(canonical.PlanApproveRequest{
			RequestID: "req-1",
			PlanText:  "...",
			Actions: []canonical.PlanAction{
				{ID: "plan.approve.bypass_permissions", Kind: canonical.PlanActionApprove},
				{ID: "plan.approve.manual", Kind: canonical.PlanActionApprove},
				{ID: "plan.refine", Kind: canonical.PlanActionRefine, RequiresFeedback: true},
			},
		})
		So(dto.Kind, ShouldEqual, canonical.KindPlanApproveRequest)
		So(dto.PlanApprove.Actions, ShouldHaveLength, 3)
		So(dto.PlanApprove.Actions[0].Kind, ShouldEqual, canonical.PlanActionApprove)
	})

}
