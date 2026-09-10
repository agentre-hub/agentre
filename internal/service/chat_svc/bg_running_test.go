package chat_svc

import (
	"context"
	"testing"

	cagoblocks "github.com/cago-frame/agents/agent/blocks"
	"go.uber.org/mock/gomock"

	"github.com/agentre-hub/agentre/internal/model/entity/chat_entity"
	"github.com/agentre-hub/agentre/internal/repository/chat_repo"
	"github.com/agentre-hub/agentre/internal/repository/chat_repo/mock_chat_repo"
	"github.com/agentre-hub/agentre/internal/service/chat_svc/blocks"
)

func TestBgRunningSet_AddRemoveClearActive(t *testing.T) {
	s := &chatSvc{}
	if s.bgRunningActive(7) {
		t.Fatal("empty session should be inactive")
	}
	if !s.addBgRunning(7, "tu-1", "tu-2") {
		t.Fatal("first add should report changed")
	}
	if !s.bgRunningActive(7) {
		t.Fatal("session should be active after add")
	}
	if s.addBgRunning(7, "tu-1") {
		t.Fatal("re-add existing id should be no-op (idempotent)")
	}
	if !s.removeBgRunning(7, "tu-1") {
		t.Fatal("remove existing should report changed")
	}
	if s.removeBgRunning(7, "tu-1") {
		t.Fatal("remove missing should be no-op")
	}
	if !s.bgRunningActive(7) {
		t.Fatal("still active: tu-2 remains")
	}
	if !s.clearBgRunning(7) {
		t.Fatal("clear non-empty should report changed")
	}
	if s.bgRunningActive(7) {
		t.Fatal("inactive after clear")
	}
	if s.clearBgRunning(7) {
		t.Fatal("clear empty should be no-op")
	}
}

func TestEmitBgRunningStatus_CarriesFlag(t *testing.T) {
	rec := &captureEmitter{}
	s := &chatSvc{emitter: rec}
	s.addBgRunning(9, "tu-x")
	sess := &chat_entity.Session{ID: 9, AgentStatus: "idle"}
	s.emitBgRunningStatus(context.Background(), sess, "stream-9")

	if len(rec.events) != 1 {
		t.Fatalf("want 1 event, got %d", len(rec.events))
	}
	ev := rec.events[0]
	if ev.Kind != StreamSessionStatus || ev.SessionStatus == nil {
		t.Fatalf("want session_status event, got %+v", ev)
	}
	if !ev.SessionStatus.BgRunning {
		t.Fatal("want BgRunning=true")
	}
	if ev.SessionStatus.AgentStatus != "idle" {
		t.Fatalf("want agentStatus idle, got %q", ev.SessionStatus.AgentStatus)
	}
}

func TestSessionLiteFromEntity_CarriesBgRunning(t *testing.T) {
	s := &chatSvc{}
	s.addBgRunning(42, "tu-bg")
	sess := &chat_entity.Session{ID: 42, AgentStatus: "idle"}
	lite := s.sessionLiteFromEntity(sess)
	if !lite.BgRunning {
		t.Fatal("want ChatSessionLite.BgRunning=true after addBgRunning")
	}
}

func TestReconcileBgRunningOnFinalize_AddsAndEmits(t *testing.T) {
	rec := &captureEmitter{}
	s := &chatSvc{emitter: rec}
	sess := &chat_entity.Session{}
	sess.ID = 5
	sess.AgentStatus = "idle"
	final := []cagoblocks.ContentBlock{
		&cagoblocks.ToolUseBlock{ID: "bg-1", Input: map[string]any{"run_in_background": true}},
		&blocks.SubagentStateBlock{ParentToolCallID: "bg-1", Kind: "local_agent", Status: "running"},
	}
	s.reconcileBgRunningOnFinalize(context.Background(), sess, final, "stream-5")
	if !s.bgRunningActive(5) {
		t.Fatal("want active after finalize with running bg subagent")
	}
	if len(rec.events) != 1 || !rec.events[0].SessionStatus.BgRunning {
		t.Fatalf("want 1 session_status event with BgRunning=true, got %+v", rec.events)
	}
}

func TestReconcileBgRunningOnComplete_RemovesAndEmits(t *testing.T) {
	rec := &captureEmitter{}
	s := &chatSvc{emitter: rec}
	s.addBgRunning(11, "bg-done")
	if !s.bgRunningActive(11) {
		t.Fatal("precondition: active")
	}
	sess := &chat_entity.Session{}
	sess.ID = 11
	sess.AgentStatus = "idle"
	s.reconcileBgRunningOnComplete(context.Background(), sess, "bg-done", "stream-11")
	if s.bgRunningActive(11) {
		t.Fatal("want inactive after complete removes bg-done")
	}
	if len(rec.events) != 1 || rec.events[0].SessionStatus.BgRunning {
		t.Fatalf("want 1 session_status event with BgRunning=false, got %+v", rec.events)
	}
}

