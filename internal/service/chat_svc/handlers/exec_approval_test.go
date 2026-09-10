package handlers

import (
	"context"
	"testing"

	. "github.com/smartystreets/goconvey/convey"

	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
	"github.com/agentre-hub/agentre/internal/service/chat_svc/blocks"
	"github.com/agentre-hub/agentre/internal/service/chat_svc/turn"
)

type execApprovalTransitions struct {
	waiting int
	running int
}

func (f *execApprovalTransitions) MarkWaiting(context.Context, any, string) { f.waiting++ }
func (f *execApprovalTransitions) MarkRunning(context.Context, any, string) { f.running++ }

func TestExecApprovalHandlers(t *testing.T) {
	Convey("Given a Gateway approval request when dispatched then a pending replayable block and live event retain allowedDecisions", t, func() {
		acc := turn.New()
		emit := &fakeEmit{}
		err := ExecApprovalRequestedHandler{}.Apply(context.Background(), agentruntime.ExecApprovalRequested{
			ID: "approval-1", CommandText: "pwd", AllowedDecisions: []string{"allow-once", "deny"},
			SessionKey: "agentre:12:42", ExpiresAtMs: 99,
		}, acc, emit, nil, nil)
		So(err, ShouldBeNil)
		block := acc.Finalize()[0].(*blocks.ExecApprovalBlock)
		So(block.Status, ShouldEqual, "pending")
		So(block.AllowedDecisions, ShouldResemble, []string{"allow-once", "deny"})
		payload := emit.events[0].payload.(map[string]any)
		So(payload["kind"], ShouldEqual, "exec_approval")
		So(payload["execApproval"], ShouldEqual, block)
	})

	Convey("Given an approval is resolved when the terminal event arrives then the card changes state without becoming exec completion", t, func() {
		acc := turn.New()
		_ = ExecApprovalRequestedHandler{}.Apply(context.Background(), agentruntime.ExecApprovalRequested{
			ID: "approval-2", CommandText: "false", AllowedDecisions: []string{"deny"},
		}, acc, nil, nil, nil)
		emit := &fakeEmit{}
		err := ExecApprovalResolvedHandler{}.Apply(context.Background(), agentruntime.ExecApprovalResolved{
			ID: "approval-2", Status: "resolved", Decision: "deny", ResolvedBy: "device-2", ResolvedAtMs: 100,
		}, acc, emit, nil, nil)
		So(err, ShouldBeNil)
		block := acc.Finalize()[0].(*blocks.ExecApprovalBlock)
		So(block.Status, ShouldEqual, "resolved")
		So(block.Decision, ShouldEqual, "deny")
		So(block.ResolvedBy, ShouldEqual, "device-2")
		So(emit.events, ShouldHaveLength, 1)
		payload := emit.events[0].payload.(map[string]any)
		So(payload["kind"], ShouldEqual, "exec_approval")
	})

	Convey("Given two pending exec approvals when one resolves then the session remains waiting until the last approval is terminal", t, func() {
		acc := turn.New()
		transitions := &execApprovalTransitions{}
		tc := &turn.TurnContext{
			Session:             &struct{}{},
			Stream:              "chat:event:42:7",
			SessionTransitioner: transitions,
		}
		for _, id := range []string{"approval-a", "approval-b"} {
			err := (ExecApprovalRequestedHandler{}).Apply(context.Background(), agentruntime.ExecApprovalRequested{
				ID: id, CommandText: "pwd", AllowedDecisions: []string{"allow-once", "deny"},
			}, acc, nil, nil, tc)
			So(err, ShouldBeNil)
		}
		So(transitions.waiting, ShouldEqual, 2)

		err := (ExecApprovalResolvedHandler{}).Apply(context.Background(), agentruntime.ExecApprovalResolved{
			ID: "approval-a", Status: "resolved", Decision: "allow-once",
		}, acc, nil, nil, tc)
		So(err, ShouldBeNil)
		So(transitions.running, ShouldEqual, 0)

		err = (ExecApprovalResolvedHandler{}).Apply(context.Background(), agentruntime.ExecApprovalResolved{
			ID: "approval-b", Status: "expired",
		}, acc, nil, nil, tc)
		So(err, ShouldBeNil)
		So(transitions.running, ShouldEqual, 1)
	})
}
