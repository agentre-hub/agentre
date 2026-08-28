package handlers

import (
	"context"
	"testing"

	cagoblocks "github.com/cago-frame/agents/agent/blocks"
	. "github.com/smartystreets/goconvey/convey"

	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
	"github.com/agentre-hub/agentre/internal/service/chat_svc/blocks"
	"github.com/agentre-hub/agentre/internal/service/chat_svc/turn"
)

func TestSubagentLifecycle(t *testing.T) {
	Convey("Started → Progress → Done 累计状态", t, func() {
		acc := turn.New()
		_ = SubagentStartedHandler{}.Apply(context.Background(),
			agentruntime.SubagentStarted{ToolCallID: "task-1"}, acc, nil, nil, nil)
		_ = SubagentProgressHandler{}.Apply(context.Background(),
			agentruntime.SubagentProgress{
				ToolCallID: "task-1",
				Info:       agentruntime.SubagentInfo{ToolUses: 3, LastToolName: "Read", TotalTokens: 1000},
			},
			acc, nil, nil, nil)
		_ = SubagentDoneHandler{}.Apply(context.Background(),
			agentruntime.SubagentDone{
				ToolCallID: "task-1",
				Info:       agentruntime.SubagentInfo{Status: "completed", DurationMs: 1234},
			},
			acc, nil, nil, nil)

		got := acc.Finalize()[0].(*blocks.SubagentStateBlock)
		So(got.Status, ShouldEqual, "completed")
		So(got.DurationMs, ShouldEqual, 1234)
	})
}

func TestSubagentStarted_PersistsKindAndDescription(t *testing.T) {
	Convey("SubagentStarted 落 kind/description + running", t, func() {
		acc := turn.New()
		err := SubagentStartedHandler{}.Apply(context.Background(),
			agentruntime.SubagentStarted{
				ToolCallID: "tu1",
				Info: agentruntime.SubagentInfo{
					Kind:            "local_bash",
					TaskDescription: "sleep 20",
				},
			}, acc, nil, nil, &turn.TurnContext{})
		So(err, ShouldBeNil)

		blks := acc.Finalize()
		So(blks, ShouldHaveLength, 1)
		sb := blks[0].(*blocks.SubagentStateBlock)
		So(sb.Kind, ShouldEqual, "local_bash")
		So(sb.Description, ShouldEqual, "sleep 20")
		So(sb.Status, ShouldEqual, "running")
	})
}

func TestSubagentStarted_PersistsTaskID(t *testing.T) {
	Convey("SubagentStarted 落 CLI task_id(供 stop_task 定位)", t, func() {
		acc := turn.New()
		err := SubagentStartedHandler{}.Apply(context.Background(),
			agentruntime.SubagentStarted{
				ToolCallID: "tu1",
				Info: agentruntime.SubagentInfo{
					TaskID:          "b0n82mqaj",
					Kind:            "local_agent",
					TaskDescription: "background subagent",
				},
			}, acc, nil, nil, &turn.TurnContext{})
		So(err, ShouldBeNil)

		sb := acc.Finalize()[0].(*blocks.SubagentStateBlock)
		So(sb.TaskID, ShouldEqual, "b0n82mqaj")
	})
}

func TestSubagentProgress_BackfillsTaskID(t *testing.T) {
	Convey("Started 缺 task_id 时,Progress 帧回填", t, func() {
		acc := turn.New()
		_ = SubagentStartedHandler{}.Apply(context.Background(),
			agentruntime.SubagentStarted{
				ToolCallID: "tu1",
				Info:       agentruntime.SubagentInfo{Kind: "local_agent"},
			}, acc, nil, nil, &turn.TurnContext{})
		_ = SubagentProgressHandler{}.Apply(context.Background(),
			agentruntime.SubagentProgress{
				ToolCallID: "tu1",
				Info:       agentruntime.SubagentInfo{TaskID: "b0n82mqaj", ToolUses: 2},
			}, acc, nil, nil, &turn.TurnContext{})

		sb := acc.Finalize()[0].(*blocks.SubagentStateBlock)
		So(sb.TaskID, ShouldEqual, "b0n82mqaj")
	})
}

func TestSubagentStarted_ForegroundBash_NoOverlay(t *testing.T) {
	Convey("前台 bash(无 run_in_background)的 local_bash 帧不建 overlay", t, func() {
		acc := turn.New()
		// 真实流里 Bash tool_use 先于 task_started 到达。
		acc.AddToolUse(&cagoblocks.ToolUseBlock{
			ID:    "tu-fg",
			Name:  "Bash",
			Input: map[string]any{"command": "git stash -u"},
		}, "")
		err := SubagentStartedHandler{}.Apply(context.Background(),
			agentruntime.SubagentStarted{
				ToolCallID: "tu-fg",
				Info:       agentruntime.SubagentInfo{Kind: "local_bash", TaskDescription: "Stash"},
			}, acc, nil, nil, &turn.TurnContext{})
		So(err, ShouldBeNil)

		blks := acc.Finalize()
		// 只剩 Bash tool_use,没有 SubagentStateBlock overlay。
		So(blks, ShouldHaveLength, 1)
		for _, b := range blks {
			_, isOverlay := b.(*blocks.SubagentStateBlock)
			So(isOverlay, ShouldBeFalse)
		}
	})
}

