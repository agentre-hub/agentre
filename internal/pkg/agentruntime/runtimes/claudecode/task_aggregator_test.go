package claudecode

import (
	"encoding/json"
	"testing"

	. "github.com/smartystreets/goconvey/convey"

	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/canonical"
	"github.com/agentre-hub/agentre/pkg/claudecode"
)

func newPreToolUse(toolCallID, name, input string) claudecode.Event {
	return claudecode.Event{
		Kind: claudecode.EventPreToolUse,
		Tool: &claudecode.ToolEvent{
			ID:    toolCallID,
			Name:  name,
			Input: json.RawMessage(input),
		},
	}
}

// newPostToolUse 镜像 pkg/claudecode/session.go::parseUserContent —— CLI 在
// user 帧解析 tool_result 时只填 ID + Response + ResultMeta,Name 字段恒为空。
// 早期 helper 会塞一个 name 进来,导致聚合器单测看起来过了,但生产里所有
// PostToolUse 的 Name=="",task_aggregator 按 Name 过滤就把 TaskCreate 的真实
// task.id 全吃掉了。helper 不再接受 name 参数,强制和 SDK 对齐。
func newPostToolUse(toolCallID, resultMeta string) claudecode.Event {
	return claudecode.Event{
		Kind: claudecode.EventPostToolUse,
		Tool: &claudecode.ToolEvent{
			ID:         toolCallID,
			ResultMeta: json.RawMessage(resultMeta),
		},
	}
}

func TestTaskAggregator_TaskCreate(t *testing.T) {
	Convey("TaskCreate Pre 不 emit,Post 携 task.id 后 emit Steps[1]", t, func() {
		ta := newTaskAggregator()
		So(ta.observePreToolUse(newPreToolUse("tu-1", "TaskCreate", `{"subject":"hello"}`)), ShouldBeNil)
		snap := ta.observePostToolUse(newPostToolUse("tu-1", `{"task":{"id":"task-1"}}`))
		So(snap, ShouldNotBeNil)
		So(snap.Steps, ShouldHaveLength, 1)
		So(snap.Steps[0].ID, ShouldEqual, "task-1")
		So(snap.Steps[0].Step, ShouldEqual, "hello")
		So(snap.Steps[0].Status, ShouldEqual, canonical.StepPending)
	})
}

func TestTaskAggregator_DescriptionFallback(t *testing.T) {
	Convey("subject 缺省时回退 description", t, func() {
		ta := newTaskAggregator()
		ta.observePreToolUse(newPreToolUse("tu-1", "TaskCreate", `{"description":"fallback"}`))
		snap := ta.observePostToolUse(newPostToolUse("tu-1", `{"task":{"id":"t1"}}`))
		So(snap.Steps[0].Step, ShouldEqual, "fallback")
	})
}

func TestTaskAggregator_MultipleCreates(t *testing.T) {
	Convey("3 个 TaskCreate 顺序入队 → 最后一条 emit Steps[3]", t, func() {
		ta := newTaskAggregator()
		ta.observePreToolUse(newPreToolUse("tu-1", "TaskCreate", `{"subject":"a"}`))
		s1 := ta.observePostToolUse(newPostToolUse("tu-1", `{"task":{"id":"id-a"}}`))
		So(s1.Steps, ShouldHaveLength, 1)
		ta.observePreToolUse(newPreToolUse("tu-2", "TaskCreate", `{"subject":"b"}`))
		s2 := ta.observePostToolUse(newPostToolUse("tu-2", `{"task":{"id":"id-b"}}`))
		So(s2.Steps, ShouldHaveLength, 2)
		ta.observePreToolUse(newPreToolUse("tu-3", "TaskCreate", `{"subject":"c"}`))
		s3 := ta.observePostToolUse(newPostToolUse("tu-3", `{"task":{"id":"id-c"}}`))
		So(s3.Steps, ShouldHaveLength, 3)
		So(s3.Steps[0].ID, ShouldEqual, "id-a")
		So(s3.Steps[2].ID, ShouldEqual, "id-c")
	})
}

func TestTaskAggregator_TaskUpdate_StatusChange(t *testing.T) {
	Convey("TaskUpdate(in_progress) → 对应条目状态变更,emit 完整 Steps", t, func() {
		ta := newTaskAggregator()
		ta.observePreToolUse(newPreToolUse("tu-1", "TaskCreate", `{"subject":"a"}`))
		ta.observePostToolUse(newPostToolUse("tu-1", `{"task":{"id":"id-a"}}`))
		ta.observePreToolUse(newPreToolUse("tu-2", "TaskCreate", `{"subject":"b"}`))
		ta.observePostToolUse(newPostToolUse("tu-2", `{"task":{"id":"id-b"}}`))

		snap := ta.observePreToolUse(newPreToolUse(
			"tu-3", "TaskUpdate", `{"taskId":"id-b","status":"in_progress"}`,
		))
		So(snap, ShouldNotBeNil)
		So(snap.Steps, ShouldHaveLength, 2)
		So(snap.Steps[1].ID, ShouldEqual, "id-b")
		So(snap.Steps[1].Status, ShouldEqual, canonical.StepInProgress)
		So(snap.Steps[0].Status, ShouldEqual, canonical.StepPending)
	})
}

