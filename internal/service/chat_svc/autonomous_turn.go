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

	"github.com/agentre-ai/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-ai/agentre/internal/model/entity/chat_entity"
	"github.com/agentre-ai/agentre/internal/pkg/agentruntime"
	"github.com/agentre-ai/agentre/internal/repository/chat_repo"
	"github.com/agentre-ai/agentre/internal/service/chat_svc/handlers"
	"github.com/agentre-ai/agentre/internal/service/chat_svc/turn"
)

// startAutonomousWatcher 为某 claudecode 会话惰性启动一个 watcher goroutine,订阅
// runtime 的「自主续轮」(CLI 在 run_in_background 任务完成后**自主**跑的一轮),
// 逐轮落成纯 assistant 轮。每会话只起一个(autoWatchers 去重);底层 AutonomousTurns
// channel 在子进程 evict / CloseSession 时 close,watcher 随之退出并清去重位。
//
// 并发约束(关键):watcher 在 driveAutonomousTurn 里 **绝不持 chat 会话锁** drain。
// 否则与 pkg/claudecode.Session 常驻 reader 死锁 —— evOut 不被 drain → Session 活跃
// 槽位不释放 → 用户 turn 卡在 Session.turnMu 上(且它持着 chat 锁)→ watcher 永远拿
// 不到锁。自主轮与用户 turn 的串行由底层 Session 单活跃槽位天然保证(FIFO);跨 turn
// 的 session 行写按 last-write-wins,极少数重叠(用户在自主轮进行中又发消息)靠
// 前端 StreamDone→reloadSession 收敛。
func (s *chatSvc) startAutonomousWatcher(sessionID int64, be *agent_backend_entity.AgentBackend, src agentruntime.AutonomousTurnSource) {
	if sessionID <= 0 || be == nil || src == nil {
		return
	}
	if _, loaded := s.autoWatchers.LoadOrStore(sessionID, struct{}{}); loaded {
		return
	}
	beCopy := *be
	go func() {
		defer s.autoWatchers.Delete(sessionID)
		for at := range src.AutonomousTurns(sessionID) {
			s.driveAutonomousTurn(context.Background(), sessionID, &beCopy, at)
		}
	}()
}

