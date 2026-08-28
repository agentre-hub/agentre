package chat_svc

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/cago-frame/agents/agent/blocks"
	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/agent_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/chat_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/llm_provider_entity"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
	"github.com/agentre-hub/agentre/internal/repository/chat_repo"
	"github.com/agentre-hub/agentre/internal/service/chat_svc/handlers"
	"github.com/agentre-hub/agentre/internal/service/chat_svc/ipc"
	"github.com/agentre-hub/agentre/internal/service/chat_svc/turn"
	"github.com/agentre-hub/agentre/internal/service/chat_svc/view"
)

// turnRun 承载 runTurn 一轮执行期间的全部可变状态。字段逐一对应原先散在
// runTurn 函数体里的 local,拆成方法后语义不变:attachRuntime / initSegment /
// consumeEvents / finalize 依次跑,顺序与原函数一致。
type turnRun struct {
	svc *chatSvc

	sess         *chat_entity.Session
	a            *agent_entity.Agent
	be           *agent_backend_entity.AgentBackend
	prov         *llm_provider_entity.LLMProvider
	userMsg      *chat_entity.Message
	assistantMsg *chat_entity.Message
	stream       string
	compact      bool
	extras       turnExtras

	runner agentruntime.Runtime
	events <-chan agentruntime.Event
	result *agentruntime.RunResult
	req    agentruntime.RunRequest

	acc           *turn.Accumulator
	streamStopErr error
	segmentStart  time.Time
	dispEmit      *dispatcherEmitter
	turnCtx       *turn.TurnContext
	// pendingSteers 已被 backend 消费、但分段还没落地的 steer。见 flushPendingSteers。
	pendingSteers []agentruntime.ConsumedSteer
}

// attachRuntime 落 runner-start 侧效果:回吐 provider session id、按 runtime 能力
// 惰性起自主续轮 / 后台 subagent 活动 watcher、把实际下发的 permission mode 落库。
func (t *turnRun) attachRuntime(ctx context.Context) {
	if t.result != nil && (t.be.IsClaudeCode() || t.be.IsCodex() || t.be.IsPiAgent()) {
		t.svc.persistProviderSessionID(ctx, t.sess, t.result.ProviderSessionID, "runner-start")
	}
	// runtime 若支持「自主续轮」(claudecode / remote claudecode 在 run_in_background
	// 任务完成后**自主**跑一轮),惰性起每会话 watcher 把它落成纯 assistant 轮。session
	// 已在 Run 内 spawn,此刻订阅 AutonomousTurns 才能拿到该会话的 channel;每会话去重,
	// 重复调用幂等。watcher 在子进程 evict / CloseSession(channel close)时自行退出。
	if src, ok := t.runner.(agentruntime.AutonomousTurnSource); ok {
		t.svc.startAutonomousWatcher(t.sess.ID, t.be, src)
	}
	// runtime 若支持「后台 subagent 内部活动流」(本地 claudecode 在
	// run_in_background subagent 空闲态产出内部工具调用),惰性起每会话 watcher 把每轮活动
	// 嵌套渲染回发起卡并跨消息落库。每会话去重,channel close 时自行退出。
	// 注: remote claudecode (agentred) 目前未实现 SubagentActivitySource,
	// 仅本地 claudecode runtime 走这条路径。
	if src, ok := t.runner.(agentruntime.SubagentActivitySource); ok {
		t.svc.startSubagentActivityWatcher(t.sess.ID, t.be, src)
	}
	// runtime spawn 新 CLI 子进程时把实际下发的 --permission-mode 同步回吐到
	// result.LaunchPermissionMode(claudecode 专用,其它 runtime 留空);这里把
	// 它落库到 session.PermissionModeAtLaunch。历史上由 runtime 直接 chat_repo
	// 写,导致 agentred daemon 进程 nil panic,搬到此处后 runtime 不再反向依
	// 赖 repository。值与库内一致时跳过,避免每轮多一次 UPDATE。
	if t.result != nil && t.result.LaunchPermissionMode != "" &&
		t.result.LaunchPermissionMode != t.sess.PermissionModeAtLaunch {
		t.sess.PermissionModeAtLaunch = t.result.LaunchPermissionMode
		if perr := chat_repo.Session().UpdatePermissionModeAtLaunch(
			ctx, t.sess.ID, t.result.LaunchPermissionMode); perr != nil {
			logger.Ctx(ctx).Warn("chat_svc: persist permission_mode_at_launch failed",
				zap.Int64("sessionId", t.sess.ID),
				zap.String("mode", t.result.LaunchPermissionMode),
				zap.Error(perr))
		}
	}
}