func TestSubagentStarted_BackgroundBash_CreatesOverlay(t *testing.T) {
	Convey("run_in_background bash 的 local_bash 帧照常建 overlay", t, func() {
		acc := turn.New()
		acc.AddToolUse(&cagoblocks.ToolUseBlock{
			ID:    "tu-bg",
			Name:  "Bash",
			Input: map[string]any{"command": "sleep 20", "run_in_background": true},
		}, "")
		err := SubagentStartedHandler{}.Apply(context.Background(),
			agentruntime.SubagentStarted{
				ToolCallID: "tu-bg",
				Info:       agentruntime.SubagentInfo{Kind: "local_bash", TaskDescription: "sleep 20"},
			}, acc, nil, nil, &turn.TurnContext{})
		So(err, ShouldBeNil)

		var sb *blocks.SubagentStateBlock
		for _, b := range acc.Finalize() {
			if x, ok := b.(*blocks.SubagentStateBlock); ok {
				sb = x
			}
		}
		So(sb, ShouldNotBeNil)
		So(sb.Kind, ShouldEqual, "local_bash")
		So(sb.ParentToolCallID, ShouldEqual, "tu-bg")
		So(sb.Status, ShouldEqual, "running")
	})
}

func TestSubagentDone_DefaultStatus(t *testing.T) {
	Convey("SubagentDone info.Status 空时默认 completed", t, func() {
		acc := turn.New()
		_ = SubagentStartedHandler{}.Apply(context.Background(),
			agentruntime.SubagentStarted{ToolCallID: "t-2"}, acc, nil, nil, nil)
		_ = SubagentDoneHandler{}.Apply(context.Background(),
			agentruntime.SubagentDone{ToolCallID: "t-2", Info: agentruntime.SubagentInfo{}},
			acc, nil, nil, nil)
		got := acc.Finalize()[0].(*blocks.SubagentStateBlock)
		So(got.Status, ShouldEqual, "completed")
	})
}

// TestSubagentModelHandler_RecordsModelAndEmits 覆盖 R2/R6 的 chat_svc 一半:
// SubagentModel 事件到达时,模型并入既有 overlay 的 Model 字段(供 replay),并 emit
// 一个只带 toolUseId + model 的流事件(不是整个 info/block 快照)。
func TestSubagentModelHandler_RecordsModelAndEmits(t *testing.T) {
	Convey("SubagentModel 命中既有 overlay → 记录 Model + emit 只带模型的事件", t, func() {
		acc := turn.New()
		_ = SubagentStartedHandler{}.Apply(context.Background(),
			agentruntime.SubagentStarted{ToolCallID: "task-1", Info: agentruntime.SubagentInfo{Kind: "local_agent"}},
			acc, nil, nil, &turn.TurnContext{})

		emit := &fakeEmit{}
		err := SubagentModelHandler{}.Apply(context.Background(),
			agentruntime.SubagentModel{ToolCallID: "task-1", Model: "claude-haiku-4-5-20251001"},
			acc, emit, nil, &turn.TurnContext{Stream: "chat:event:1:2"})
		So(err, ShouldBeNil)

		got := acc.Finalize()[0].(*blocks.SubagentStateBlock)
		So(got.Model, ShouldEqual, "claude-haiku-4-5-20251001")

		So(emit.events, ShouldHaveLength, 1)
		So(emit.events[0].stream, ShouldEqual, "chat:event:1:2")
		payload := emit.events[0].payload.(map[string]any)
		So(payload["kind"], ShouldEqual, "subagent_model")
		So(payload["toolUseId"], ShouldEqual, "task-1")
		So(payload["model"], ShouldEqual, "claude-haiku-4-5-20251001")
		// 只带模型 —— 不夹带 toolUses/totalTokens/status 等累计态字段,
		// 避免下游对整个 info/block 结构做浅合并时把已有状态覆盖成空串。
		_, hasStatus := payload["status"]
		So(hasStatus, ShouldBeFalse)
		_, hasInfo := payload["info"]
		So(hasInfo, ShouldBeFalse)
	})
}

