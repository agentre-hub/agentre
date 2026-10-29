package chat_svc

import (
	"context"
	"encoding/json"
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
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/runtimes/remote"
	"github.com/agentre-hub/agentre/internal/pkg/transcript/handlers"
	"github.com/agentre-hub/agentre/internal/pkg/transcript/turn"
	"github.com/agentre-hub/agentre/internal/repository/chat_repo"
	"github.com/agentre-hub/agentre/internal/repository/transcript_repo"
)

// autonomousTurnRun 承载 driveAutonomousTurn 一轮自主续轮期间的全部可变状态。
// 它**刻意不复用 turnRun**:两条路径的收尾
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

	// previews 是远端执行那一路的**预览帧**流(见 preview_stream.go)。自主续轮同样
	// 是一条运行中的轮次:宿主为它发的逐 token 增量是预览帧(agentred 在
	// forwardAutonomousTurn 里显式打 Preview),而 at.Events 上到达的是宿主实时发布
	// 的持久帧。非 nil 时这一轮分工:预览帧负责呈现,持久帧负责转录。本机执行时它是
	// nil,一条流两件事都干,与今天逐字一致。
	previews <-chan agentruntime.Event
	// previewAcc 是预览帧用完即弃的累加器:呈现要的那点上下文攒在它里面,它不落库。
	previewAcc *turn.Accumulator
	// durableCtx 是持久帧那一路自己的轮上下文。理由同 turnRun.durableCtx:持久帧只
	// 累积、不产生宿主副作用,拿同一个 turnCtx 再走一遍会把已经答完的待决策挂回去。
	durableCtx *turn.TurnContext
	// cards 持久帧那一路的子代理卡帧路由,见 durableCards。
	cards *durableCards

	// lazy 这一轮等第一次真正有内容才落行(开轮时定下,见 opensLazily)。
	lazy bool
	// unopened 懒开的这一轮还没落行:没有消息行、会话没翻 running、前端没收到开轮。
	// 第一帧真正给本轮添块的帧到来时落行并清掉它(openOnContent)。
	unopened bool
	// openFailed 懒开落行失败:失败处置已做完(会话翻 error、事件流已抽干),此后的帧一律丢弃。
	openFailed bool
}

// opensLazily 报这一轮要不要等到第一次有内容才落行。
//
// 只有补齐轮(TriggerCatchUp)这样做:远端父轮结束后 agentred 改写原消息里那张 agrctl 卡,
// 变化的块以新 seq 的持久帧发来,桌面端没有在飞的轮就开出一条补齐轮 —— 这些帧全被写回
// 更早那条消息(durableCards),这一轮自己什么都没有。先落行就是一行空 assistant、一条
// 「补齐」分隔、一个停在 running 的会话(r2 报告 R5)。懒开靠持久帧那一路「先累积、后落行」
// 不产生宿主副作用,所以只在两级帧那一路(previews 非 nil;补齐只发生在远端执行)成立。
//
// 其余照旧立刻落行:真·自主续轮(background_task 等)是 CLI 真跑的一轮;带发起方标记的
// 那一轮要先落用户行(R18);本地有派发时建下、还在飞的那一行时,补齐要立刻认领它
// (adoptInFlightAssistant)。判不出有没有可认领的那一行时也照旧,失败处置归 open。
func (t *autonomousTurnRun) opensLazily(ctx context.Context) bool {
	if t.at.Trigger != remote.TriggerCatchUp || t.prelude != nil || t.previews == nil {
		return false
	}
	latest, err := transcript_repo.Message().LatestAssistant(ctx, t.sessionID)
	if err != nil {
		return false
	}
	return !assistantRowStillInFlight(latest)
}

// open 落这一轮的消息行、翻 running、告诉前端开了一轮,并起第一个分段。交回 false 表示
// 落库失败:失败处置(会话翻 error、推错误事件、中断、抽干事件流)已经做完。
func (t *autonomousTurnRun) open(ctx context.Context) bool {
	if err := t.persistTurnMessages(ctx); err != nil {
		logger.Ctx(ctx).Error("chat_svc: driveAutonomousTurn persist assistant failed",
			zap.Int64("sessionId", t.sessionID), zap.Error(err))
		t.openFailed = true
		t.svc.failAutonomousTurnPersist(ctx, t.sessionID, t.sess, t.be, err, t.at.Events, t.at.TurnToken)
		return false
	}
	t.unopened = false
	t.emitStarted(ctx)
	t.initSegment()
	if t.cards != nil {
		t.cards.liveStream = t.stream
	}
	return true
}

