package handlers

import (
	"context"
	"sync"
	"time"

	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
	"github.com/agentre-hub/agentre/internal/pkg/agenttool"
	transcriptblocks "github.com/agentre-hub/agentre/internal/pkg/transcript/blocks"
	"github.com/agentre-hub/agentre/internal/pkg/transcript/ctlbind"
	"github.com/agentre-hub/agentre/internal/pkg/transcript/turn"
	"github.com/agentre-hub/agentre/pkg/wire/devicefp"
)

// CtlSessions 是 agentred 上 agrctl 会话调用的那张表(规格 2026-09-22
// agrctl-resource-management「Executors / agentred」):
//
//   - 注入:runtime.run 为本轮 CLI 子进程签一个绑定 (agent, backend 会话键) 的会话级
//     token,连同本机 gateway 端点经 agentruntime.RegisterCtlCredentialSource 注入
//     (AGENTRE_CTL_ENDPOINT / AGENTRE_CTL_TOKEN);
//   - 归属:ctl 代理凭 token 解出会话,再按这里登记的归属转发 —— 桌面端经 runtime.run
//     交来过它自己的会话 token(DesktopCtlSession)就是桌面端拥有的,否则是控制台拥有的;
//   - 审批:控制台拥有的会话,写入的审批卡挂在这里等 toolApproval.answer 唤醒。
//
// 它是 Daemon 级的一份:RuntimeHandlers 是每条连接一个,而 CLI 子进程跨轮、跨重连复用,
// 手里那个 token 必须一直解得出来。签名密钥 per-instance(agenttool.TokenSigner),所以
// 只认本进程签的 token,也只给本进程跑过的会话签。表随会话数增长、随进程退出释放
// (与 RuntimeHandlers.sessionTokens 同一个有界口径)。
type CtlSessions struct {
	signer *agenttool.TokenSigner
	// endpoint 是注入给 CLI 的控制端点(本机 gateway base URL);空 = gateway 还没起,不注入。
	endpoint func() string

	// answerAuth 是 toolApproval.answer 的闸:作答会让 agentred 用自己的设备凭据提交写入,
	// 只收已登录账号的调用方。nil = 没接闸,一律拒绝(fail closed)。
	answerAuth func(context.Context) error

	mu       sync.Mutex
	sessions map[int64]*ctlSession // backend 会话键(runtimeSessionID) →
	waiters  map[string]ctlWaiter  // 审批卡 requestId →
}

// ctlSession 是一条会话的 ctl 归属。
type ctlSession struct {
	conversationID string
	// peer 是这条会话的发起端:桌面端拥有的会话经它的连接转回去(tunnelTargetFor),
	// 所以桌面端拥有时它固定是交来桌面 token 的那台桌面端,别的对端派发的轮次不改它。
	peer devicefp.Initiator
	// desktop 是桌面端经 runtime.run 交来的会话 token;hasDesktop=false 即控制台拥有。
	desktop    DesktopCtlSession
	hasDesktop bool
	// turn 是此刻在跑的那一轮转录;控制台拥有的会话的审批卡落在它上面。nil = 没有在跑的轮。
	turn approvalSink
	// rid 是这条会话的 backend 会话键(resolve 交出的那份拷贝上才有值)。
	rid int64
	// sends 是 turn 这一轮里 send --wait 的绑定账(占住的、在等的、说不准的调用)。换轮即
	// 清空;只在 CtlSessions.mu 下碰,resolve 交出的拷贝上恒为 nil。
	sends *ctlSendBook
}

// ctlSendBook 是一轮里 send --wait 绑定的账,让并发的几个 send 守住「不挂错卡」与「一条调用
// 最多绑一个子会话」(spec「Binding」规则 2、5)。
type ctlSendBook struct {
	// bound 是已经交给桌面端绑定子代理卡的 shell 调用。桌面端注入的 overlay 要过一跳才回到
	// 累加器里,在那之前同一条命令里的下一个 send 只能靠这张表认出它。
	bound map[string]bool
	// waiting 是此刻还在等自己那条 shell 调用出现的 send(awaitCtlSendBinding)。
	waiting map[*ctlSendWaiter]bool
	// contested 是同时是两个 send 候选的调用:它到底是谁发的说不准,不再绑给任何一个。
	contested map[string]bool
}

// ctlSendWaiter 是一个在等自己那条 shell 调用出现的 send。
type ctlSendWaiter struct{ target ctlbind.Target }