// driveAutonomousTurn 把一轮自主续轮落成 **纯 assistant 消息(无 user 行)**:
//  1. 加载 session(取最新状态);
//  2. 事务建 assistant 消息(seq 续在末尾)+ 翻 running;
//  3. 经会话级旁路 emit StreamAutonomousStarted —— per-turn 流只有用户 Send 才有入口,
//     自主轮没有,所以把 stream 名 + 新 assistant 行推给前端,让它插入并 openStream;
//  4. 用 dispatcher drain at.Events(实时 stream chunk / tool / plan ...);
//  5. 收尾:落 blocks + usage/model、翻 idle、emit StreamDone。
//
// R18 例外:浏览器在一条**空闲**会话上「开新一轮」跑起的一轮,daemon 在事件流开头注入
// 一条 user_message 标记(带发起方设备身份)。它不是自主轮 —— 它在桌面端必须落成
// **一行用户消息 + 一行 assistant**,用户消息带来源标识;若把标记当普通事件跳过,
// 这一轮就会退化成「没有提问的回复」,与真·自主续轮在界面上同形。标记从事件流开头
// 剥出(先读首条),事务里先建 user 行再建 assistant 行,emit 时随 StreamAutonomousStarted
// 一起推给前端。
//
// 任何一步加载/落库失败 → log + 把 at.Events 抽干(别让 Session reader 阻塞)+ 返回。
func (s *chatSvc) driveAutonomousTurn(ctx context.Context, sessionID int64, be *agent_backend_entity.AgentBackend, at agentruntime.AutonomousTurn) {
	sess, err := chat_repo.Session().Find(ctx, sessionID)
	if err != nil || sess == nil {
		logger.Ctx(ctx).Warn("chat_svc: driveAutonomousTurn load session failed; draining events",
			zap.Int64("sessionId", sessionID), zap.Error(err))
		drainAndDiscard(at.Events)
		return
	}

	// 先读首条事件:是 user_message 标记就把它剥出来,在事务里先落一行 user 消息。
	first, hasFirst := <-at.Events
	var prelude *agentruntime.UserMessageEvent
	if hasFirst {
		if um, ok := first.(agentruntime.UserMessageEvent); ok {
			prelude = &um
		}
	}

	assistantMsg := &chat_entity.Message{
		SessionID:  sessionID,
		DeviceID:   be.DeviceID,
		Role:       "assistant",
		BlocksJSON: "[]",
	}
	if at.Result != nil && at.Result.Model != "" {
		assistantMsg.Model = at.Result.Model
	}
	var userMsg *chat_entity.Message
	if err := db.Ctx(ctx).Transaction(func(tx *gorm.DB) error {
		txCtx := db.WithContextDB(ctx, tx)
		nextSeq, err := chat_repo.Message().NextSeq(txCtx, sessionID)
		if err != nil {
			return err
		}
		// R18:浏览器发起的一轮先落 user 行(seq 在 assistant 之前),转录顺序才正确。
		if prelude != nil {
			userMsg = &chat_entity.Message{
				SessionID: sessionID,
				DeviceID:  be.DeviceID,
				Role:      "user",
				Seq:       nextSeq,
			}
			if err := userMsg.SetBlocks([]blocks.ContentBlock{&blocks.TextBlock{Text: prelude.Text}}); err != nil {
				return err
			}
			// R18/R21:来源标识必须**写进落库的 block data**,与 Send / consumed-steer
			// 三条同类路径同一个写点(chat.go 的 persistPeerMessageSource 调用)。只把它
			// 挂在实时事件上的话,刷新 / 重开会话后转录读路径(peerMessageSourceOf)读不
			// 到来源,那行用户消息看起来像本机自己打的字;下游 peer 补齐读同一批字段,
			// 同样拿不到。R22:本机发起(SourceDevice 为空)时这里是 no-op,落库行逐字节
			// 不变。
			if err := persistPeerMessageSource(userMsg, peerMessageSource{
				Device: prelude.SourceDevice, Name: prelude.SourceDeviceName,
			}); err != nil {
				return err
			}
			if err := chat_repo.Message().Create(txCtx, userMsg); err != nil {
				return err
			}
			nextSeq++
		}
		assistantMsg.Seq = nextSeq
		if err := chat_repo.Message().Create(txCtx, assistantMsg); err != nil {
			return err
		}
		sess.AgentStatus = "running"
		sess.NeedsAttention = false
		sess.LastMessageAt = time.Now().UnixMilli()
		return chat_repo.Session().Update(txCtx, sess)
	}); err != nil {
		logger.Ctx(ctx).Error("chat_svc: driveAutonomousTurn persist assistant failed",
			zap.Int64("sessionId", sessionID), zap.Error(err))
		s.failAutonomousTurnPersist(ctx, sessionID, sess, be, err, at.Events, at.TurnToken)
		return
	}

	// 若本自主轮由后台命令完成触发,带上完成任务身份,供前端即时翻转上一条消息里
	// 的 subagent_state 块,并在收尾后落库定向翻转。remote 转发当前不携带 CompletedTask
	// (v1 已知限制),此处对 nil/空 ToolUseID 全程 no-op。
	var completedRef *CompletedTaskRef
	if at.CompletedTask != nil && at.CompletedTask.ToolUseID != "" {
		st := at.CompletedTask.Status
		if st == "" {
			st = "completed"
		}
		completedRef = &CompletedTaskRef{
			ToolUseID: at.CompletedTask.ToolUseID,
			Status:    st,
			Summary:   at.CompletedTask.Summary,
		}
	}

	stream := StreamName(sessionID, assistantMsg.ID)
	logger.Ctx(ctx).Info("chat_svc: autonomous turn started",
		zap.Int64("sessionId", sessionID),
		zap.Int64("assistantMsgId", assistantMsg.ID),
		zap.String("trigger", at.Trigger))
	// R18:浏览器发起的一轮,把刚落的 user 行随 started 事件带给前端(带来源标识)。
	var userEvents []ChatMessage
	if userMsg != nil {
		um, err := toChatMessage(userMsg)
		if err != nil {
			logger.Ctx(ctx).Warn("chat_svc: driveAutonomousTurn encode user msg failed",
				zap.Int64("sessionId", sessionID), zap.Error(err))
		} else {
			um.SessionID = sessionID
			// 来源标识不在这里手动覆盖:toChatMessage 已经从**落库的** block data 里读出
			// 它(R17 同款投影,本机/未知为空前端就不渲染,名字缺失保持空由前端回退指纹
			// R19)。手动覆盖会让实时事件即使在落库丢了来源时也照样正确,把「实时对、刷新
			// 就没了」这类分歧藏起来 —— 实时与重载因此共用同一个数据源。
			userEvents = []ChatMessage{um}
		}
	}
	// 会话级旁路:让前端插入新 assistant 行并 openStream 订阅 per-turn 流。
	s.emitter.Emit(ctx, AutonomousStreamName(sessionID), ChatStreamEvent{
		Kind:             StreamAutonomousStarted,
		Stream:           stream,
		Trigger:          at.Trigger,
		UserMessages:     userEvents,
		AssistantMessage: chatMessageForEvent(sess, assistantMsg),
		CompletedTask:    completedRef,
	})

	acc := turn.New()
	dispEmit := &dispatcherEmitter{svc: s}
	turnCtx := s.newTurnContext(assistantMsg, sess, stream, be.Type)
	// The first event can be the persisted user-message prelude; it is still a
	// canonical event for remote peers even though the local reducer does not
	// add it to assistant blocks.
	if hasFirst {
		s.publishPeerEvent(sessionID, first)
	}
	// 首条若已是标记之外的普通事件,它仍要进 dispatcher(用户消息不进 assistant 内容)。
	if hasFirst && prelude == nil {
		if err := s.dispatcher.Apply(ctx, first, acc, dispEmit, nil, turnCtx); err != nil {
			logger.Ctx(ctx).Warn("chat_svc: autonomous dispatcher Apply failed",
				zap.String("eventType", fmt.Sprintf("%T", first)), zap.Error(err))
		}
		if shouldCheckpointAssistantAfterEvent(first) {
			s.checkpointAssistantNew(ctx, assistantMsg, acc)
		}
	}
	for ev := range at.Events {
		s.publishPeerEvent(sessionID, ev)
		if err := s.dispatcher.Apply(ctx, ev, acc, dispEmit, nil, turnCtx); err != nil {
			logger.Ctx(ctx).Warn("chat_svc: autonomous dispatcher Apply failed",
				zap.String("eventType", fmt.Sprintf("%T", ev)), zap.Error(err))
		}
		if shouldCheckpointAssistantAfterEvent(ev) {
			s.checkpointAssistantNew(ctx, assistantMsg, acc)
		}
	}

	finalBlocks := acc.Finalize()
	// 镜像 Send 路径(chat.go):本自主轮结束时仍 running 的 subagent(没等到
	// SubagentDone,如轮被中断)翻成 "canceled",否则原样落 DB 让前端后台任务芯片
	// 永远 spin。只动本轮 finalBlocks,不碰更早消息里的后台 bash 块(那条由
	// FlipSubagentStatus 定向翻转)。
	handlers.MarkRunningSubagentsCancelled(finalBlocks)
	_ = assistantMsg.SetBlocks(finalBlocks)
	if at.Result != nil {
		if at.Result.Usage != nil {
			assistantMsg.PromptTokens = at.Result.Usage.PromptTokens
			assistantMsg.CompletionTokens = at.Result.Usage.CompletionTokens
			assistantMsg.CachedTokens = at.Result.Usage.CachedTokens
			assistantMsg.CacheCreationTokens = at.Result.Usage.CacheCreationTokens
			assistantMsg.ReasoningTokens = at.Result.Usage.ReasoningTokens
		}
		if at.Result.Model != "" {
			assistantMsg.Model = at.Result.Model
		}
		if at.Result.ProviderSessionID != "" {
			sess.SetProviderSession(at.Result.ProviderSessionID)
		}
	}
	// finalCtx 去掉 cancel 信号但保留 DB 句柄 —— 已经流出去的内容必须落库。
	finalCtx := context.WithoutCancel(ctx)
	// 这一轮是被截断的(远端断连 / 会话在那台 daemon 上已中断)时,StopErr 带着终止理由。
	// 不看它就等于把一条**半截**的回答按「正常跑完」落库:errorText 空、会话翻 idle、
	// emit StreamDone —— 用户看到一条戛然而止却「成功」的回答,分不出发生了什么。
	// 文案由 mapTurnError 统一给(与用户发起的那条轮次同一套),这里只负责落成终态。
	var stopErr error
	if at.Result != nil && at.Result.StopErr != nil {
		stopErr = s.mapTurnError(finalCtx, sess, be, at.Result.StopErr)
		assistantMsg.ErrorText = stopErr.Error()
		logger.Ctx(finalCtx).Warn("chat_svc: autonomous turn terminated",
			zap.Int64("sessionId", sessionID),
			zap.Int64("assistantMsgId", assistantMsg.ID),
			zap.Error(at.Result.StopErr))
	}
	_ = chat_repo.Message().Update(finalCtx, assistantMsg)

	sess.AgentStatus = "idle"
	if stopErr != nil {
		sess.AgentStatus = "error"
	}
	sess.NeedsAttention = false
	sess.LastMessageAt = time.Now().UnixMilli()
	_ = s.persistSessionStatus(finalCtx, sess)
	logger.Ctx(finalCtx).Info("chat_svc: autonomous turn finalized",
		zap.Int64("sessionId", sessionID),
		zap.Int64("assistantMsgId", assistantMsg.ID),
		zap.String("agentStatus", sess.AgentStatus))

	// 后台命令在本自主轮才完成:它发起的 subagent_state 块住在更早的消息里,过不了
	// per-turn accumulator,只能定向重写持久化态。completedRef 为 nil(含 remote
	// 不携带 CompletedTask 的情形)时跳过。
	if completedRef != nil {
		if err := chat_repo.Message().FlipSubagentStatus(finalCtx, sessionID, completedRef.ToolUseID, completedRef.Status, completedRef.Summary); err != nil {
			logger.Ctx(finalCtx).Warn("chat_svc.driveAutonomousTurn: FlipSubagentStatus failed",
				zap.Int64("sessionId", sessionID),
				zap.String("toolUseId", completedRef.ToolUseID),
				zap.Error(err))
		}
		s.reconcileBgRunningOnComplete(finalCtx, sess, completedRef.ToolUseID, stream)
	}

	final := chatMessageForEvent(sess, assistantMsg)
	s.emitter.Emit(finalCtx, stream, ChatStreamEvent{
		Kind: StreamSessionStatus,
		SessionStatus: &ChatSessionStatusPatch{
			AgentStatus:    sess.AgentStatus,
			NeedsAttention: sess.NeedsAttention,
			BgRunning:      s.bgRunningActive(sess.ID),
		},
	})
	if stopErr != nil {
		s.emitter.Emit(finalCtx, stream, ChatStreamEvent{
			Kind: StreamError, Error: stopErr.Error(), Message: final,
		})
	} else {
		s.emitter.Emit(finalCtx, stream, ChatStreamEvent{Kind: StreamDone, Message: final})
	}
	s.emitter.Emit(finalCtx, stream, ChatStreamEvent{Kind: StreamClosed})
	// 会话级流补发终态兜底:StreamAutonomousStarted 是前端拿 per-turn 流名的唯一入口,
	// 前端收到才 openStream、ChatStreamsHost 才在下一 render EventsOn 订阅。若本轮很短,
	// 上面的 per-turn StreamDone 可能赶在订阅注册前发完 → 前端漏终态 → 该
	// LiveStream 永远留在 store → streaming 卡死。会话级流由 ChatPanel 挂载即订阅、常驻,
	// 先于本轮,补一发让前端据 LaunchMessageID 兜底 finishStream(幂等)。见 StreamAutonomousFinished。
	s.emitter.Emit(finalCtx, AutonomousStreamName(sessionID), ChatStreamEvent{
		Kind:            StreamAutonomousFinished,
		LaunchMessageID: assistantMsg.ID,
	})
}

