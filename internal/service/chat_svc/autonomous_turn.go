package chat_svc

import (
	"context"

	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/chat_entity"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
	"github.com/agentre-hub/agentre/internal/repository/chat_repo"
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

	t := &autonomousTurnRun{
		svc:       s,
		sessionID: sessionID,
		be:        be,
		at:        at,
		sess:      sess,
		first:     first,
		hasFirst:  hasFirst,
		prelude:   prelude,
	}
	if err := t.persistTurnMessages(ctx); err != nil {
		logger.Ctx(ctx).Error("chat_svc: driveAutonomousTurn persist assistant failed",
			zap.Int64("sessionId", sessionID), zap.Error(err))
		s.failAutonomousTurnPersist(ctx, sessionID, sess, be, err, at.Events, at.TurnToken)
		return
	}

	t.emitStarted(ctx)
	t.initSegment()
	t.consumeEvents(ctx)
	t.finalize(ctx)
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
