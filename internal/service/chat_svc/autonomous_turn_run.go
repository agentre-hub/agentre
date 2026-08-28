package chat_svc

import (
	"context"
	"fmt"
	"time"

	"github.com/cago-frame/agents/agent/blocks"
	"github.com/cago-frame/cago/database/db"
	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"
	"gorm.io/gorm"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/chat_entity"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
	"github.com/agentre-hub/agentre/internal/repository/chat_repo"
	"github.com/agentre-hub/agentre/internal/service/chat_svc/handlers"
	"github.com/agentre-hub/agentre/internal/service/chat_svc/turn"
)

// autonomousTurnRun 承载 driveAutonomousTurn 一轮自主续轮期间的全部可变状态。
// 字段逐一对应原先散在函数体里的 local。它**刻意不复用 turnRun**:两条路径的收尾
// 语义不同(前台/全量 subagent 翻转、usage 覆盖口径、无 anchor / 无自动接续、多两发
// 终态事件),合并会把历史上踩出来的差异抹平。
type autonomousTurnRun struct {
	svc *chatSvc

	sessionID int64
	be        *agent_backend_entity.AgentBackend
	at        agentruntime.AutonomousTurn
	sess      *chat_entity.Session

	first    agentruntime.Event
	hasFirst bool
	prelude  *agentruntime.UserMessageEvent

	userMsg      *chat_entity.Message
	assistantMsg *chat_entity.Message
	completedRef *CompletedTaskRef
	stream       string

	acc           *turn.Accumulator
	dispEmit      *dispatcherEmitter
	turnCtx       *turn.TurnContext
	segmentStart  time.Time
	pendingSteers []agentruntime.ConsumedSteer
}

// persistTurnMessages 事务建 assistant 行(R18 下先建 user 行)并把会话翻 running。
func (t *autonomousTurnRun) persistTurnMessages(ctx context.Context) error {
	t.assistantMsg = &chat_entity.Message{
		SessionID:         t.sessionID,
		DeviceFingerprint: t.be.DeviceFingerprint,
		Role:              "assistant",
		BlocksJSON:        "[]",
	}
	if t.at.Result != nil && t.at.Result.Model != "" {
		t.assistantMsg.Model = t.at.Result.Model
	}
	return db.Ctx(ctx).Transaction(func(tx *gorm.DB) error {
		txCtx := db.WithContextDB(ctx, tx)
		nextSeq, err := chat_repo.Message().NextSeq(txCtx, t.sessionID)
		if err != nil {
			return err
		}
		// R18:浏览器发起的一轮先落 user 行(seq 在 assistant 之前),转录顺序才正确。
		if t.prelude != nil {
			t.userMsg = &chat_entity.Message{
				SessionID:         t.sessionID,
				DeviceFingerprint: t.be.DeviceFingerprint,
				Role:              "user",
				Seq:               nextSeq,
			}
			if err := t.userMsg.SetBlocks([]blocks.ContentBlock{&blocks.TextBlock{Text: t.prelude.Text}}); err != nil {
				return err
			}
			// R18/R21:来源标识必须**写进落库的 block data**,与 Send / consumed-steer
			// 三条同类路径同一个写点(chat.go 的 persistPeerMessageSource 调用)。只把它
			// 挂在实时事件上的话,刷新 / 重开会话后转录读路径(peerMessageSourceOf)读不
			// 到来源,那行用户消息看起来像本机自己打的字;下游 peer 补齐读同一批字段,
			// 同样拿不到。R22:本机发起(SourceDevice 为空)时这里是 no-op,落库行逐字节
			// 不变。
			if err := persistPeerMessageSource(t.userMsg, peerMessageSource{
				Device: t.prelude.SourceDevice, Name: t.prelude.SourceDeviceName,
			}); err != nil {
				return err
			}
			if err := chat_repo.Message().Create(txCtx, t.userMsg); err != nil {
				return err
			}
			nextSeq++
		}
		t.assistantMsg.Seq = nextSeq
		if err := chat_repo.Message().Create(txCtx, t.assistantMsg); err != nil {
			return err
		}
		t.sess.AgentStatus = "running"
		t.sess.NeedsAttention = false
		t.sess.LastMessageAt = time.Now().UnixMilli()
		return chat_repo.Session().Update(txCtx, t.sess)
	})
}