// openOnContent 交回这一轮此刻有没有行可写。懒开的一轮还没落行时,hasContent 为真才落行。
func (t *autonomousTurnRun) openOnContent(ctx context.Context, hasContent bool) bool {
	if !t.unopened {
		return true
	}
	if t.openFailed || !hasContent {
		return false
	}
	return t.open(ctx)
}

// addsBlock 报一帧单独落进一只空累加器会不会长出块。懒开的一轮还没落行时,预览帧靠它判断
// 「这一轮开始有内容了」:此刻呈现那一路的累加器本来就是空的,判断与真落进去一致。一次性
// 累加器配丢弃发射器,不产生宿主副作用(同持久帧那一路)。
func (t *autonomousTurnRun) addsBlock(ctx context.Context, ev agentruntime.Event) bool {
	probe := turn.New()
	_ = t.svc.dispatcher.Apply(ctx, ev, probe, discardEmitter{}, &turn.TurnContext{Waits: turn.NewWaitTracker()})
	return !probe.Empty()
}

// persistTurnMessages 事务建 assistant 行(R18 下先建 user 行)并把会话翻 running。
func (t *autonomousTurnRun) persistTurnMessages(ctx context.Context) error {
	t.assistantMsg = &chat_entity.Message{
		SessionID:         t.sessionID,
		DeviceFingerprint: t.be.DeviceFingerprint,
		Role:              "assistant",
		BlocksJSON:        "[]",
		// 「这一轮是被什么起的」要跟着这一行落库:结构上非用户发起的轮不止一种,
		// 而它们在转录里长得一模一样,前端要据此决定给用户看哪一句交代(sess-3797)。
		// 注意只写在**新建**的行上 —— 下面 adoptInFlightAssistant 认领的那一行属于
		// 一次用户发起的轮(补齐重放只是把它跑完),它的来源仍然是「用户」。
		TurnTrigger: t.at.Trigger,
	}
	if t.at.Result != nil && t.at.Result.Model != "" {
		t.assistantMsg.Model = t.at.Result.Model
	}
	return db.Ctx(ctx).Transaction(func(tx *gorm.DB) error {
		txCtx := db.WithContextDB(ctx, tx)
		// 补齐重放的一轮可能是**本端自己派发过**的那一轮:派发时这里已经建过一行
		// assistant,断线期间宿主把它跑完了。续写那一行,不另起一行 —— 否则一个
		// prompt 底下挂两个助手回合,其中一个空白无解释(spec 2026-09-07 决策 2)。
		adopted, err := t.adoptInFlightAssistant(txCtx)
		if err != nil {
			return err
		}
		if adopted {
			t.sess.AgentStatus = "running"
			t.sess.NeedsAttention = false
			t.sess.LastMessageAt = time.Now().UnixMilli()
			return chat_repo.Session().Update(txCtx, t.sess)
		}
		nextSeq, err := transcript_repo.Message().NextSeq(txCtx, t.sessionID)
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
			if err := transcript_repo.Message().Create(txCtx, t.userMsg); err != nil {
				return err
			}
			nextSeq++
		}
		t.assistantMsg.Seq = nextSeq
		if err := transcript_repo.Message().Create(txCtx, t.assistantMsg); err != nil {
			return err
		}
		t.sess.AgentStatus = "running"
		t.sess.NeedsAttention = false
		t.sess.LastMessageAt = time.Now().UnixMilli()
		return chat_repo.Session().Update(txCtx, t.sess)
	})
}

// adoptInFlightAssistant 认领本地那一行「派发时建下、还没定稿」的 assistant,让补齐
// 续写它而不是另起一行(spec 2026-09-07「补齐与本地在飞的那一轮」的第 2 路)。
//
// 三个前提缺一不可:
//   - 这一轮是**补齐重放**(TriggerCatchUp)。真·自主续轮是后端自发的**新**一轮,
//     把它塞进上一轮那一行会把两轮内容并成一条。
//   - 重放的头一帧不是发起方标记(prelude 为 nil)。带标记的那一路是「别的对端在一条
//     空闲会话上开了新一轮」(R18),本地本来就没有这一轮,该新建。
//   - 本地最后那一行 assistant 既没定稿、也还没落过正文(见 assistantRowStillInFlight)。
//     懒开的一轮在开轮时已经判过没有这样一行(opensLazily),落行时不再认领 —— 那时最后
//     一行可能已经是之后另起的一轮。
//
// 交回 true 表示已把 t.assistantMsg 指到那一行上,调用方不再新建。
func (t *autonomousTurnRun) adoptInFlightAssistant(ctx context.Context) (bool, error) {
	if t.lazy || t.at.Trigger != remote.TriggerCatchUp || t.prelude != nil {
		return false, nil
	}
	latest, err := transcript_repo.Message().LatestAssistant(ctx, t.sessionID)
	if err != nil || latest == nil {
		return false, err
	}
	if !assistantRowStillInFlight(latest) {
		return false, nil
	}
	t.assistantMsg = latest
	if t.at.Result != nil && t.at.Result.Model != "" {
		t.assistantMsg.Model = t.at.Result.Model
	}
	return true, nil
}