// TestSubagentModelHandler_FirstWins 覆盖 R3(A5):同一次派遣只认第一个实际模型,
// 后续内部帧(如子代理内部用小模型做摘要)到达时不得改写已记录的模型。
func TestSubagentModelHandler_FirstWins(t *testing.T) {
	Convey("同一 ToolCallID 第二个模型事件不改写已记录模型", t, func() {
		acc := turn.New()
		_ = SubagentStartedHandler{}.Apply(context.Background(),
			agentruntime.SubagentStarted{ToolCallID: "task-1"}, acc, nil, nil, nil)

		emit := &fakeEmit{}
		_ = SubagentModelHandler{}.Apply(context.Background(),
			agentruntime.SubagentModel{ToolCallID: "task-1", Model: "claude-opus-5"},
			acc, emit, nil, nil)
		err := SubagentModelHandler{}.Apply(context.Background(),
			agentruntime.SubagentModel{ToolCallID: "task-1", Model: "claude-haiku-4-5-20251001"},
			acc, emit, nil, nil)
		So(err, ShouldBeNil)

		got := acc.Finalize()[0].(*blocks.SubagentStateBlock)
		// first-wins: 第二次帧不应改写。
		So(got.Model, ShouldEqual, "claude-opus-5")
		// 拒绝改写的第二次不应再 emit(只有第一次成功记录才 emit)。
		So(emit.events, ShouldHaveLength, 1)
	})
}

// TestSubagentModelHandler_EmptyModelNoOp 防御性兜底:任务 1 的契约是 claudecode
// 只在 message.model 非空时才产出 SubagentModel 事件,但 handler 自己不应该假设
// 上游永远守约 —— 空模型不该写入累计态,也不该 emit 一个「宣称观测到模型」但其实
// 什么都没有的事件。
func TestSubagentModelHandler_EmptyModelNoOp(t *testing.T) {
	Convey("Model 为空 → 不写入、不 emit", t, func() {
		acc := turn.New()
		_ = SubagentStartedHandler{}.Apply(context.Background(),
			agentruntime.SubagentStarted{ToolCallID: "task-1"}, acc, nil, nil, nil)

		emit := &fakeEmit{}
		err := SubagentModelHandler{}.Apply(context.Background(),
			agentruntime.SubagentModel{ToolCallID: "task-1", Model: ""},
			acc, emit, nil, nil)
		So(err, ShouldBeNil)
		So(emit.events, ShouldHaveLength, 0)

		got := acc.Finalize()[0].(*blocks.SubagentStateBlock)
		So(got.Model, ShouldEqual, "")
	})
}

// TestSubagentModelHandler_OrphanNoEmit 命中不到既有 overlay(如前台 bash 从未
// track,或 tool_use id 拼写不一致)时不应 emit 孤儿事件,呼应 Progress/Done handler
// 的既有约定(见 trackSubagentState 注释)。
func TestSubagentModelHandler_OrphanNoEmit(t *testing.T) {
	Convey("无匹配 overlay → 不 emit、不 panic", t, func() {
		acc := turn.New()
		emit := &fakeEmit{}
		err := SubagentModelHandler{}.Apply(context.Background(),
			agentruntime.SubagentModel{ToolCallID: "no-such-task", Model: "claude-opus-5"},
			acc, emit, nil, nil)
		So(err, ShouldBeNil)
		So(emit.events, ShouldHaveLength, 0)
		So(acc.Finalize(), ShouldHaveLength, 0)
	})
}

// TestSubagentModelHandler_DoesNotClobberProgress 覆盖 R4(A6):模型事件只更新
// Model 字段,已累计的 toolUses/totalTokens/status 三处都不得被清零或改写。
func TestSubagentModelHandler_DoesNotClobberProgress(t *testing.T) {
	Convey("模型事件到达前后,既有累计态原样保留", t, func() {
		acc := turn.New()
		_ = SubagentStartedHandler{}.Apply(context.Background(),
			agentruntime.SubagentStarted{ToolCallID: "task-1"}, acc, nil, nil, nil)
		_ = SubagentProgressHandler{}.Apply(context.Background(),
			agentruntime.SubagentProgress{
				ToolCallID: "task-1",
				Info:       agentruntime.SubagentInfo{ToolUses: 3, TotalTokens: 14500},
			}, acc, nil, nil, nil)

		_ = SubagentModelHandler{}.Apply(context.Background(),
			agentruntime.SubagentModel{ToolCallID: "task-1", Model: "claude-opus-5"},
			acc, nil, nil, nil)

		got := acc.Finalize()[0].(*blocks.SubagentStateBlock)
		So(got.ToolUses, ShouldEqual, 3)
		So(got.TotalTokens, ShouldEqual, 14500)
		So(got.Status, ShouldEqual, "running")
		So(got.Model, ShouldEqual, "claude-opus-5")
	})
}

// TestSubagentProgressHandler_CopiesDurationMs 覆盖 R10/A11:task_progress 帧携带的
// duration_ms 必须并入累计态,运行中的耗时才能跳动(此前 SubagentProgressHandler 只抄
// TotalTokens/LastToolName/ToolUses,DurationMs 只在 Done 时才写入)。
func TestSubagentProgressHandler_CopiesDurationMs(t *testing.T) {
	Convey("Progress 帧的 DurationMs 写入累计态", t, func() {
		acc := turn.New()
		_ = SubagentStartedHandler{}.Apply(context.Background(),
			agentruntime.SubagentStarted{ToolCallID: "task-1"}, acc, nil, nil, nil)
		_ = SubagentProgressHandler{}.Apply(context.Background(),
			agentruntime.SubagentProgress{
				ToolCallID: "task-1",
				Info:       agentruntime.SubagentInfo{TotalTokens: 12056, ToolUses: 1, DurationMs: 1959},
			}, acc, nil, nil, nil)

		got := acc.Finalize()[0].(*blocks.SubagentStateBlock)
		So(got.DurationMs, ShouldEqual, 1959)
	})
}