func newCtlSendBook() *ctlSendBook {
	return &ctlSendBook{bound: map[string]bool{}, waiting: map[*ctlSendWaiter]bool{}, contested: map[string]bool{}}
}

// approvalSink 是审批卡在本会话转录里的落点(*turnTranscript)。
type approvalSink interface {
	// beginApproval 把一张 pending 卡落进本轮转录并实时推出,返回本机会话 id(等待行用)。
	beginApproval(ctx context.Context, blk *transcriptblocks.ToolApprovalBlock) (int64, error)
	// resolveApproval 把卡置为终态(approved / denied / expired)并推出决议帧。
	resolveApproval(ctx context.Context, requestID, status, result string)
	// ended 在这一轮收口时关上:收口时还挂着的卡已记成 expired,此后的作答一律不算数。
	ended() <-chan struct{}
}

// ctlWaiter 是一张挂起的审批卡:只认它自己那条会话里的作答。
type ctlWaiter struct {
	conversationID string
	ch             chan bool
	// ended 是卡所在那一轮的收口信号;关上之后这张卡不再接受作答。
	ended <-chan struct{}
}

// NewCtlSessions 造一张空表;endpoint 每次签发时现取(gateway 端口晚绑定)。
func NewCtlSessions(endpoint func() string) *CtlSessions {
	return &CtlSessions{
		signer:   agenttool.NewTokenSigner(),
		endpoint: endpoint,
		sessions: map[int64]*ctlSession{},
		waiters:  map[string]ctlWaiter{},
	}
}

// WithAnswerAuth 装上 toolApproval.answer 的调用方闸(生产是 RequireLoggedInAccount)。
func (c *CtlSessions) WithAnswerAuth(auth func(context.Context) error) *CtlSessions {
	c.answerAuth = auth
	return c
}

// Credentials 是注册给 agentruntime.RegisterCtlCredentialSource 的签发函数:sessionID 是
// 本轮 RunRequest.SessionID(backend 会话键)。本机没跑过的会话、gateway 未起时为零值。
func (c *CtlSessions) Credentials(agentID, sessionID int64) agentruntime.CtlCredentials {
	if c == nil || c.endpoint == nil {
		return agentruntime.CtlCredentials{}
	}
	endpoint := c.endpoint()
	if endpoint == "" {
		return agentruntime.CtlCredentials{}
	}
	c.mu.Lock()
	_, known := c.sessions[sessionID]
	c.mu.Unlock()
	if !known {
		return agentruntime.CtlCredentials{}
	}
	return agentruntime.CtlCredentials{Endpoint: endpoint, Token: c.signer.MintToken(agentID, sessionID)}
}

// bind 由 runtime.run 在交给 backend 之前调用:登记这条会话与它此刻的归属。不带桌面
// token 的一轮(控制台在桌面端的会话上派发)不抹掉已有的桌面归属 —— token 绑定的是
// 会话,不是轮次(与 recordDesktopCtl 同一条规则)。
func (c *CtlSessions) bind(rid int64, peer devicefp.Initiator, conversationID string, desktop DesktopCtlSession, hasDesktop bool) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	s := c.sessions[rid]
	if s == nil {
		s = &ctlSession{}
		c.sessions[rid] = s
	}
	s.conversationID = conversationID
	switch {
	case hasDesktop:
		// 桌面 token 与交来它的那台桌面端成对记:之后只经它的连接转回去。
		s.desktop, s.hasDesktop, s.peer = desktop, true, peer
	case !s.hasDesktop:
		s.peer = peer
	}
}

// detachTurn 在一轮收口时放掉它(仍是它时):收口的一轮不再是审批卡的落点,也不该被
// 会话表一直攥着整轮的累积状态。
func (c *CtlSessions) detachTurn(rid int64, sink approvalSink) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if s := c.sessions[rid]; s != nil && s.turn == sink {
		s.turn, s.sends = nil, nil
	}
}

// attachTurn 把这一轮的转录登记成审批卡的落点(每轮开头,后一轮顶掉前一轮)。
func (c *CtlSessions) attachTurn(rid int64, sink approvalSink) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if s := c.sessions[rid]; s != nil {
		s.turn, s.sends = sink, nil
	}
}

