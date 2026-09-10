package handlers

// runtime_transcript.go 是 agentred 的转录写入侧:把一轮执行的后端事件累积成块并
// 落库。它与桌面端 chat_svc 的那一路**同形**,而且用的是同一份代码 ——
// internal/pkg/transcript 的 dispatcher + accumulator(决策 2)、
// internal/repository/transcript_repo 的消息与块仓储(决策 8)。
//
// 这里没有的东西同样是刻意的:发射器(RPC 通知 vs Wails 事件)、会话行与生命周期、
// usage / error 的适配器,都归各自宿主持有(规格「复用边界」)。

import (
	"context"

	"github.com/cago-frame/agents/agent/blocks"
	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"

	"github.com/agentre-hub/agentre/internal/model/entity/transcript_entity"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/runtimes/remote/wire"
	"github.com/agentre-hub/agentre/internal/pkg/transcript"
	"github.com/agentre-hub/agentre/internal/pkg/transcript/turn"
)

// turnTranscript 是一轮执行的转录累积状态。零值不可用;构造走 beginTranscript,
// 没接存储 / 起手失败时它是 nil,后续每个方法都在 nil 上安全地什么都不做 ——
// 转录写不进去不该反过来打断这一轮执行(与会话行写入同一条纪律)。
type turnTranscript struct {
	port       TranscriptPort
	dispatcher *turn.Dispatcher
	acc        *turn.Accumulator
	turnCtx    *turn.TurnContext
	msg        *transcript_entity.Message
	// conversationID 是这条对话的线上身份:投影出来的每一帧带的都是它。
	conversationID string
	// publish 把一条**持久帧**交给这一轮的推送出口。nil = 这一轮没有出口,内容照旧
	// 落库,对端下次补齐时按同样的号拿到它。
	publish func(frame wire.EventFrame)
	// publisher 记着哪些位置已经发布过什么内容。它是两个宿主共用的那一份
	// (transcript.FramePublisher):轮内哪些帧还不该发、哪一次原地修补要重发,
	// agentred 与桌面端必须一字不差。FrameKey 按消息 id 索引,所以它跨得过分段 ——
	// 一轮里的每一条消息共用这一只。
	publisher *transcript.FramePublisher
	// pendingSteers 是收到了、但还不能分段的插话。工具在途时先攒着:claudecode 的
	// PostToolUse hook 在 CLI 写出 tool_result 帧**之前**就 drain 走排队消息,
	// SteerConsumed 因此会先于同一个工具的 ToolResult 到达。此刻收口 assistant 会把
	// tool_use 冻在旧消息里,随后的 tool_result 在新累加器里查不到它,被当孤儿丢弃 ——
	// 工具卡永远停在 running。判据与桌面端同一条(chat_svc.turnRun.applyLive)。
	pendingSteers []agentruntime.ConsumedSteer
}

// discardEmitter 是 dispatcher 要的那个发射器的空位。agentred 的实时推送走 RPC 通知
// (sessionEmitter),与「事件怎么累积成块」无关 —— handler 往这里 emit 的是桌面端
// 那条 Wails 流的载荷,这台机器上没有它的读者。
type discardEmitter struct{}

func (discardEmitter) Emit(context.Context, string, any) {}