// TestSubagentProgressHandler_ZeroDurationMsDoesNotClobber 边界:同一子代理稍后一帧
// DurationMs 为 0(帧未带该字段/异常帧)不应把已记录的耗时清零 —— 与 R4 的
// "不得清空既有累计态" 同一思路。
func TestSubagentProgressHandler_ZeroDurationMsDoesNotClobber(t *testing.T) {
	Convey("后续 Progress 帧 DurationMs=0 不清空已记录耗时", t, func() {
		acc := turn.New()
		_ = SubagentStartedHandler{}.Apply(context.Background(),
			agentruntime.SubagentStarted{ToolCallID: "task-1"}, acc, nil, nil, nil)
		_ = SubagentProgressHandler{}.Apply(context.Background(),
			agentruntime.SubagentProgress{
				ToolCallID: "task-1",
				Info:       agentruntime.SubagentInfo{DurationMs: 1959},
			}, acc, nil, nil, nil)
		_ = SubagentProgressHandler{}.Apply(context.Background(),
			agentruntime.SubagentProgress{
				ToolCallID: "task-1",
				Info:       agentruntime.SubagentInfo{DurationMs: 0, ToolUses: 2},
			}, acc, nil, nil, nil)

		got := acc.Finalize()[0].(*blocks.SubagentStateBlock)
		So(got.DurationMs, ShouldEqual, 1959)
		So(got.ToolUses, ShouldEqual, 2)
	})
}

// TestSubagentDoneHandler_ZeroDurationMsDoesNotClobber 覆盖 wrap-up 复审 Finding 2:
// SubagentProgressHandler 的 DurationMs 写入已经有零值守卫(见上面
// TestSubagentProgressHandler_ZeroDurationMsDoesNotClobber),但 SubagentDoneHandler 仍是
// 无条件赋值 `b.DurationMs = r.Info.DurationMs`。一帧不带 usage 对象的 task_notification
// 解码成零值 taskUsage{}(DurationMs==0)到达 Done 时,会把 Progress 阶段已累计的耗时清零 ——
// 与 R4「部分更新不得清空既有累计态」同一条不变量,两个 handler 的守卫必须对齐。
func TestSubagentDoneHandler_ZeroDurationMsDoesNotClobber(t *testing.T) {
	Convey("Done 帧 DurationMs=0 不清空 Progress 阶段已累计的耗时", t, func() {
		acc := turn.New()
		_ = SubagentStartedHandler{}.Apply(context.Background(),
			agentruntime.SubagentStarted{ToolCallID: "task-1"}, acc, nil, nil, nil)
		_ = SubagentProgressHandler{}.Apply(context.Background(),
			agentruntime.SubagentProgress{
				ToolCallID: "task-1",
				Info:       agentruntime.SubagentInfo{DurationMs: 1959},
			}, acc, nil, nil, nil)
		_ = SubagentDoneHandler{}.Apply(context.Background(),
			agentruntime.SubagentDone{
				ToolCallID: "task-1",
				Info:       agentruntime.SubagentInfo{Status: "completed", DurationMs: 0},
			}, acc, nil, nil, nil)

		got := acc.Finalize()[0].(*blocks.SubagentStateBlock)
		So(got.Status, ShouldEqual, "completed")
		So(got.DurationMs, ShouldEqual, 1959)
	})
}

// TestSubagentProgressHandler_ZeroTotalTokensAndToolUsesDoesNotClobber 覆盖 wrap-up
// 复审第二轮 Finding 1:同一个 mutate 块里,DurationMs 已经有「0 值不覆盖已记录值」的
// 守卫(见 TestSubagentProgressHandler_ZeroDurationMsDoesNotClobber),但 TotalTokens
// 与 ToolUses 仍是无条件赋值。三者来自同一个 CLI usage 对象(taskUsage,值类型无
// 存在性区分):task_progress 帧偶尔缺 usage,解码成零值后若无条件赋值,会把已经
// 攒起来的 token 数与工具数抹回 0(仓库自己在 chat_repo/message.go:336 记着这一点)。
func TestSubagentProgressHandler_ZeroTotalTokensAndToolUsesDoesNotClobber(t *testing.T) {
	Convey("后续 Progress 帧 TotalTokens/ToolUses=0 不清空已记录的累计态", t, func() {
		acc := turn.New()
		_ = SubagentStartedHandler{}.Apply(context.Background(),
			agentruntime.SubagentStarted{ToolCallID: "task-1"}, acc, nil, nil, nil)
		_ = SubagentProgressHandler{}.Apply(context.Background(),
			agentruntime.SubagentProgress{
				ToolCallID: "task-1",
				Info:       agentruntime.SubagentInfo{TotalTokens: 14096, ToolUses: 1, DurationMs: 4254},
			}, acc, nil, nil, nil)
		// 下一帧不带 usage(如 task_notification),解码成零值 —— 不该清空。
		_ = SubagentProgressHandler{}.Apply(context.Background(),
			agentruntime.SubagentProgress{
				ToolCallID: "task-1",
				Info:       agentruntime.SubagentInfo{TotalTokens: 0, ToolUses: 0, DurationMs: 0},
			}, acc, nil, nil, nil)

		got := acc.Finalize()[0].(*blocks.SubagentStateBlock)
		So(got.TotalTokens, ShouldEqual, 14096)
		So(got.ToolUses, ShouldEqual, 1)
		So(got.DurationMs, ShouldEqual, 4254)
	})
}

