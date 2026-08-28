package handlers

import (
	"context"
	"testing"

	. "github.com/smartystreets/goconvey/convey"

	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/canonical"
	"github.com/agentre-hub/agentre/internal/service/chat_svc/turn"
)

func TestPlanUpdatedHandler_EmitsCanonicalPlanUpdate(t *testing.T) {
	Convey("PlanUpdated emit plan_update with canonical plan payload", t, func() {
		acc := turn.New()
		emit := &fakeEmit{}
		err := PlanUpdatedHandler{}.Apply(
			context.Background(),
			agentruntime.PlanUpdated{Plan: canonical.PlanUpdate{
				Text: "# Plan\n\n1. Inspect\n2. Test",
				Steps: []canonical.PlanStep{
					{Step: "Inspect", Status: canonical.StepCompleted},
					{Step: "Test", Status: canonical.StepInProgress},
				},
			}},
			acc,
			emit,
			nil,
			&turn.TurnContext{Stream: "chat:event:1:2"},
		)

		So(err, ShouldBeNil)
		So(emit.events, ShouldHaveLength, 1)
		So(emit.events[0].stream, ShouldEqual, "chat:event:1:2")
		payload := emit.events[0].payload.(map[string]any)
		So(payload["kind"], ShouldEqual, "plan_update")
		So(payload["delta"], ShouldEqual, "# Plan\n\n1. Inspect\n2. Test")
		plan, ok := payload["canonical"].(canonical.PlanUpdate)
		So(ok, ShouldBeTrue)
		So(plan.Text, ShouldEqual, "# Plan\n\n1. Inspect\n2. Test")
		So(plan.Steps, ShouldHaveLength, 2)
		So(plan.Actions, ShouldBeNil) // claudecode/未指定 backend → 无 actions
	})
}

func TestPlanUpdatedHandler_PreservesPlanActions(t *testing.T) {
	Convey("PlanUpdated actions 透传到 emit 和 writer", t, func() {
		acc := turn.New()
		emit := &fakeEmit{}
		writer := &fakePlanWriter{}
		input := canonical.PlanUpdate{
			Text: "## P",
			Actions: []canonical.PlanAction{
				{ID: "runtime.execute", Kind: canonical.PlanActionApprove},
				{ID: "runtime.refine", Kind: canonical.PlanActionRefine, RequiresFeedback: true},
			},
		}
		err := PlanUpdatedHandler{}.Apply(
			context.Background(),
			agentruntime.PlanUpdated{Plan: input},
			acc, emit, nil,
			&turn.TurnContext{Stream: "chat:event:1:2"},
		)
		So(err, ShouldBeNil)
		plan := emit.events[0].payload.(map[string]any)["canonical"].(canonical.PlanUpdate)
		So(plan.Actions, ShouldHaveLength, 2)
		So(plan.Actions[0].ID, ShouldEqual, "runtime.execute")
		So(plan.Actions[1].ID, ShouldEqual, "runtime.refine")
		So(plan.Actions[1].RequiresFeedback, ShouldBeTrue)

		acc = turn.New()
		emit = &fakeEmit{}
		err = PlanUpdatedHandler{Writer: writer}.Apply(
			context.Background(),
			agentruntime.PlanUpdated{Plan: input},
			acc, emit, nil,
			&turn.TurnContext{Stream: "chat:event:1:2"},
		)
		So(err, ShouldBeNil)
		So(writer.plan.Actions, ShouldHaveLength, 2)
	})
}

func TestPlanUpdatedHandler_NoSyntheticActions(t *testing.T) {
	Convey("PlanUpdated 不合成 backend-specific actions", t, func() {
		acc := turn.New()
		emit := &fakeEmit{}
		_ = PlanUpdatedHandler{}.Apply(
			context.Background(),
			agentruntime.PlanUpdated{Plan: canonical.PlanUpdate{Text: "## P"}},
			acc, emit, nil,
			&turn.TurnContext{Stream: "chat:event:1:2", BackendType: "codex"},
		)
		plan := emit.events[0].payload.(map[string]any)["canonical"].(canonical.PlanUpdate)
		So(plan.Actions, ShouldBeNil)
	})
}

func TestPlanUpdatedHandler_ClaudecodeBackendNoActions(t *testing.T) {
	Convey("BackendType=claudecode → canonical.Actions=nil", t, func() {
		acc := turn.New()
		emit := &fakeEmit{}
		_ = PlanUpdatedHandler{}.Apply(
			context.Background(),
			agentruntime.PlanUpdated{Plan: canonical.PlanUpdate{Steps: []canonical.PlanStep{
				{Step: "a", Status: canonical.StepPending},
			}}},
			acc, emit, nil,
			&turn.TurnContext{Stream: "chat:event:1:2", BackendType: "claudecode"},
		)
		plan := emit.events[0].payload.(map[string]any)["canonical"].(canonical.PlanUpdate)
		So(plan.Actions, ShouldBeNil)
	})
}

type fakePlanWriter struct {
	plan canonical.PlanUpdate
}

func (f *fakePlanWriter) WritePlan(_ *turn.Accumulator, plan canonical.PlanUpdate) {
	f.plan = plan
}
