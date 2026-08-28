package remote

import (
	"context"
	"fmt"
	"sync"

	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"

	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/runtimes/remote/wire"
	"github.com/agentre-hub/agentre/internal/pkg/orderedpipe"
)

// autoSession 是某 chat session 的「自主续轮」(AutonomousTurnSource)本地镜像。
// out 是 AutonomousTurns() 返回给 chat_svc watcher 的 channel;cur 是当前在飞的一轮
// (daemon 串行转发,任一时刻至多一轮)。按 sessionID 持久,跨 turn / 子进程 evict 复用,
// conn close 时由 closeAllAutoSessions 统一拆。
type autoSession struct {
	id int64
	// out 交付给 chat_svc watcher 的自主续轮。走 orderedpipe 而不是有界 channel:
	// 投递方是 protorpc 读循环,它绝不能因为「消费方还没接上」而停下,见 Push 处。
	out *orderedpipe.Pipe[agentruntime.AutonomousTurn]

	mu     sync.Mutex
	cur    *autoTurn
	closed bool
}

// autoTurn 一轮自主续轮:events 是事件流(daemon 的 AutonomousTurnEvent 路由进来),
// result 在 Done 帧到达时填好、events close 前可见(与 Run 的 RunResult 契约一致)。
type autoTurn struct {
	// events 这一轮的事件流。同样走 orderedpipe,理由同 autoSession.out。
	events *orderedpipe.Pipe[agentruntime.Event]
	result *agentruntime.RunResult
	// catchUp 这一轮是**补齐合成**的:内容来自 daemon 通知日志的重放,不是 daemon
	// 宣告的自主续轮。两者对上层是同一种东西(一轮没有 user 行的 assistant 轮),
	// 差别只在被别的一轮顶掉时该不该算作「被打断」——补齐轮的内容按重放区间天然
	// 完整,自主续轮被顶掉则是真的没跑完。
	catchUp bool
}

// openTurnLocked 在该会话上开一轮并投给消费方。调用方必须持 a.mu。
//
// 手上已有在飞的一轮时**先收尾它**:少了这一步,a.cur 被无声覆盖,旧那一轮的 events
// 永远关不掉 —— 上层 driveAutonomousTurn 卡在 `for ev := range at.Events` 上不返回,
// 它那条 assistant 消息永久停在 running(界面上一张永远转圈的卡片),旁边又多出一张
// 新卡片。App 重启后「回放一整段历史」是常态,这一幕随之从边角变成常见路径。
//
// 投给消费方走 orderedpipe.Push:永不阻塞。这是硬要求而不是优化 —— 调用它的是
// protorpc 读循环,而读循环停下,这条连接上所有会话的通知与所有在飞 RPC 的应答会
// 一起停。旧实现在一个 4 格 channel 上做阻塞发送,消费方还没接上(App 刚起来、
// watcher 未就位)第 5 轮就能把整条连接焊死,而且是**持着 a.mu** 焊死的 ——
// 连断连清理(closeAllAutoSessions 也要 a.mu)都拆不开这个死结。
func (a *autoSession) openTurnLocked(trigger string, catchUp bool, turnToken uint64) *autoTurn {
	if a.closed {
		return nil
	}
	if a.cur != nil {
		// 补齐轮的内容按重放区间天然完整,被下一轮顶掉不是「被打断」;自主续轮的
		// done 没到就被顶掉,才是真的截断,理由必须带出去,否则上层会把一条半截的
		// 回答按「正常跑完」落库。
		var cause error
		if !a.cur.catchUp {
			cause = ErrRunInterrupted
		}
		a.finishCurLocked(cause)
	}
	turn := &autoTurn{
		events:  orderedpipe.New[agentruntime.Event](),
		result:  &agentruntime.RunResult{},
		catchUp: catchUp,
	}
	a.cur = turn
	a.out.Push(agentruntime.AutonomousTurn{
		Events:    turn.events.Out(),
		Result:    turn.result,
		Trigger:   trigger,
		TurnToken: turnToken,
	})
	return turn
}

// finishCurLocked 收尾手上这一轮:写终止理由(已经有理由时不覆盖)、close events、
// 腾出 a.cur。调用方必须持 a.mu。
func (a *autoSession) finishCurLocked(cause error) {
	cur := a.cur
	a.cur = nil
	if cur == nil {
		return
	}
	if cause != nil && cur.result != nil && cur.result.StopErr == nil {
		cur.result.StopErr = cause
	}
	cur.events.Close()
}

