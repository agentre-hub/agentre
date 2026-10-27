package chat_svc

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	cagoblocks "github.com/cago-frame/agents/agent/blocks"
	"github.com/cago-frame/agents/provider"
	"github.com/cago-frame/cago/pkg/logger"
	. "github.com/smartystreets/goconvey/convey"
	"go.uber.org/mock/gomock"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/chat_entity"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
	"github.com/agentre-hub/agentre/internal/pkg/transcript/blocks"
	"github.com/agentre-hub/agentre/internal/pkg/transcript/ctlbind"
	"github.com/agentre-hub/agentre/internal/pkg/transcript/turn"
)

// ctl_subagent_test.go 钉住 agrctl send --wait 的本机前台子代理卡(spec 2026-10-27
// 「Binding」「Card lifecycle」):父轮(本机、进行中)的那条 shell 调用经 t.mu 原地升级成
// agrctl overlay;子会话的工具调用 / 结果实时镜像成嵌套步骤且只出现一次;进度、模型、
// 用量跟着子会话走;终态在 Finish 返回时已经落定(serveSend 随后才写响应、shell 才拿到
// 结果);一条 shell 调用只绑一个子会话;不能确定时一律不绑并记 info 日志。
//
// 父轮驱动的是生产的 runTurn(approvalTurnRig,会话 41);子会话是一只真实的 turnRun,
// 经生产的 applyLive 投递事件 —— 镜像接在那里,事件循环怎么与父轮交错正是要守的东西。

const ctlChildSessionID = int64(99)

const reviewCommand = `agrctl send --wait --agent reviewer "审一下 PR 42"`

func bashCall(id, command string, background bool) agentruntime.ToolCall {
	in := map[string]any{"command": command}
	if background {
		in["run_in_background"] = true
	}
	raw, _ := json.Marshal(in)
	return agentruntime.ToolCall{ID: id, Name: "Bash", Input: raw}
}

func reviewBind() CtlSendBindRequest {
	return CtlSendBindRequest{CallerSessionID: approvalSessionID, AgentName: "reviewer", Task: "审一下 PR 42"}
}

// newCtlChildTurn 起一只子会话的 turnRun(本机 runtime);事件经 feedChild 走生产的 applyLive。
func newCtlChildTurn(svc *chatSvc, model string) *turnRun {
	child := &turnRun{
		svc:  svc,
		sess: &chat_entity.Session{ID: ctlChildSessionID, AgentID: 8},
		be:   &agent_backend_entity.AgentBackend{ID: 12, Type: string(agent_backend_entity.TypeClaudeCode)},
		assistantMsg: &chat_entity.Message{
			ID: 9901, SessionID: ctlChildSessionID, Role: "assistant", Model: model, BlocksJSON: "[]",
		},
		stream: StreamName(ctlChildSessionID, 9901),
		acc:    turn.New(),
	}
	child.initSegment(time.Now())
	return child
}

func feedChild(child *turnRun, ev agentruntime.Event) {
	child.locked(func() { child.applyLive(context.Background(), ev, false) })
}

// parentView 在父轮的 t.mu 下取一份父转录快照(值拷贝,不与镜像那条 goroutine 竞争)。
type parentView struct {
	overlays      []blocks.SubagentStateBlock
	nestedUses    []string
	nestedResults []string
}

func readParent(svc *chatSvc) parentView {
	t := svc.activeTurn(approvalSessionID)
	if t == nil {
		return parentView{}
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return viewOf(t.acc.Snapshot())
}

func outerCalls(svc *chatSvc) int {
	t := svc.activeTurn(approvalSessionID)
	t.mu.Lock()
	defer t.mu.Unlock()
	return len(t.acc.OuterToolUses())
}

func viewOf(snapshot []cagoblocks.ContentBlock) parentView {
	var v parentView
	for _, b := range snapshot {
		switch blk := b.(type) {
		case *blocks.SubagentStateBlock:
			cp := *blk
			cp.Runs = append([]agentruntime.SubagentRun(nil), blk.Runs...)
			v.overlays = append(v.overlays, cp)
		case *blocks.NestedToolUseBlock:
			v.nestedUses = append(v.nestedUses, blk.ID)
		case *blocks.NestedToolResultBlock:
			v.nestedResults = append(v.nestedResults, blk.ToolCallID)
		}
	}
	return v
}

func eventually(cond func() bool) bool {
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		time.Sleep(time.Millisecond)
	}
	return cond()
}