// failAutonomousTurnPersist 处理"新建 assistant 消息的事务最终失败"这一分支
// (design decisions 6/7/9 + spec"自主续轮落库失败时的可观察结果"),依次产出四个
// 互相独立的可观察结果:
//  1. 会话翻 error 并持久化。失败的是消息写入事务;会话状态写入是独立一次写,
//     允许其独立成功。若这次写也失败,记录日志后继续后续步骤,不再重试
//     (应用层重试已被本轮决策 1 拒绝,重试属于驱动层 busy handler 的职责)。
//  2. 经会话级流(AutonomousStreamName)推一条错误事件,不依赖数据库就能让前端
//     看到这一轮出错;文案复用既有 mapTurnError,与用户发起的轮次一致。
//  3. 主动中断 CLI 当前这一轮,让子进程解除等待(与 Stop 遗孤路径共用
//     chat.go 的 requestRuntimeAbort)。selectRunner / Abort 失败
//     (含子进程已消失,即 ErrNoActiveTurn)只记日志,不影响前两步已产生的结果。
//     **必须异步发出**:Abort → Session.Interrupt 写完 control_request 后要阻塞等
//     CLI 的 control_response,而那条回执只能由常驻 readLoop 派发,readLoop 又停在
//     feed(at.ch <- ev) 上等本轮事件被消费 —— 也就是等第 4 步。同步调用会让 3 和 4
//     互相等着,watcher goroutine 永久卡死(ctx 是 context.Background(),没有
//     deadline),比修复前的静默丢弃更糟:连事件流都不再被抽干。异步发出后第 4 步
//     得以进行,回执随之到达,中断真正完成 —— 这正是 Hard invariant 说的
//     「在 drain 之外**或以非阻塞方式进行**」。
//  4. Hard invariant:抽干 at.Events —— 出口 channel 无人 drain 会导致 Session 活跃
//     槽位不释放,后续用户 turn 全部卡死。排在 1-3 发起之后。
//
// 四步互相独立,任何一步失败都不影响其余步骤,也不 panic、不阻塞 watcher goroutine
// (spec:「失败处置本身不得抛出或阻塞 watcher goroutine」)。
func (s *chatSvc) failAutonomousTurnPersist(
	ctx context.Context,
	sessionID int64,
	sess *chat_entity.Session,
	be *agent_backend_entity.AgentBackend,
	persistErr error,
	events <-chan agentruntime.Event,
	turnToken uint64,
) {
	// 1. 会话翻 error 并持久化;这次写失败只记日志,不重试。
	sess.AgentStatus = "error"
	sess.NeedsAttention = false
	if err := chat_repo.Session().Update(ctx, sess); err != nil {
		logger.Ctx(ctx).Error("chat_svc.failAutonomousTurnPersist: session status persist failed",
			zap.Int64("sessionId", sessionID), zap.Error(err))
	}

	// 2. 经会话级流推错误事件,不依赖数据库;文案复用 mapTurnError。
	mappedErr := s.mapTurnError(ctx, sess, be, persistErr)
	s.emitter.Emit(ctx, AutonomousStreamName(sessionID), ChatStreamEvent{
		Kind:  StreamError,
		Error: mappedErr.Error(),
	})

	// 3. 主动中断 CLI 当前这一轮 —— 异步发出,让第 4 步的抽干得以进行(见函数注释:
	// 中断要等的回执反过来依赖抽干)。走与 Stop 遗孤路径同一个 requestRuntimeAbort:
	// selectRunner / Abort 失败(含子进程已消失)在那里只记日志,不影响前两步已经产
	// 生的结果,这里的布尔判据也就无人可报,直接丢弃。
	//
	// turnToken 携带本轮的身份(决策 1):异步中断到达时若这一轮仍是当前活跃轮就中断,
	// 否则 stale no-op —— drain 完成后即使新轮已起,迟到的中断也不会杀掉新轮。
	go func() { _, _ = s.requestRuntimeAbort(ctx, be, sessionID, turnToken) }()

	// 4. Hard invariant:抽干事件 channel,别让 Session reader 阻塞。
	drainAndDiscard(events)
}

// drainAndDiscard 把事件 channel 抽干丢弃。关键不是丢内容,而是别让底层
// Session reader 因为出口 channel 没人 drain 而阻塞(活跃槽位不释放 → 后续用户
// turn 卡死)。失败路径用它兜底。
func drainAndDiscard(events <-chan agentruntime.Event) {
	for range events { //nolint:revive // 故意抽干丢弃
	}
}
