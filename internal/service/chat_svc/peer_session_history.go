package chat_svc

import (
	"context"
	"fmt"
	"reflect"
	"sync"

	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"

	"github.com/agentre-hub/agentre/internal/model/entity/chat_entity"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/runtimes/remote/wire"
	"github.com/agentre-hub/agentre/internal/pkg/transcript"
	"github.com/agentre-hub/agentre/internal/repository/transcript_repo"
)

// peerSessionPublication owns the one ordered notification universe for a
// desktop session. It is deliberately in-memory: persisted transcript seeds
// the initial prefix, and subsequent live canonical frames are retained only
// for the running desktop process so reconnects share one dedup universe.
//
// Delivery to subscribers is serialized through a single worker goroutine
// (peerFlushLoop) rather than being written inline from the canonical event
// loop: Notify blocks on a relay WebSocket write, and doing that while holding
// the publication mutex would stall the local turn itself when an attached
// peer (or the relay path to it) is slow. Frames are only ever queued by the
// turn loop; the worker drains the queues outside the publication lock.
type peerSessionPublication struct {
	// conversationID 是这条会话在线上的身份(chat_sessions.conversation_id)。
	// 建立这份宇宙的那一刻就定死,此后不再改 —— 每一帧都要盖它,而它是个不可变值,
	// 所以不进锁、也不必回头查库。
	conversationID string
	// sessionID 是这条会话的本地主键 —— 帧编号台账按它分命名空间。与 conversationID
	// 一样在建立这份宇宙时定死。
	sessionID int64

	mu      sync.Mutex
	history []wire.EventFrame
	// createtimes 与 history 一一对应:第 i 帧在**这台桌面端**上发生的时刻
	// (Unix 毫秒)。补齐把它随帧交出去,对端的转录才有 HH:mm 可显示。
	//
	// 单开一条并行切片而不是塞进 EventFrame:帧是线上格式,实时投递的那一份不需要
	// 它(收到即当下),只有补齐这条路要。
	createtimes []int64
	nextSeq     int64
	initialized bool
	subscribers map[string]*peerSessionSubscription
	// publisher 记每个帧位置**当前**发布出去的那份内容,据此挑出此刻该发的帧。它是
	// 两个宿主共用的那一份(transcript.FramePublisher):轮内哪些帧还不该发、哪一次
	// 原地修补要重发,桌面端与 agentred 必须一字不差。
	publisher *transcript.FramePublisher

	// publishMu 串行化「取号 → 发布」这一整段。取号要落库(不能在 mu 里做网络/磁盘
	// IO),而两次发布若交错,后取到的号可能先进 history,对端就会看到乱序。
	publishMu sync.Mutex

	// wake carries a single-slot non-blocking signal for the flush worker;
	// startOnce guarantees at most one worker per publication.
	wake      chan struct{}
	startOnce sync.Once
}

// peerSubscriberQueueDepth 是**每个订阅者**的投递缓冲深度。
//
// 从前 pending 没有上限:一个写不动的对端能让它一直涨到内存吃光 —— 中继是网络入口,
// 「对面猛灌就能撑爆本机」不是可以留着的形状。
//
// 满了之后丢帧是可恢复的:帧上带 seq,对端的闸门看到跳号会从游标发起一次补齐,
// 而 publication.history + PullPeerSession 正是补齐读的那份日志。日志不参与丢弃。
//
// 与 agentred 那侧同一个数(connRegistry 的 subscriberQueueDepth):同一条纪律,
// 没有理由两边取不同的深度。
const peerSubscriberQueueDepth = 256

type peerSessionSubscription struct {
	subscriber PeerSessionSubscriber
	highWater  int64
	cursor     int64
	pending    []wire.EventFrame
	// dropped 记这个订阅者被丢过帧。只用于日志:对端靠持久帧的 seq 跳号自己发现
	// 并补齐,不需要服务端告诉它。
	dropped bool
	// flushing 表示这个订阅者此刻有一条投递在飞。每个订阅者至多一条 —— 它保证
	// 这个订阅者收到的帧仍然有序,同时让**不同**订阅者彼此独立:一个卡住的对端
	// 不再拖住同一会话上的其他人。
	flushing bool
}