// observeChatLogs 把全局 logger 换成可观测的 core,用例结束还原。
func observeChatLogs(t *testing.T) *observer.ObservedLogs {
	core, logs := observer.New(zapcore.InfoLevel)
	old := logger.Default()
	logger.SetLogger(zap.New(core))
	t.Cleanup(func() { logger.SetLogger(old) })
	return logs
}

func ctlNotBoundReasons(logs *observer.ObservedLogs) []string {
	entries := logs.FilterMessage("chat_svc.bindCtlSend: subagent card not bound").All()
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		out = append(out, e.ContextMap()["reason"].(string))
	}
	return out
}

func (r *approvalTurnRig) subagentEvents(kind ChatStreamEventKind) []ChatStreamEvent {
	r.emitter.mu.Lock()
	defer r.emitter.mu.Unlock()
	var out []ChatStreamEvent
	for _, p := range r.emitter.payloads {
		if ev, ok := p.(ChatStreamEvent); ok && ev.Kind == kind {
			out = append(out, ev)
		}
	}
	return out
}

func TestCtlSend_GivenLocalForegroundShellCall_WhenChildRunsToCompletion_ThenCardIsLiveAndSettledBeforeReturn(t *testing.T) {
	Convey("Given 本机父轮正在跑 `agrctl send --wait` 的前台 Bash, 子会话先吐了一步才绑定", t, func() {
		rig := startApprovalTurn(t)
		ctx := context.Background()
		rig.deps.message.EXPECT().UpdateUsage(gomock.Any(), int64(9901), gomock.Any()).Return(nil).AnyTimes()

		rig.feed(bashCall("sh-1", reviewCommand, false), StreamToolUse)
		watch := rig.svc.WatchCtlSend(ctlChildSessionID)
		defer watch.Close()
		child := newCtlChildTurn(rig.svc, "claude-sonnet-4")
		feedChild(child, agentruntime.ToolCall{ID: "c-1", Name: "Read", Input: json.RawMessage(`{"file_path":"a.go"}`)})

		Convey("When 绑定, Then 那条 shell 调用原地升级成运行中的 agrctl overlay(落库 + 实时事件),起轮前的那一步也补上", func() {
			So(watch.Bind(ctx, reviewBind()), ShouldBeTrue)

			v := readParent(rig.svc)
			So(v.overlays, ShouldHaveLength, 1)
			o := v.overlays[0]
			So(o.ParentToolCallID, ShouldEqual, "sh-1")
			So(o.Origin, ShouldEqual, agentruntime.SubagentOriginAgrctl)
			So(o.Kind, ShouldEqual, agentruntime.SubagentKindAgrctl)
			So(o.ChildSessionID, ShouldEqual, ctlChildSessionID)
			So(o.Status, ShouldEqual, "running")
			So(o.Description, ShouldEqual, "审一下 PR 42")
			So(o.Runs, ShouldHaveLength, 1)
			So(o.Runs[0].Agent, ShouldEqual, "reviewer")
			So(o.Runs[0].Task, ShouldEqual, "审一下 PR 42")
			So(o.Runs[0].Status, ShouldEqual, "running")

			stored := rig.lastCheckpoint()
			So(blockTypes(stored), ShouldContain, "subagent_state")
			started := rig.subagentEvents(StreamSubagentStarted)
			So(started, ShouldHaveLength, 1)
			So(started[0].ToolCallID, ShouldEqual, "sh-1")

			So(eventually(func() bool {
				return len(readParent(rig.svc).nestedUses) == 1
			}), ShouldBeTrue)
			So(readParent(rig.svc).nestedUses[0], ShouldEqual, ctlbind.NestedToolCallID("sh-1", "c-1"))

			Convey("When 子会话继续(同一调用重复投递、结果、用量), Then 嵌套步骤各只一份,进度/模型/用量跟上", func() {
				feedChild(child, agentruntime.ToolCall{ID: "c-1", Name: "Read", Input: json.RawMessage(`{"file_path":"a.go"}`)})
				feedChild(child, agentruntime.ToolResult{ToolCallID: "c-1", Content: "package a"})
				feedChild(child, agentruntime.ToolResult{ToolCallID: "c-1", Content: "package a"})
				feedChild(child, agentruntime.UsageUpdate{Usage: &provider.Usage{CompletionTokens: 30}, TotalInputTokens: 1000})

				So(eventually(func() bool {
					v := readParent(rig.svc)
					return len(v.nestedResults) == 1 && len(v.overlays) == 1 && v.overlays[0].TotalTokens == 1030
				}), ShouldBeTrue)
				v := readParent(rig.svc)
				So(v.nestedUses, ShouldHaveLength, 1)
				o := v.overlays[0]
				So(o.ToolUses, ShouldEqual, 1)
				So(o.LastToolName, ShouldEqual, "Read")
				So(o.Model, ShouldEqual, "claude-sonnet-4")

				Convey("When Finish(completed), Then 返回时卡片已是完成态并带最终文本、实时 done 已发,之后子会话的事件不再镜像", func() {
					watch.Finish(ctx, CtlSendOutcome{Status: CtlSendCompleted, FinalText: "LGTM", AssistantMessageID: 9901})

					v := readParent(rig.svc)
					o := v.overlays[0]
					So(o.Status, ShouldEqual, "completed")
					So(o.FinalText, ShouldEqual, "LGTM")
					So(o.Runs[0].Status, ShouldEqual, "completed")
					So(o.Runs[0].Summary, ShouldEqual, "LGTM")
					So(o.Runs[0].Model, ShouldEqual, "claude-sonnet-4")
					So(rig.subagentEvents(StreamSubagentDone), ShouldHaveLength, 1)
					So(approvalsOverlayStatus(rig.lastCheckpoint()), ShouldEqual, "completed")

					feedChild(child, agentruntime.ToolCall{ID: "c-2", Name: "Bash"})
					time.Sleep(20 * time.Millisecond)
					So(readParent(rig.svc).nestedUses, ShouldHaveLength, 1)

					rig.feed(agentruntime.ToolResult{ToolCallID: "sh-1", Content: "LGTM"}, StreamToolResult)
					rig.endTurn()
					rig.mu.Lock()
					final := decodeStored(t, rig.finalBlocks)
					rig.mu.Unlock()
					So(approvalsOverlayStatus(final), ShouldEqual, "completed")
				})

				Convey("When Finish(failed), Then 卡片收成失败,原因进最终文本,已镜像的步骤保留", func() {
					watch.Finish(ctx, CtlSendOutcome{Status: CtlSendFailed, FinalText: "boom"})
					v := readParent(rig.svc)
					So(v.overlays[0].Status, ShouldEqual, "failed")
					So(v.overlays[0].FinalText, ShouldEqual, "boom")
					So(v.overlays[0].Runs[0].ErrorMessage, ShouldEqual, "boom")
					So(v.nestedUses, ShouldHaveLength, 1)
				})

				Convey("When Finish(canceled), Then 卡片显示已停止", func() {
					watch.Finish(ctx, CtlSendOutcome{Status: CtlSendCanceled})
					So(readParent(rig.svc).overlays[0].Status, ShouldEqual, "canceled")
				})
			})
		})
	})
}

