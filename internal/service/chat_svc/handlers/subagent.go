package handlers

import (
	"context"

	cagoblocks "github.com/cago-frame/agents/agent/blocks"

	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
	"github.com/agentre-hub/agentre/internal/service/chat_svc/blocks"
	"github.com/agentre-hub/agentre/internal/service/chat_svc/turn"
)

// MarkRunningSubagentsCancelled 在 turn abort 收尾时把外层累计态和 normalized runs
// 中仍未终止的 waiting/running 状态改成 canceled。已完成、失败、取消、跳过或 unknown
// 的证据原样保留，避免 abort 覆盖已经到达的终态。
//
// 这一版不加区分:轮被中断/截断时 CLI 已经不在了,后台任务同样等不到 SubagentDone。
// 正常收尾请用 MarkRunningForegroundSubagentsCancelled。
func MarkRunningSubagentsCancelled(finalBlocks []cagoblocks.ContentBlock) {
	markRunningSubagentsCancelled(finalBlocks, func(*blocks.SubagentStateBlock) bool { return true })
}

// MarkRunningForegroundSubagentsCancelled 是**正常**收尾用的同款补救,但只翻前台
// subagent。后台任务(Agent 默认后台 / run_in_background 的 Bash)本就有权活过发起
// 它的那一轮 —— runtime 随后另开旁路活动轮继续收它的帧 —— 跟着一起翻成 canceled 会
// 让派遣卡显示「已停止」、后台任务胶囊算进「已完成」,而任务其实还在跑(sess-3275)。
//
// 判据与前端 background-tasks/derive.ts 的 isBackground 同源,读的是发起它的
// tool_use 入参;判不出来(kind 未知)时按前台处理,宁可翻 canceled 也不让卡片永远转。
func MarkRunningForegroundSubagentsCancelled(acc *turn.Accumulator, finalBlocks []cagoblocks.ContentBlock) {
	markRunningSubagentsCancelled(finalBlocks, func(sb *blocks.SubagentStateBlock) bool {
		return !isBackgroundSubagent(acc, sb)
	})
}

func markRunningSubagentsCancelled(finalBlocks []cagoblocks.ContentBlock, cancel func(*blocks.SubagentStateBlock) bool) {
	for _, b := range finalBlocks {
		sb, ok := b.(*blocks.SubagentStateBlock)
		if !ok || !cancel(sb) {
			continue
		}
		if isNonTerminalSubagentStatus(sb.Status) {
			sb.Status = "canceled"
		}
		for i := range sb.Runs {
			if isNonTerminalSubagentStatus(sb.Runs[i].Status) {
				sb.Runs[i].Status = "canceled"
			}
		}
	}
}

// isBackgroundSubagent 判定 overlay 背后的任务是否后台。两种工具的默认相反:
//   - local_agent(Agent):默认后台,只有显式 run_in_background==false 才是前台
//     (真实 CLI 实测:后台 Agent 根本不带此入参);
//   - local_bash(Bash):默认前台,只有显式 run_in_background==true 才是后台。
//
// 其它 kind(含空 kind 的旧帧)一律按前台处理。
func isBackgroundSubagent(acc *turn.Accumulator, sb *blocks.SubagentStateBlock) bool {
	switch sb.Kind {
	case subagentKindLocalAgent:
		bg, explicit := runInBackgroundInput(acc, sb.ParentToolCallID)
		return !explicit || bg
	case subagentKindLocalBash:
		bg, explicit := runInBackgroundInput(acc, sb.ParentToolCallID)
		return explicit && bg
	default:
		return false
	}
}

// runInBackgroundInput 读发起 toolCallID 的 tool_use 入参 run_in_background,
// explicit 区分「显式给了布尔值」与「缺省/找不到那块 tool_use」。
func runInBackgroundInput(acc *turn.Accumulator, toolCallID string) (bg bool, explicit bool) {
	if acc == nil {
		return false, false
	}
	input, ok := acc.ToolUseInput(toolCallID)
	if !ok {
		return false, false
	}
	v, isBool := input["run_in_background"].(bool)
	return v, isBool
}

func isNonTerminalSubagentStatus(status string) bool {
	return status == "waiting" || status == "running"
}

func cloneSubagentRuns(runs []agentruntime.SubagentRun) []agentruntime.SubagentRun {
	if runs == nil {
		return nil
	}
	out := make([]agentruntime.SubagentRun, len(runs))
	copy(out, runs)
	return out
}

func mergeNormalizedSnapshot(b *blocks.SubagentStateBlock, info agentruntime.SubagentInfo) {
	if info.Mode != "" {
		b.Mode = info.Mode
	}
	if info.Runs != nil {
		b.Runs = cloneSubagentRuns(info.Runs)
	}
	if info.Status != "" {
		b.Status = info.Status
	}
}

// subagentKindLocalBash / subagentKindLocalAgent 是 CLI task_type 里 bash / subagent
// 的取值(对应 SubagentStateBlock.Kind)。
const (
	subagentKindLocalBash  = "local_bash"
	subagentKindLocalAgent = "local_agent"
)