func (s *chatSvc) peerPublication(sessionID int64, conversationID string) *peerSessionPublication {
	value, _ := s.peerPublications.LoadOrStore(sessionID, &peerSessionPublication{
		conversationID: conversationID,
		sessionID:      sessionID,
		subscribers:    map[string]*peerSessionSubscription{},
		publisher:      transcript.NewFramePublisher(),
		wake:           make(chan struct{}, 1),
	})
	publication := value.(*peerSessionPublication)
	publication.startOnce.Do(func() { go s.peerFlushLoop(publication) })
	return publication
}

// peerFlushLoop drains every subscriber's queued frames on the publication's
// own goroutine. A blocked Notify (stalled peer / relay) pauses only this
// session's peer fan-out, never the local turn's event loop.
func (s *chatSvc) peerFlushLoop(publication *peerSessionPublication) {
	for range publication.wake {
		s.flushPeerPending(publication)
	}
}

// flushPeerPending 把每个就绪订阅者排着的帧交出去。订阅者在拉取游标追上 attach
// 高水位之后才算就绪;pull 那条路在应答里带回日志覆盖的前缀,并在追平时叫醒本
// worker 一次,所以这个队列里只会有真正的实时帧。
//
// **每个订阅者一条独立的投递 goroutine**,而不是在这里逐个串行调阻塞的 Notify。
// Notify 写的是一条中继 websocket(跨副本时还要等一次 Redis 回执,最坏 5 秒),
// 串行意味着一台卡住的机器会让同一条对话上其它所有端一起停住。同一条纪律在
// agentred 那侧是 connRegistry 的 asyncNotifier,这里是它的对称实现。
//
// 每个订阅者至多一条投递在飞(flushing),所以它收到的帧仍然有序;投递完成后
// 如果队列里又攒了新的,worker 自己接着跑下一轮,不必再等一次 wake。
func (s *chatSvc) flushPeerPending(publication *peerSessionPublication) {
	publication.mu.Lock()
	starting := make([]string, 0, len(publication.subscribers))
	for key, sub := range publication.subscribers {
		if sub.flushing || sub.cursor < sub.highWater || len(sub.pending) == 0 {
			continue
		}
		sub.flushing = true
		starting = append(starting, key)
	}
	publication.mu.Unlock()

	for _, key := range starting {
		go s.deliverPeerPending(publication, key)
	}
}

// deliverPeerPending 是一个订阅者的投递循环:取走它排着的帧、在**锁外**逐条交付,
// 交付期间新到的帧继续入队,交付完再看一轮。写失败即认为这个订阅者不行了,摘掉它
// (与从前同一判据)。
func (s *chatSvc) deliverPeerPending(publication *peerSessionPublication, key string) {
	for {
		publication.mu.Lock()
		sub := publication.subscribers[key]
		if sub == nil || len(sub.pending) == 0 {
			if sub != nil {
				sub.flushing = false
			}
			publication.mu.Unlock()
			return
		}
		frames := sub.pending
		sub.pending = nil
		subscriber := sub.subscriber
		publication.mu.Unlock()

		for _, frame := range frames {
			if err := subscriber.Notify(wire.NotifyEvent, frame); err != nil {
				publication.mu.Lock()
				if publication.subscribers[key] == sub {
					delete(publication.subscribers, key)
				}
				sub.flushing = false
				publication.mu.Unlock()
				return
			}
		}
	}
}

// enqueuePeerFrame 把一帧排给一个订阅者。**永不阻塞**,而且封顶。
//
// 队列满说明这个订阅者已经落后这么多帧了,继续排只会无界吃内存。此时丢掉最旧的
// 那一批里的这一帧并记一次:对端按帧上的 seq 看到跳号,走既有的游标补齐把缺口
// 拉回来 —— 日志(publication.history)是完整的,补得回来正是可以丢的前提。
func enqueuePeerFrame(sub *peerSessionSubscription, frame wire.EventFrame) {
	if len(sub.pending) >= peerSubscriberQueueDepth {
		sub.dropped = true
		return
	}
	sub.pending = append(sub.pending, frame)
}