// assistantRowStillInFlight 报这一行 assistant 是不是「派发时建下、这一轮还什么都
// 没往里落」。
//
// 两条判据:
//   - 收口那一发写下的那几格(模型 / 耗时 / 错误)都还空着。它们只在 finalize 里被写,
//     有任何一格就说明这一轮已经给过用户交代 —— 补齐不得静默改写它,宁可多一个可
//     解释的回合(spec 2026-09-07「补齐与本地在飞的那一轮」的失败分支)。
//   - 正文块还是空的。补齐重放的只是**游标之后**那一截,而续写用的累加器从空起手:
//     认领一行已经 checkpoint 过内容的消息,下一次 checkpoint 就会把整份正文换成那
//     一截,断线前已落库的块就此消失 —— 那是硬不变量 1 的「漏」,比多一个回合更糟。
//     派发后立刻离线的那一行本来就是空的(spec 2026-09-07 问题 4 的实测:role=assistant、
//     **无块**、error_text 与 model 皆空),这一路要治的正是它。
func assistantRowStillInFlight(m *chat_entity.Message) bool {
	if m == nil || m.Role != "assistant" ||
		m.Model != "" || m.ErrorText != "" || m.DurationMs != 0 {
		return false
	}
	if m.BlocksJSON == "" {
		return true
	}
	var stored []json.RawMessage
	if err := json.Unmarshal([]byte(m.BlocksJSON), &stored); err != nil {
		// 读不懂的正文按「有内容」算:宁可多一个可解释的回合,也不要覆盖掉一行看不清的记录。
		return false
	}
	return len(stored) == 0
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
	// 这一轮的 assistant 行已落库:此后的切换 notice 在桌面端排在它之后,对端同样扣到
	// 本轮收口(finalize 的 publishPeerTurnDone)之后再发,见 peer_notice_hold.go。
	t.svc.peerTurnOpened(t.sessionID, t.stream)
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

// initSegment 初始化本轮第一个 assistant 分段的轮上下文与计时口径。累加器开轮前就在:
// 懒开的一轮落行之前,持久帧已经往里累积了。
func (t *autonomousTurnRun) initSegment() {
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
		// 呈现那一路也从这个分段起重新攒:两只累加器都是「这一段」的状态。
		t.previewAcc = nil
		t.segmentStart = time.Now()
		t.turnCtx = t.svc.newTurnContext(t.assistantMsg, t.sess, t.stream, t.be.Type)
		t.svc.emitter.Emit(ctx, t.stream, *payload)
	}
}