// beginTranscript 起一轮转录:落下用户那一行,建一条空的 assistant 消息。
//
// userText 与 userBlocks 都空(自主续轮)时不落用户行 —— 那一轮不是任何人发起的,
// 凭空造一句会在转录里印出一条没人说过的话。
//
// userBlocks 是这一轮随文本一起来的附件,与文本落进同一行用户消息(决策 3)。
//
// source 是提交这一轮的对端身份,盖进用户那一行 —— 它必须骑在转录里,来源才随持久帧
// 一起投影给消费方、也才补得齐。
//
// 后两个返回值是用户那一行占掉的持久帧的**最低号与最高号**(没有用户行 / 取号失败
// 时都是 0)。Run 把它们放进应答交给发起方:发起方拿最低号比闸门(它与游标之间不许
// 有洞)、拿最高号定落点,游标从此表达「我已经持有的内容」—— 补齐不再重放它自己
// 写下的那条用户消息(spec 2026-09-07 决策 1;区间见
// 2026-09-07-host-transcript-user-input 决策 4)。带附件的一条用户消息占不止一帧,
// 所以这里必须是区间而不是单个号。
// 正因为这两个号要随应答走,本函数必须在 Run 的**同步段**跑完,不能留在 fanout 协程里。
func (h *RuntimeHandlers) beginTranscript(
	em *sessionEmitter, userText string, userBlocks []blocks.ContentBlock, source transcript.UserSource,
) (*turnTranscript, int64, int64) {
	if h.deps.Transcript == nil {
		return nil, 0, 0
	}
	user, msg, err := h.deps.Transcript.StartTurn(em.ctx, em.conversationID, userText, userBlocks, source)
	if err != nil {
		logger.Ctx(em.ctx).Error("handlers.RuntimeHandlers.beginTranscript: start turn failed",
			zap.String("conversationId", em.conversationID),
			zap.String("peerFingerprint", em.peer),
			zap.Error(err))
		return nil, 0, 0
	}
	if msg == nil {
		return nil, 0, 0
	}
	t := &turnTranscript{
		port:       h.deps.Transcript,
		dispatcher: transcript.NewTurnDispatcher(transcript.Adapters{}),
		acc:        turn.New(),
		// Waits 是待决策 handler 要的那本账;宿主那几格(AssistantMsg / Session /
		// SessionUpdater ...)留空 —— 它们是桌面端的接线,不共用(规格「复用边界」)。
		turnCtx:        &turn.TurnContext{Waits: turn.NewWaitTracker()},
		msg:            msg,
		conversationID: em.conversationID,
		publish: func(frame wire.EventFrame) {
			em.emit(wire.NotifyEvent, &frame)
		},
		publisher: transcript.NewFramePublisher(),
	}
	// 用户那一行起手就定稿了:它现在就该以持久帧的身份出去,取到的号排在这一轮的
	// 最前面。晚发(等到补齐才编号)会让它排到这一轮的正文之后 —— 对端的转录里
	// 提问跑到回答后面去。
	lowest, highest := t.publishDurable(em.ctx, user, true)
	return t, lowest, highest
}

// publishDurable 把 msg 此刻可以定稿的持久帧取号发出去。
//
// 这是规格 2026-09-05「两级帧与补齐」的第 3 条:**宿主必须实时发布持久帧**,不得只在
// 补齐时才交出。一条持续在线的对端若只收得到预览帧,它的游标永不前进,重连补齐就会
// 从头重放它已经看过的内容 —— 那正是硬不变量 1 禁止的「重」。
//
// 取号与发布不可分:号从台账里取(一次事务),取到了才发 —— 取号失败就不发,否则对端
// 会持有一个宿主认不回来的号(规格「帧编号」)。桌面端做宿主时走的是同一条路
// (chat_svc.publishPeerMessageFrames)。
//
// 返回这一发里取到的**最低号与最高号**,没发出任何帧时都是 0。开轮那一发的返回值随
// 应答交给发起方(见 beginTranscript);轮内与收口那两发的调用方不需要它们。
func (t *turnTranscript) publishDurable(
	ctx context.Context, msg *transcript_entity.Message, final bool,
) (int64, int64) {
	if t == nil || msg == nil || t.publish == nil {
		return 0, 0
	}
	keyed, err := transcript.ProjectKeyedMessage(t.conversationID, msg)
	if err != nil {
		logger.Ctx(ctx).Warn("handlers.turnTranscript.publishDurable: project failed",
			zap.Int64("messageId", msg.ID), zap.Error(err))
		return 0, 0
	}
	pending := t.publisher.Pending(keyed, final)
	if len(pending) == 0 {
		return 0, 0
	}
	keys := make([]transcript.FrameKey, 0, len(pending))
	for _, frame := range pending {
		keys = append(keys, frame.Key)
	}
	seqs, err := t.port.AllocateFrameSeqs(ctx, msg.SessionID, keys)
	if err != nil || len(seqs) != len(pending) {
		logger.Ctx(ctx).Warn("handlers.turnTranscript.publishDurable: allocate failed; frames withheld",
			zap.Int64("messageId", msg.ID), zap.Error(err))
		return 0, 0
	}
	var lowest, highest int64
	for index := range pending {
		pending[index].Frame.Seq = seqs[index]
		if lowest == 0 || seqs[index] < lowest {
			lowest = seqs[index]
		}
		highest = max(highest, seqs[index])
		t.publish(pending[index].Frame)
	}
	t.publisher.Commit(pending)
	return lowest, highest
}