// TestSubagentDoneHandler_ZeroTotalTokensAndToolUsesDoesNotClobber 是上一条测试在
// SubagentDoneHandler 上的镜像:一帧不带 usage 对象的 task_notification 到达 Done 时,
// 同样不该把 Progress 阶段已累计的 TotalTokens/ToolUses 清零。
func TestSubagentDoneHandler_ZeroTotalTokensAndToolUsesDoesNotClobber(t *testing.T) {
	Convey("Done 帧 TotalTokens/ToolUses=0 不清空 Progress 阶段已累计的值", t, func() {
		acc := turn.New()
		_ = SubagentStartedHandler{}.Apply(context.Background(),
			agentruntime.SubagentStarted{ToolCallID: "task-1"}, acc, nil, nil, nil)
		_ = SubagentProgressHandler{}.Apply(context.Background(),
			agentruntime.SubagentProgress{
				ToolCallID: "task-1",
				Info:       agentruntime.SubagentInfo{TotalTokens: 14096, ToolUses: 1, DurationMs: 4254},
			}, acc, nil, nil, nil)
		_ = SubagentDoneHandler{}.Apply(context.Background(),
			agentruntime.SubagentDone{
				ToolCallID: "task-1",
				Info:       agentruntime.SubagentInfo{Status: "completed", TotalTokens: 0, ToolUses: 0, DurationMs: 0},
			}, acc, nil, nil, nil)

		got := acc.Finalize()[0].(*blocks.SubagentStateBlock)
		So(got.Status, ShouldEqual, "completed")
		So(got.TotalTokens, ShouldEqual, 14096)
		So(got.ToolUses, ShouldEqual, 1)
		So(got.DurationMs, ShouldEqual, 4254)
	})
}

// MarkRunningSubagentsCancelled 是 turn abort 收尾的补救：用户 Stop 后 runtime 不会
// 再来 SubagentDone，外层累计态和 normalized runs 的 waiting/running 都必须落为
// canceled；已经 terminal 的证据保持不动。
func TestSubagentLifecycle_NormalizedSnapshotsReplaceAtomically(t *testing.T) {
	Convey("Given a normalized two-run snapshot, When progress and done snapshots arrive, Then runs replace as whole snapshots and omitted legacy runs do not clear them", t, func() {
		acc := turn.New()
		startedRuns := []agentruntime.SubagentRun{
			{ID: "run-0", Index: 0, Task: "inspect", Status: "running"},
			{ID: "run-1", Index: 1, Task: "test", Status: "waiting"},
		}
		_ = SubagentStartedHandler{}.Apply(context.Background(), agentruntime.SubagentStarted{
			ToolCallID: "outer",
			Info:       agentruntime.SubagentInfo{Mode: "parallel", Runs: startedRuns, Status: "running"},
		}, acc, nil, nil, nil)

		state := acc.Finalize()[0].(*blocks.SubagentStateBlock)
		So(state.Mode, ShouldEqual, "parallel")
		So(state.Runs, ShouldResemble, startedRuns)
		startedRuns[0].Status = "failed"
		So(state.Runs[0].Status, ShouldEqual, "running")

		progressRuns := []agentruntime.SubagentRun{
			{ID: "run-0", Index: 0, Task: "inspect", Status: "completed", Summary: "done"},
			{ID: "run-1", Index: 1, Task: "test", Status: "running", LastToolName: "bash"},
		}
		_ = SubagentProgressHandler{}.Apply(context.Background(), agentruntime.SubagentProgress{
			ToolCallID: "outer",
			Info:       agentruntime.SubagentInfo{Mode: "parallel", Runs: progressRuns, Status: "running"},
		}, acc, nil, nil, nil)
		So(state.Runs, ShouldResemble, progressRuns)
		progressRuns[1].Status = "failed"
		So(state.Runs[1].Status, ShouldEqual, "running")
		progressRuns[1].Status = "running"

		_ = SubagentProgressHandler{}.Apply(context.Background(), agentruntime.SubagentProgress{
			ToolCallID: "outer",
			Info:       agentruntime.SubagentInfo{ToolUses: 9},
		}, acc, nil, nil, nil)
		So(state.Mode, ShouldEqual, "parallel")
		So(state.Runs, ShouldResemble, progressRuns)

		emptyRuns := make([]agentruntime.SubagentRun, 0)
		_ = SubagentProgressHandler{}.Apply(context.Background(), agentruntime.SubagentProgress{
			ToolCallID: "outer",
			Info:       agentruntime.SubagentInfo{Runs: emptyRuns},
		}, acc, nil, nil, nil)
		So(state.Runs, ShouldNotBeNil)
		So(state.Runs, ShouldHaveLength, 0)
		_ = SubagentProgressHandler{}.Apply(context.Background(), agentruntime.SubagentProgress{
			ToolCallID: "outer",
			Info:       agentruntime.SubagentInfo{Runs: progressRuns},
		}, acc, nil, nil, nil)

		doneRuns := []agentruntime.SubagentRun{
			{ID: "run-0", Index: 0, Task: "inspect", Status: "completed", Summary: "done"},
			{ID: "run-1", Index: 1, Task: "test", Status: "failed", ErrorMessage: "boom"},
		}
		_ = SubagentDoneHandler{}.Apply(context.Background(), agentruntime.SubagentDone{
			ToolCallID: "outer",
			Info:       agentruntime.SubagentInfo{Mode: "parallel", Runs: doneRuns, Status: "failed"},
		}, acc, nil, nil, nil)
		So(state.Status, ShouldEqual, "failed")
		So(state.Runs, ShouldResemble, doneRuns)
	})
}