// approvalsOverlayStatus 取落库转录里唯一那块 subagent_state 的状态。
func approvalsOverlayStatus(stored []storedBlock) string {
	for _, b := range stored {
		if b.Type != "subagent_state" {
			continue
		}
		var sb blocks.SubagentStateBlock
		if err := json.Unmarshal(b.Data, &sb); err == nil {
			return sb.Status
		}
	}
	return ""
}

func TestCtlSend_GivenChildWithoutLiveModel_WhenFinished_ThenModelComesFromChildFinalMessage(t *testing.T) {
	Convey("Given 子会话起轮时还不知道模型(CLI 登录), When 收尾, Then 模型取自子轮最终那条消息", t, func() {
		rig := startApprovalTurn(t)
		ctx := context.Background()
		rig.deps.message.EXPECT().Find(gomock.Any(), int64(9901)).
			Return(&chat_entity.Message{ID: 9901, SessionID: ctlChildSessionID, Model: "gpt-5"}, nil)
		rig.feed(bashCall("sh-1", reviewCommand, false), StreamToolUse)
		watch := rig.svc.WatchCtlSend(ctlChildSessionID)
		defer watch.Close()
		So(watch.Bind(ctx, reviewBind()), ShouldBeTrue)

		watch.Finish(ctx, CtlSendOutcome{Status: CtlSendCompleted, FinalText: "ok", AssistantMessageID: 9901})
		o := readParent(rig.svc).overlays[0]
		So(o.Model, ShouldEqual, "gpt-5")
		So(o.Runs[0].Model, ShouldEqual, "gpt-5")
	})
}