// emitStarted 经会话级旁路把 per-turn 流名 + 新落的消息行推给前端。
func (t *autonomousTurnRun) emitStarted(ctx context.Context) {
	// 若本自主轮由后台命令完成触发,带上完成任务身份,供前端即时翻转上一条消息里
	// 的 subagent_state 块,并在收尾后落库定向翻转。remote 转发当前不携带 CompletedTask
	// (v1 已知限制),此处对 nil/空 ToolCallID 全程 no-op。
	if t.at.CompletedTask != nil && t.at.CompletedTask.ToolCallID != "" {
		st := t.at.CompletedTask.Status
		if st == "" {
			st = "completed"
		}
		t.completedRef = &CompletedTaskRef{
			ToolCallID: t.at.CompletedTask.ToolCallID,
			Status:     st,
			Summary:    t.at.CompletedTask.Summary,
		}
	}

	t.stream = StreamName(t.sessionID, t.assistantMsg.ID)
	logger.Ctx(ctx).Info("chat_svc: autonomous turn started",
		zap.Int64("sessionId", t.sessionID),
		zap.Int64("assistantMsgId", t.assistantMsg.ID),
		zap.String("trigger", t.at.Trigger))
	// R18:浏览器发起的一轮,把刚落的 user 行随 started 事件带给前端(带来源标识)。
	var userEvents []ChatMessage
	if t.userMsg != nil {
		um, err := toChatMessage(t.userMsg)
		if err != nil {
			logger.Ctx(ctx).Warn("chat_svc: driveAutonomousTurn encode user msg failed",
				zap.Int64("sessionId", t.sessionID), zap.Error(err))
		} else {
			um.SessionID = t.sessionID
			// 来源标识不在这里手动覆盖:toChatMessage 已经从**落库的** block data 里读出
			// 它(R17 同款投影,本机/未知为空前端就不渲染,名字缺失保持空由前端回退指纹
			// R19)。手动覆盖会让实时事件即使在落库丢了来源时也照样正确,把「实时对、刷新
			// 就没了」这类分歧藏起来 —— 实时与重载因此共用同一个数据源。
			userEvents = []ChatMessage{um}
		}
	}
	// 会话级旁路:让前端插入新 assistant 行并 openStream 订阅 per-turn 流。
	t.svc.emitter.Emit(ctx, AutonomousStreamName(t.sessionID), ChatStreamEvent{
		Kind:             StreamAutonomousStarted,
		Stream:           t.stream,
		Trigger:          t.at.Trigger,
		UserMessages:     userEvents,
		AssistantMessage: chatMessageForEvent(t.sess, t.assistantMsg),
		CompletedTask:    t.completedRef,
	})
}

// initSegment 初始化本轮第一个 assistant 分段的累加器与计时口径。
func (t *autonomousTurnRun) initSegment() {
	t.acc = turn.New()
	t.dispEmit = &dispatcherEmitter{svc: t.svc}
	t.turnCtx = t.svc.newTurnContext(t.assistantMsg, t.sess, t.stream, t.be.Type)
	t.segmentStart = time.Now()
}