// PullPeerSession serves the same runtime.session.pull contract used by
// agentred. The subscriber identifies the account connection whose attach
// handoff cursor advances; it is not a new wire field.
func (s *chatSvc) PullPeerSession(ctx context.Context, params wire.SessionPullParams, subscriber PeerSessionSubscriber) (wire.SessionPullResult, error) {
	if subscriber == nil {
		return wire.SessionPullResult{}, ErrPeerSessionNotFound
	}
	sessionID, err := ResolvePeerConversation(ctx, params.ConversationID)
	if err != nil {
		return wire.SessionPullResult{}, err
	}
	publication := s.peerPublication(sessionID, params.ConversationID)
	key := peerSubscriberKey(subscriber)
	publication.mu.Lock()

	subscription := publication.subscribers[key]
	if subscription == nil {
		publication.mu.Unlock()
		return wire.SessionPullResult{}, ErrPeerSessionNotFound
	}
	limit := clampPeerPullLimit(params.Limit)
	out := wire.SessionPullResult{Cursor: params.Cursor}
	if subscription.highWater > 0 {
		out.OldestSeq = 1
	}
	for index, frame := range publication.history {
		if frame.Seq <= params.Cursor || frame.Seq > subscription.highWater {
			continue
		}
		if len(out.Notifications) == limit {
			out.HasMore = true
			break
		}
		// createtimes 与 history 逐格对应,但读的时候不假定它一定齐:两条切片在同一
		// 把锁下一起长,长度失配只可能是本文件里出了 bug,而那时少一个时刻远好过 panic。
		var createtime int64
		if index < len(publication.createtimes) {
			createtime = publication.createtimes[index]
		}
		out.Notifications = append(out.Notifications, wire.JournaledNotification{
			Seq: frame.Seq, Method: wire.NotifyEvent, Params: &frame, Createtime: createtime,
		})
		out.Cursor = frame.Seq
	}
	if out.Cursor > subscription.cursor {
		subscription.cursor = out.Cursor
	}
	caughtUp := subscription.cursor >= subscription.highWater
	publication.mu.Unlock()

	// 拉平后把 live 交付完全交给单个 flush worker：这里只发一个 wake 信号，绝不在
	// publication 锁内调用 subscriber.Notify。因此慢对端只卡自己的扇出、不卡本地
	// turn，也不会与 worker 的 out-of-lock 投递交错出乱序（worker 是唯一投递者）。
	// 不拉平的订阅保持 cursor < highWater，worker 的 flush 会照旧跳过它。
	if caughtUp {
		select {
		case publication.wake <- struct{}{}:
		default:
		}
	}
	return out, nil
}

func clampPeerPullLimit(limit int) int {
	if limit <= 0 {
		return wire.DefaultSessionPullLimit
	}
	if limit > wire.MaxSessionPullLimit {
		return wire.MaxSessionPullLimit
	}
	return limit
}

// attachPeerTranscript 把一个对端挂到这条会话的通知宇宙上,并交回它的起始高水位。
//
// running 说的是「此刻这条会话还有一轮在飞」:在飞那条消息结尾还会继续长的正文块与
// 它这一轮的消息级派生帧都还没定稿,不能在这一刻取号 —— 判据与实时发布那一侧、与
// agentred 的补齐读侧都是同一行代码(transcript.WithoutUnsettledTail)。
func (s *chatSvc) attachPeerTranscript(ctx context.Context, sessionID int64, conversationID string, running bool, subscriber PeerSessionSubscriber) (int64, func(), error) {
	publication := s.peerPublication(sessionID, conversationID)
	key := peerSubscriberKey(subscriber)
	// Holding this lock across the initial repository read makes the synthesized
	// prefix and registration one publication boundary: a live event is either
	// in 1..H or assigned after H and buffered for this subscriber.
	publication.mu.Lock()
	if !publication.initialized {
		messages, err := transcript_repo.Message().List(ctx, sessionID)
		if err != nil {
			publication.mu.Unlock()
			return 0, nil, operationFailedWithCause(ctx, err)
		}
		keyed, err := transcript.ProjectKeyedMessages(conversationID, messages)
		if err != nil {
			publication.mu.Unlock()
			return 0, nil, fmt.Errorf("synthesize desktop peer history: %w", err)
		}
		if running && len(messages) > 0 {
			keyed = transcript.WithoutUnsettledTail(keyed, messages[len(messages)-1].ID)
		}
		if err := numberPeerFramesLocked(ctx, publication, keyed); err != nil {
			publication.mu.Unlock()
			return 0, nil, operationFailedWithCause(ctx, err)
		}
		publication.initialized = true
	}
	highWater := publication.nextSeq
	subscription := &peerSessionSubscription{subscriber: subscriber, highWater: highWater}
	publication.subscribers[key] = subscription
	publication.mu.Unlock()

	var once sync.Once
	detach := func() {
		once.Do(func() {
			publication.mu.Lock()
			if publication.subscribers[key] == subscription {
				delete(publication.subscribers, key)
			}
			publication.mu.Unlock()
		})
	}
	return highWater, detach, nil
}