func TestCtlSend_GivenShellCallAlreadyBound_WhenSecondSendArrives_ThenItStaysUnbound(t *testing.T) {
	Convey("Given 同一条 shell 调用已绑过一个子会话, When 同一命令里的第二个 send --wait 到达, Then 不再绑定,记 already_bound", t, func() {
		logs := observeChatLogs(t)
		rig := startApprovalTurn(t)
		ctx := context.Background()
		rig.feed(bashCall("sh-1", reviewCommand+"; "+reviewCommand, false), StreamToolUse)
		first := rig.svc.WatchCtlSend(ctlChildSessionID)
		defer first.Close()
		So(first.Bind(ctx, reviewBind()), ShouldBeTrue)

		second := rig.svc.WatchCtlSend(ctlChildSessionID + 1)
		defer second.Close()
		So(second.Bind(ctx, reviewBind()), ShouldBeFalse)
		So(ctlNotBoundReasons(logs), ShouldResemble, []string{string(ctlbind.ReasonAlreadyBound)})

		v := readParent(rig.svc)
		So(v.overlays, ShouldHaveLength, 1)
		So(v.overlays[0].ChildSessionID, ShouldEqual, ctlChildSessionID)
		second.Finish(ctx, CtlSendOutcome{Status: CtlSendFailed, FinalText: "x"})
		So(readParent(rig.svc).overlays[0].Status, ShouldEqual, "running")
	})
}

func TestCtlSend_GivenNoSafeTarget_ThenNothingIsBoundAndTheReasonIsLogged(t *testing.T) {
	Convey("不能确定子会话对应哪条 shell 调用时,一律不绑定、父转录不变、记 info 日志", t, func() {
		logs := observeChatLogs(t)
		ctx := context.Background()

		Convey("调用方会话没有在跑的轮 → no_active_turn", func() {
			deps := setupPeerSessionTest(t)
			w := deps.svc.WatchCtlSend(ctlChildSessionID)
			defer w.Close()
			So(w.Bind(ctx, reviewBind()), ShouldBeFalse)
			So(ctlNotBoundReasons(logs), ShouldResemble, []string{"no_active_turn"})
		})

		Convey("调用方会话的轮在 agentred 上跑(远端父轮)且 agentred 没有附来绑定 → no_host_binding", func() {
			deps := setupPeerSessionTest(t)
			parent := &turnRun{
				svc: deps.svc, sess: &chat_entity.Session{ID: approvalSessionID}, acc: turn.New(),
				previews: make(chan agentruntime.Event),
			}
			deps.svc.activeTurns.Store(approvalSessionID, parent)
			w := deps.svc.WatchCtlSend(ctlChildSessionID)
			defer w.Close()
			So(w.Bind(ctx, reviewBind()), ShouldBeFalse)
			So(ctlNotBoundReasons(logs), ShouldResemble, []string{"no_host_binding"})
		})

		cases := []struct {
			name   string
			calls  []agentruntime.ToolCall
			reason string
		}{
			{"命令里没有 agrctl send → no_candidate", []agentruntime.ToolCall{bashCall("sh-1", "ls -la", false)}, string(ctlbind.ReasonNoCandidate)},
			{"两条一模一样的命令 → ambiguous", []agentruntime.ToolCall{
				bashCall("sh-1", reviewCommand, false), bashCall("sh-2", reviewCommand, false),
			}, string(ctlbind.ReasonAmbiguous)},
		}
		for _, tc := range cases {
			Convey(tc.name, func() {
				rig := startApprovalTurn(t)
				for _, c := range tc.calls {
					rig.feed(c, StreamToolUse)
				}
				// feed 只等「见过 tool_use」:第二条要另等它真的进了父轮的累加器。
				So(eventually(func() bool { return outerCalls(rig.svc) == len(tc.calls) }), ShouldBeTrue)
				w := rig.svc.WatchCtlSend(ctlChildSessionID)
				defer w.Close()
				child := newCtlChildTurn(rig.svc, "m")
				feedChild(child, agentruntime.ToolCall{ID: "c-1", Name: "Read"})

				So(w.Bind(ctx, reviewBind()), ShouldBeFalse)
				So(ctlNotBoundReasons(logs), ShouldResemble, []string{tc.reason})
				w.Finish(ctx, CtlSendOutcome{Status: CtlSendCompleted, FinalText: "x"})
				v := readParent(rig.svc)
				So(v.overlays, ShouldBeEmpty)
				So(v.nestedUses, ShouldBeEmpty)
				So(rig.subagentEvents(StreamSubagentStarted), ShouldBeEmpty)
			})
		}
	})
}

