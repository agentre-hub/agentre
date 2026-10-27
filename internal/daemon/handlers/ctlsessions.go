package handlers

import (
	"context"
	"sync"

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
	// bound 是 turn 这一轮里已经交给桌面端绑定子代理卡的 shell 调用(spec「Binding」规则 5:
	// 一条调用最多绑一个子会话)。桌面端注入的 overlay 要过一跳才回到累加器里,在那之前
	// 同一条命令里的下一个 send 只能靠这张表认出它。换轮即清空;只在 CtlSessions.mu 下碰。
	bound map[string]bool
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
		s.turn, s.bound = nil, nil
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
		s.turn, s.bound = sink, nil
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
	out.rid, out.bound = ref.SessionID, nil
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
// agrctl overlay 占住的调用。live=false 表示这一轮已经收口。
type ctlSendTurn interface {
	ctlSendCandidates() (calls []turn.OuterToolUse, bound map[string]bool, live bool)
}

// ctlSendCandidates 在 t.mu 下取这一轮当前那一段的外层调用与已绑定的调用(与桌面端
// chat_svc.bindCtlSend 同一个口径:只看当前累加器)。
func (t *turnTranscript) ctlSendCandidates() ([]turn.OuterToolUse, map[string]bool, bool) {
	if t == nil {
		return nil, nil, false
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.finished {
		return nil, nil, false
	}
	return t.acc.OuterToolUses(), ctlbind.BoundToolCallIDs(t.acc.Snapshot()), true
}

// bindCtlSend 在 owner 那一轮里匹配发出这次 send 的 shell 调用并占住它(spec「Binding」:
// 父轮的宿主负责绑定)。reason 非空 = 不绑定。转录锁与会话表的锁不嵌套:先在 t.mu 下取
// 候选,再在 c.mu 下核对这一轮仍是当前轮、并与已占住的调用一起匹配、占位。
func (c *CtlSessions) bindCtlSend(owner ctlSession, target ctlbind.Target) (ctlbind.Binding, string) {
	src, ok := owner.turn.(ctlSendTurn)
	if !ok {
		return ctlbind.Binding{}, ctlSendNoActiveTurn
	}
	calls, bound, live := src.ctlSendCandidates()
	if !live {
		return ctlbind.Binding{}, ctlSendNoActiveTurn
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	s := c.sessions[owner.rid]
	if s == nil || s.turn != owner.turn {
		return ctlbind.Binding{}, ctlSendNoActiveTurn
	}
	for id := range s.bound {
		bound[id] = true
	}
	binding, reason := ctlbind.Match(calls, bound, target)
	if reason != "" {
		return ctlbind.Binding{}, string(reason)
	}
	if s.bound == nil {
		s.bound = map[string]bool{}
	}
	s.bound[binding.ToolCallID] = true
	return binding, ""
}

// unbindCtlSend 放掉一次没有成交的占位(桌面端拒绝了这次 send,规则 4)。桌面端若已把
// overlay 注入进那一轮,累加器里的 overlay 照样挡住重绑。
func (c *CtlSessions) unbindCtlSend(owner ctlSession, toolCallID string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if s := c.sessions[owner.rid]; s != nil && s.turn == owner.turn {
		delete(s.bound, toolCallID)
	}
}
