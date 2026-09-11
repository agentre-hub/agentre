package chat_svc

import (
	"context"
	"testing"

	"github.com/cago-frame/agents/agent/blocks"
	. "github.com/smartystreets/goconvey/convey"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/agentre-hub/agentre/internal/model/entity/chat_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/transcript_entity"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/canonical"
	"github.com/agentre-hub/agentre/internal/pkg/code"
)

// planTranscript 是清 plan actions 的 fixture:较早的 assistant 与最新一条带 plan 的 assistant
// 都挂着 actions,最新一条之后还有一条不带 plan 的 assistant。只有「最新的可操作 plan」该被清。
func planTranscript(t *testing.T) []*chat_entity.Message {
	actions := []canonical.PlanAction{
		{ID: canonical.PlanActionIDExecute, Kind: canonical.PlanActionApprove},
		{ID: canonical.PlanActionIDRefine, Kind: canonical.PlanActionRefine, RequiresFeedback: true},
	}
	user := msgWithBlocks(t, 1, blocks.TextBlock{Text: "plan it"})
	user.Role, user.Seq = "user", 1
	older := msgWithBlocks(t, 2, PlanBlock{Text: "# Old plan", Actions: actions})
	older.Seq = 2
	latest := msgWithBlocks(t, 3,
		blocks.TextBlock{Text: "here is the plan"},
		PlanBlock{Text: "# Plan", Actions: actions},
		blocks.ToolUseBlock{ID: "t1", Name: "Read", Input: map[string]any{"file_path": "/wt/a.go"}},
	)
	latest.Seq = 3
	trailing := msgWithBlocks(t, 4, blocks.TextBlock{Text: "no plan here"})
	trailing.Seq = 4
	return []*chat_entity.Message{user, older, latest, trailing}
}

func copyMessages(msgs []*chat_entity.Message) []*chat_entity.Message {
	out := make([]*chat_entity.Message, 0, len(msgs))
	for _, m := range msgs {
		c := *m
		out = append(out, &c)
	}
	return out
}

// planWrite 记下一次写回:哪条消息、写成什么正文。
type planWrite struct {
	id   int64
	body string
}

// servePlanTranscript 让整条读写(List + Update)与窄读写(ListMeta + FillBlocksByType +
// FillBlocks + CheckpointBlocks)都按同一份 fixture 作答,交回记录下来的写入。
func servePlanTranscript(t *testing.T, msgs []*chat_entity.Message) *[]planWrite {
	repo := registerMessageMock(t)
	writes := &[]planWrite{}
	record := func(m *chat_entity.Message) { *writes = append(*writes, planWrite{id: m.ID, body: m.BlocksJSON}) }
	repo.EXPECT().List(gomock.Any(), int64(7)).DoAndReturn(
		func(context.Context, int64) ([]*chat_entity.Message, error) { return copyMessages(msgs), nil }).AnyTimes()
	repo.EXPECT().Update(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, m *chat_entity.Message) error { record(m); return nil }).AnyTimes()
	repo.EXPECT().ListMeta(gomock.Any(), int64(7)).DoAndReturn(
		func(context.Context, int64) ([]*chat_entity.Message, error) { return metaOf(msgs), nil }).AnyTimes()
	repo.EXPECT().FillBlocksByType(gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(fillBlocksByTypeFrom(t, msgs)).AnyTimes()
	repo.EXPECT().FillBlocks(gomock.Any(), gomock.Any()).DoAndReturn(fillAllBlocksFrom(msgs)).AnyTimes()
	repo.EXPECT().CheckpointBlocks(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, m *chat_entity.Message, _ string) error { record(m); return nil }).AnyTimes()
	return writes
}

// fillAllBlocksFrom 模拟 FillBlocks:按 ID 回到 fixture 取全部正文。
func fillAllBlocksFrom(source []*chat_entity.Message) func(context.Context, []*chat_entity.Message) error {
	return func(_ context.Context, msgs []*chat_entity.Message) error {
		for _, m := range msgs {
			for _, src := range source {
				if src.ID == m.ID {
					m.BlocksJSON = src.BlocksJSON
				}
			}
		}
		return nil
	}
}

