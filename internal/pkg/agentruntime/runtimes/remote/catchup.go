// Package remote — catchup.go 是**桌面 App 重启后**的补齐入口。
//
// reconnect.go 管的是「连接断了又接上」:那时本进程里还有在飞的一轮,补齐的范围就是
// 它。App 重启后没有任何一轮在跑 —— 会话在库里、日志在 daemon 上、本进程两手空空,
// 而用户故事恰恰是「退出桌面 App 后下次打开看到这段时间发生的全部内容」。
//
// 所以补齐要能由**外部点名**发起:上层(chat_svc)按 chat_sessions.exec_device_id
// 找出「这些会话跑在那台 daemon 上」,把会话号交进来,本文件走规格的三步走 ——
// 先取清单与状态,再逐条按游标拉增量并按序重放,最后取待决策清单。
//
// 重放出来的内容没有在飞的一轮可交付,落点是**合成的一轮**(TriggerCatchUp),经既有
// 的自主续轮通道交给上层落成一条没有 user 行的 assistant 消息(见 autoturn.go 的
// deliverToCatchUpTurn)。补齐因此不必自建第二套持久化路径。
package remote

import (
	"context"
	"errors"

	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"

	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/runtimes/remote/wire"
)

// TriggerCatchUp 标记一轮是**补齐合成**出来的:内容来自 daemon 通知日志的重放,既不是
// 用户在本进程里发起的那一轮,也不是 backend 自主起的续轮。上层据此给出措辞。
const TriggerCatchUp = "catchup"

// CatchUpSessions 对点名的一组会话跑补齐三步,不要求本进程内有任何一轮在飞。
//
// 第一步的会话清单是**每次一问**而不是每条会话一问:重启后本地可能记着几十条老会话
// 都在同一台 daemon 上,而其中绝大多数一条新通知都没有。清单一次就把「谁落下了内容、
// 谁已经被 daemon 重启标成中断」问清楚,后两步只对真的需要的那几条发。
//
// 交回 live:清单上 daemon 报「还在跑 / 正等着用户拍板」的那些会话。它是给调用方判
// **本地那一行该不该收尾**用的 —— 桌面 App 重启后,库里的 running 既可能是这台 daemon
// 上真的还在跑,也可能是上一个进程留下的遗孤,而只有 daemon 分得清。有内容可重放的会话
// 由重放自己写终态;一个字都没产出的会话没有任何东西会改写它,这份名单是它唯一的判据
// (见 chat_svc.CatchUpRemoteSessions)。出错时 live 为空:什么都不知道就按今天的语义
// 全部收尾,而不是把它们永远留在 running 上。
func (r *Runtime) CatchUpSessions(ctx context.Context, sessionIDs []int64) (live []int64, err error) {
	if len(sessionIDs) == 0 {
		return nil, nil
	}
	summaries, err := r.sessionSummaries(ctx)
	if err != nil {
		return nil, err
	}
	need := make([]int64, 0, len(sessionIDs))
	live = make([]int64, 0, len(sessionIDs))
	for _, sid := range sessionIDs {
		sum, known := summaries[sid]
		if !known {
			// 不在这台 daemon 的清单里:它属于别的对端,或已被 daemon 的重启清扫掉。
			// 连 attach 都不该发。
			logger.Ctx(ctx).Info("remote runtime: session not on this daemon, skipping catch-up",
				zap.Int64("sid", sid))
			continue
		}
		if sum.LifecycleState == wire.SessionLifecycleInterrupted {
			r.catchUpInterrupted(ctx, sid, sum)
			continue
		}
		if liveOnDaemon(sum) {
			live = append(live, sid)
		}
		if !r.needsCatchUp(ctx, sid, sum) {
			continue
		}
		need = append(need, sid)
	}
	// 先登记再补齐:补齐过程中断线,watchClose 要认得这些会话才会重连接着补,而不是
	// 判成「本进程没有在飞的一轮」直接收工、此后再也接不回去。
	r.trackSessions(need)
	logger.Ctx(ctx).Info("remote runtime: catching up sessions after restart",
		zap.Int("requested", len(sessionIDs)), zap.Int("pulling", len(need)),
		zap.Int("liveOnDaemon", len(live)))
	err = r.catchUpEach(ctx, need)
	// 补完就收摊 —— 但只收 daemon 说不在跑的那些。放在这里(而不是 defer 到每条返回
	// 路径)是有意的:清单都没问到时手上没有任何判据,把还在流的会话镜像关掉会让上层
	// 把一条半截的回答按「正常跑完」落库。
	r.releaseCaughtUp(sessionIDs, live)
	return live, err
}