// resolve 验签并解出 token 绑定的会话。验不过、或会话不是本机登记的,一律 !ok。
func (c *CtlSessions) resolve(token string) (ctlSession, bool) {
	if c == nil || token == "" {
		return ctlSession{}, false
	}
	ref, ok := c.signer.Lookup(token)
	if !ok {
		return ctlSession{}, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	s := c.sessions[ref.SessionID]
	if s == nil {
		return ctlSession{}, false
	}
	out := *s
	out.rid, out.sends = ref.SessionID, nil
	return out, true
}

// beginWait 挂起一张卡,返回作答 channel(true = 批准)。ended 是卡所在那一轮的收口信号。
func (c *CtlSessions) beginWait(conversationID, requestID string, ended <-chan struct{}) <-chan bool {
	ch := make(chan bool, 1)
	c.mu.Lock()
	c.waiters[requestID] = ctlWaiter{conversationID: conversationID, ch: ch, ended: ended}
	c.mu.Unlock()
	return ch
}

// endWait 撤下一张卡(超时 / 调用方断开 / 登记失败);此后的作答一律「不挂起」。
func (c *CtlSessions) endWait(requestID string) {
	c.mu.Lock()
	delete(c.waiters, requestID)
	c.mu.Unlock()
}

// ── agrctl send --wait 的子代理卡绑定(远端父会话)────────────────────────────

// 不绑定的原因里由本代理判出的那几种(其余取 ctlbind.Reason)。
const (
	ctlSendNoWait          = "no_wait"
	ctlSendExistingSession = "existing_session"
	ctlSendNoActiveTurn    = "no_active_turn"
)

// ctlSendTurn 是绑定在途那一轮里要看的东西(*turnTranscript):外层工具调用,以及已经被
// agrctl overlay 占住的调用。changed 在这一轮此后每见到一条外层工具调用时关上(在等的
// send 据此重新匹配);live=false 表示这一轮已经收口。
type ctlSendTurn interface {
	ctlSendCandidates() (calls []turn.OuterToolUse, bound map[string]bool, changed <-chan struct{}, live bool)
}

// ctlSendCandidates 在 t.mu 下取这一轮当前那一段的外层调用与已绑定的调用(与桌面端
// chat_svc.bindCtlSend 同一个口径:只看当前累加器),连同下一条外层调用的到达信号。
func (t *turnTranscript) ctlSendCandidates() ([]turn.OuterToolUse, map[string]bool, <-chan struct{}, bool) {
	if t == nil {
		return nil, nil, nil, false
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.finished {
		return nil, nil, nil, false
	}
	if t.outerCallSeen == nil {
		t.outerCallSeen = make(chan struct{})
	}
	return t.acc.OuterToolUses(), ctlbind.BoundToolCallIDs(t.acc.Snapshot()), t.outerCallSeen, true
}

// awaitCtlSendBinding 是转发一次 send --wait 之前的绑定:当场匹配,只有「这一轮里还没有
// 候选」时才在 wait 之内等 —— Codex 的 command_execution 在 send 到达之后才开始(运行时
// 验证 V10),那条调用晚到一步不能就此丢了卡。这一轮每见到一条外层调用就重新匹配一次:
// 命中即交回;到期、这一轮收口、调用方断开时交回最后一次的原因,照不绑定转发。有歧义、
// 已绑过的 send 当场交回,不等。到达时那一次按 ctlbind.Match;等着时重新匹配按
// ctlbind.MatchPending —— 先登记进来的可能是另一个发往同一 agent 的 send 的调用,只认命令
// 里有自己任务文本的那条。
func (c *CtlSessions) awaitCtlSendBinding(ctx context.Context, owner ctlSession, target ctlbind.Target, wait time.Duration) (ctlbind.Binding, string) {
	self := &ctlSendWaiter{target: target}
	defer c.forgetCtlSendWaiter(owner, self)
	binding, reason, changed := c.bindCtlSend(owner, target, self, ctlbind.Match)
	if reason != string(ctlbind.ReasonNoCandidate) {
		return binding, reason
	}
	timer := time.NewTimer(wait)
	defer timer.Stop()
	for {
		select {
		case <-changed:
		case <-owner.turn.ended():
			// 下一次匹配看到这一轮已收口,交回 no_active_turn。
		case <-timer.C:
			return binding, reason
		case <-ctx.Done():
			return binding, reason
		}
		binding, reason, changed = c.bindCtlSend(owner, target, self, ctlbind.MatchPending)
		if reason != string(ctlbind.ReasonNoCandidate) {
			return binding, reason
		}
	}
}

// bindCtlSend 在 owner 那一轮里匹配发出这次 send 的 shell 调用并占住它(spec「Binding」:
// 父轮的宿主负责绑定),按 match 判定。reason 非空 = 不绑定;reason 为 no_candidate 时 self
// 记进这一轮的等待名单,changed 是下一条外层调用的到达信号。
//
// 转录锁与会话表的锁不嵌套:先在 t.mu 下取候选,再在 c.mu 下核对这一轮仍是当前轮、候选
// 取出之后没有新的外层调用到达(到达了就重取 —— 歧义按此刻的全部调用判),并与已占住的调用、
// 别的在等的 send 一起判定、占位。命中的调用若也是别的在等的 send 会认的(MatchPending),它
// 是谁发的说不准:记成 contested,两边都不绑(Hard invariant「不挂错卡」)。
func (c *CtlSessions) bindCtlSend(owner ctlSession, target ctlbind.Target, self *ctlSendWaiter, match ctlSendMatcher) (ctlbind.Binding, string, <-chan struct{}) {
	src, ok := owner.turn.(ctlSendTurn)
	if !ok {
		return ctlbind.Binding{}, ctlSendNoActiveTurn, nil
	}
	for {
		calls, bound, changed, live := src.ctlSendCandidates()
		if !live {
			return ctlbind.Binding{}, ctlSendNoActiveTurn, nil
		}
		c.mu.Lock()
		if isClosed(changed) {
			c.mu.Unlock()
			continue
		}
		binding, reason := c.settleCtlSendLocked(owner, calls, bound, target, self, match)
		c.mu.Unlock()
		return binding, reason, changed
	}
}

// ctlSendMatcher 是 ctlbind.Match / ctlbind.MatchPending。
type ctlSendMatcher func(calls []turn.OuterToolUse, bound map[string]bool, target ctlbind.Target) (ctlbind.Binding, ctlbind.Reason)

// settleCtlSendLocked 是 bindCtlSend 在 c.mu 下的那一段。
func (c *CtlSessions) settleCtlSendLocked(owner ctlSession, calls []turn.OuterToolUse, bound map[string]bool, target ctlbind.Target, self *ctlSendWaiter, match ctlSendMatcher) (ctlbind.Binding, string) {
	s := c.sessions[owner.rid]
	if s == nil || s.turn != owner.turn {
		return ctlbind.Binding{}, ctlSendNoActiveTurn
	}
	if s.sends == nil {
		s.sends = newCtlSendBook()
	}
	book := s.sends
	// 只有此刻仍落在「没有候选」上的 send 才算在等:有了结论的,不再与别的 send 争调用。
	delete(book.waiting, self)
	for id := range book.bound {
		bound[id] = true
	}
	binding, reason := match(calls, bound, target)
	switch {
	case reason == ctlbind.ReasonNoCandidate:
		book.waiting[self] = true
		return ctlbind.Binding{}, string(reason)
	case reason != "":
		return ctlbind.Binding{}, string(reason)
	case book.contested[binding.ToolCallID] || book.claimedByAnotherWaiter(calls, binding.ToolCallID, self):
		book.contested[binding.ToolCallID] = true
		return ctlbind.Binding{}, string(ctlbind.ReasonAmbiguous)
	}
	book.bound[binding.ToolCallID] = true
	return binding, ""
}

// claimedByAnotherWaiter:toolCallID 那条调用是不是另一个在等的 send 会认的(它重新匹配时
// 按 MatchPending:命令里没有它任务文本的调用不是它的)。
func (b *ctlSendBook) claimedByAnotherWaiter(calls []turn.OuterToolUse, toolCallID string, self *ctlSendWaiter) bool {
	var call []turn.OuterToolUse
	for _, c := range calls {
		if c.ID == toolCallID {
			call = append(call, c)
		}
	}
	for w := range b.waiting {
		if w == self {
			continue
		}
		if _, reason := ctlbind.MatchPending(call, nil, w.target); reason == "" {
			return true
		}
	}
	return false
}

// forgetCtlSendWaiter 把 self 移出它那一轮的等待名单(已换轮时那张账早已作废,无事可做)。
func (c *CtlSessions) forgetCtlSendWaiter(owner ctlSession, self *ctlSendWaiter) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if s := c.sessions[owner.rid]; s != nil && s.turn == owner.turn && s.sends != nil {
		delete(s.sends.waiting, self)
	}
}

// unbindCtlSend 放掉一次没有成交的占位(桌面端拒绝了这次 send,规则 4)。桌面端若已把
// overlay 注入进那一轮,累加器里的 overlay 照样挡住重绑。
func (c *CtlSessions) unbindCtlSend(owner ctlSession, toolCallID string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if s := c.sessions[owner.rid]; s != nil && s.turn == owner.turn && s.sends != nil {
		delete(s.sends.bound, toolCallID)
	}
}
