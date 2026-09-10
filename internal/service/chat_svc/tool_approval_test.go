package chat_svc

import (
	"context"
	"testing"

	. "github.com/smartystreets/goconvey/convey"
	"go.uber.org/mock/gomock"

	"github.com/agentre-hub/agentre/internal/model/entity/chat_entity"
	"github.com/agentre-hub/agentre/internal/repository/chat_repo"
	"github.com/agentre-hub/agentre/internal/repository/chat_repo/mock_chat_repo"
	"github.com/agentre-hub/agentre/internal/service/chat_svc/blocks"
)

// recordEmitter 收集每次 Emit 的 (stream, payload),供审批事件断言。
// BeginToolApproval/FinishToolApproval emit 的是裸 map[string]any(tool_approval 事件)
// 与 ChatStreamEvent(session_status patch),两类都收。
type recordEmitter struct {
	streams  []string
	payloads []any
}

func (r *recordEmitter) Emit(_ context.Context, name string, payload any) {
	r.streams = append(r.streams, name)
	r.payloads = append(r.payloads, payload)
}

// toolApprovalEvents 过滤出 kind=tool_approval 的裸 map 事件。
func (r *recordEmitter) toolApprovalEvents() []map[string]any {
	var out []map[string]any
	for _, p := range r.payloads {
		if m, ok := p.(map[string]any); ok && m["kind"] == "tool_approval" {
			out = append(out, m)
		}
	}
	return out
}

// newToolApprovalSvc 造一个注好宽松 session repo mock 的 chatSvc(Begin/Finish 都会
// Find(sessionID) 然后 markSessionWaiting/Running 写库)。
func newToolApprovalSvc(t *testing.T) (*chatSvc, *recordEmitter) {
	t.Helper()
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)
	sessRepo := mock_chat_repo.NewMockSessionRepo(ctrl)
	prev := chat_repo.Session()
	chat_repo.RegisterSession(sessRepo)
	t.Cleanup(func() { chat_repo.RegisterSession(prev) })

	sessRepo.EXPECT().Find(gomock.Any(), int64(42)).
		DoAndReturn(func(_ context.Context, id int64) (*chat_entity.Session, error) {
			return &chat_entity.Session{ID: id, AgentStatus: "running"}, nil
		}).AnyTimes()
	sessRepo.EXPECT().Update(gomock.Any(), gomock.Any()).Return(nil).AnyTimes()

	em := &recordEmitter{}
	svc := &chatSvc{
		emitter:       em,
		toolApprovals: map[int64][]*blocks.ToolApprovalBlock{},
	}
	return svc, em
}