func planActionsOf(t *testing.T, body string) [][]canonical.PlanAction {
	m := &chat_entity.Message{BlocksJSON: body}
	bs, err := m.GetBlocks()
	require.NoError(t, err)
	var out [][]canonical.PlanAction
	for _, b := range bs {
		switch p := b.(type) {
		case PlanBlock:
			out = append(out, p.Actions)
		case *PlanBlock:
			out = append(out, p.Actions)
		}
	}
	return out
}

func TestClearLatestActionablePlanActions(t *testing.T) {
	Convey("clearLatestActionablePlanActions 只清最新一条带可操作 plan 的 assistant", t, func() {
		Convey("最新的可操作 plan 被清空 actions,其余块原样,较早的 plan 不动", func() {
			msgs := planTranscript(t)
			writes := servePlanTranscript(t, msgs)

			So((&chatSvc{}).clearLatestActionablePlanActions(context.Background(), 7), ShouldBeNil)

			So(*writes, ShouldHaveLength, 1)
			So((*writes)[0].id, ShouldEqual, 3)
			written := &chat_entity.Message{BlocksJSON: (*writes)[0].body}
			bs, err := written.GetBlocks()
			So(err, ShouldBeNil)
			So(bs, ShouldHaveLength, 3)
			So(planActionsOf(t, (*writes)[0].body), ShouldResemble, [][]canonical.PlanAction{nil})
			So(bs[0], ShouldResemble, blocks.TextBlock{Text: "here is the plan"})
		})

		Convey("没有可操作的 plan 时不写库", func() {
			user := msgWithBlocks(t, 1, blocks.TextBlock{Text: "plan it"})
			user.Role, user.Seq = "user", 1
			plain := msgWithBlocks(t, 2, PlanBlock{Text: "# Plan without actions"})
			plain.Seq = 2
			writes := servePlanTranscript(t, []*chat_entity.Message{user, plain})

			So((&chatSvc{}).clearLatestActionablePlanActions(context.Background(), 7), ShouldBeNil)
			So(*writes, ShouldBeEmpty)
		})

		Convey("只按 plan 块定位、只补命中那一条的正文,写回只改 plan 块那一行", func() {
			msgs := planTranscript(t)
			latestBody := msgs[2].BlocksJSON
			// 严格 mock:List / Update 没有期望,被调用即失败。
			repo := registerMessageMock(t)
			repo.EXPECT().ListMeta(gomock.Any(), int64(7)).Return(metaOf(msgs), nil).Times(1)
			repo.EXPECT().FillBlocksByType(gomock.Any(), gomock.Any(), []string{PlanBlock{}.Type()}).
				DoAndReturn(fillBlocksByTypeFrom(t, msgs)).Times(1)
			repo.EXPECT().FillBlocks(gomock.Any(), gomock.Len(1)).DoAndReturn(
				func(ctx context.Context, got []*chat_entity.Message) error {
					require.Equal(t, int64(3), got[0].ID, "只给命中的那条 assistant 补全文")
					return fillAllBlocksFrom(msgs)(ctx, got)
				}).Times(1)
			var diff transcript_entity.BlockDiff
			repo.EXPECT().CheckpointBlocks(gomock.Any(), gomock.Any(), latestBody).DoAndReturn(
				func(_ context.Context, m *chat_entity.Message, prev string) error {
					var err error
					diff, err = transcript_entity.DiffBlocks(m.ID, prev, m.BlocksJSON)
					return err
				}).Times(1)

			So((&chatSvc{}).clearLatestActionablePlanActions(context.Background(), 7), ShouldBeNil)
			So(diff.Upserts, ShouldHaveLength, 1)
			So(diff.Upserts[0].Idx, ShouldEqual, 1)
			So(diff.Upserts[0].Type, ShouldEqual, "plan")
			So(diff.TruncateFrom, ShouldEqual, -1)
		})
	})
}

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