// numberPeerFramesLocked 给一整条转录的持久帧配编号,并把它装成这份宇宙的初始前缀。
// 调用方持 publication.mu。
//
// 编号本身归共用的那一份(transcript_repo.NumberFrames):已有编号原样沿用、存量惰性
// 补齐、按 seq 重排,两个宿主一字不差。这里只做桌面端自己的事 —— 把配好号的帧装进
// 这份内存宇宙。
func numberPeerFramesLocked(ctx context.Context, publication *peerSessionPublication, keyed []transcript.KeyedFrame) error {
	if err := transcript_repo.NumberFrames(ctx, publication.sessionID, keyed); err != nil {
		return err
	}
	publication.history = make([]wire.EventFrame, 0, len(keyed))
	publication.createtimes = make([]int64, 0, len(keyed))
	for i, frame := range keyed {
		publication.history = append(publication.history, frame.Frame)
		publication.createtimes = append(publication.createtimes, frame.Createtime)
		publication.publisher.Commit(keyed[i : i+1])
		if frame.Frame.Seq > publication.nextSeq {
			publication.nextSeq = frame.Frame.Seq
		}
	}
	return nil
}

// publishPeerEvent 把一条密封事件当作**预览帧**挂进该会话的对端通知宇宙。
//
// 预览帧只为即时呈现:不带 seq、不进日志、不参与游标推进与去重,丢失即丢失
// (spec「两级帧与补齐」)。逐 token 的生成过程因此照旧实时可见,而**同一段内容的
// 持久帧**由 publishPeerMessageFrames 在它落库之后另行发出,并带着落库的编号。
//
// 从前这里给每一帧都发一个号:一次重启之后同一份内容会被重新编号,对端的游标随即
// 指向一个宿主认不回来的号(问题 B)。
func (s *chatSvc) publishPeerEvent(sessionID int64, event agentruntime.Event) {
	if sessionID <= 0 || event == nil {
		return
	}
	value, ok := s.peerPublications.Load(sessionID)
	if !ok {
		return
	}
	publication := value.(*peerSessionPublication)
	frame := wire.EventFrame{ConversationID: publication.conversationID, Event: event, Preview: true}
	publication.mu.Lock()
	for _, subscription := range publication.subscribers {
		// Queue only: the flush worker performs the (potentially blocking) relay
		// write. Never Notify inline from a canonical event loop — a stalled
		// peer must not stall this desktop's own turn.
		enqueuePeerFrame(subscription, frame)
	}
	publication.mu.Unlock()
	select {
	case publication.wake <- struct{}{}:
	default:
	}
}

