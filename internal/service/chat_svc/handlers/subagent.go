package handlers

import (
	"context"

	cagoblocks "github.com/cago-frame/agents/agent/blocks"
	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"

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
	if owner, resumed := adoptCrossTurnResume(ctx, r, status, tc); resumed {
		if emit != nil {
			emit.Emit(ctx, streamOf(tc), map[string]any{
				"kind":      "subagent_started",
				"toolUseId": owner,
				"info":      r.Info,
			})
		}
		return nil
	}
	if owner, resumed := adoptResumedSubagent(acc, r, status); resumed {
		// 恢复重开:同一个 task 换了 tool call,已认领到原卡上,不另起第二块。
		if emit != nil {
			emit.Emit(ctx, streamOf(tc), map[string]any{
				"kind":      "subagent_started",
				"toolUseId": owner,
				"info":      r.Info,
			})
		}
		return nil
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

// adoptResumedSubagent 认领「同一个 task_id、新的 tool_use_id」的 task_started。
//
// CLI 恢复一个子代理(SendMessage)时不会新起一个 task,而是用原 task_id 重发一遍
// task_started/task_notification,只换 tool_use_id(sess-3504 实测)。overlay 若只按
// tool call 归集,恢复后的那一段会另起一块挂在 SendMessage 那次调用上 —— 而
// SendMessage 不是 agent.spawn 工具名(canonical/from_tool_use.go 只认 task/agent),
// 那块永远没有卡片渲染:原卡永远停在 failed,恢复后跑出来的结论一个字都看不到。
//
// 认领 = 把新 tool call 的 mutate key 指到原块上(后续 progress/done/model 帧不必知道
// 自己是恢复来的),把原块推回运行态,并把被覆盖的那一段终态记进 Resumes 留证。
// task_id 为空时不参与 —— 否则一轮里所有无 id 的 overlay 会互相吞并。
//
// owner 是被认领到的原卡 tool_use_id。归一之后所有 live 事件都必须报它:前端
// mergeSubagentMetaBlocks 按 toolUseId 找外层 tool_use 块挂元数据,继续报新 id 会挂到
// SendMessage 那个块上 —— 后端归一了,界面上仍是两张卡,直到刷新才对齐。
func adoptResumedSubagent(acc *turn.Accumulator, r agentruntime.SubagentStarted, status string) (owner string, resumed bool) {
	if r.Info.TaskID == "" {
		return "", false
	}
	hit := turn.AdoptMutateKey(acc, "subagent_state:"+r.ToolCallID,
		func(b *blocks.SubagentStateBlock) bool {
			return b.TaskID == r.Info.TaskID && b.ParentToolCallID != r.ToolCallID
		},
		func(b *blocks.SubagentStateBlock) {
			b.Resumes = append(b.Resumes, blocks.SubagentInterruption{
				Status:  b.Status,
				Summary: b.Summary,
			})
			b.Status = status
			// 上一段的结论已收进 Resumes;留着它会让恢复后仍在跑的卡片显示上一次的
			// 中断原因,新的结论到达前这里应当是空的。
			b.Summary = ""
			owner = b.ParentToolCallID
		})
	return owner, hit
}

// adoptCrossTurnResume 处理「恢复发生在后一轮」——原卡早已落库,同轮那条
// adoptResumedSubagent 够不着它。按 task_id 让仓储把它推回运行态,并把本轮这个新
// tool call 记成它的别名,后续 done 帧才翻得到原卡而不是凭空造一张。
//
// 只在本轮确实没有这个 task 的 overlay 时才走(调用点排在 adoptResumedSubagent 之前
// 但两者互斥:同轮命中时仓储那次查询根本不会发生 —— 见下面的 acc 判空)。
//
// 重放误伤不成立:实测 685 帧 task_started 零个完全重复(--resume 不重放它);即便
// 重放,它带的是原来那个 tool_use_id,交回的 owner 与它自身相等,不记别名也不算恢复。
func adoptCrossTurnResume(
	ctx context.Context, r agentruntime.SubagentStarted, status string, tc *turn.TurnContext,
) (owner string, resumed bool) {
	if tc == nil || tc.SubagentFlipper == nil || r.Info.TaskID == "" || r.ToolCallID == "" {
		return "", false
	}
	got, err := tc.SubagentFlipper.ResumeSubagentByTaskID(ctx, r.Info.TaskID, status)
	if err != nil {
		logger.Ctx(ctx).Warn("chat_svc.SubagentStarted: ResumeSubagentByTaskID failed",
			zap.String("taskId", r.Info.TaskID), zap.Error(err))
		return "", false
	}
	if got == "" || got == r.ToolCallID {
		return "", false
	}
	tc.AliasSubagentToolCall(r.ToolCallID, got)
	return got, true
}

type SubagentProgressHandler struct{}

func (SubagentProgressHandler) Apply(ctx context.Context, ev agentruntime.Event, acc *turn.Accumulator, emit turn.Emitter, _ turn.View, tc *turn.TurnContext) error {
	r := ev.(agentruntime.SubagentProgress)
	owner := r.ToolCallID
	// task_progress 帧不带 task_type,无法自己判前台/后台;靠 Mutate 是否命中既有
	// overlay 来判定 —— 前台 bash 在 Started 已被跳过,这里命中不到 → 不 emit 孤儿事件。
	hit := turn.Mutate[blocks.SubagentStateBlock](acc, "subagent_state:"+r.ToolCallID, func(b *blocks.SubagentStateBlock) {
		owner = b.ParentToolCallID
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
			"toolUseId": owner,
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
	owner := r.ToolCallID
	hit := turn.Mutate[blocks.SubagentStateBlock](acc, "subagent_state:"+r.ToolCallID, func(b *blocks.SubagentStateBlock) {
		owner = b.ParentToolCallID
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
			"toolUseId": owner,
			"model":     r.Model,
		})
	}
	return nil
}

type SubagentDoneHandler struct{}

func (SubagentDoneHandler) Apply(ctx context.Context, ev agentruntime.Event, acc *turn.Accumulator, emit turn.Emitter, _ turn.View, tc *turn.TurnContext) error {
	r := ev.(agentruntime.SubagentDone)
	owner := r.ToolCallID
	hit := turn.Mutate[blocks.SubagentStateBlock](acc, "subagent_state:"+r.ToolCallID, func(b *blocks.SubagentStateBlock) {
		owner = b.ParentToolCallID
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
		// Summary 是这一 task 的结论(成功时子代理交回的报告全文,失败时中断原因)。
		// 同上守卫:不带 summary 的帧不覆盖已记录值。
		if r.Info.Summary != "" {
			b.Summary = r.Info.Summary
		}
	})
	if !hit {
		return flipSubagentOutsideTurn(ctx, acc, tc, r)
	}
	if emit != nil {
		emit.Emit(ctx, streamOf(tc), map[string]any{
			"kind":      "subagent_done",
			"toolUseId": owner,
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
	if _, inThisTurn := acc.ToolUseInput(r.ToolCallID); inThisTurn &&
		tc.ResolveSubagentToolCall(r.ToolCallID) == r.ToolCallID {
		// 发起它的 tool_use 就在本轮且没被别名过 —— 前台 bash,按 trackSubagentState
		// 的约定从未建 overlay,静默忽略。别名过的那些相反:SendMessage 也在本轮,
		// 但它承载的是更早那张卡的恢复段,终态必须翻过去。
		return nil
	}
	status := r.Info.Status
	if status == "" {
		status = "completed" // 与命中路径同一默认,否则空 status 会被 flipper 直接丢弃
	}
	// 跨轮恢复过的 task:本轮这个新 tool call 是原卡的别名,终态要翻在原卡上。
	return tc.SubagentFlipper.FlipSubagentStatus(
		ctx, tc.ResolveSubagentToolCall(r.ToolCallID), status, r.Info.Summary)
}