// flushPendingSteers 把 pendingSteers 落成「收口当前 assistant + 插 user 行 +
// 开新 assistant」,并整体切换 assistantMsg/acc/segmentStart/turnCtx 这四个字段。
// 与 runTurn 共用 persistConsumedSteers,分段语义两条路径同源。
func (t *autonomousTurnRun) flushPendingSteers(ctx context.Context) {
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
		logger.Ctx(ctx).Warn("chat_svc: autonomous steer segmentation failed",
			zap.Int64("sessionId", t.sessionID),
			zap.Int64("assistantMsgId", t.assistantMsg.ID),
			zap.Error(perr))
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

// consumeSteer 认领 SteerConsumed 并报告「这条事件已处理,不要进 dispatcher」。
// 分段不走 dispatcher —— 它是 assistantMsg/acc/segmentStart/turnCtx 这四个字段的
// 整体替换,handler 接口表达不了(与 runTurn 的 switch 同一个理由)。
func (t *autonomousTurnRun) consumeSteer(ctx context.Context, ev agentruntime.Event) bool {
	sc, ok := ev.(agentruntime.SteerConsumed)
	if !ok {
		return false
	}
	t.pendingSteers = append(t.pendingSteers, sc.Steers...)
	// 工具在途时先不分段:claudecode 的 PostToolUse hook 在 CLI 写出 tool_result
	// 帧**之前**就 drain 走排队消息,SteerConsumed 因此会先于同一个工具的
	// ToolResult 到达。此刻收口 assistant 会把 tool_use 冻在旧消息里,随后的
	// tool_result 在新 accumulator 里查不到 tool_use,被当孤儿丢弃 —— 工具卡
	// 永远停在 running。
	if !t.acc.HasOpenToolUse() {
		t.flushPendingSteers(ctx)
	}
	return true
}

// consumeEvents 消费本轮事件流直到 channel 关闭(首条已被上游先读走)。
func (t *autonomousTurnRun) consumeEvents(ctx context.Context) {
	// The first event can be the persisted user-message prelude; it is still a
	// canonical event for remote peers even though the local reducer does not
	// add it to assistant blocks.
	if t.hasFirst {
		t.svc.publishPeerEvent(t.sessionID, t.first)
	}
	// 首条若已是标记之外的普通事件,它仍要进 dispatcher(用户消息不进 assistant 内容)。
	if t.hasFirst && t.prelude == nil && !t.consumeSteer(ctx, t.first) {
		if err := t.svc.dispatcher.Apply(ctx, t.first, t.acc, t.dispEmit, nil, t.turnCtx); err != nil {
			logger.Ctx(ctx).Warn("chat_svc: autonomous dispatcher Apply failed",
				zap.String("eventType", fmt.Sprintf("%T", t.first)), zap.Error(err))
		}
		if shouldCheckpointAssistantAfterEvent(t.first) {
			t.svc.checkpointAssistantNew(ctx, t.assistantMsg, t.acc)
		}
	}
	for ev := range t.at.Events {
		t.svc.publishPeerEvent(t.sessionID, ev)
		if t.consumeSteer(ctx, ev) {
			continue
		}
		if err := t.svc.dispatcher.Apply(ctx, ev, t.acc, t.dispEmit, nil, t.turnCtx); err != nil {
			logger.Ctx(ctx).Warn("chat_svc: autonomous dispatcher Apply failed",
				zap.String("eventType", fmt.Sprintf("%T", ev)), zap.Error(err))
		}
		// 上一条事件收口了工具 → 之前推迟的分段现在可以落地。
		if len(t.pendingSteers) > 0 && !t.acc.HasOpenToolUse() {
			t.flushPendingSteers(ctx)
		}
		if shouldCheckpointAssistantAfterEvent(ev) {
			t.svc.checkpointAssistantNew(ctx, t.assistantMsg, t.acc)
		}
	}
	// 流结束时仍在推迟的分段必须落地:插话已经从 inbox drain 走了,不落就丢。
	t.flushPendingSteers(ctx)
}

// finalize 收尾本轮:落 blocks + usage/model、翻终态、定向翻转后台任务、发终态事件。
func (t *autonomousTurnRun) finalize(ctx context.Context) {
	finalBlocks := t.acc.Finalize()
	// 镜像 Send 路径(chat.go):本自主轮结束时仍 running 的 subagent(没等到
	// SubagentDone,如轮被中断)翻成 "canceled",否则原样落 DB 让前端后台任务芯片
	// 永远 spin。只动本轮 finalBlocks,不碰更早消息里的后台 bash 块(那条由
	// FlipSubagentStatus 定向翻转)。
	//
	// 但「本轮结束」不等于「任务结束」:轮被截断(StopErr)时 CLI 已经不在了,谁都
	// 等不到 SubagentDone,全翻;正常收尾时后台任务本就活过这一轮 —— runtime 随后
	// 会为它另开旁路活动轮继续收帧 —— 只能翻前台的,否则卡片显示「已停止」而任务
	// 还在跑(sess-3275)。
	if t.at.Result != nil && t.at.Result.StopErr != nil {
		handlers.MarkRunningSubagentsCancelled(finalBlocks)
	} else {
		handlers.MarkRunningForegroundSubagentsCancelled(t.acc, finalBlocks)
	}
	_ = t.assistantMsg.SetBlocks(finalBlocks)
	if t.at.Result != nil {
		if t.at.Result.Usage != nil {
			t.assistantMsg.PromptTokens = t.at.Result.Usage.PromptTokens
			t.assistantMsg.CompletionTokens = t.at.Result.Usage.CompletionTokens
			t.assistantMsg.CachedTokens = t.at.Result.Usage.CachedTokens
			t.assistantMsg.CacheCreationTokens = t.at.Result.Usage.CacheCreationTokens
			t.assistantMsg.ReasoningTokens = t.at.Result.Usage.ReasoningTokens
		}
		if t.at.Result.Model != "" {
			t.assistantMsg.Model = t.at.Result.Model
		}
		if t.at.Result.ProviderSessionID != "" {
			t.sess.SetProviderSession(t.at.Result.ProviderSessionID)
		}
	}
	// finalCtx 去掉 cancel 信号但保留 DB 句柄 —— 已经流出去的内容必须落库。
	finalCtx := context.WithoutCancel(ctx)
	// 这一轮是被截断的(远端断连 / 会话在那台 daemon 上已中断)时,StopErr 带着终止理由。
	// 不看它就等于把一条**半截**的回答按「正常跑完」落库:errorText 空、会话翻 idle、
	// emit StreamDone —— 用户看到一条戛然而止却「成功」的回答,分不出发生了什么。
	// 文案由 mapTurnError 统一给(与用户发起的那条轮次同一套),这里只负责落成终态。
	var stopErr error
	if t.at.Result != nil && t.at.Result.StopErr != nil {
		stopErr = t.svc.mapTurnError(finalCtx, t.sess, t.be, t.at.Result.StopErr)
		t.assistantMsg.ErrorText = stopErr.Error()
		logger.Ctx(finalCtx).Warn("chat_svc: autonomous turn terminated",
			zap.Int64("sessionId", t.sessionID),
			zap.Int64("assistantMsgId", t.assistantMsg.ID),
			zap.Error(t.at.Result.StopErr))
	}
	_ = chat_repo.Message().Update(finalCtx, t.assistantMsg)

	t.sess.AgentStatus = "idle"
	if stopErr != nil {
		t.sess.AgentStatus = "error"
	}
	t.sess.NeedsAttention = false
	t.sess.LastMessageAt = time.Now().UnixMilli()
	_ = t.svc.persistSessionStatus(finalCtx, t.sess)
	logger.Ctx(finalCtx).Info("chat_svc: autonomous turn finalized",
		zap.Int64("sessionId", t.sessionID),
		zap.Int64("assistantMsgId", t.assistantMsg.ID),
		zap.String("agentStatus", t.sess.AgentStatus))

	// 后台命令在本自主轮才完成:它发起的 subagent_state 块住在更早的消息里,过不了
	// per-turn accumulator,只能定向重写持久化态。completedRef 为 nil(含 remote
	// 不携带 CompletedTask 的情形)时跳过。
	if t.completedRef != nil {
		if err := chat_repo.Message().FlipSubagentStatus(finalCtx, t.sessionID, t.completedRef.ToolCallID, t.completedRef.Status, t.completedRef.Summary); err != nil {
			logger.Ctx(finalCtx).Warn("chat_svc.driveAutonomousTurn: FlipSubagentStatus failed",
				zap.Int64("sessionId", t.sessionID),
				zap.String("toolUseId", t.completedRef.ToolCallID),
				zap.Error(err))
		}
		t.svc.reconcileBgRunningOnComplete(finalCtx, t.sess, t.completedRef.ToolCallID, t.stream)
	}

	final := chatMessageForEvent(t.sess, t.assistantMsg)
	t.svc.emitter.Emit(finalCtx, t.stream, ChatStreamEvent{
		Kind: StreamSessionStatus,
		SessionStatus: &ChatSessionStatusPatch{
			AgentStatus:    t.sess.AgentStatus,
			NeedsAttention: t.sess.NeedsAttention,
			BgRunning:      t.svc.bgRunningActive(t.sess.ID),
		},
	})
	if stopErr != nil {
		t.svc.emitter.Emit(finalCtx, t.stream, ChatStreamEvent{
			Kind: StreamError, Error: stopErr.Error(), Message: final,
		})
	} else {
		t.svc.emitter.Emit(finalCtx, t.stream, ChatStreamEvent{Kind: StreamDone, Message: final})
	}
	t.svc.emitter.Emit(finalCtx, t.stream, ChatStreamEvent{Kind: StreamClosed})
	// 会话级流补发终态兜底:StreamAutonomousStarted 是前端拿 per-turn 流名的唯一入口,
	// 前端收到才 openStream、ChatStreamsHost 才在下一 render EventsOn 订阅。若本轮很短,
	// 上面的 per-turn StreamDone 可能赶在订阅注册前发完 → 前端漏终态 → 该
	// LiveStream 永远留在 store → streaming 卡死。会话级流由 ChatPanel 挂载即订阅、常驻,
	// 先于本轮,补一发让前端据 LaunchMessageID 兜底 finishStream(幂等)。见 StreamAutonomousFinished。
	t.svc.emitter.Emit(finalCtx, AutonomousStreamName(t.sessionID), ChatStreamEvent{
		Kind:            StreamAutonomousFinished,
		LaunchMessageID: t.assistantMsg.ID,
	})
}
