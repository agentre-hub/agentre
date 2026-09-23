package handlers

import (
	"context"
	"sync"

	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
	"github.com/agentre-hub/agentre/internal/pkg/agenttool"
	transcriptblocks "github.com/agentre-hub/agentre/internal/pkg/transcript/blocks"
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

	mu       sync.Mutex
	sessions map[int64]*ctlSession // backend 会话键(runtimeSessionID) →
	waiters  map[string]ctlWaiter  // 审批卡 requestId →
}

// ctlSession 是一条会话的 ctl 归属。
type ctlSession struct {
	conversationID string
	// peer 是这条会话的发起端:桌面端拥有的会话经它的连接转回去(tunnelTargetFor)。
	peer devicefp.Initiator
	// desktop 是桌面端经 runtime.run 交来的会话 token;hasDesktop=false 即控制台拥有。
	desktop    DesktopCtlSession
	hasDesktop bool
	// turn 是此刻在跑的那一轮转录;控制台拥有的会话的审批卡落在它上面。nil = 没有在跑的轮。
	turn approvalSink
}

// approvalSink 是审批卡在本会话转录里的落点(*turnTranscript)。
type approvalSink interface {
	// beginApproval 把一张 pending 卡落进本轮转录并实时推出,返回本机会话 id(等待行用)。
	beginApproval(ctx context.Context, blk *transcriptblocks.ToolApprovalBlock) (int64, error)
	// resolveApproval 把卡置为终态(approved / denied / expired)并推出决议帧。
	resolveApproval(ctx context.Context, requestID, status, result string)
}

// ctlWaiter 是一张挂起的审批卡:只认它自己那条会话里的作答。
type ctlWaiter struct {
	conversationID string
	ch             chan bool
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
	s.conversationID, s.peer = conversationID, peer
	if hasDesktop {
		s.desktop, s.hasDesktop = desktop, true
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
		s.turn = sink
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
	return *s, true
}

// beginWait 挂起一张卡,返回作答 channel(true = 批准)。
func (c *CtlSessions) beginWait(conversationID, requestID string) <-chan bool {
	ch := make(chan bool, 1)
	c.mu.Lock()
	c.waiters[requestID] = ctlWaiter{conversationID: conversationID, ch: ch}
	c.mu.Unlock()
	return ch
}

// endWait 撤下一张卡(超时 / 调用方断开 / 登记失败);此后的作答一律「不挂起」。
func (c *CtlSessions) endWait(requestID string) {
	c.mu.Lock()
	delete(c.waiters, requestID)
	c.mu.Unlock()
}