// trackSubagentState 判定这次 task 帧是否该建/维护 SubagentStateBlock overlay。
// 真实 CLI 对*每一次* Bash 都发 task_type:"local_bash" 帧,但只有 run_in_background
// 的 bash 才是真正的后台任务;普通前台 bash 不该有 overlay(否则污染后台任务面板 +
// 白存一堆无意义持久化块)。subagent(local_agent)与空 kind 一律 track。找不到对应
// tool_use 块时保守 track —— 真实流里 Bash tool_use 总先于 task_started 到达,找不到
// 属异常,宁可多挂一个也不漏掉真后台任务。
func trackSubagentState(acc *turn.Accumulator, toolCallID, kind string) bool {
	if kind != subagentKindLocalBash {
		return true
	}
	input, ok := acc.ToolUseInput(toolCallID)
	if !ok {
		return true
	}
	bg, _ := input["run_in_background"].(bool)
	return bg
}

type SubagentStartedHandler struct{}

func (SubagentStartedHandler) Apply(ctx context.Context, ev agentruntime.Event, acc *turn.Accumulator, emit turn.Emitter, _ turn.View, tc *turn.TurnContext) error {
	r := ev.(agentruntime.SubagentStarted)
	if !trackSubagentState(acc, r.ToolCallID, r.Info.Kind) {
		return nil // 前台 bash:不建 overlay,也不 emit(后续 progress/done 经 Mutate 未命中自然静默)
	}
	status := r.Info.Status
	if status == "" {
		status = "running"
	}
	blk := &blocks.SubagentStateBlock{
		ParentToolCallID: r.ToolCallID,
		TaskID:           r.Info.TaskID, // CLI task_id,供 StopBackgroundTask 下发 stop_task 定位
		Kind:             r.Info.Kind,
		Description:      r.Info.TaskDescription,
		Status:           status,
		Mode:             r.Info.Mode,
		Runs:             cloneSubagentRuns(r.Info.Runs),
	}
	acc.AddBlock(blk, "subagent_state:"+r.ToolCallID)

	if emit != nil {
		emit.Emit(ctx, streamOf(tc), map[string]any{
			"kind":      "subagent_started",
			"toolUseId": r.ToolCallID,
			"info":      r.Info,
		})
	}
	return nil
}

type SubagentProgressHandler struct{}

func (SubagentProgressHandler) Apply(ctx context.Context, ev agentruntime.Event, acc *turn.Accumulator, emit turn.Emitter, _ turn.View, tc *turn.TurnContext) error {
	r := ev.(agentruntime.SubagentProgress)
	// task_progress 帧不带 task_type,无法自己判前台/后台;靠 Mutate 是否命中既有
	// overlay 来判定 —— 前台 bash 在 Started 已被跳过,这里命中不到 → 不 emit 孤儿事件。
	hit := turn.Mutate[blocks.SubagentStateBlock](acc, "subagent_state:"+r.ToolCallID, func(b *blocks.SubagentStateBlock) {
		mergeNormalizedSnapshot(b, r.Info)
		// R4/R10:TotalTokens/ToolUses/DurationMs 三者来自同一个 CLI usage 对象
		// (taskUsage,值类型无存在性区分)。task_progress 帧偶尔缺 usage,解码成
		// 零值后若无条件赋值,会把已经攒起来的 token 数 / 工具数 / 耗时抹回 0——
		// 真实 CLI 帧本身单调递增,0 只可能是缺失/异常帧,故 0 值不覆盖已记录值。
		if r.Info.TotalTokens != 0 {
			b.TotalTokens = r.Info.TotalTokens
		}
		b.LastToolName = r.Info.LastToolName
		if r.Info.ToolUses != 0 {
			b.ToolUses = r.Info.ToolUses
		}
		if b.TaskID == "" && r.Info.TaskID != "" {
			b.TaskID = r.Info.TaskID // task_started 缺 task_id 时由 task_progress 回填
		}
		if r.Info.DurationMs != 0 {
			b.DurationMs = r.Info.DurationMs
		}
	})
	if !hit {
		return nil
	}
	if emit != nil {
		emit.Emit(ctx, streamOf(tc), map[string]any{
			"kind":      "subagent_progress",
			"toolUseId": r.ToolCallID,
			"info":      r.Info,
		})
	}
	return nil
}

// SubagentModelHandler 接住 agentruntime.SubagentModel(claudecode 从 subagent 内部
// assistant 帧解析出的实际模型,R2)。独立事件类型,刻意不复用 SubagentProgressHandler
// —— 那个 handler 对 TotalTokens/LastToolName/ToolUses 是无条件赋值,混进一个只带
// 模型的 SubagentInfo 会把已累计的进度清零(R4)。
type SubagentModelHandler struct{}