// AutonomousTurns 实现 agentruntime.AutonomousTurnSource:返回该 session 的自主续轮
// channel。惰性创建 autoSession —— 即便 Started 帧先于本调用到达(理论上不会,自主轮
// 总在 Run 收尾后才发),handleAutonomousTurnStarted 也会把同一个 autoSession 建好,
// 两边拿到同一个 out。
func (r *Runtime) AutonomousTurns(sessionID int64) <-chan agentruntime.AutonomousTurn {
	return r.getOrCreateAutoSession(sessionID).out.Out()
}

func (r *Runtime) getOrCreateAutoSession(sid int64) *autoSession {
	r.mu.Lock()
	defer r.mu.Unlock()
	if a, ok := r.autoSessions[sid]; ok {
		return a
	}
	a := &autoSession{id: sid, out: orderedpipe.New[agentruntime.AutonomousTurn]()}
	r.autoSessions[sid] = a
	return a
}

func (r *Runtime) lookupAutoSession(sid int64) *autoSession {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.autoSessions[sid]
}

func (r *Runtime) handleAutonomousTurnStarted(ctx context.Context, params any) (any, error) {
	frame, ok := notificationFrameOf[wire.AutonomousTurnStartedFrame](ctx, "remote.handleAutonomousTurnStarted", params)
	if !ok {
		return nil, nil
	}
	a := r.getOrCreateAutoSession(frame.SessionID)
	logger.Ctx(ctx).Info("remote runtime: autonomous turn started",
		zap.Int64("sid", frame.SessionID), zap.String("trigger", frame.Trigger))
	// a.mu 保护的是 a.cur 的读改写。投递本身走 pipe,已 Close 之后再 Push 只是丢弃,
	// 所以断连(watchClose 独立 goroutine)与投递并发不再有 send-on-closed-channel
	// 之虞,持锁期间也不会阻塞。
	a.mu.Lock()
	defer a.mu.Unlock()
	a.openTurnLocked(frame.Trigger, false, frame.TurnToken)
	return nil, nil
}

// deliverToCatchUpTurn 把一条**没有在飞轮次可收**的事件交给该会话的补齐轮(没有就
// 现开一轮)。返回是否收下了。
//
// 这是补齐重放在「本进程内一轮都没在跑」时的唯一落点:App 重启后既没有 Run 起来的
// 会话、也没有 events channel,而重放的内容必须进得了转录。合成一轮交给上层,是因为
// 上层已经有一条把「没有 user 行的一轮」落成 assistant 消息的成熟路径
// (chat_svc.driveAutonomousTurn),补齐不必再造第二套持久化。
//
// 手上已经有在飞的一轮(自主续轮,或上一段补齐开的)就直接投进去:内容落在用户看得
// 见的那张卡片上,总好过丢掉。
func (r *Runtime) deliverToCatchUpTurn(ctx context.Context, sid int64, ev agentruntime.Event) bool {
	a := r.getOrCreateAutoSession(sid)
	// 送出与 close 都在 a.mu 下(保护 a.cur),理由同 handleAutonomousTurnEvent。
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closed {
		return false
	}
	if a.cur == nil {
		logger.Ctx(ctx).Info("remote runtime: opening catch-up turn for replayed content",
			zap.Int64("sid", sid))
		if a.openTurnLocked(TriggerCatchUp, true, 0) == nil {
			return false
		}
	}
	a.cur.events.Push(ev)
	return true
}