func TestMarkRunningSubagentsCancelled_NormalizedRuns(t *testing.T) {
	Convey("abort cancels only waiting/running normalized runs and preserves terminal evidence", t, func() {
		state := &blocks.SubagentStateBlock{
			ParentToolCallID: "outer",
			Status:           "running",
			Mode:             "parallel",
			Runs: []agentruntime.SubagentRun{
				{ID: "waiting", Status: "waiting"},
				{ID: "running", Status: "running"},
				{ID: "completed", Status: "completed"},
				{ID: "failed", Status: "failed"},
				{ID: "canceled", Status: "canceled"},
				{ID: "skipped", Status: "skipped"},
				{ID: "unknown", Status: "unknown"},
			},
		}
		MarkRunningSubagentsCancelled([]cagoblocks.ContentBlock{state})

		So(state.Status, ShouldEqual, "canceled")
		So(state.Runs[0].Status, ShouldEqual, "canceled")
		So(state.Runs[1].Status, ShouldEqual, "canceled")
		So(state.Runs[2].Status, ShouldEqual, "completed")
		So(state.Runs[3].Status, ShouldEqual, "failed")
		So(state.Runs[4].Status, ShouldEqual, "canceled")
		So(state.Runs[5].Status, ShouldEqual, "skipped")
		So(state.Runs[6].Status, ShouldEqual, "unknown")
	})
}

func TestMarkRunningSubagentsCancelled(t *testing.T) {
	Convey("abort 时将 running 改成 canceled,其它终态不动", t, func() {
		acc := turn.New()
		_ = SubagentStartedHandler{}.Apply(context.Background(),
			agentruntime.SubagentStarted{ToolCallID: "running-1"}, acc, nil, nil, nil)
		_ = SubagentStartedHandler{}.Apply(context.Background(),
			agentruntime.SubagentStarted{ToolCallID: "done-1"}, acc, nil, nil, nil)
		_ = SubagentDoneHandler{}.Apply(context.Background(),
			agentruntime.SubagentDone{
				ToolCallID: "done-1",
				Info:       agentruntime.SubagentInfo{Status: "completed"},
			},
			acc, nil, nil, nil)

		final := acc.Finalize()
		MarkRunningSubagentsCancelled(final)

		var running, done *blocks.SubagentStateBlock
		for _, b := range final {
			sb, ok := b.(*blocks.SubagentStateBlock)
			if !ok {
				continue
			}
			switch sb.ParentToolCallID {
			case "running-1":
				running = sb
			case "done-1":
				done = sb
			}
		}
		So(running, ShouldNotBeNil)
		So(done, ShouldNotBeNil)
		So(running.Status, ShouldEqual, "canceled")
		So(done.Status, ShouldEqual, "completed")
	})

	Convey("空切片 / 无 SubagentStateBlock 不 panic", t, func() {
		MarkRunningSubagentsCancelled(nil)
		MarkRunningSubagentsCancelled([]cagoblocks.ContentBlock{})
		MarkRunningSubagentsCancelled([]cagoblocks.ContentBlock{
			&cagoblocks.TextBlock{Text: "hi"},
		})
	})
}

// fakeSubagentFlipper 记录跨消息定向翻转的调用。
type fakeSubagentFlipper struct {
	calls []flipCall
	err   error
}