// initSegment 初始化本轮第一个 assistant 分段的累加器与计时口径。
func (t *turnRun) initSegment(startedAt time.Time) {
	t.acc = turn.New()
	t.segmentStart = startedAt
	t.dispEmit = &dispatcherEmitter{svc: t.svc}
	t.turnCtx = t.svc.newTurnContext(t.assistantMsg, t.sess, t.stream, t.be.Type)
}

// flushPendingSteers 把 pendingSteers 落成「收口当前 assistant + 插 user 行 +
// 开新 assistant」,并整体切换 assistantMsg/acc/segmentStart/turnCtx 这四个字段。
func (t *turnRun) flushPendingSteers(ctx context.Context) {
	if len(t.pendingSteers) == 0 {
		return
	}
	steers := t.pendingSteers
	t.pendingSteers = nil
	nextAssistant, payload, perr := t.svc.persistConsumedSteers(
		ctx, t.sess, t.be, t.assistantMsg, t.acc, t.segmentStart,
		t.assistantMsg.Model, steers, t.turnCtx,
	)
	if perr != nil {
		logger.Ctx(ctx).Warn("chat_svc: streamStopErr set by persistConsumedSteers",
			zap.Int64("sessionId", t.sess.ID),
			zap.Int64("assistantMsgId", t.assistantMsg.ID),
			zap.Error(perr))
		t.streamStopErr = perr
		return
	}
	if nextAssistant != nil && payload != nil {
		t.assistantMsg = nextAssistant
		t.acc = turn.New()
		t.segmentStart = time.Now()
		t.turnCtx = t.svc.newTurnContext(t.assistantMsg, t.sess, t.stream, t.be.Type)
		t.svc.emitter.Emit(ctx, t.stream, *payload)
	}
}

