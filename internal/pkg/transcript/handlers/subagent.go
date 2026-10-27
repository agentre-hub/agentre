package handlers

import (
	"context"
	"fmt"
	"slices"

	cagoblocks "github.com/cago-frame/agents/agent/blocks"
	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"

	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
	"github.com/agentre-hub/agentre/internal/pkg/transcript/blocks"
	"github.com/agentre-hub/agentre/internal/pkg/transcript/turn"
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
//
// agrctl 绑定的卡(Origin = agrctl)例外:它的子任务不随父轮结束,前台卡由注入方用子轮结果
// 收尾、后台卡由 CLI 收尾(spec「agrctl send 子代理卡」Card lifecycle「前台与后台」)。父轮
// 正常结束时 Codex 的命令可能还在跑,翻成已取消就是在说谎。
func isBackgroundSubagent(acc *turn.Accumulator, sb *blocks.SubagentStateBlock) bool {
	if sb.Origin == agentruntime.SubagentOriginAgrctl {
		return true
	}
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

// isTerminalSubagentStatus 已收尾:completed / failed / canceled / skipped / unknown 等。
func isTerminalSubagentStatus(status string) bool {
	return status != "" && !isNonTerminalSubagentStatus(status)
}

func cloneSubagentRuns(runs []agentruntime.SubagentRun) []agentruntime.SubagentRun {
	return slices.Clone(runs)
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
	subagentKindLocalBash  = agentruntime.SubagentKindLocalBash
	subagentKindLocalAgent = agentruntime.SubagentKindLocalAgent
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

func (SubagentStartedHandler) Apply(ctx context.Context, ev agentruntime.Event, acc *turn.Accumulator, emit turn.Emitter, tc *turn.TurnContext) error {
	r := ev.(agentruntime.SubagentStarted)
	if isAgrctlFrame(r.Info) {
		return startAgrctlOverlay(ctx, r, acc, emit, tc)
	}
	if bound, accepted, applied := applyAgrctlFrame(acc, r.ToolCallID, r.Info, "running"); bound {
		if accepted {
			// 卡片已经存在,CLI 这一帧对它只是一次增量更新,不是新建。
			emitAgrctlFrame(ctx, emit, tc, "subagent_progress", r.ToolCallID, applied)
		}
		return nil
	}
	if !trackSubagentState(acc, r.ToolCallID, r.Info.Kind) {
		return nil // 前台 bash:不建 overlay,也不 emit(后续 progress/done 经 Mutate 未命中自然静默)
	}
	status := r.Info.Status
	if status == "" {
		status = "running"
	}
	// 先认本轮的 overlay,再去库里找更早的卡:本轮已有这个 task 的卡时,它就是原卡,
	// 收尾时随本条消息一起落库;此时再查库推回运行态,改的是一份马上会被覆盖的旧行。
	owner, resumed := adoptResumedSubagent(acc, r, status)
	if !resumed {
		owner, resumed = adoptCrossTurnResume(ctx, r, status, tc)
	}
	if resumed {
		// 恢复 / 重开:同一个 task 的又一段,已认领到原卡上,不另起第二块。
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

// adoptResumedSubagent 认领本轮已有 overlay 的那个 task 再发的 task_started —— 无论它换了
// 新的 tool_use_id(SendMessage 恢复),还是沿用原来那个(完成后被继续,sess-4198)。
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
			return b.TaskID == r.Info.TaskID
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
// 只在本轮确实没有这个 task 的 overlay 时才走:调用点先试 adoptResumedSubagent,命中就
// 不会发生这次仓储查询。
//
// CLI 会用同一个 task、同一个 tool_use_id 再发 task_started(后台 subagent 完成后被继续,
// sess-4198 实测同一 task 最多三遍)。那也是认领:交回的 owner 与它自身相等,不记别名。
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
	if got == "" {
		return "", false
	}
	// 同一个 tool call 的重开(got == r.ToolCallID)同样是认领:原卡已被推回运行态,
	// 本轮不能再起一块副本,否则完成帧只翻副本、原卡永远停在 running(sess-4198)。
	// 它不需要别名 —— 完成帧带的就是原卡的 tool call,落空后直接跨消息翻到原卡上。
	if got != r.ToolCallID {
		tc.AliasSubagentToolCall(r.ToolCallID, got)
	}
	return got, true
}

type SubagentProgressHandler struct{}

func (SubagentProgressHandler) Apply(ctx context.Context, ev agentruntime.Event, acc *turn.Accumulator, emit turn.Emitter, tc *turn.TurnContext) error {
	r := ev.(agentruntime.SubagentProgress)
	if bound, accepted, applied := applyAgrctlFrame(acc, r.ToolCallID, r.Info, ""); bound || isAgrctlFrame(r.Info) {
		if accepted {
			emitAgrctlFrame(ctx, emit, tc, "subagent_progress", r.ToolCallID, applied)
		}
		return nil
	}
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

func (SubagentModelHandler) Apply(ctx context.Context, ev agentruntime.Event, acc *turn.Accumulator, emit turn.Emitter, tc *turn.TurnContext) error {
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
		recorded = RecordSubagentModel(b, r.Model)
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

// RecordSubagentModel 把子代理的实际模型记到 overlay 上,交回是否写入。first-wins(R3):
// 模型一经记录,后续帧不再改写。agrctl 卡的单运行快照跟着对齐。它是累加器之外的同一条规则:
// 宿主在父轮结束后经仓储改写原消息里的卡时也调它。
func RecordSubagentModel(b *blocks.SubagentStateBlock, model string) bool {
	if model == "" || b.Model != "" {
		return false
	}
	b.Model = model
	syncAgrctlRun(b)
	return true
}

type SubagentDoneHandler struct{}

func (SubagentDoneHandler) Apply(ctx context.Context, ev agentruntime.Event, acc *turn.Accumulator, emit turn.Emitter, tc *turn.TurnContext) error {
	r := ev.(agentruntime.SubagentDone)
	if bound, accepted, applied := applyAgrctlFrame(acc, r.ToolCallID, r.Info, "completed"); bound || isAgrctlFrame(r.Info) {
		if accepted {
			emitAgrctlFrame(ctx, emit, tc, "subagent_done", r.ToolCallID, applied)
		}
		return nil
	}
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
	// 跨轮恢复过的 task:本轮这个新 tool call 是原卡的别名,终态要翻在原卡上。恢复若发生在
	// 更早的轮里,别名已随那一轮丢失,只能靠 task_id 让 flipper 找回原卡(sess-4198)。
	return tc.SubagentFlipper.FlipSubagentStatus(
		ctx, tc.ResolveSubagentToolCall(r.ToolCallID), r.Info.TaskID, status, r.Info.Summary)
}

// ---- agrctl send --wait 绑定的 overlay ----
//
// 父轮里 agent 跑 `agrctl send --wait` 时,父轮的宿主把子会话绑到那条外层 shell 调用上
// (ctlbind.Match),并以 Info.Origin = SubagentOriginAgrctl 的 task 帧注入一块 overlay、
// 推进度与收尾。同一个 tool call ID 上 CLI 也会发自己的 local_bash 帧,写入分工:
//   - 注入方:嵌套步骤、用量、工具数、子任务最终文本(FinalText);前台卡的状态。
//   - CLI:后台卡(Kind 仍是 local_bash)的状态、task_id、summary(退出码文本);
//     前台卡(Kind = SubagentKindAgrctl)上 CLI 的帧一律不收。
//   - 终态之后,任何一方迟到的非终态更新都不能把卡片写回运行中(例如父轮被中止、卡片
//     已标成取消后,注入方才到的进度),取消也不能覆盖已收尾的卡(resume 前奏补发的
//     stopped)。
//   - 单运行快照(Mode/Runs[0])不归任何一方:每次写入之后都从 overlay 自身重新派生
//     (syncAgrctlRun)。SubagentStateBlock 不持久化 SubagentType,重载时卡片名只能取
//     Runs[0].Agent,所以本机与 agentred 都在这一层写它。

// agrctlRunMode 是 agrctl 卡单运行快照的 Mode。
const agrctlRunMode = "single"

// isAgrctlFrame 这一帧是否由 agrctl 绑定的注入方发出。
func isAgrctlFrame(info agentruntime.SubagentInfo) bool {
	return info.Origin == agentruntime.SubagentOriginAgrctl
}

// startAgrctlOverlay 把注入方的 Started 落成 overlay。CLI 若已为这条后台 Bash 建了
// local_bash overlay(它的 task_started 可能先于 send 请求到达),就认领那一块,不另起;
// 这条调用已绑过子会话时整帧忽略 —— 一条 shell 调用最多绑定一个子会话。
func startAgrctlOverlay(
	ctx context.Context, r agentruntime.SubagentStarted, acc *turn.Accumulator, emit turn.Emitter, tc *turn.TurnContext,
) error {
	key := "subagent_state:" + r.ToolCallID
	var adopted, alreadyBound bool
	turn.Mutate[blocks.SubagentStateBlock](acc, key, func(b *blocks.SubagentStateBlock) {
		if b.Origin == agentruntime.SubagentOriginAgrctl {
			alreadyBound = true
			return
		}
		bindAgrctl(b, r.Info) // 后台卡的状态 / kind / task_id 仍归 CLI,不动
		adopted = true
	})
	if alreadyBound {
		return nil
	}
	if !adopted {
		status := r.Info.Status
		if status == "" {
			status = "running"
		}
		blk := &blocks.SubagentStateBlock{ParentToolCallID: r.ToolCallID, Kind: r.Info.Kind, Status: status}
		bindAgrctl(blk, r.Info)
		acc.AddBlock(blk, key)
	}
	if emit != nil {
		emit.Emit(ctx, streamOf(tc), map[string]any{
			"kind":      "subagent_started",
			"toolUseId": r.ToolCallID,
			"info":      r.Info,
		})
	}
	return nil
}

// bindAgrctl 写入绑定时就确定的那几项:归属、子会话、isolated、任务文本,以及单运行快照的
// 身份(子会话运行 ID、目标 agent 名、任务文本)。
func bindAgrctl(b *blocks.SubagentStateBlock, info agentruntime.SubagentInfo) {
	b.Origin = agentruntime.SubagentOriginAgrctl
	b.ChildSessionID = info.ChildSessionID
	b.Isolated = info.Isolated
	if info.TaskDescription != "" {
		b.Description = info.TaskDescription
	}
	b.Runs = []agentruntime.SubagentRun{{
		ID:    fmt.Sprintf("agrctl:%d", info.ChildSessionID),
		Agent: info.SubagentType,
		Task:  info.TaskDescription,
	}}
	syncAgrctlRun(b)
}

// syncAgrctlRun 把 agrctl 卡的单运行快照对齐到 overlay 当前的状态:身份(ID / Agent / Task)
// 沿用绑定时写下的,其余从 overlay 派生 —— 不另存一份会漂移的状态。子任务最终文本失败时
// 是错误信息,否则是结论;CLI 的 summary(退出码文本)不进快照。快照只由 bindAgrctl 建立:
// 没有它的卡(不知道目标 agent)这里不凭空造一份没有身份的运行。
func syncAgrctlRun(b *blocks.SubagentStateBlock) {
	if b.Origin != agentruntime.SubagentOriginAgrctl || len(b.Runs) != 1 {
		return
	}
	run := agentruntime.SubagentRun{ID: b.Runs[0].ID, Agent: b.Runs[0].Agent, Task: b.Runs[0].Task}
	run.Status = b.Status
	run.Model = b.Model
	run.ToolUses = b.ToolUses
	run.LastToolName = b.LastToolName
	if b.Status == "failed" {
		run.ErrorMessage = b.FinalText
	} else {
		run.Summary = b.FinalText
	}
	b.Mode = agrctlRunMode
	b.Runs = []agentruntime.SubagentRun{run}
}

// applyAgrctlFrame 若 toolCallID 上挂的是 agrctl 绑定的 overlay,就按写入分工把这一帧
// 落上去。bound=false 表示那里没有 agrctl overlay,调用方走原有逻辑;accepted 表示这一帧
// 被接收(前台卡上 CLI 的帧不被接收,也不该 emit);applied 是实际生效的字段,供 emit。
// defaultStatus 是这一帧不带 status 时隐含的状态(Started = running,Done = completed)。
//
// 注入方的帧只落在本轮 accumulator 里已有的 agrctl overlay 上:调用方见到
// isAgrctlFrame 却 bound=false 时直接丢弃,不走 CLI 帧的跨消息翻转那条路 —— 那条路只
// 认 Summary,会丢掉 FinalText,还会越权改后台卡归 CLI 的状态。父轮结束后的更新由宿主
// 先把原消息里的 overlay 种进 accumulator 再投递。
func applyAgrctlFrame(
	acc *turn.Accumulator, toolCallID string, info agentruntime.SubagentInfo, defaultStatus string,
) (bound, accepted bool, applied agentruntime.SubagentInfo) {
	turn.Mutate[blocks.SubagentStateBlock](acc, "subagent_state:"+toolCallID, func(b *blocks.SubagentStateBlock) {
		bound, accepted, applied = ApplyAgrctlInfo(b, info, defaultStatus)
	})
	return bound, accepted, applied
}

// ApplyAgrctlInfo 按写入分工把一帧落到一块 overlay 上,返回值同 applyAgrctlFrame。它是
// 累加器之外的同一套规则:父轮结束后,宿主经仓储读-改-写原消息里的那块(注入方的写回、
// CLI 跨消息的状态翻转)也调它,两边不各写一份。
func ApplyAgrctlInfo(
	b *blocks.SubagentStateBlock, info agentruntime.SubagentInfo, defaultStatus string,
) (bound, accepted bool, applied agentruntime.SubagentInfo) {
	if b.Origin != agentruntime.SubagentOriginAgrctl {
		return false, false, applied
	}
	fromInjector := isAgrctlFrame(info)
	background := b.Kind == subagentKindLocalBash
	if !fromInjector && !background {
		return true, false, applied // 前台卡:CLI 为这条 shell 调用发的 local_bash 帧一律不收
	}
	status := info.Status
	if status == "" {
		status = defaultStatus
	}
	// CLI 的帧只带状态与退出码文本;状态不被接受时(resume 前奏补发的 stopped 落在已完成
	// 的卡上)整帧都是过期信息,summary 也不收。
	if !fromInjector && !agrctlStatusAccepted(b.Status, status) {
		return true, false, applied
	}
	if fromInjector {
		applied = mergeAgrctlContent(b, info)
	} else {
		applied = mergeCLIBackgroundFields(b, info)
	}
	// 状态归属:前台卡归注入方,后台卡归 CLI。
	if fromInjector != background && setAgrctlStatus(b, status) {
		applied.Status = status
	}
	syncAgrctlRun(b)
	return true, true, applied
}

// mergeAgrctlContent 落注入方拥有的内容字段;0 / 空值不覆盖已记录值(同 R4 守卫)。
func mergeAgrctlContent(b *blocks.SubagentStateBlock, info agentruntime.SubagentInfo) agentruntime.SubagentInfo {
	applied := agentruntime.SubagentInfo{Origin: info.Origin}
	if info.ToolUses != 0 {
		b.ToolUses, applied.ToolUses = info.ToolUses, info.ToolUses
	}
	if info.TotalTokens != 0 {
		b.TotalTokens, applied.TotalTokens = info.TotalTokens, info.TotalTokens
	}
	if info.DurationMs != 0 {
		b.DurationMs, applied.DurationMs = info.DurationMs, info.DurationMs
	}
	if info.LastToolName != "" {
		b.LastToolName, applied.LastToolName = info.LastToolName, info.LastToolName
	}
	if info.FinalText != "" {
		b.FinalText, applied.FinalText = info.FinalText, info.FinalText
	}
	return applied
}

// mergeCLIBackgroundFields 落 CLI 在后台卡上仍拥有的字段:task_id(停止按钮靠它下发
// stop_task)与 summary(退出码文本,和 FinalText 分开存,互不覆盖)。
func mergeCLIBackgroundFields(b *blocks.SubagentStateBlock, info agentruntime.SubagentInfo) agentruntime.SubagentInfo {
	var applied agentruntime.SubagentInfo
	if b.TaskID == "" && info.TaskID != "" {
		b.TaskID, applied.TaskID = info.TaskID, info.TaskID
	}
	if info.Summary != "" {
		b.Summary, applied.Summary = info.Summary, info.Summary
	}
	return applied
}

// setAgrctlStatus 写 overlay 状态,但终态之后不接受非终态:卡片一旦完成 / 失败 / 取消,
// 迟到的运行中更新不能把它写回运行中。单运行快照随后由调用方 syncAgrctlRun 对齐。
func setAgrctlStatus(b *blocks.SubagentStateBlock, status string) bool {
	if status == "" || !agrctlStatusAccepted(b.Status, status) {
		return false
	}
	b.Status = status
	return true
}

// agrctlStatusAccepted 已收尾的卡只接受「另一个真实结果」:运行中不能复活它(父轮被中止
// 后注入方迟到的进度),取消也不能覆盖它 —— 已完成 / 失败的卡收到的取消只可能是过期的
// (resume 前奏为上个进程补发的 stopped 通知),已取消的卡再取消一次没有意义。
func agrctlStatusAccepted(current, next string) bool {
	if next == "" || !isTerminalSubagentStatus(current) {
		return true
	}
	return !isNonTerminalSubagentStatus(next) && next != "canceled"
}

func emitAgrctlFrame(
	ctx context.Context, emit turn.Emitter, tc *turn.TurnContext, kind, toolCallID string, info agentruntime.SubagentInfo,
) {
	if emit == nil {
		return
	}
	emit.Emit(ctx, streamOf(tc), map[string]any{
		"kind":      kind,
		"toolUseId": toolCallID,
		"info":      info,
	})
}