type flipCall struct {
	toolCallID string
	status     string
}

func (f *fakeSubagentFlipper) FlipSubagentStatus(_ context.Context, toolCallID, status string) error {
	f.calls = append(f.calls, flipCall{toolCallID: toolCallID, status: status})
	return f.err
}

// TestSubagentDone_CrossTurn_FlipsEarlierMessage 是 sess-2825 的回归:一次
// run_in_background 派遣的 subagent,其完成通知(CLI system.task_notification)可能在
// **别人的轮**进行中到达 —— 此时 pkg/claudecode 不另起自主续轮,而是把这一帧并进当前
// 活跃轮。派遣卡的 subagent_state 块住在更早那条消息里,过不了本轮 accumulator,
// turn.Mutate 必然落空。旧实现在落空时静默 return,该块就永远停在 running:
// FlipSubagentStatus 那条跨轮通路只挂在自主续轮上,这一帧根本走不到。
func TestSubagentDone_CrossTurn_FlipsEarlierMessage(t *testing.T) {
	Convey("Given 派遣卡在更早的消息里(本轮 accumulator 既无 overlay 也无该 tool_use)", t, func() {
		acc := turn.New()
		// 本轮自己的工具调用,与那个后台 subagent 无关。
		acc.AddToolUse(&cagoblocks.ToolUseBlock{
			ID:   "tu-this-turn",
			Name: "Read",
		}, "")
		flipper := &fakeSubagentFlipper{}
		emit := &fakeEmit{}

		Convey("When 它的 SubagentDone 在本轮到达", func() {
			err := SubagentDoneHandler{}.Apply(context.Background(),
				agentruntime.SubagentDone{
					ToolCallID: "toolu-earlier-msg",
					Info:       agentruntime.SubagentInfo{Kind: "local_agent", Status: "completed"},
				},
				acc, emit, nil, &turn.TurnContext{
					Stream:          "chat:event:1:2",
					SubagentFlipper: flipper,
				})

			Convey("Then 该块经跨消息定向翻转落成终态,而不是被静默丢弃", func() {
				So(err, ShouldBeNil)
				So(flipper.calls, ShouldHaveLength, 1)
				So(flipper.calls[0].toolCallID, ShouldEqual, "toolu-earlier-msg")
				So(flipper.calls[0].status, ShouldEqual, "completed")
				// 本轮 accumulator 不该被塞进一个孤儿 overlay(块不属于这条消息)。
				So(acc.Finalize(), ShouldHaveLength, 1)
			})
		})
	})
}

// TestSubagentDone_CrossTurn_DefaultsStatusWhenEmpty 边界:CLI 偶尔不带 status 的
// 完成帧,跨消息路径要与命中路径同样默认 completed —— 否则 FlipSubagentStatus 拿到
// 空 status 会直接 return,卡片仍旧停在 running。
func TestSubagentDone_CrossTurn_DefaultsStatusWhenEmpty(t *testing.T) {
	Convey("Info.Status 为空的跨轮完成帧默认翻成 completed", t, func() {
		flipper := &fakeSubagentFlipper{}
		err := SubagentDoneHandler{}.Apply(context.Background(),
			agentruntime.SubagentDone{ToolCallID: "toolu-earlier-msg"},
			turn.New(), nil, nil, &turn.TurnContext{SubagentFlipper: flipper})
		So(err, ShouldBeNil)
		So(flipper.calls, ShouldHaveLength, 1)
		So(flipper.calls[0].status, ShouldEqual, "completed")
	})
}

// TestSubagentDone_ForegroundBash_NoCrossTurnFlip 是上面那条的对照面:前台 bash 的
// tool_use 就在本轮,只是按 trackSubagentState 的约定从未建 overlay。它同样命中不到
// Mutate,但绝不能因此去跨消息扫一遍 —— 一条消息里几十次普通 Bash,每次都白跑一趟
// blocks_json LIKE 全表扫描。
func TestSubagentDone_ForegroundBash_NoCrossTurnFlip(t *testing.T) {
	Convey("本轮内的前台 bash 完成帧不触发跨消息翻转", t, func() {
		acc := turn.New()
		acc.AddToolUse(&cagoblocks.ToolUseBlock{
			ID:    "tu-fg",
			Name:  "Bash",
			Input: map[string]any{"command": "git status"},
		}, "")
		_ = SubagentStartedHandler{}.Apply(context.Background(),
			agentruntime.SubagentStarted{
				ToolCallID: "tu-fg",
				Info:       agentruntime.SubagentInfo{Kind: "local_bash"},
			}, acc, nil, nil, &turn.TurnContext{})

		flipper := &fakeSubagentFlipper{}
		err := SubagentDoneHandler{}.Apply(context.Background(),
			agentruntime.SubagentDone{
				ToolCallID: "tu-fg",
				Info:       agentruntime.SubagentInfo{Kind: "local_bash", Status: "completed"},
			}, acc, nil, nil, &turn.TurnContext{SubagentFlipper: flipper})

		So(err, ShouldBeNil)
		So(flipper.calls, ShouldHaveLength, 0)
	})
}