// consumeEvents 消费 runner 事件流直到 channel 关闭。
func (t *turnRun) consumeEvents(ctx context.Context) {
	for ev := range t.events {
		// Peer fanout observes the original canonical event before local reduction;
		// it never replaces the desktop emitter or dispatcher.
		t.svc.publishPeerEvent(t.sess.ID, ev)
		if t.streamStopErr != nil {
			if eventShowsProgressAfterError(ev) {
				fields := make([]zap.Field, 0, 6)
				fields = append(fields,
					zap.Int64("sessionId", t.sess.ID),
					zap.Int64("assistantMsgId", t.assistantMsg.ID),
					zap.String("clearedBy", fmt.Sprintf("%T", ev)),
				)
				fields = append(fields, chatRuntimeErrorLogFields(t.streamStopErr)...)
				logger.Ctx(ctx).Info("chat_svc.runTurn: stream error cleared by progress event", fields...)
				t.streamStopErr = nil
			} else {
				continue
			}
		}
		// SteerConsumed + ErrorEvent 不走 dispatcher:
		//   - SteerConsumed:turn-segmentation 紧耦合 assistantMsg/segmentStart/acc/turnCtx
		//     的整体切换,handler 接口表达不了这 4 个字段的同步替换。
		//   - ErrorEvent:旧路径只设 streamStopErr,真正的 StreamError emit 在 finalize
		//     阶段(带 ChatMessage 完整快照);ErrorHandler 单独 emit 会与 finalize 重复
		//     且缺 Message 字段。
		switch e := ev.(type) {
		case agentruntime.SteerConsumed:
			t.pendingSteers = append(t.pendingSteers, e.Steers...)
			// 工具在途时先不分段:claudecode 的 PostToolUse hook 在 CLI 写出
			// tool_result 帧**之前**就 drain 走排队消息,SteerConsumed 因此会先于
			// 同一个工具的 ToolResult 到达。此刻收口 assistant 会把 tool_use 冻在
			// 旧消息里,随后的 tool_result 在新 accumulator 里查不到 tool_use,被
			// ToolResultHandler 当孤儿丢弃 —— 工具卡永远停在 running。
			if t.acc.HasOpenToolUse() {
				continue
			}
			t.flushPendingSteers(ctx)
			continue
		case agentruntime.ErrorEvent:
			if e.Err != nil {
				fields := make([]zap.Field, 0, 6)
				fields = append(fields,
					zap.Int64("sessionId", t.sess.ID),
					zap.Int64("assistantMsgId", t.assistantMsg.ID),
					zap.String("stream", t.stream),
				)
				fields = append(fields, chatRuntimeErrorLogFields(e.Err)...)
				logger.Ctx(ctx).Warn("chat_svc.runTurn: ErrorEvent intercepted", fields...)
				t.streamStopErr = e.Err
			}
			continue
		}
		// 推迟中的分段:这一帧不是 tool_result,说明在途 tool_use 的结果根本不走流
		// (AskUserQuestion 这类),不再等 —— 且必须赶在 Apply 之前落地,否则这一帧的
		// 内容会被记进本该收口的旧 assistant。推迟至多一个事件。
		if len(t.pendingSteers) > 0 {
			if _, isToolResult := ev.(agentruntime.ToolResult); !isToolResult {
				t.flushPendingSteers(ctx)
			}
		}
		if err := t.svc.dispatcher.Apply(ctx, ev, t.acc, t.dispEmit, nil, t.turnCtx); err != nil {
			logger.Ctx(ctx).Warn("chat dispatcher Apply failed",
				zap.String("eventType", fmt.Sprintf("%T", ev)),
				zap.Error(err))
		}
		// 在途工具都配上结果了:分段落地,tool_use 与 tool_result 一起留在旧 assistant。
		if len(t.pendingSteers) > 0 && !t.acc.HasOpenToolUse() {
			t.flushPendingSteers(ctx)
		}
		if shouldCheckpointAssistantAfterEvent(ev) {
			t.svc.checkpointAssistantNew(ctx, t.assistantMsg, t.acc)
		}
	}
}