// consumeSteer 认领 SteerConsumed 并报告「这条事件已处理,不要进 dispatcher」。
// 分段不走 dispatcher —— 它是 assistantMsg/acc/segmentStart/turnCtx 这四个字段的
// 整体替换,handler 接口表达不了(与 runTurn 的 switch 同一个理由)。
func (t *autonomousTurnRun) consumeSteer(ctx context.Context, ev agentruntime.Event, preview bool) bool {
	sc, ok := ev.(agentruntime.SteerConsumed)
	if !ok {
		return false
	}
	// 预览帧只驱动呈现:落库归 at.Events 上的持久帧,理由与 turnRun 那一处同源
	// (spec 2026-09-07-host-transcript-user-input 决策 2)。chip 仍在这一刻清掉 ——
	// 它是呈现,不是转录。
	if preview {
		t.previewAcc = nil
		t.svc.emitter.Emit(ctx, t.stream, ChatStreamEvent{
			Kind: StreamSteerConsumed, QueuedIDs: consumedSteerIDs(sc.Steers),
		})
		return true
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

// consumeEvents 消费本轮事件流直到 at.Events 关闭(首条已被上游先读走)。
//
// 远端执行那一路是**两级帧**(规格 2026-09-05「两级帧与补齐」):at.Events 上到的是
// 宿主实时发布的持久帧,只累积、不呈现;逐 token 的呈现走预览流。两条流在这一个
// goroutine 上消费 —— dispatcher 会动 turnCtx 与前端流,两个 goroutine 同时进去
// 就是一场数据竞争。
func (t *autonomousTurnRun) consumeEvents(ctx context.Context) {
	// The first event can be the persisted user-message prelude; it is still a
	// canonical event for remote peers even though the local reducer does not
	// add it to assistant blocks.
	if t.hasFirst {
		t.svc.publishPeerEvent(t.sessionID, t.first)
	}
	// 首条若已是标记之外的普通事件,它仍要进 dispatcher(用户消息不进 assistant 内容)。
	if t.hasFirst && t.prelude == nil {
		switch {
		case t.previews != nil:
			t.applyDurable(ctx, t.first)
		case !t.consumeSteer(ctx, t.first, false):
			if err := t.svc.dispatcher.Apply(ctx, t.first, t.acc, t.dispEmit, t.turnCtx); err != nil {
				logger.Ctx(ctx).Warn("chat_svc: autonomous dispatcher Apply failed",
					zap.String("eventType", fmt.Sprintf("%T", t.first)), zap.Error(err))
			}
			if shouldCheckpointAssistantAfterEvent(t.first) {
				t.svc.checkpointAssistantNew(ctx, t.assistantMsg, t.acc)
			}
		}
	}
	if t.previews == nil {
		// 本机执行:没有两级帧,一条流既呈现也落库。
		for ev := range t.at.Events {
			t.applyLive(ctx, ev, false)
		}
	} else {
		t.consumeTwoLevelFrames(ctx)
	}
	// 流结束时仍在推迟的分段必须落地:插话已经从 inbox drain 走了,不落就丢。
	t.flushPendingSteers(ctx)
}

// consumeTwoLevelFrames 消费远端执行那一路的两条流:持久帧进转录,预览帧只呈现。
func (t *autonomousTurnRun) consumeTwoLevelFrames(ctx context.Context) {
	for {
		select {
		case ev, ok := <-t.at.Events:
			if !ok {
				// 轮结束。预览与持久帧走同一条读循环、同一个顺序,所以此刻缓冲里剩下
				// 的都是这一轮的,呈现完再收尾。
				t.drainPreviews(ctx)
				return
			}
			t.applyDurable(ctx, ev)
		case preview := <-t.previews:
			t.applyLive(ctx, preview, true)
		}
	}
}

// drainPreviews 把此刻还没呈现的预览帧呈现完。
func (t *autonomousTurnRun) drainPreviews(ctx context.Context) {
	for {
		select {
		case preview := <-t.previews:
			t.applyLive(ctx, preview, true)
		default:
			return
		}
	}
}

// liveAcc 交回呈现这一路此刻该往里写的累加器。分段落地会把 acc 整只换掉,所以每次现取。
func (t *autonomousTurnRun) liveAcc(preview bool) *turn.Accumulator {
	if !preview {
		return t.acc
	}
	if t.previewAcc == nil {
		t.previewAcc = turn.New()
	}
	return t.previewAcc
}

// applyDurable 把一条持久帧累积进本轮转录,并在定稿时刻 checkpoint 一次。
//
// 它**只累积**:呈现归预览那一路 —— 同一段内容在这条会话上已经实时呈现过一次,
// 持久帧是它落库之后的那一份事实,再走一遍宿主副作用就是把一段话在前端印两遍。
func (t *autonomousTurnRun) applyDurable(ctx context.Context, ev agentruntime.Event) {
	if ev == nil {
		return
	}
	// 宿主分段之后发来的那一行用户消息(持久帧投影)。判据是**来源标识**,与 turnRun
	// 那一处同源:本轮自己那条提问不带来源,插话那一行必带。
	if um, ok := ev.(agentruntime.UserMessageEvent); ok {
		if um.SourceDevice == "" || !t.openOnContent(ctx, true) {
			return
		}
		t.pendingSteers = append(t.pendingSteers, agentruntime.ConsumedSteer{
			Text: um.Text, SourcePeer: um.SourceDevice, SourceName: um.SourceDeviceName,
		})
		if !t.acc.HasOpenToolUse() {
			t.flushPendingSteers(ctx)
		}
		return
	}
	if t.durableCtx == nil {
		t.durableCtx = &turn.TurnContext{Waits: turn.NewWaitTracker()}
	}
	if t.cards == nil {
		t.cards = t.svc.newDurableCards(t.sessionID, t.stream, nil)
	}
	if t.cards.restore(ctx, t.acc, ev) {
		return
	}
	if err := t.svc.dispatcher.Apply(ctx, ev, t.acc, discardEmitter{}, t.durableCtx); err != nil {
		logger.Ctx(ctx).Warn("chat_svc: autonomous dispatcher Apply failed",
			zap.String("eventType", fmt.Sprintf("%T", ev)), zap.Error(err))
	}
	// 持久帧只累积、不产生宿主副作用,所以先累积、再看累加器长没长出块来决定落不落行。
	if !t.openOnContent(ctx, !t.acc.Empty()) {
		return
	}
	if shouldCheckpointAssistantAfterEvent(ev) {
		t.svc.checkpointAssistantNew(ctx, t.assistantMsg, t.acc)
	}
}

// applyLive 呈现一条实时事件:对端扇出、插话分段这些**宿主副作用**都在这里,内容则
// 累积进 acc。preview=true 时累积进用完即弃的那只 —— 落库归 at.Events 上的持久帧。
//
// 懒开的一轮还没落行时只有预览帧会到这里(懒开只在两级帧那一路):给本轮添块的那一帧先落行
// 再呈现,其余丢掉 —— 还没有一行可以呈现在上面。
func (t *autonomousTurnRun) applyLive(ctx context.Context, ev agentruntime.Event, preview bool) {
	if t.unopened && !t.openOnContent(ctx, t.addsBlock(ctx, ev)) {
		return
	}
	t.svc.publishPeerEvent(t.sessionID, ev)
	if t.consumeSteer(ctx, ev, preview) {
		return
	}
	acc := t.liveAcc(preview)
	if err := t.svc.dispatcher.Apply(ctx, ev, acc, t.dispEmit, t.turnCtx); err != nil {
		logger.Ctx(ctx).Warn("chat_svc: autonomous dispatcher Apply failed",
			zap.String("eventType", fmt.Sprintf("%T", ev)), zap.Error(err))
	}
	// 上一条事件收口了工具 → 之前推迟的分段现在可以落地。
	if len(t.pendingSteers) > 0 && !acc.HasOpenToolUse() {
		t.flushPendingSteers(ctx)
	}
	if !preview && shouldCheckpointAssistantAfterEvent(ev) {
		t.svc.checkpointAssistantNew(ctx, t.assistantMsg, t.acc)
	}
}

// finalize 收尾本轮:落 blocks + usage/model、翻终态、定向翻转后台任务、发终态事件。
// 懒开的一轮到收尾都没落行(帧全写回了更早的消息,或落行失败已处置)时什么都不做:没有
// 行可收,会话状态也从没被这一轮动过。
func (t *autonomousTurnRun) finalize(ctx context.Context) {
	if t.unopened {
		if !t.openFailed {
			logger.Ctx(ctx).Info("chat_svc: catch-up turn closed without content of its own; no row",
				zap.Int64("sessionId", t.sessionID))
		}
		return
	}
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
	// 与用户轮同一套收表口径(turn_run.finalize):同一条会话里自动续的轮与用户发起的轮
	// 一样要有耗时 / 首字 / 速率。
	t.assistantMsg.DurationMs = int(time.Since(t.segmentStart).Milliseconds())
	t.turnCtx.PauseGeneration()
	t.assistantMsg.FirstTokenMs = t.turnCtx.FirstTokenMs()
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
	// 分子要等 usage 落定,所以在 Result 的用量 patch 之后。
	t.assistantMsg.TokensPerSec = t.turnCtx.TokensPerSec(t.assistantMsg.CompletionTokens)
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
	_ = transcript_repo.Message().Update(finalCtx, t.assistantMsg)
	t.svc.publishPeerTurnDone(finalCtx, t.sessionID, t.stream, t.assistantMsg)

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
	// per-turn accumulator,只能定向重写持久化态。与「完成帧落在别人的轮里」同一条路收尾
	// (落库 → 按实际翻到的那张卡镜像 → 收敛 bgRunning):SendMessage 恢复的那一段带的是
	// 新 tool call,仓储要靠 task_id 找回原卡,界面也得按原卡的 id 翻(sess-4198)。
	// completedRef 为 nil(含 remote 不携带 CompletedTask 的情形)时跳过。
	if t.completedRef != nil {
		flipper := subagentFlipperAdapter{svc: t.svc, sess: t.sess, stream: t.stream}
		// completedRef 只在 at.CompletedTask 非空时建立,task_id 直接从那里取。
		if err := flipper.FlipSubagentStatus(finalCtx, t.completedRef.ToolCallID, t.at.CompletedTask.TaskID,
			t.completedRef.Status, t.completedRef.Summary); err != nil {
			logger.Ctx(finalCtx).Warn("chat_svc.driveAutonomousTurn: FlipSubagentStatus failed",
				zap.Int64("sessionId", t.sessionID),
				zap.String("toolUseId", t.completedRef.ToolCallID),
				zap.Error(err))
		}
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
