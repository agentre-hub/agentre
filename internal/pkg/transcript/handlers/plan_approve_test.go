package handlers

import (
	"testing"

	. "github.com/smartystreets/goconvey/convey"

	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/canonical"
)

func TestBuildPlanApproveActions(t *testing.T) {
	Convey("BuildPlanApproveActions", t, func() {
		Convey("launch=bypassPermissions → 首项是 bypass,其余 manual + refine", func() {
			got := BuildPlanApproveActions("bypassPermissions")
			So(got, ShouldHaveLength, 3)
			So(got[0].ID, ShouldEqual, "plan.approve.bypass_permissions")
			So(got[0].Kind, ShouldEqual, canonical.PlanActionApprove)
			So(got[1].ID, ShouldEqual, "plan.approve.manual")
			So(got[2].ID, ShouldEqual, "plan.refine")
			So(got[2].Kind, ShouldEqual, canonical.PlanActionRefine)
			So(got[2].RequiresFeedback, ShouldBeTrue)
		})
		Convey("launch=default → 首项是 accept_edits", func() {
			got := BuildPlanApproveActions("default")
			So(got[0].ID, ShouldEqual, "plan.approve.accept_edits")
			So(got[2].ID, ShouldEqual, "plan.refine")
		})
		Convey("launch 空串 → 兜底 accept_edits", func() {
			got := BuildPlanApproveActions("")
			So(got[0].ID, ShouldEqual, "plan.approve.accept_edits")
		})
		Convey("launch=acceptEdits → accept_edits", func() {
			got := BuildPlanApproveActions("acceptEdits")
			So(got[0].ID, ShouldEqual, "plan.approve.accept_edits")
		})
	})
}
