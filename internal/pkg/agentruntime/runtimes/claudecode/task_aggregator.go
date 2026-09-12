package claudecode

import (
	"encoding/json"
	"sync"

	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/canonical"
	"github.com/agentre-hub/agentre/pkg/claudecode"
)

// taskAggregator 收编 claudecode CLI 的 TaskCreate / TaskUpdate 工具调用为
// canonical.PlanUpdate 快照流。
//
// 背景:CLI 提供两套 plan 工具:
//   - TodoWrite:bulk snapshot,一次性传完整 todos[],translator 经
//     recognizeCanonical 委托 canonical.FromToolUse → canonical.PlanUpdate;
//   - TaskCreate(单条新增)+ TaskUpdate(单条状态变更):增量,翻译层不能
//     stateless 处理。本聚合器在每次工具事件后维护一份完整任务列表,合成
//     EventPlanUpdated 推到事件流,下游(前端 task-progress-bar 等)只读最新
//     canonical 即可。
//
// 状态作用域:claudeActive(一个 chat session 一个聚合器,跨 turn 复用)。
// 当 LRU 把 claudeActive 踢出 / app restart → 状态丢失;TaskUpdate 引用未知
// taskID 时 silently ignored,frontend 仍能展示上一次持久化的 PlanBlock(只是
// 状态停在那个时间点)。如需跨 cache miss 续接,后续可由 chat_svc.runTurn 在
// RunRequest 上加 SeedTaskSteps 注入。
type taskAggregator struct {
	mu      sync.Mutex
	list    []canonical.PlanStep // 顺序与 TaskCreate 的到达顺序一致
	pending map[string]string    // toolCallID → description(TaskCreate 等 ToolResult 绑 id)
}

func newTaskAggregator() *taskAggregator {
	return &taskAggregator{pending: make(map[string]string)}
}

// observePreToolUse claudecode.EventPreToolUse 帧入聚合器:
//   - TaskCreate:把 description 暂存 pending(此时还没拿到 server-assigned id);
//   - TaskUpdate:直接改 list 中对应 id 的状态(或删除)→ 返一份新快照;
//   - 其它工具(含 TodoWrite)→ 不处理,返 nil。
func (ta *taskAggregator) observePreToolUse(ev claudecode.Event) *canonical.PlanUpdate {
	if ta == nil || ev.Tool == nil {
		return nil
	}
	switch ev.Tool.Name {
	case "TaskCreate":
		desc := taskDescriptionFromInput(ev.Tool.Input)
		ta.mu.Lock()
		defer ta.mu.Unlock()
		ta.pending[ev.Tool.ID] = desc
		return nil
	case "TaskUpdate":
		taskID, statusStr := taskUpdateFromInput(ev.Tool.Input)
		if taskID == "" {
			return nil
		}
		ta.mu.Lock()
		defer ta.mu.Unlock()
		if statusStr == "deleted" {
			if !removeByID(&ta.list, taskID) {
				return nil
			}
		} else {
			mapped := mapClaudeTaskStatus(statusStr)
			if mapped == "" {
				return nil
			}
			if !updateStatusByID(ta.list, taskID, mapped) {
				return nil
			}
		}
		return &canonical.PlanUpdate{Steps: cloneSteps(ta.list)}
	}
	return nil
}