// releaseCaughtUp 把「补齐做完、daemon 那侧也不会再有推送」的会话就地收摊:摘掉补齐
// 登记、关掉并摘掉自主轮镜像(上层挂在 out 上的 watcher goroutine 随之退出)。
//
// 不收摊的代价按会话数累加,且到进程退出都不还:tracked 只在 failSession 删,hasLiveRun()
// 于是永远为真 —— 此后每次断连都要为早就结束的会话跑完整退避 + 重接管;autoSessions 条目
// 只由 closeAllAutoSessions 关,于是每条补齐过的会话常驻一个 goroutine,还撑大
// liveSessionIDs(),让之后每次重连把它们全部重新接管。
//
// 两类会话一条都不碰:daemon 说还在跑/等拍板的(推送还要落在这里,断线也必须重连接回去),
// 以及本进程里正跑着一轮的(那一轮的收尾归 Run 自己)。
//
// 终止理由是 nil:daemon 说这条会话没在跑了,补齐的内容按重放区间天然完整 —— 说
// 「被打断」或「连不上了」都是错的(R15 靠这个哨兵分文案)。
func (r *Runtime) releaseCaughtUp(all, live []int64) {
	liveSet := make(map[int64]struct{}, len(live))
	for _, sid := range live {
		liveSet[sid] = struct{}{}
	}
	r.mu.Lock()
	autos := make([]*autoSession, 0, len(all))
	for _, sid := range all {
		if _, isLive := liveSet[sid]; isLive {
			continue
		}
		if _, running := r.sessions[sid]; running {
			continue
		}
		delete(r.tracked, sid)
		if a, ok := r.autoSessions[sid]; ok {
			autos = append(autos, a)
			delete(r.autoSessions, sid)
		}
	}
	r.mu.Unlock()
	for _, a := range autos {
		closeAutoSession(a, nil)
	}
}

// needsCatchUp 清单里的 LatestSeq 与本地游标一比就知道这条会话落下了多少条。一条都
// 没落下就不必 attach + pull —— 那是两次白跑的往返,而重启后本地记着同一台 daemon 的
// 老会话可能有几十条。
//
// 两种例外:
//   - 还在跑的会话必须接管,哪怕此刻一条都没落下 —— attach 认领的是**推送流**,不接
//     管的话它接下来产生的通知在 daemon 那侧没有目标,只落库不推送,用户盯着一条正在
//     跑的会话却一个字都等不到。
//   - 正在等输入的会话:它的宣告事件可能早在游标之前就交付过(上次 App 是在它提问
//     之后才关的),日志里没有新行,但待决策清单必须重新枚举一遍,否则用户回来看到
//     的是一条静止的会话,而远端正在等他拍板。
func (r *Runtime) needsCatchUp(ctx context.Context, sid int64, sum wire.SessionSummary) bool {
	if liveOnDaemon(sum) {
		return true
	}
	ss := r.syncFor(ctx, sid)
	if sum.LatestSeq > ss.cursorNow() {
		return true
	}
	logger.Ctx(ctx).Debug("remote runtime: session already caught up",
		zap.Int64("sid", sid), zap.Int64("latestSeq", sum.LatestSeq))
	return false
}

// liveOnDaemon 报告 daemon 此刻是不是还为这条会话在做事:一轮正在跑,或者一轮停在
// 「等用户拍板」上。两者都不是终态 —— 前者随时还会出字,后者在等一个只有用户能给的答案。
func liveOnDaemon(sum wire.SessionSummary) bool {
	return sum.LifecycleState == wire.SessionLifecycleRunning || sum.WaitingForInput
}

// catchUpInterrupted 中断态会话(daemon 进程重启后按 R10 标的终态):没有实时推送可
// 接管、不能续跑,但**中断前的历史仍要补回来** —— 规格明写「能读到中断前的全部历史」。
//
// 所以跳过 attach(接管的是推送目标,而这条会话已经没有推送了)直接按游标拉,拉完
// 按中断收尾:失败理由用 ErrRunInterrupted 而不是 ErrDaemonDisconnected,连接明明是
// 通的,说「连不上了」是错的(R15 要求这两种情形由文案区分)。
func (r *Runtime) catchUpInterrupted(ctx context.Context, sid int64, sum wire.SessionSummary) {
	ss, pinned, err := r.pinCursorFor(ctx, sid)
	if err == nil {
		r.dropCursorAboveHighWater(ctx, sid, ss, pinned, sum.LatestSeq)
		var replayed int
		if replayed, err = r.pullUntilCaughtUp(ctx, sid, ss); err == nil {
			logger.Ctx(ctx).Info("remote runtime: interrupted session history caught up",
				zap.Int64("sid", sid), zap.Int("replayed", replayed))
		}
	}
	if err != nil && !errors.Is(err, errSessionUnrecoverable) {
		logger.Ctx(ctx).Warn("remote runtime: interrupted session history pull failed",
			zap.Int64("sid", sid), zap.Error(err))
	}
	// 收尾:补齐轮(若还开着)拿到 ErrRunInterrupted,连接态转 lost,补齐登记一并摘掉。
	r.failSession(sid)
}

// trackSessions 登记「本进程没有轮次在跑、但要为之补齐」的会话。
func (r *Runtime) trackSessions(sids []int64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.tracked == nil {
		r.tracked = make(map[int64]struct{}, len(sids))
	}
	for _, sid := range sids {
		r.tracked[sid] = struct{}{}
	}
}