func (SubagentModelHandler) Apply(ctx context.Context, ev agentruntime.Event, acc *turn.Accumulator, emit turn.Emitter, _ turn.View, tc *turn.TurnContext) error {
	r := ev.(agentruntime.SubagentModel)
	if r.Model == "" {
		// 这不是同进程内对生产者契约的重复判空(那一层已在 translator.go 删除,见
		// wrap-up 复审第三轮 Finding 2)。此 handler 消费的 agentruntime.Event 在
		// 远程执行场景下经 daemon 通过 WebSocket 把 wire JSON 反序列化而来
		// (internal/pkg/agentruntime/event_wire.go),协议边界之外没有编译期保证
		// 字段非空——这里是真正的信任边界,必须自己校验,不能假设上游守约。
		return nil
	}
	var recorded bool
	hit := turn.Mutate[blocks.SubagentStateBlock](acc, "subagent_state:"+r.ToolCallID, func(b *blocks.SubagentStateBlock) {
		if b.Model != "" {
			return // first-wins(R3):模型一经记录,后续内部帧不再改写
		}
		b.Model = r.Model
		recorded = true
	})
	if !hit || !recorded {
		// 命中不到既有 overlay(如前台 bash 从未 track)→ 不 emit 孤儿事件,同
		// Progress/Done handler 的既有约定;命中但已记录过(first-wins 拒绝改写)
		// 同样不必再通知前端。
		return nil
	}
	if emit != nil {
		// 只带 toolUseId + model,不携带 toolUses/totalTokens/status 等累计态字段
		// (R4)—— 前端浅合并若拿到整个 info/block 快照,SubagentStateBlock.Status
		// 的 JSON 标签没有 omitempty,会把已有状态覆盖成空串。
		emit.Emit(ctx, streamOf(tc), map[string]any{
			"kind":      "subagent_model",
			"toolUseId": r.ToolCallID,
			"model":     r.Model,
		})
	}
	return nil
}

type SubagentDoneHandler struct{}

func (SubagentDoneHandler) Apply(ctx context.Context, ev agentruntime.Event, acc *turn.Accumulator, emit turn.Emitter, _ turn.View, tc *turn.TurnContext) error {
	r := ev.(agentruntime.SubagentDone)
	hit := turn.Mutate[blocks.SubagentStateBlock](acc, "subagent_state:"+r.ToolCallID, func(b *blocks.SubagentStateBlock) {
		mergeNormalizedSnapshot(b, r.Info)
		if r.Info.Status == "" {
			b.Status = "completed"
		}
		// 与 SubagentProgressHandler 同一守卫(R4):0 值不覆盖已记录值。一帧不带
		// usage 对象的 task_notification 解码成零值 taskUsage{},若无条件赋值会把
		// Progress 阶段已累计的 token 数 / 工具数 / 耗时清零。
		if r.Info.TotalTokens != 0 {
			b.TotalTokens = r.Info.TotalTokens
		}
		if r.Info.DurationMs != 0 {
			b.DurationMs = r.Info.DurationMs
		}
		if r.Info.ToolUses != 0 {
			b.ToolUses = r.Info.ToolUses
		}
	})
	if !hit {
		return flipSubagentOutsideTurn(ctx, acc, tc, r)
	}
	if emit != nil {
		emit.Emit(ctx, streamOf(tc), map[string]any{
			"kind":      "subagent_done",
			"toolUseId": r.ToolCallID,
			"info":      r.Info,
		})
	}
	return nil
}

// flipSubagentOutsideTurn 处理「本轮 accumulator 里没有这个 overlay」的完成帧。落空有
// 两种成因,结果完全不同:
//
//   - 发起它的 tool_use **就在本轮** —— 前台 bash,按 trackSubagentState 的约定从未建
//     overlay。静默忽略:一条消息里几十次普通 Bash,每次都去跨消息扫一遍纯属白跑。
//   - 发起它的 tool_use **不在本轮** —— 后台任务(run_in_background 的 bash / subagent)
//     在别人的轮里完成了。它的派遣卡住在更早那条消息里,过不了 per-turn accumulator。
//
// 后一种此前被一并静默丢弃,派遣卡就永远停在 running(sess-2825)。跨消息翻转那条通路
// 当时只挂在自主续轮上,而完成通知只有在**会话空闲**时才起得了自主续轮;若它在某一轮
// 进行中到达,CLI 会把这一帧并进当前活跃轮,此处便是该终态唯一的落点。
func flipSubagentOutsideTurn(
	ctx context.Context, acc *turn.Accumulator, tc *turn.TurnContext, r agentruntime.SubagentDone,
) error {
	if tc == nil || tc.SubagentFlipper == nil || r.ToolCallID == "" {
		return nil
	}
	if _, inThisTurn := acc.ToolUseInput(r.ToolCallID); inThisTurn {
		return nil
	}
	status := r.Info.Status
	if status == "" {
		status = "completed" // 与命中路径同一默认,否则空 status 会被 flipper 直接丢弃
	}
	return tc.SubagentFlipper.FlipSubagentStatus(ctx, r.ToolCallID, status)
}