// observe 把一条事件累积进本轮的块,并在定稿时刻 checkpoint 一次。
//
// 判「哪一帧之后 checkpoint」用的是共用的那一份(transcript.ShouldCheckpointAfter):
// 两个宿主必须在同一帧上落同一次 checkpoint,否则「换台机器跑,崩溃后看得见的东西
// 不一样」。
func (t *turnTranscript) observe(ctx context.Context, ev agentruntime.Event) {
	if t == nil {
		return
	}
	// 插话不走那张事件→块表:它要的是**轮次分段**(收口当前 assistant、插一行用户
	// 消息、开一条新的),而那张表只表达「这一帧累积成哪个块」。桌面端在同一位置
	// 也把它单独拦下(chat_svc.turnRun.applyLive 的 case SteerConsumed)。
	if consumed, ok := ev.(agentruntime.SteerConsumed); ok {
		for _, steer := range consumed.Steers {
			if steer.Text != "" {
				t.pendingSteers = append(t.pendingSteers, steer)
			}
		}
		if !t.acc.HasOpenToolUse() {
			t.flushPendingSteers(ctx)
		}
		return
	}
	// 推迟中的分段:这一帧不是 tool_result,说明在途 tool_use 的结果根本不走流
	// (AskUserQuestion 这类),不再等 —— 且必须赶在 Apply 之前落地,否则这一帧的内容
	// 会被记进本该收口的旧 assistant。推迟至多一个事件,判据与桌面端同一条
	// (chat_svc.turnRun.applyLive)。
	if len(t.pendingSteers) > 0 {
		if _, isToolResult := ev.(agentruntime.ToolResult); !isToolResult {
			t.flushPendingSteers(ctx)
		}
	}
	if err := t.dispatcher.Apply(ctx, ev, t.acc, discardEmitter{}, nil, t.turnCtx); err != nil {
		logger.Ctx(ctx).Warn("handlers.turnTranscript.observe: dispatcher apply failed",
			zap.Int64("messageId", t.msg.ID), zap.Error(err))
	}
	if transcript.ShouldCheckpointAfter(ev) {
		t.checkpoint(ctx)
	}
	// 工具刚刚收口的那一刻,先前攒下的插话可以分段了。
	if len(t.pendingSteers) > 0 && !t.acc.HasOpenToolUse() {
		t.flushPendingSteers(ctx)
	}
}

// flushPendingSteers 把攒下的插话落成分段:收口当前 assistant、每条插话一行用户消息、
// 再开一条新的 assistant 承接后半段,并按转录顺序依次取号发布。
//
// 「工具在途时先攒着」的判据在**调用方**,不在这里:轮内那两处按 HasOpenToolUse 决定
// 等不等,而「不是 tool_result」与收口那两处必须无条件落地 —— 攒着的插话已经被后端
// 消费进这一轮的上下文了,不落就是用户打的字进了模型却没进转录。
//
// 分段失败(落库出错)时不切换:当前那条 assistant 继续承接后半段,这一轮退回本轮之前
// 的形状(前后两段并在一条消息里)—— 半截切换会让后半段落进一条库里并不存在的消息。
// 插话则退回待分段队列等下一个时机(轮末那一次是最后的机会):清空之后只记一条日志,
// 那一段用户自己打的字就既不在库里也不在队列里,谁也找不回来。
func (t *turnTranscript) flushPendingSteers(ctx context.Context) {
	if t == nil || len(t.pendingSteers) == 0 {
		return
	}
	steers := t.pendingSteers
	t.pendingSteers = nil
	if err := t.msg.SetBlocks(t.acc.Finalize()); err != nil {
		logger.Ctx(ctx).Warn("handlers.turnTranscript.flushPendingSteers: encode failed",
			zap.Int64("messageId", t.msg.ID), zap.Error(err))
		t.pendingSteers = steers
		return
	}
	users, next, err := t.port.SegmentTurn(ctx, t.msg, steers)
	if err != nil || next == nil {
		logger.Ctx(ctx).Warn("handlers.turnTranscript.flushPendingSteers: persist failed",
			zap.Int64("messageId", t.msg.ID), zap.Int("steers", len(steers)), zap.Error(err))
		t.pendingSteers = steers
		return
	}
	// 取号顺序就是补齐的重放顺序:收口的那一条在前,插话那几行紧随其后。晚一步取号
	// 的会排到整段之后 —— 对端的转录里插话就跑到后半段回答的后面去了。
	_, _ = t.publishDurable(ctx, t.msg, true)
	for _, user := range users {
		_, _ = t.publishDurable(ctx, user, true)
	}
	// 整体切换:后半段从这里起攒进新的那一条。turnCtx 也要换 —— 它记的是「这一段」
	// 的首字时刻与待决策账本。
	t.msg = next
	t.acc = turn.New()
	t.turnCtx = &turn.TurnContext{Waits: turn.NewWaitTracker()}
}