func TestCtlSend_GivenParentLockHeld_WhenChildEmits_ThenChildEventLoopNeverWaitsOnParent(t *testing.T) {
	Convey("Given 已绑定且父轮的 t.mu 正被占着, When 子会话吐事件, Then 子会话的事件循环立刻返回,父轮放手后照样镜像", t, func() {
		rig := startApprovalTurn(t)
		ctx := context.Background()
		rig.feed(bashCall("sh-1", reviewCommand, false), StreamToolUse)
		watch := rig.svc.WatchCtlSend(ctlChildSessionID)
		defer watch.Close()
		So(watch.Bind(ctx, reviewBind()), ShouldBeTrue)
		child := newCtlChildTurn(rig.svc, "m")

		parent := rig.svc.activeTurn(approvalSessionID)
		parent.mu.Lock()
		done := make(chan struct{})
		go func() {
			feedChild(child, agentruntime.ToolCall{ID: "c-1", Name: "Read"})
			close(done)
		}()
		var returned bool
		select {
		case <-done:
			returned = true
		case <-time.After(time.Second):
		}
		parent.mu.Unlock()
		So(returned, ShouldBeTrue)
		So(eventually(func() bool { return len(readParent(rig.svc).nestedUses) == 1 }), ShouldBeTrue)
	})
}

func TestCtlSend_GivenForegroundCallStillRunningWhenParentTurnEnds_ThenTheCardIsSettledInTheOriginalMessage(t *testing.T) {
	Convey("Given 前台卡已绑定,父轮先结束而子任务还在跑(例如 Codex 的命令在父轮结束后仍在运行)", t, func() {
		rig := startApprovalTurn(t)
		ctx := context.Background()
		store := expectCardWrites(rig)
		rig.deps.message.EXPECT().UpdateUsage(gomock.Any(), int64(9901), gomock.Any()).Return(nil).AnyTimes()
		rig.feed(bashCall("sh-1", reviewCommand, false), StreamToolUse)
		watch := rig.svc.WatchCtlSend(ctlChildSessionID)
		defer watch.Close()
		So(watch.Bind(ctx, reviewBind()), ShouldBeTrue)
		child := newCtlChildTurn(rig.svc, "gpt-5")
		rig.endTurn()

		Convey("Then 父轮收尾不把卡片翻成已取消", func() {
			So(store.load().Status, ShouldEqual, "running")
		})

		Convey("When 子会话继续出步骤并收尾, Then 步骤、进度与终态写回原消息,不碰已收口那一轮的转录", func() {
			checkpointsBefore := parentCheckpoints(rig)

			feedChild(child, agentruntime.ToolCall{ID: "c-1", Name: "Read", Input: json.RawMessage(`{"file_path":"a.go"}`)})
			feedChild(child, agentruntime.ToolResult{ToolCallID: "c-1", Content: "package a"})
			feedChild(child, agentruntime.ToolCall{ID: "c-1", Name: "Read", Input: json.RawMessage(`{"file_path":"a.go"}`)})
			So(eventually(func() bool { return len(store.children()) == 2 }), ShouldBeTrue)

			watch.Finish(ctx, CtlSendOutcome{Status: CtlSendCompleted, FinalText: "late"})

			o := store.load()
			So(o.Status, ShouldEqual, "completed")
			So(o.FinalText, ShouldEqual, "late")
			So(o.ToolUses, ShouldEqual, 1)
			So(o.Model, ShouldEqual, "gpt-5")
			So(o.Runs, ShouldHaveLength, 1)
			So(o.Runs[0].Status, ShouldEqual, "completed")
			So(o.Runs[0].Summary, ShouldEqual, "late")
			So(o.Runs[0].Agent, ShouldEqual, "reviewer")
			So(store.children(), ShouldResemble, []string{
				"nested_tool_use:" + ctlbind.NestedToolCallID("sh-1", "c-1"),
				"nested_tool_result:" + ctlbind.NestedToolCallID("sh-1", "c-1"),
			})
			So(store.childIDs(), ShouldResemble, []string{ctlbind.NestedToolCallID("sh-1", "c-1")})

			So(parentCheckpoints(rig), ShouldEqual, checkpointsBefore)
			// 实时:写回走会话级旁路(同后台 subagent 的空闲活动),卡片元数据镜像到会话级流。
			started := rig.subagentEvents(StreamSubagentActivityStarted)
			So(started, ShouldNotBeEmpty)
			So(started[0].LaunchMessageID, ShouldEqual, 9001)
			So(started[0].ToolCallID, ShouldEqual, "sh-1")
			So(started[0].Stream, ShouldEqual, StreamName(approvalSessionID, 9001))
			So(rig.subagentEvents(StreamSubagentDone), ShouldHaveLength, 2)
		})
	})
}