// publishPeerMessageFrames 把一条**已落库**的消息此刻投影出的持久帧发给对端。
//
// 取号与落库不可分:号从台账里取(一次事务),取到了才发布 —— 落库失败就不发,否则
// 对端会持有一个宿主认不回来的号(spec「帧编号」)。已发布过、内容没变的位置直接跳过;
// 内容变了(块被原地修补)则取一个新的末尾号,已发布的编号一律不动。
//
// final=false 是轮内 checkpoint:结尾那些还会继续长的正文块与消息级派生帧留到收口
// 再发。挑帧本身归共用的那一份(transcript.FramePublisher) —— agentred 实时发布走的
// 是同一只。
func (s *chatSvc) publishPeerMessageFrames(ctx context.Context, sessionID int64, msg *chat_entity.Message, final bool) {
	if sessionID <= 0 || msg == nil {
		return
	}
	value, ok := s.peerPublications.Load(sessionID)
	if !ok {
		return
	}
	publication := value.(*peerSessionPublication)
	publication.publishMu.Lock()
	defer publication.publishMu.Unlock()

	publication.mu.Lock()
	initialized := publication.initialized
	publication.mu.Unlock()
	// 没人 attach 过就没有编号宇宙可言:这条消息的号留给下一次 attach 惰性补齐。
	if !initialized {
		return
	}
	keyed, err := transcript.ProjectKeyedMessage(publication.conversationID, msg)
	if err != nil {
		logger.Ctx(ctx).Warn("chat_svc: project peer durable frames failed",
			zap.Int64("sessionId", sessionID), zap.Int64("messageId", msg.ID), zap.Error(err))
		return
	}
	publication.mu.Lock()
	pending := publication.publisher.Pending(keyed, final)
	publication.mu.Unlock()
	if len(pending) == 0 {
		return
	}
	keys := make([]transcript_repo.FrameKey, 0, len(pending))
	for _, frame := range pending {
		keys = append(keys, frame.Key)
	}
	seqs, err := transcript_repo.FrameSeq().Allocate(context.WithoutCancel(ctx), sessionID, keys)
	if err != nil || len(seqs) != len(pending) {
		logger.Ctx(ctx).Warn("chat_svc: allocate peer frame seq failed; frames withheld",
			zap.Int64("sessionId", sessionID), zap.Int64("messageId", msg.ID), zap.Error(err))
		return
	}
	publication.mu.Lock()
	for index := range pending {
		pending[index].Frame.Seq = seqs[index]
		publication.history = append(publication.history, pending[index].Frame)
		// 时刻取投影器配给的那一个(= 所属消息的 createtime),不是「此刻」。这一格由
		// PullPeerSession 交出去,而重启之后同一条转录是由 numberPeerFramesLocked 从
		// 投影重建的 —— 那边取的就是投影器给的值。写「此刻」会让同一个 seq 在重启前后
		// 报出两个时刻:一轮里消息建行与块定稿隔着整趟工具往返,对端那条转录的 HH:mm
		// 会在重连之后整体跳回本轮起点。时刻的归属在 transcript.ProjectMessages 说死。
		publication.createtimes = append(publication.createtimes, pending[index].Createtime)
		publication.publisher.Commit(pending[index : index+1])
		if seqs[index] > publication.nextSeq {
			publication.nextSeq = seqs[index]
		}
		for _, subscription := range publication.subscribers {
			enqueuePeerFrame(subscription, pending[index].Frame)
		}
	}
	publication.mu.Unlock()
	select {
	case publication.wake <- struct{}{}:
	default:
	}
}

// publishPeerTurnDone 在一轮收口时把这条 assistant 消息的持久帧整份发给对端订阅者。
//
// 收口那一发是 final:结尾的正文块此刻已是终稿,消息级派生帧(usage / done)也才有值。
// 对端 Peer Tab 与浏览器控制台走的是同一个共享转录投影器,那边 meta 那一行
// (模型 · 耗时 · 首字 · 速率)读的正是 done 事件上的这几格 —— 它们与重连后补齐读到的
// 是同一条,因为两边都出自 transcript.ProjectMessages。
func (s *chatSvc) publishPeerTurnDone(ctx context.Context, sessionID int64, msg *chat_entity.Message) {
	s.publishPeerMessageFrames(ctx, sessionID, msg, true)
}

func peerSubscriberKey(subscriber PeerSessionSubscriber) string {
	if keyer, ok := subscriber.(PeerSessionSubscriberKeyer); ok && keyer.PeerSessionSubscriberKey() != "" {
		return keyer.PeerSessionSubscriberKey()
	}
	value := reflect.ValueOf(subscriber)
	if value.IsValid() && value.Kind() == reflect.Pointer && !value.IsNil() {
		return fmt.Sprintf("%T:%x", subscriber, value.Pointer())
	}
	return fmt.Sprintf("%T:%v", subscriber, subscriber)
}