// checkpoint 把此刻的累积状态落库(只写变化的块行)。这是在途那一轮唯一的抗崩溃
// 手段(决策 5):宿主在轮中消失时,checkpoint 过的块留下,没 checkpoint 的尾巴丢失。
func (t *turnTranscript) checkpoint(ctx context.Context) {
	// 差分的基准是内存里那份**上一次落库成功**的正文:SetBlocks 马上就要覆写它,所以
	// 在覆写前留一份。不留就只能整表替换,而 checkpoint 是每个 ToolResult 一次的高频
	// 调用(理由见 transcript_repo.syncBlocks:实测一条消息被 checkpoint 840 次)。
	prev := t.msg.BlocksJSON
	if err := t.msg.SetBlocks(t.acc.Snapshot()); err != nil {
		logger.Ctx(ctx).Warn("handlers.turnTranscript.checkpoint: encode failed",
			zap.Int64("messageId", t.msg.ID), zap.Error(err))
		return
	}
	if err := t.port.Checkpoint(ctx, t.msg, prev); err != nil {
		logger.Ctx(ctx).Warn("handlers.turnTranscript.checkpoint: persist failed",
			zap.Int64("messageId", t.msg.ID), zap.Error(err))
		// 落库失败时把内存正文退回上一次落库的那份:留着没落库的新正文会让下一次
		// checkpoint 拿一个库里并不存在的基准做差分,差出来的块行从此对不上。
		t.msg.BlocksJSON = prev
		return
	}
	// 块落了库才轮到取号(决策 3)。轮内只发已经定稿的那些帧 —— 结尾还会继续长的
	// 正文块与消息级派生帧留给收口那一发。
	_, _ = t.publishDurable(ctx, t.msg, false)
}

// finish 收口本轮:正文定稿,并把这一轮的模型 / 用量 / 计时 / 错误写在同一行上。
// 取值来自这一轮自己的终态帧 —— fanout 就着同一条事件流量出来的那一份。
func (t *turnTranscript) finish(ctx context.Context, frame wire.RunResultDoneFrame) {
	if t == nil {
		return
	}
	// 还攒着的插话必须在这里落地(桌面端 chat_svc.turnRun.finalize 开头同样先 flush):
	// 它已经被后端消费进这一轮的上下文了,轮末丢掉就是用户打的字进了模型却没进转录 ——
	// 那正是 2026-09-05 不变量 1 的「漏」。此刻不再看 HasOpenToolUse:这一轮已经没有
	// 后续事件能等到那个 tool_result 了。
	t.flushPendingSteers(ctx)
	if err := t.msg.SetBlocks(t.acc.Finalize()); err != nil {
		logger.Ctx(ctx).Warn("handlers.turnTranscript.finish: encode failed",
			zap.Int64("messageId", t.msg.ID), zap.Error(err))
		return
	}
	t.msg.Model = frame.Model
	t.msg.ErrorText = frame.StopErrMsg
	t.msg.DurationMs = frame.DurationMs
	t.msg.FirstTokenMs = frame.FirstTokenMs
	t.msg.TokensPerSec = frame.TokensPerSec
	if u := frame.Usage; u != nil {
		t.msg.PromptTokens = u.PromptTokens
		t.msg.CompletionTokens = u.CompletionTokens
		t.msg.CachedTokens = u.CachedTokens
		t.msg.CacheCreationTokens = u.CacheCreationTokens
		t.msg.ReasoningTokens = u.ReasoningTokens
		t.msg.TotalInputTokens = u.PromptTokens + u.CachedTokens + u.CacheCreationTokens
	}
	if err := t.port.FinishTurn(ctx, t.msg); err != nil {
		logger.Ctx(ctx).Warn("handlers.turnTranscript.finish: persist failed",
			zap.Int64("messageId", t.msg.ID), zap.Error(err))
		return
	}
	// 收口:正文定稿,连同消息级派生帧(usage / done)一起发出去。
	_, _ = t.publishDurable(ctx, t.msg, true)
}