func TestCtlSend_GivenClaudeBackgroundBash_ThenTheCardKeepsLocalBashSemanticsAndOutlivesTheTurn(t *testing.T) {
	Convey("Given 父轮跑 run_in_background 的 `agrctl send --wait`,shell 调用已经拿到结果", t, func() {
		rig := startApprovalTurn(t)
		ctx := context.Background()
		store := expectCardWrites(rig)
		rig.deps.message.EXPECT().UpdateUsage(gomock.Any(), int64(9901), gomock.Any()).Return(nil).AnyTimes()
		rig.feed(bashCall("sh-bg", reviewCommand, true), StreamToolUse)
		rig.feed(agentruntime.ToolResult{ToolCallID: "sh-bg", Content: "Command running in background with ID: bg-1"}, StreamToolResult)
		watch := rig.svc.WatchCtlSend(ctlChildSessionID)
		defer watch.Close()
		child := newCtlChildTurn(rig.svc, "claude-sonnet-4")

		Convey("When 绑定, Then 仍升级成卡片,kind 保留 local_bash,带归属与子会话", func() {
			So(watch.Bind(ctx, reviewBind()), ShouldBeTrue)
			o := readParent(rig.svc).overlays[0]
			So(o.Kind, ShouldEqual, agentruntime.SubagentKindLocalBash)
			So(o.Origin, ShouldEqual, agentruntime.SubagentOriginAgrctl)
			So(o.ChildSessionID, ShouldEqual, ctlChildSessionID)
			So(o.Status, ShouldEqual, "running")

			Convey("When CLI 的 task_started 认领这块、父轮结束, Then 后台面板列着它,停止按钮拿得到 task_id", func() {
				rig.feed(agentruntime.SubagentStarted{ToolCallID: "sh-bg", Info: agentruntime.SubagentInfo{
					Kind: agentruntime.SubagentKindLocalBash, TaskID: "bg-1", TaskDescription: "agrctl send",
				}}, StreamSubagentProgress)
				rig.endTurn()

				persisted := store.load()
				So(persisted.Status, ShouldEqual, "running")
				So(persisted.Kind, ShouldEqual, agentruntime.SubagentKindLocalBash)
				So(persisted.TaskID, ShouldEqual, "bg-1")
				So(persisted.Origin, ShouldEqual, agentruntime.SubagentOriginAgrctl)
				So(rig.svc.bgRunningActive(approvalSessionID), ShouldBeTrue)

				Convey("When 子会话出步骤并收尾, Then 内容写回原消息,状态仍归 CLI", func() {
					feedChild(child, agentruntime.ToolCall{ID: "c-1", Name: "Grep"})
					feedChild(child, agentruntime.UsageUpdate{Usage: &provider.Usage{CompletionTokens: 30}, TotalInputTokens: 1000})
					So(eventually(func() bool {
						o := store.load()
						return len(store.children()) == 1 && o.TotalTokens == 1030
					}), ShouldBeTrue)

					watch.Finish(ctx, CtlSendOutcome{Status: CtlSendCompleted, FinalText: "LGTM"})
					o := store.load()
					So(o.FinalText, ShouldEqual, "LGTM")
					So(o.ToolUses, ShouldEqual, 1)
					So(o.LastToolName, ShouldEqual, "Grep")
					So(o.Model, ShouldEqual, "claude-sonnet-4")
					So(o.Status, ShouldEqual, "running")
					So(o.Runs[0].Summary, ShouldEqual, "LGTM")
					So(o.TaskID, ShouldEqual, "bg-1")
				})
			})
		})
	})
}