// TestSubagentDone_CrossTurn_NilFlipperNoPanic:TurnContext 可能不带 flipper
// (老调用点 / 测试构造的空上下文),此时退回旧的静默行为,不得 panic。
func TestSubagentDone_CrossTurn_NilFlipperNoPanic(t *testing.T) {
	Convey("TurnContext 无 flipper 时跨轮完成帧静默忽略", t, func() {
		So(func() {
			_ = SubagentDoneHandler{}.Apply(context.Background(),
				agentruntime.SubagentDone{ToolCallID: "toolu-earlier-msg"},
				turn.New(), nil, nil, &turn.TurnContext{})
			_ = SubagentDoneHandler{}.Apply(context.Background(),
				agentruntime.SubagentDone{ToolCallID: "toolu-earlier-msg"},
				turn.New(), nil, nil, nil)
		}, ShouldNotPanic)
	})
}

// MarkRunningForegroundSubagentsCancelled 是**正常**收尾用的补救。后台任务(Agent
// 默认后台 / run_in_background 的 Bash)有权活过发起它的那一轮,不能跟着前台 subagent
// 一起判死 —— 否则卡片显示「已停止」而任务还在跑(sess-3275)。
func TestMarkRunningForegroundSubagentsCancelled(t *testing.T) {
	Convey("Given a clean turn end with both foreground and background subagents still running", t, func() {
		acc := turn.New()
		acc.AddToolUse(&cagoblocks.ToolUseBlock{
			ID: "agent-bg", Name: "Agent",
			Input: map[string]any{"description": "Wrap-up code review axis"},
		}, "")
		acc.AddToolUse(&cagoblocks.ToolUseBlock{
			ID: "agent-fg", Name: "Agent",
			Input: map[string]any{"run_in_background": false},
		}, "")
		acc.AddToolUse(&cagoblocks.ToolUseBlock{
			ID: "bash-bg", Name: "Bash",
			Input: map[string]any{"command": "sleep 600", "run_in_background": true},
		}, "")

		bgAgent := &blocks.SubagentStateBlock{
			ParentToolCallID: "agent-bg", Kind: "local_agent", Status: "running",
			Runs: []agentruntime.SubagentRun{{ID: "run-0", Status: "running"}},
		}
		fgAgent := &blocks.SubagentStateBlock{
			ParentToolCallID: "agent-fg", Kind: "local_agent", Status: "running",
			Runs: []agentruntime.SubagentRun{{ID: "run-0", Status: "waiting"}},
		}
		bgBash := &blocks.SubagentStateBlock{
			ParentToolCallID: "bash-bg", Kind: "local_bash", Status: "running",
		}
		// kind 未知的旧帧:判不出后台,按前台处理(宁可翻 canceled 也不让卡片永远转)。
		unknown := &blocks.SubagentStateBlock{
			ParentToolCallID: "legacy", Kind: "", Status: "running",
		}
		final := append(acc.Finalize(), bgAgent, fgAgent, bgBash, unknown)

		MarkRunningForegroundSubagentsCancelled(acc, final)

		Convey("Then background subagents keep running and only foreground ones are canceled", func() {
			So(bgAgent.Status, ShouldEqual, "running")
			So(bgAgent.Runs[0].Status, ShouldEqual, "running")
			So(bgBash.Status, ShouldEqual, "running")
			So(fgAgent.Status, ShouldEqual, "canceled")
			So(fgAgent.Runs[0].Status, ShouldEqual, "canceled")
			So(unknown.Status, ShouldEqual, "canceled")
		})
	})

	Convey("Given a background Agent whose tool_use block is not in this turn", t, func() {
		acc := turn.New()
		orphan := &blocks.SubagentStateBlock{
			ParentToolCallID: "agent-elsewhere", Kind: "local_agent", Status: "running",
		}

		MarkRunningForegroundSubagentsCancelled(acc, []cagoblocks.ContentBlock{orphan})

		Convey("Then it is still treated as background (Agent defaults to background)", func() {
			So(orphan.Status, ShouldEqual, "running")
		})
	})

	Convey("Given terminal subagent evidence", t, func() {
		acc := turn.New()
		acc.AddToolUse(&cagoblocks.ToolUseBlock{
			ID: "agent-fg", Name: "Agent", Input: map[string]any{"run_in_background": false},
		}, "")
		done := &blocks.SubagentStateBlock{
			ParentToolCallID: "agent-fg", Kind: "local_agent", Status: "completed",
			Runs: []agentruntime.SubagentRun{{ID: "run-0", Status: "failed"}},
		}

		MarkRunningForegroundSubagentsCancelled(acc, []cagoblocks.ContentBlock{done})

		Convey("Then it is preserved untouched", func() {
			So(done.Status, ShouldEqual, "completed")
			So(done.Runs[0].Status, ShouldEqual, "failed")
		})
	})
}