// observePostToolUse claudecode.EventPostToolUse 帧入聚合器:仅对 TaskCreate
// 起作用 —— 此时 tool_result.meta.task.id 透出真实 id,把对应 pending 条目
// 绑 id 并 push 进 list;返一份新快照。其它工具帧返 nil。
//
// 重复触发同一 toolCallID(罕见的 retry 场景):pending 已被 take,后续 ToolResult
// 找不到 entry → silently skip。
//
// 注意 SDK 在 parseUserContent 里构造 PostToolUse 的 ToolEvent 不会填 Tool.Name
// (只有 ID/Response/ResultMeta),所以这里不能按 Name 过滤,得拿 pending[toolCallID]
// 做唯一门控 —— pending 只有 TaskCreate 的 Pre 阶段会写入,等价于"只对 TaskCreate
// 的 ToolResult 生效"。
func (ta *taskAggregator) observePostToolUse(ev claudecode.Event) *canonical.PlanUpdate {
	if ta == nil || ev.Tool == nil || ev.Tool.ID == "" {
		return nil
	}
	ta.mu.Lock()
	defer ta.mu.Unlock()
	desc, ok := ta.pending[ev.Tool.ID]
	if !ok {
		return nil
	}
	realID := taskIDFromResultMeta(ev.Tool.ResultMeta)
	if realID == "" {
		// pending 条目保留:CLI 偶尔会因 stop 提前结束而漏 task.id,
		// 留着等下一次同 toolCallID 的 ToolResult 不至于状态错乱。
		return nil
	}
	delete(ta.pending, ev.Tool.ID)
	upsertStep(&ta.list, canonical.PlanStep{
		ID:     realID,
		Step:   desc,
		Status: canonical.StepPending,
	})
	return &canonical.PlanUpdate{Steps: cloneSteps(ta.list)}
}

// emitSnapshot 把 canonical 快照包成 EventPlanUpdated 推到 out。
// active 调用前要保证 out 已 setOut。
func emitSnapshot(out chan<- agentruntime.Event, snap *canonical.PlanUpdate) {
	if out == nil || snap == nil {
		return
	}
	out <- agentruntime.PlanUpdated{Plan: *snap}
}

// taskDescriptionFromInput 取 TaskCreate 的"显示文本":优先 subject(真实
// schema required 字段),退到 description。前端 derive.ts 老逻辑同形。
func taskDescriptionFromInput(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return ""
	}
	for _, k := range []string{"subject", "description"} {
		if v, ok := m[k].(string); ok && v != "" {
			return v
		}
	}
	return ""
}

// taskUpdateFromInput 取 TaskUpdate 的 (taskId, statusStr)。
func taskUpdateFromInput(raw json.RawMessage) (string, string) {
	if len(raw) == 0 {
		return "", ""
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return "", ""
	}
	taskID, _ := m["taskId"].(string)
	statusStr, _ := m["status"].(string)
	return taskID, statusStr
}

// taskIDFromResultMeta 解 tool_result.meta.task.id —— CLI 在 TaskCreate 完成
// 后透出真实 server-assigned id 的唯一渠道(input 里没有)。
func taskIDFromResultMeta(meta json.RawMessage) string {
	if len(meta) == 0 {
		return ""
	}
	var m map[string]any
	if err := json.Unmarshal(meta, &m); err != nil {
		return ""
	}
	task, _ := m["task"].(map[string]any)
	if task == nil {
		return ""
	}
	id, _ := task["id"].(string)
	return id
}

// mapClaudeTaskStatus 把 CLI 原始 status 文本映射成 canonical 枚举值。
// "deleted" 是删除信号,由调用方单独处理(本函数对它返空)。未知值返空 →
// 调用方跳过这次更新。
func mapClaudeTaskStatus(raw string) canonical.PlanStepStatus {
	switch raw {
	case "pending":
		return canonical.StepPending
	case "in_progress":
		return canonical.StepInProgress
	case "completed":
		return canonical.StepCompleted
	}
	return ""
}

func removeByID(list *[]canonical.PlanStep, id string) bool {
	for i, s := range *list {
		if s.ID == id {
			*list = append((*list)[:i], (*list)[i+1:]...)
			return true
		}
	}
	return false
}

func updateStatusByID(list []canonical.PlanStep, id string, status canonical.PlanStepStatus) bool {
	for i := range list {
		if list[i].ID == id {
			list[i].Status = status
			return true
		}
	}
	return false
}

// upsertStep id 已存在 → 重置 description + status=pending(语义同 TaskCreate
// 复用 id);否则 append。
func upsertStep(list *[]canonical.PlanStep, step canonical.PlanStep) {
	for i := range *list {
		if (*list)[i].ID == step.ID {
			(*list)[i] = step
			return
		}
	}
	*list = append(*list, step)
}

func cloneSteps(in []canonical.PlanStep) []canonical.PlanStep {
	if len(in) == 0 {
		return nil
	}
	out := make([]canonical.PlanStep, len(in))
	copy(out, in)
	return out
}