func TestCtlSend_GivenParentAborted_WhenLateChildEventsArrive_ThenTheCanceledCardIsNotRevived(t *testing.T) {
	Convey("Given 前台卡已绑定,用户中止父轮、收尾把卡片标成已取消", t, func() {
		rig := startApprovalTurn(t)
		ctx := context.Background()
		store := expectCardWrites(rig)
		rig.deps.message.EXPECT().UpdateUsage(gomock.Any(), int64(9901), gomock.Any()).Return(nil).AnyTimes()
		rig.feed(bashCall("sh-1", reviewCommand, false), StreamToolUse)
		watch := rig.svc.WatchCtlSend(ctlChildSessionID)
		defer watch.Close()
		So(watch.Bind(ctx, reviewBind()), ShouldBeTrue)
		child := newCtlChildTurn(rig.svc, "m")
		rig.svc.aborted.Store(approvalSessionID, struct{}{})
		rig.endTurn()
		So(store.load().Status, ShouldEqual, "canceled")

		Convey("When 子会话迟到的步骤、进度与收尾到达, Then 步骤照常写回,卡片仍是已取消", func() {
			feedChild(child, agentruntime.ToolCall{ID: "c-9", Name: "Bash"})
			So(eventually(func() bool { return len(store.children()) == 1 && store.load().ToolUses == 1 }), ShouldBeTrue)
			watch.Finish(ctx, CtlSendOutcome{Status: CtlSendCanceled, FinalText: "stopped"})

			o := store.load()
			So(o.Status, ShouldEqual, "canceled")
			So(o.Runs[0].Status, ShouldEqual, "canceled")
		})
	})
}

func TestCtlSend_GivenParentTurnStillPersisting_WhenChildEmits_ThenTheWriteWaitsForTheHandOff(t *testing.T) {
	Convey("Given 前台卡已绑定,父轮已收口但收尾那一发落库还没完成", t, func() {
		rig := startApprovalTurn(t)
		ctx := context.Background()
		store := expectCardWrites(rig)
		rig.feed(bashCall("sh-1", reviewCommand, false), StreamToolUse)
		watch := rig.svc.WatchCtlSend(ctlChildSessionID)
		defer watch.Close()
		So(watch.Bind(ctx, reviewBind()), ShouldBeTrue)
		child := newCtlChildTurn(rig.svc, "m")
		parent := rig.svc.activeTurn(approvalSessionID)

		rig.mu.Lock() // 收尾的 Update 卡在这把锁上:整条消息的块还没落库
		close(rig.events)
		So(eventually(func() bool {
			parent.mu.Lock()
			defer parent.mu.Unlock()
			return parent.approvalsClosed
		}), ShouldBeTrue)

		Convey("When 子会话此刻吐出一步, Then 收尾落库之前不写原消息(否则会被整条覆盖掉),落库之后补上", func() {
			feedChild(child, agentruntime.ToolCall{ID: "c-1", Name: "Read"})
			time.Sleep(50 * time.Millisecond)
			writesBeforeHandOff := store.appendCalls()
			rig.mu.Unlock()
			<-rig.done

			So(writesBeforeHandOff, ShouldEqual, 0)
			So(eventually(func() bool { return len(store.children()) == 1 }), ShouldBeTrue)
			So(store.childIDs(), ShouldResemble, []string{ctlbind.NestedToolCallID("sh-1", "c-1")})
			watch.Finish(ctx, CtlSendOutcome{Status: CtlSendCompleted, FinalText: "ok"})
			So(store.load().Status, ShouldEqual, "completed")
		})
	})
}