// finalize 收尾本轮:落地残留分段、结算 usage / anchor / 状态,分派终态事件,
// 并在有残留 steer 时递归跑自动接续轮。
func (t *turnRun) finalize(ctx context.Context) {
	// 流结束时仍在推迟的分段必须落地:steer 已经从 inbox drain 走了,不落就丢。
	t.flushPendingSteers(ctx)
	t.turnCtx.ClearWaits()

	if t.req.CollaborationMode == ipc.ModePlan && !t.compact && t.acc.Empty() {
		t.acc.AddText("Plan mode completed without executable changes.")
	}
	if t.compact && t.streamStopErr == nil && !hasCompactBoundaryBlock(t.acc.Snapshot()) {
		if err := t.svc.dispatcher.Apply(ctx, agentruntime.CompactBoundary{Trigger: "manual"}, t.acc, t.dispEmit, nil, t.turnCtx); err != nil {
			logger.Ctx(ctx).Warn("chat compact fallback boundary failed", zap.Error(err))
		}
	}
	finalBlocks := t.acc.Finalize()
	// abort flag 提前到这里取(原在下方 LoadAndDelete) —— 若已 abort,需要在 SetBlocks
	// 之前把仍 running 的 subagent 状态改成 "canceled"。否则 CLI 被 interrupt
	// 后没有 SubagentDone 事件到达,running 会被原样落 DB 让前端 AgentSpawnCard
	// 永远 spin。
	_, aborted := t.svc.aborted.LoadAndDelete(t.sess.ID)
	if aborted {
		handlers.MarkRunningSubagentsCancelled(finalBlocks)
	}
	// 把本会话登记的工具审批 block merge 进 assistant 消息(*ToolApprovalBlock
	// 实现 cago ContentBlock);仍 pending 的在 take 内被标 expired。
	for _, b := range t.svc.takeToolApprovals(t.sess.ID) {
		finalBlocks = append(finalBlocks, b)
	}
	// 未答的 AskUserQuestion 在 turn 结束后会变死卡(runner 已 Close，再提交走
	// ErrNoActiveTurn / 无 waiter 必然失败)。与 MarkRunningSubagentsCancelled /
	// takeToolApprovals 同模式标 expired：落库让 reload 可见，下方 finalCtx 就绪后
	// 对被标记的 block emit 锁定 patch，让在屏活卡不用 reload 立即锁。
	expiredAsks := handlers.MarkUnansweredUserAsksExpired(finalBlocks)

	t.assistantMsg.DurationMs = int(time.Since(t.segmentStart).Milliseconds())
	t.turnCtx.PauseGeneration()
	t.assistantMsg.FirstTokenMs = t.turnCtx.FirstTokenMs()
	stopErr := t.streamStopErr
	var anchorPersistErr error
	if t.result != nil {
		if t.result.Usage != nil {
			t.assistantMsg.PromptTokens = t.result.Usage.PromptTokens
			t.assistantMsg.CachedTokens = t.result.Usage.CachedTokens
			t.assistantMsg.CacheCreationTokens = t.result.Usage.CacheCreationTokens
			// completion / reasoning 由 usage 帧按调用累加；Done 的 usage 是最后一跳，
			// 不能覆盖合计。没有 usage 帧时才用 result 兜底。
			if t.assistantMsg.CompletionTokens == 0 {
				t.assistantMsg.CompletionTokens = t.result.Usage.CompletionTokens
			}
			if t.assistantMsg.ReasoningTokens == 0 {
				t.assistantMsg.ReasoningTokens = t.result.Usage.ReasoningTokens
			}
		}
		// runner 上报的实际模型 id 覆盖创建时的占位值：
		//   - builtin: 与原值相同（都来自解析出的 ModelID）→ 不变
		//   - claudecode CLI login: 创建时 model="" → 这里被填上 system.init.model 真值
		//   - codex CLI login: 同上，填上 Agentre 的 codex 默认模型
		// LoadSession 后续就能用这个字段查 cago catalog 解析 contextWindow。
		if t.result.Model != "" {
			t.assistantMsg.Model = t.result.Model
		}
		if stopErr == nil && t.result.StopErr != nil {
			stopErr = t.svc.mapTurnError(ctx, t.sess, t.be, t.result.StopErr)
			fields := make([]zap.Field, 0, 6)
			fields = append(fields,
				zap.Int64("sessionId", t.sess.ID),
				zap.Int64("assistantMsgId", t.assistantMsg.ID),
				zap.String("stream", t.stream),
			)
			fields = append(fields, chatRuntimeErrorLogFields(stopErr)...)
			logger.Ctx(ctx).Warn("chat_svc.runTurn: stopErr promoted from RunResult.StopErr", fields...)
		}
		// Send 时 sess 之前没有 session id，runner 返回新 id 落库；
		// Regenerate-fork 时 sess 有旧 id 但 runner 返回 fork 出来的新 id，必须覆盖。
		// claudecode resume 同 session 时返回的 id 与原 id 相同，覆盖无副作用。
		if t.result.ProviderSessionID != "" {
			t.sess.SetProviderSession(t.result.ProviderSessionID)
		}
		// Runtime 抽到的本轮 user anchor 必须可靠落库；短暂写失败重试一次，
		// 持续失败则保留已生成回答但把 turn 标成 error，不能伪装成可继续分叉的成功轮。
		if err := t.svc.persistUserAnchor(
			context.WithoutCancel(ctx),
			t.userMsg,
			t.result.UserAnchor,
			t.be.IsPiAgent(),
		); err != nil {
			anchorPersistErr = err
			stopErr = errors.Join(stopErr, err)
		}
		// codex app-server 上报的 modelContextWindow 落到 session 字段，下次
		// LoadSession 用 resolveContextWindowWithRuntime 优先读这个值——比
		// provider 静态配置和 catalog 兜底都准。仅在 runner 真的探到时更新，
		// 否则保留旧值，避免 claudecode / builtin 的 0 把先前 codex 写入的覆盖掉。
		if t.result.ContextWindow > 0 {
			t.sess.ContextWindow = t.result.ContextWindow
		}
		// 会话所选供应商缺失/停用/不兼容、本轮回退 agent 绑定(spec 决策 8,本地):追加
		// 一条持久 notice。必须排在 assistantMsg.Model 被 result.Model 覆盖之后与
		// SetBlocks 之前。
		if t.extras.providerFallbackNotice != nil {
			finalBlocks = append(finalBlocks, *t.extras.providerFallbackNotice)
		}
		// 远端(决策 9):daemon 按 wire 的 effectiveProviderKey 自解失败、回退 agent
		// 绑定后经 ack 回传被回退的 provider_key,这里据此追加同一条持久 notice(与
		// 本地 Q3 一致;provider_key 不清除)。wire 只带 key 不带展示名(远端不在本轮
		// 范围内,见 spec Out of scope),notice 保持只显示 key。
		if t.result.ProviderFallbackKey != "" {
			finalBlocks = append(finalBlocks, blocks.NoticeBlock{
				Level: "info",
				Text:  view.EncodeProviderFallback(t.result.ProviderFallbackKey, ""),
			})
		}
	}
	t.assistantMsg.TokensPerSec = t.turnCtx.TokensPerSec(t.assistantMsg.CompletionTokens)
	_ = t.assistantMsg.SetBlocks(finalBlocks)
	// aborted 已在 acc.Finalize() 之后取出(见上方 MarkRunningSubagentsCancelled 调用)；
	// 这里的判定决定 StreamAborted vs StreamError/Done,以及 abort 路径跳过自动接续。
	awaitingPlanAction := stopErr == nil && !aborted &&
		!t.compact &&
		t.req.CollaborationMode == ipc.ModePlan &&
		hasActionablePlanBlock(finalBlocks)

	if stopErr != nil && (!aborted || anchorPersistErr != nil) {
		t.assistantMsg.ErrorText = stopErr.Error()
	}
	// finalCtx：去掉 cancel 信号但保留 DB 句柄。abort 路径下 turnCtx 已 cancel，
	// 用它写 Update 会静默失败，partial 内容就丢了。
	finalCtx := context.WithoutCancel(ctx)
	_ = chat_repo.Message().Update(finalCtx, t.assistantMsg)

	// 对 finalize 时标 expired 的 AskUserQuestion emit 锁定 patch(形态同
	// UserAskResolvedHandler):前端按 requestId merge,把在屏活卡立即翻到失效态,
	// 无需等下一次 LoadSession 回放持久化的 expired block。
	for _, blk := range expiredAsks {
		t.dispEmit.Emit(finalCtx, t.stream, map[string]any{
			"kind":            "ask_user_question",
			"requestId":       blk.RequestID,
			"askUserQuestion": blk,
		})
	}

	// turn 结束（无错且未 abort）→ 看 runner 还有没有 mid-turn 排进来但 hook 没拉走的
	// 残留 Steer 消息。有的话合并成一条 user msg、emit StreamSteerConsumed、
	// 复用当前 goroutine + 锁递归跑下一轮 —— 替代旧 Stop hook block=continue
	// 把戏（旧路径在 Claude TUI 会渲染成红色 "Stop hook error" 误导文案，
	// 且 hook 自身执行期内到达的新消息会因 stop_hook_active=true 被静默丢掉）。
	// abort 路径：跳过自动接续，让用户自己决定要不要再发。
	var pending []agentruntime.ConsumedSteer
	if stopErr == nil && !aborted {
		if drainer, ok := t.runner.(agentruntime.SteerDrainer); ok {
			pending = nonEmptyConsumedSteers(drainer.DrainPending(finalCtx, t.sess.ID))
		}
	}

	t.sess.LastMessageAt = time.Now().UnixMilli()
	// 即将自动接续的中间态：不要把 session 状态打成 idle，等最终轮收尾再翻。
	if len(pending) == 0 {
		switch {
		case stopErr != nil && (!aborted || anchorPersistErr != nil):
			t.sess.AgentStatus = "error"
			t.sess.NeedsAttention = false
		case awaitingPlanAction:
			t.sess.AgentStatus = "waiting"
			t.sess.NeedsAttention = true
		default:
			t.sess.AgentStatus = "idle"
			// turn 真正结束（含 abort）：清掉 ask/审批待响应留下的 attention 标记，
			// 防止用户在等待期间点「停止」后 sidebar bubble 永远亮着。
			t.sess.NeedsAttention = false
		}
	}
	_ = t.svc.persistSessionStatus(finalCtx, t.sess)
	if aborted || stopErr != nil {
		if t.svc.clearBgRunning(t.sess.ID) {
			t.svc.emitBgRunningStatus(finalCtx, t.sess, t.stream)
		}
	} else {
		t.svc.reconcileBgRunningOnFinalize(finalCtx, t.sess, finalBlocks, t.stream)
	}
	// 诊断: 落库的最终(或自动接续中间态)agent_status。下面那段只在 error/waiting 时
	// emit+log,idle 收尾历史上完全没日志 —— 这正是 agentre.log 里看不到 running→idle
	// 翻转、排查「状态停在 running / 被过期快照盖回 idle」时无从对时间线的原因。这里补一条
	// 覆盖所有终态(含 pending>0 自动接续仍 running 的中间态)。
	logger.Ctx(finalCtx).Info("chat_svc: agent_status finalized",
		zap.Int64("sessionId", t.sess.ID),
		zap.Int64("assistantMsgId", t.assistantMsg.ID),
		zap.String("agentStatus", t.sess.AgentStatus),
		zap.Bool("needsAttention", t.sess.NeedsAttention),
		zap.Bool("aborted", aborted),
		zap.Int("pending", len(pending)))
	// 最后一轮收尾统一先推 session_status，再推 done/error/aborted。前端底部输出由
	// LiveStream 生命周期驱动，tab/toolbar/sidebar 由 session-status-store 驱动；若
	// idle 只靠 done 后异步 reload 回填，两套视图必然存在不一致窗口。
	if len(pending) == 0 {
		fields := make([]zap.Field, 0, 11)
		fields = append(fields,
			zap.Int64("sessionId", t.sess.ID),
			zap.Int64("assistantMsgId", t.assistantMsg.ID),
			zap.String("stream", t.stream),
			zap.String("agentStatus", t.sess.AgentStatus),
			zap.Bool("needsAttention", t.sess.NeedsAttention),
			zap.Bool("aborted", aborted),
			zap.Bool("awaitingPlanAction", awaitingPlanAction),
			zap.String("source", "finalize"),
		)
		fields = append(fields, chatRuntimeErrorLogFields(stopErr)...)
		logger.Ctx(finalCtx).Info("chat_svc.runTurn: session status emit", fields...)
		t.svc.emitter.Emit(finalCtx, t.stream, ChatStreamEvent{
			Kind: StreamSessionStatus,
			SessionStatus: &ChatSessionStatusPatch{
				AgentStatus:    t.sess.AgentStatus,
				NeedsAttention: t.sess.NeedsAttention,
				BgRunning:      t.svc.bgRunningActive(t.sess.ID),
			},
		})
	}

	if len(pending) > 0 {
		nextUser, nextAssistant, payload, perr := t.svc.persistAutoContinueTurn(finalCtx, t.sess, t.be, t.assistantMsg, t.assistantMsg.Model, pending)
		if perr == nil {
			t.svc.emitter.Emit(finalCtx, t.stream, *payload)
			if t.be.IsClaudeCode() || t.be.IsCodex() || t.be.IsPiAgent() {
				t.svc.permissionModes().RefreshForAutoContinue(finalCtx, t.sess)
			}
			// 同 goroutine + 同锁 + 同 stream 名递归跑下一轮：runTurn 内部
			// chatMessageForEvent / StreamDone 会以 nextAssistant 为目标 emit，
			// 前端 store 通过 StreamSteerConsumed.AssistantMessage 已经把活动
			// assistant 切到 nextAssistant。
			// 自动续轮沿用本轮 extras:群成员会话的 MCP 注入 + 群上下文 suffix
			// 需要在同一会话的整个生命周期内保持,而非只在首轮生效。
			t.svc.runTurn(ctx, t.sess, t.a, t.be, t.prov, nextUser, nextAssistant, t.stream, "", false, nil, t.extras)
			return
		}
		// 写新轮失败 → pending 已经从 SteerInbox drain 走，无法回滚，只能丢。
		// 至少 (a) 落日志 + sessionID 方便排查；(b) emit 一个只带 QueuedIDs
		// 的 StreamSteerConsumed 让前端清掉 chip，否则用户看到 chip 永远不消失
		// 但消息没被任何 turn 处理。补 idle 让 list UI 状态回正。
		logger.Default().Error("chat_svc: persist auto-continue turn failed; pending messages lost",
			zap.Int64("sessionId", t.sess.ID),
			zap.Int("pendingCount", len(pending)),
			zap.Error(perr),
		)
		t.svc.emitter.Emit(finalCtx, t.stream, ChatStreamEvent{
			Kind:      StreamSteerConsumed,
			QueuedIDs: consumedSteerIDs(pending),
		})
		t.sess.AgentStatus = "idle"
		t.sess.NeedsAttention = false
		_ = t.svc.persistSessionStatus(finalCtx, t.sess)
		// 上面那条 "agent_status finalized" 打的是自动接续的中间态(仍 running)。这条
		// 恢复分支刚把它改成 idle 并落库,不补一条的话,这次会话在日志里的**最后**一条
		// 终态记录与库里的值正好相反 —— 排「卡 running」时按日志对时间线会看反。
		logger.Ctx(finalCtx).Info("chat_svc: agent_status finalized",
			zap.Int64("sessionId", t.sess.ID),
			zap.Int64("assistantMsgId", t.assistantMsg.ID),
			zap.String("agentStatus", t.sess.AgentStatus),
			zap.Bool("needsAttention", t.sess.NeedsAttention),
			zap.Bool("aborted", aborted),
			zap.Int("pending", 0))
		t.svc.emitter.Emit(finalCtx, t.stream, ChatStreamEvent{
			Kind: StreamSessionStatus,
			SessionStatus: &ChatSessionStatusPatch{
				AgentStatus:    t.sess.AgentStatus,
				NeedsAttention: t.sess.NeedsAttention,
				BgRunning:      t.svc.bgRunningActive(t.sess.ID),
			},
		})
	}

	final := chatMessageForEvent(t.sess, t.assistantMsg)
	switch {
	case anchorPersistErr != nil:
		t.svc.emitter.Emit(finalCtx, t.stream, ChatStreamEvent{
			Kind:    StreamError,
			Error:   stopErr.Error(),
			Message: final,
		})
	case aborted:
		t.svc.emitter.Emit(finalCtx, t.stream, ChatStreamEvent{Kind: StreamAborted, Message: final})
	case stopErr != nil:
		t.svc.emitter.Emit(finalCtx, t.stream, ChatStreamEvent{
			Kind:    StreamError,
			Error:   stopErr.Error(),
			Message: final,
		})
	default:
		t.svc.emitter.Emit(finalCtx, t.stream, ChatStreamEvent{Kind: StreamDone, Message: final})
	}
	// turn 正常收尾(含 abort)的唯一终态回灌点。错误路径走 failTurn 后 return,
	// 自动接续路径在递归 runTurn 的 finalize 回灌(本帧 len(pending)>0 已提前 return)。
	t.svc.publishTurnResult(t.sess.ID, TurnResult{
		SessionID:          t.sess.ID,
		AssistantMessageID: t.assistantMsg.ID,
		Aborted:            aborted,
		Err:                stopErr,
	})
}