func TestRunningBgSubagentIDs(t *testing.T) {
	blks := []cagoblocks.ContentBlock{
		// 后台 subagent(显式): Agent tool_use run_in_background=true + running
		&cagoblocks.ToolUseBlock{ID: "bg-1", Input: map[string]any{"run_in_background": true}},
		&blocks.SubagentStateBlock{ParentToolCallID: "bg-1", Kind: "local_agent", Status: "running"},
		// 后台 subagent(默认): Agent 工具默认后台,run_in_background 缺省即后台 → 纳入
		&cagoblocks.ToolUseBlock{ID: "bg-default", Input: map[string]any{}},
		&blocks.SubagentStateBlock{ParentToolCallID: "bg-default", Kind: "local_agent", Status: "running"},
		// 前台(同步) subagent: 显式 run_in_background=false → 不纳入
		&cagoblocks.ToolUseBlock{ID: "fg-1", Input: map[string]any{"run_in_background": false}},
		&blocks.SubagentStateBlock{ParentToolCallID: "fg-1", Kind: "local_agent", Status: "running"},
		// 前台 Bash: local_bash 默认前台,无 run_in_background → 不纳入
		&cagoblocks.ToolUseBlock{ID: "bash-fg", Input: map[string]any{}},
		&blocks.SubagentStateBlock{ParentToolCallID: "bash-fg", Kind: "local_bash", Status: "running"},
		// 后台 Bash: local_bash + run_in_background=true → 纳入
		&cagoblocks.ToolUseBlock{ID: "bash-bg", Input: map[string]any{"run_in_background": true}},
		&blocks.SubagentStateBlock{ParentToolCallID: "bash-bg", Kind: "local_bash", Status: "running"},
		// 后台但已完成 → status 非 running → 不纳入
		&cagoblocks.ToolUseBlock{ID: "done-1", Input: map[string]any{"run_in_background": true}},
		&blocks.SubagentStateBlock{ParentToolCallID: "done-1", Kind: "local_agent", Status: "completed"},
	}
	got := runningBgSubagentIDs(blks)
	want := map[string]bool{"bg-1": true, "bg-default": true, "bash-bg": true}
	if len(got) != len(want) {
		t.Fatalf("want %d ids %v, got %v", len(want), want, got)
	}
	for _, id := range got {
		if !want[id] {
			t.Fatalf("unexpected id %q in %v (want exactly %v)", id, got, want)
		}
	}
}

func TestClearBgRunningOnSourceClosed_ClearsSet(t *testing.T) {
	s := &chatSvc{}
	s.addBgRunning(3, "bg-x")
	if !s.bgRunningActive(3) {
		t.Fatal("precondition: session 3 should be active")
	}
	s.clearBgRunningOnSourceClosed(3)
	if s.bgRunningActive(3) {
		t.Fatal("want inactive after source closed")
	}
}

// TestMarkSessionWaiting_CarriesBgRunning verifies that markSessionWaiting emits
// BgRunning=true when the session has an active background subagent.
func TestMarkSessionWaiting_CarriesBgRunning(t *testing.T) {
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)
	sessRepo := mock_chat_repo.NewMockSessionRepo(ctrl)
	prev := chat_repo.Session()
	chat_repo.RegisterSession(sessRepo)
	t.Cleanup(func() { chat_repo.RegisterSession(prev) })
	sessRepo.EXPECT().Update(gomock.Any(), gomock.Any()).Return(nil).AnyTimes()

	rec := &captureEmitter{}
	s := &chatSvc{emitter: rec}
	s.addBgRunning(20, "tu-bg")

	sess := &chat_entity.Session{ID: 20, AgentStatus: "running"}
	s.markSessionWaiting(context.Background(), sess, "stream-20")

	if len(rec.events) != 1 {
		t.Fatalf("want 1 event, got %d", len(rec.events))
	}
	ev := rec.events[0]
	if ev.SessionStatus == nil {
		t.Fatal("want session_status event with non-nil SessionStatus")
	}
	if !ev.SessionStatus.BgRunning {
		t.Fatal("want BgRunning=true when bg subagent is active")
	}
}

// TestMarkSessionRunning_CarriesBgRunning verifies that markSessionRunning emits
// BgRunning=true when the session has an active background subagent.
func TestMarkSessionRunning_CarriesBgRunning(t *testing.T) {
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)
	sessRepo := mock_chat_repo.NewMockSessionRepo(ctrl)
	prev := chat_repo.Session()
	chat_repo.RegisterSession(sessRepo)
	t.Cleanup(func() { chat_repo.RegisterSession(prev) })
	sessRepo.EXPECT().Update(gomock.Any(), gomock.Any()).Return(nil).AnyTimes()

	rec := &captureEmitter{}
	s := &chatSvc{emitter: rec}
	s.addBgRunning(21, "tu-bg")

	// Start from waiting so markSessionRunning doesn't short-circuit.
	sess := &chat_entity.Session{ID: 21, AgentStatus: "waiting", NeedsAttention: true}
	s.markSessionRunning(context.Background(), sess, "stream-21")

	if len(rec.events) != 1 {
		t.Fatalf("want 1 event, got %d", len(rec.events))
	}
	ev := rec.events[0]
	if ev.SessionStatus == nil {
		t.Fatal("want session_status event with non-nil SessionStatus")
	}
	if !ev.SessionStatus.BgRunning {
		t.Fatal("want BgRunning=true when bg subagent is active")
	}
}