// parentCheckpoints 数父消息的 checkpoint(带卡片的那些;子会话自己那条消息也走同一个桩)。
func parentCheckpoints(rig *approvalTurnRig) int {
	rig.mu.Lock()
	defer rig.mu.Unlock()
	n := 0
	for _, raw := range rig.checkpoints {
		if strings.Contains(raw, `"subagent_state"`) {
			n++
		}
	}
	return n
}

// cardStore 是原消息里那张卡在库里的替身:PatchSubagentState 在它上面跑读-改-写(同仓储的
// 会话锁语义),AppendSubagentChildren 记下追加的子块。卡片的初值取父轮收尾落库的那一份 ——
// 收尾还没落库时库里没有这块(PatchSubagentState 定位不到)。
type cardStore struct {
	rig *approvalTurnRig

	mu      sync.Mutex // 守 overlay;读初值时还会去拿 rig.mu
	overlay *blocks.SubagentStateBlock

	appendMu sync.Mutex // 守追加记录,与 mu 分开:落库卡在半路时也读得到
	blocks   []string   // 追加的子块:"<type>:<id>"
	ids      []string
	appends  int
}

func expectCardWrites(rig *approvalTurnRig) *cardStore {
	store := &cardStore{rig: rig}
	rig.deps.message.EXPECT().PatchSubagentState(gomock.Any(), approvalSessionID, gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, _ int64, toolCallID string, mutate func(*blocks.SubagentStateBlock) bool) (bool, error) {
			store.mu.Lock()
			defer store.mu.Unlock()
			cur := store.persistedLocked()
			if cur == nil || cur.ParentToolCallID != toolCallID {
				return false, nil
			}
			next := cloneOverlay(*cur)
			if mutate(&next) {
				store.overlay = &next
			}
			return true, nil
		}).AnyTimes()
	rig.deps.message.EXPECT().AppendSubagentChildren(gomock.Any(), approvalSessionID, gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, _ int64, _ string, childJSON string, ids []string) error {
			var stored []storedBlock
			if err := json.Unmarshal([]byte(childJSON), &stored); err != nil {
				return err
			}
			store.appendMu.Lock()
			defer store.appendMu.Unlock()
			store.appends++
			for _, b := range stored {
				var id struct {
					ID         string `json:"id"`
					ToolCallID string `json:"tool_call_id"`
				}
				_ = json.Unmarshal(b.Data, &id)
				store.blocks = append(store.blocks, b.Type+":"+id.ID+id.ToolCallID)
			}
			store.ids = append(store.ids, ids...)
			return nil
		}).AnyTimes()
	return store
}

// persistedLocked 交回库里此刻的那张卡:改写过就是改写后的,否则是收尾落库的那一份。
func (s *cardStore) persistedLocked() *blocks.SubagentStateBlock {
	if s.overlay != nil {
		return s.overlay
	}
	s.rig.mu.Lock()
	raw := s.rig.finalBlocks
	s.rig.mu.Unlock()
	if raw == "" {
		return nil
	}
	var stored []storedBlock
	if err := json.Unmarshal([]byte(raw), &stored); err != nil {
		return nil
	}
	for _, b := range stored {
		if b.Type != "subagent_state" {
			continue
		}
		var sb blocks.SubagentStateBlock
		if json.Unmarshal(b.Data, &sb) == nil {
			s.overlay = &sb
			return s.overlay
		}
	}
	return nil
}

func (s *cardStore) load() blocks.SubagentStateBlock {
	s.mu.Lock()
	defer s.mu.Unlock()
	cur := s.persistedLocked()
	if cur == nil {
		return blocks.SubagentStateBlock{}
	}
	return cloneOverlay(*cur)
}

func (s *cardStore) children() []string {
	s.appendMu.Lock()
	defer s.appendMu.Unlock()
	return append([]string(nil), s.blocks...)
}

func (s *cardStore) childIDs() []string {
	s.appendMu.Lock()
	defer s.appendMu.Unlock()
	return append([]string(nil), s.ids...)
}

func (s *cardStore) appendCalls() int {
	s.appendMu.Lock()
	defer s.appendMu.Unlock()
	return s.appends
}

func cloneOverlay(b blocks.SubagentStateBlock) blocks.SubagentStateBlock {
	b.Runs = append([]agentruntime.SubagentRun(nil), b.Runs...)
	b.NestedToolCallIDs = append([]string(nil), b.NestedToolCallIDs...)
	return b
}