func TestToolApprovalLifecycle(t *testing.T) {
	Convey("工具审批编排 Begin/Finish/take/snapshot 全生命周期", t, func() {
		svc, em := newToolApprovalSvc(t)
		ctx := context.Background()

		// 1. 无活跃 turn → Begin 返回 error + nil channel。
		blk1 := &blocks.ToolApprovalBlock{
			ToolKey: "org", RequestID: "req-1", ToolName: "org_invite",
			ToolInput: map[string]any{"user_id": "u-1"}, Status: "pending",
		}
		ch, err := svc.BeginToolApproval(ctx, 42, blk1)
		So(err, ShouldNotBeNil)
		So(ch, ShouldBeNil)
		So(em.toolApprovalEvents(), ShouldHaveLength, 0)

		// 2. 登记活跃 turn 流名后 Begin 成功,返回 channel,emitter 收到 pending 事件。
		svc.activeTurnStreams.Store(int64(42), StreamName(42, 7))
		ch, err = svc.BeginToolApproval(ctx, 42, blk1)
		So(err, ShouldBeNil)
		So(ch, ShouldNotBeNil)
		evs := em.toolApprovalEvents()
		So(evs, ShouldHaveLength, 1)
		So(evs[0]["toolKey"], ShouldEqual, "org")
		So(evs[0]["requestId"], ShouldEqual, "req-1")
		So(evs[0]["toolName"], ShouldEqual, "org_invite")
		So(evs[0]["status"], ShouldEqual, "pending")

		// 3. FinishToolApproval(denied) → emitter 收到 denied;内部块状态已更新。
		So(svc.FinishToolApproval(ctx, 42, "req-1", "denied", "用户拒绝"), ShouldBeNil)
		evs = em.toolApprovalEvents()
		So(evs, ShouldHaveLength, 2)
		So(evs[1]["requestId"], ShouldEqual, "req-1")
		So(evs[1]["status"], ShouldEqual, "denied")
		So(evs[1]["result"], ShouldEqual, "用户拒绝")
		// 内部登记块已被改为 denied。
		snap := svc.snapshotToolApprovals(42)
		So(snap, ShouldHaveLength, 1)
		So(snap[0].Status, ShouldEqual, "denied")

		// 4. 再 Begin 一条 pending;take 返回 2 条(denied 保留 + 新 pending 标 expired),
		//    且 take 后 snapshot 变空。
		blk2 := &blocks.ToolApprovalBlock{
			ToolKey: "org", RequestID: "req-2", ToolName: "org_set_manager",
			ToolInput: map[string]any{"user_id": "u-2"}, Status: "pending",
		}
		_, err = svc.BeginToolApproval(ctx, 42, blk2)
		So(err, ShouldBeNil)
		So(svc.snapshotToolApprovals(42), ShouldHaveLength, 2)

		taken := svc.takeToolApprovals(42)
		So(taken, ShouldHaveLength, 2)
		statusByID := map[string]string{}
		for _, b := range taken {
			statusByID[b.RequestID] = b.Status
		}
		So(statusByID["req-1"], ShouldEqual, "denied")  // 终态保留
		So(statusByID["req-2"], ShouldEqual, "expired") // pending → expired
		So(svc.snapshotToolApprovals(42), ShouldHaveLength, 0)

		// 5. Finish 已取走的 requestID → error。
		So(svc.FinishToolApproval(ctx, 42, "req-1", "approved", ""), ShouldNotBeNil)

		// 6. snapshot 返回的是拷贝,改它不影响内部登记。
		svc.activeTurnStreams.Store(int64(42), StreamName(42, 9))
		blk3 := &blocks.ToolApprovalBlock{ToolKey: "org", RequestID: "req-3", ToolName: "org_invite", Status: "pending"}
		_, err = svc.BeginToolApproval(ctx, 42, blk3)
		So(err, ShouldBeNil)
		snap2 := svc.snapshotToolApprovals(42)
		So(snap2, ShouldHaveLength, 1)
		snap2[0].Status = "mutated-copy"
		So(svc.snapshotToolApprovals(42)[0].Status, ShouldEqual, "pending")
	})
}

func TestAnswerToolApproval(t *testing.T) {
	Convey("AnswerToolApproval 按 requestID 路由唤醒挂起的写工具调用", t, func() {
		svc, _ := newToolApprovalSvc(t)
		ctx := context.Background()
		svc.activeTurnStreams.Store(int64(42), StreamName(42, 7))

		Convey("空 requestID → error", func() {
			So(svc.AnswerToolApproval(ctx, 42, "", true), ShouldNotBeNil)
		})

		Convey("未知 requestID → error", func() {
			So(svc.AnswerToolApproval(ctx, 42, "nope", true), ShouldNotBeNil)
		})

		Convey("Begin 后 Answer 命中 waiter → channel 收到决策;重复 Answer → error", func() {
			blk := &blocks.ToolApprovalBlock{ToolKey: "org", RequestID: "req-a", ToolName: "org_invite", Status: "pending"}
			ch, err := svc.BeginToolApproval(ctx, 42, blk)
			So(err, ShouldBeNil)

			So(svc.AnswerToolApproval(ctx, 42, "req-a", true), ShouldBeNil)
			So(<-ch, ShouldBeTrue)
			// LoadAndDelete 已摘除 waiter,重复 Answer → error。
			So(svc.AnswerToolApproval(ctx, 42, "req-a", true), ShouldNotBeNil)
		})

		Convey("FinishToolApproval 清 waiter 后 Answer 同 requestID → error", func() {
			blk := &blocks.ToolApprovalBlock{ToolKey: "org", RequestID: "req-b", ToolName: "org_invite", Status: "pending"}
			_, err := svc.BeginToolApproval(ctx, 42, blk)
			So(err, ShouldBeNil)
			So(svc.FinishToolApproval(ctx, 42, "req-b", "denied", ""), ShouldBeNil)
			So(svc.AnswerToolApproval(ctx, 42, "req-b", true), ShouldNotBeNil)
		})
	})
}