func TestTaskAggregator_TaskUpdate_Deleted(t *testing.T) {
	Convey("TaskUpdate(deleted) → 从列表移除", t, func() {
		ta := newTaskAggregator()
		ta.observePreToolUse(newPreToolUse("tu-1", "TaskCreate", `{"subject":"a"}`))
		ta.observePostToolUse(newPostToolUse("tu-1", `{"task":{"id":"id-a"}}`))
		ta.observePreToolUse(newPreToolUse("tu-2", "TaskCreate", `{"subject":"b"}`))
		ta.observePostToolUse(newPostToolUse("tu-2", `{"task":{"id":"id-b"}}`))

		snap := ta.observePreToolUse(newPreToolUse(
			"tu-3", "TaskUpdate", `{"taskId":"id-a","status":"deleted"}`,
		))
		So(snap, ShouldNotBeNil)
		So(snap.Steps, ShouldHaveLength, 1)
		So(snap.Steps[0].ID, ShouldEqual, "id-b")
	})
}

func TestTaskAggregator_TaskUpdate_UnknownID(t *testing.T) {
	Convey("TaskUpdate 引用未知 taskId → 不 emit,不污染状态", t, func() {
		ta := newTaskAggregator()
		ta.observePreToolUse(newPreToolUse("tu-1", "TaskCreate", `{"subject":"a"}`))
		ta.observePostToolUse(newPostToolUse("tu-1", `{"task":{"id":"id-a"}}`))

		snap := ta.observePreToolUse(newPreToolUse(
			"tu-2", "TaskUpdate", `{"taskId":"id-unknown","status":"completed"}`,
		))
		So(snap, ShouldBeNil)
	})
}

func TestTaskAggregator_TaskCreate_MissingResultMeta(t *testing.T) {
	Convey("TaskCreate ToolResult 缺 task.id → 不入列表,不 emit", t, func() {
		ta := newTaskAggregator()
		ta.observePreToolUse(newPreToolUse("tu-1", "TaskCreate", `{"subject":"a"}`))
		So(ta.observePostToolUse(newPostToolUse("tu-1", `{}`)), ShouldBeNil)
		// 后续 TaskUpdate 也找不到该任务
		So(ta.observePreToolUse(newPreToolUse(
			"tu-2", "TaskUpdate", `{"taskId":"id-a","status":"completed"}`,
		)), ShouldBeNil)
	})
}

func TestTaskAggregator_IgnoresOtherTools(t *testing.T) {
	Convey("TodoWrite / 普通工具不参与聚合", t, func() {
		ta := newTaskAggregator()
		// TodoWrite 的 PreToolUse 走 translator 的 recognizeTodoWrite,聚合器
		// 不处理。
		So(ta.observePreToolUse(newPreToolUse(
			"tu-x", "TodoWrite", `{"todos":[]}`,
		)), ShouldBeNil)
		// PostToolUse 的 Tool.Name 在 SDK 里恒为空,聚合器靠"toolCallID 是否在
		// pending"识别 TaskCreate 的回结果。这里的 tu-y 没经过 TaskCreate Pre
		// 阶段(Bash 之类的普通工具走 translator),pending 里没有 → 丢弃,
		// 即便 meta 里硬塞了 task.id 也不入列表。
		So(ta.observePostToolUse(newPostToolUse(
			"tu-y", `{"task":{"id":"shouldnotmatter"}}`,
		)), ShouldBeNil)
	})
}

func TestTaskAggregator_StatusMapping(t *testing.T) {
	Convey("CLI status 文本 → canonical 枚举", t, func() {
		So(mapClaudeTaskStatus("pending"), ShouldEqual, canonical.StepPending)
		So(mapClaudeTaskStatus("in_progress"), ShouldEqual, canonical.StepInProgress)
		So(mapClaudeTaskStatus("completed"), ShouldEqual, canonical.StepCompleted)
		So(mapClaudeTaskStatus("garbage"), ShouldEqual, canonical.PlanStepStatus(""))
		// deleted 由 observePreToolUse 走专用分支,mapClaudeTaskStatus 返空表示
		// "不映射到 in_list status",调用方此时不能 fall through。
		So(mapClaudeTaskStatus("deleted"), ShouldEqual, canonical.PlanStepStatus(""))
	})
}

func TestTaskAggregator_CreateReuseID(t *testing.T) {
	Convey("同 id 重新 Create → 重置 description + status=pending", t, func() {
		ta := newTaskAggregator()
		ta.observePreToolUse(newPreToolUse("tu-1", "TaskCreate", `{"subject":"old"}`))
		ta.observePostToolUse(newPostToolUse("tu-1", `{"task":{"id":"X"}}`))
		ta.observePreToolUse(newPreToolUse("tu-2", "TaskUpdate", `{"taskId":"X","status":"completed"}`))

		ta.observePreToolUse(newPreToolUse("tu-3", "TaskCreate", `{"subject":"new"}`))
		snap := ta.observePostToolUse(newPostToolUse("tu-3", `{"task":{"id":"X"}}`))
		So(snap.Steps, ShouldHaveLength, 1)
		So(snap.Steps[0].Step, ShouldEqual, "new")
		So(snap.Steps[0].Status, ShouldEqual, canonical.StepPending)
	})
}