// closeCatchUpTurn 收尾补齐轮:重放到一条不属于任何在飞轮次的终态帧,说明这一段
// 补齐所对应的那一轮到此为止,把终态写进它的 RunResult 再 close。
//
// 只收补齐轮:在飞的自主续轮有它自己的 autonomousTurn.done 收尾,拿一条 per-Run 的
// 终态帧把它关掉会让它凭空少半截。
func (r *Runtime) closeCatchUpTurn(ctx context.Context, frame wire.RunResultDoneFrame) {
	a := r.lookupAutoSession(frame.SessionID)
	if a == nil {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.cur == nil || !a.cur.catchUp {
		return
	}
	a.cur.result.ProviderSessionID = frame.ProviderSessionID
	a.cur.result.UserAnchor = frame.UserAnchor
	a.cur.result.Model = frame.Model
	a.cur.result.ContextWindow = frame.ContextWindow
	a.cur.result.TurnToken = frame.TurnToken
	if frame.Usage != nil {
		a.cur.result.Usage = usageFromWire(frame.Usage)
	}
	a.cur.result.StopErr = stopErrFromFrame(frame)
	logger.Ctx(ctx).Info("remote runtime: catch-up turn finished",
		zap.Int64("sid", frame.SessionID), zap.String("model", frame.Model))
	a.finishCurLocked(nil)
}

func (r *Runtime) handleAutonomousTurnEvent(ctx context.Context, params any) (any, error) {
	frame, ok := notificationFrameOf[wire.EventFrame](ctx, "remote.handleAutonomousTurnEvent", params)
	if !ok {
		return nil, nil
	}
	a := r.lookupAutoSession(frame.SessionID)
	if a == nil {
		return nil, nil
	}
	ev := frame.Event
	// 投递走 orderedpipe.Push:永不阻塞,且 Close 之后再 Push 只是丢弃 —— 断连
	// (watchClose 独立 goroutine)与投递并发既不 panic 也不需要靠锁排开。
	//
	// 「不阻塞」在这里是硬要求:调用者是 protorpc 读循环,它同时还负责把 RPC 应答
	// 交回等待方。旧实现在 64 格 channel 上阻塞发送,消费方(driveAutonomousTurn)
	// 只要没跟上 —— 比如正卡在一次落库上 —— 读循环就停,而它一停,消费方在等的那个
	// RPC 应答也回不来:闭环。runtime.go 的 per-Run handleEvent 早就写明了同一件事。
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closed || a.cur == nil {
		logger.Ctx(ctx).Warn("remote runtime: autonomousTurn event with no active turn — dropped",
			zap.Int64("sid", frame.SessionID), zap.String("eventType", fmt.Sprintf("%T", ev)))
		return nil, nil
	}
	a.cur.events.Push(ev)
	return nil, nil
}

func (r *Runtime) handleAutonomousTurnDone(ctx context.Context, params any) (any, error) {
	frame, ok := notificationFrameOf[wire.RunResultDoneFrame](ctx, "remote.handleAutonomousTurnDone", params)
	if !ok {
		return nil, nil
	}
	a := r.lookupAutoSession(frame.SessionID)
	if a == nil {
		return nil, nil
	}
	a.mu.Lock()
	cur := a.cur
	a.cur = nil
	a.mu.Unlock()
	if cur == nil {
		return nil, nil
	}
	cur.result.ProviderSessionID = frame.ProviderSessionID
	cur.result.Model = frame.Model
	cur.result.ContextWindow = frame.ContextWindow
	cur.result.TurnToken = frame.TurnToken
	if frame.Usage != nil {
		cur.result.Usage = usageFromWire(frame.Usage)
	}
	cur.result.StopErr = stopErrFromFrame(*frame)
	logger.Ctx(ctx).Info("remote runtime: autonomous turn done",
		zap.Int64("sid", frame.SessionID), zap.String("model", frame.Model))
	cur.events.Close()
	return nil, nil
}

// closeAllAutoSessions 在 conn close(watchClose)时把所有自主续轮镜像拆掉:
// close 每个 out → chat_svc watcher 的 `for range` 退出;在飞的那轮 events 也 close,
// 让 driveAutonomousTurn 收尾。cause 是这一轮的终止理由,见 closeAutoSession。幂等。
func (r *Runtime) closeAllAutoSessions(cause error) {
	r.mu.Lock()
	all := make([]*autoSession, 0, len(r.autoSessions))
	for sid, a := range r.autoSessions {
		all = append(all, a)
		delete(r.autoSessions, sid)
	}
	r.mu.Unlock()
	for _, a := range all {
		closeAutoSession(a, cause)
	}
}

// closeAutoSession 拆掉一个自主续轮镜像。幂等。
//
// cause 是**在飞那一轮**的终止理由(ErrRunInterrupted / ErrDaemonDisconnected),写进
// 它的 RunResult.StopErr —— 与 per-Run 的 closeSessionWithErr 同一条纪律。少了它,
// chat_svc.driveAutonomousTurn 会把一条被截断的助手消息当作正常跑完的轮次落库:
// errorText 空、会话翻 idle,用户看到一条戛然而止却「成功」的回答。
// 终态帧已经到过(handleAutonomousTurnDone 填过 StopErr)时不覆盖。
func closeAutoSession(a *autoSession, cause error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closed {
		return
	}
	a.closed = true
	a.finishCurLocked(cause)
	a.out.Close()
}
