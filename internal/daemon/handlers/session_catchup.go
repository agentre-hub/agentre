// Package handlers — session_catchup.go 实现断连重连的补齐族 RPC:会话清单 / 增量
// 拉取 / 待决策查询 / 显式接管。它是客户端重连后的第一站。
//
// 与 runtime.go 的三条区别决定了它为什么是**独立的一族**而不是 RuntimeHandlers 的
// 几个新方法:
//
//  1. 它是 daemon 级的,不随连接生灭。RuntimeHandlers 是 per-connection 构造的,它的
//     内存会话表在重连后是空的 —— 补齐要是按那张表解会话,重连的客户端就永远拿不到
//     自己的会话,而重连正是这一族存在的全部理由。这里一律按持久化的会话行解。
//  2. 它只读存储 + 只读实时状态,不启动、不推进、不中止任何一轮执行。唯一的例外是
//     显式接管,而接管改的是「通知推给谁」,不是会话本身(且由 daemon.go 在受理后
//     执行,见 MethodSessionAttach 的注册)。
//  3. 默认范围是调用方自己的对端。可选 origin 对端只在 daemon 已认领且调用方
//     auth.account 的账号等于 daemon 账号时放宽；配对身份绝不能点名别人的会话。
package handlers

import (
	"context"
	"fmt"

	"github.com/agentre-hub/agentre/internal/daemon/connection"
	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/runtimes/remote/protowire"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/runtimes/remote/wire"
	"github.com/agentre-hub/agentre/internal/pkg/conversationid"
	"github.com/agentre-hub/agentre/internal/pkg/rpcerror"
)

// SessionCatchupDeps 是 SessionCatchupHandlers 的显式构造入参。
type SessionCatchupDeps struct {
	// Sessions 读会话行(身份 / 元数据 / 生命周期)。
	Sessions SessionQueryPort
	// Journal 读通知日志(增量拉取与最新 seq)。
	Journal JournalReaderPort
	// RuntimeFor 解 backend 类型 → 本进程里那个 runtime 单例,用来问它此刻有哪些
	// 阻塞中的 waiter。留空取 agentruntime.RuntimeFor。
	RuntimeFor func(agent_backend_entity.BackendType) agentruntime.Runtime
	// ClaimedAccountID returns the daemon account allowed to name another peer.
	ClaimedAccountID func() string
}

// SessionCatchupHandlers 实现补齐族的四个 RPC。无状态:它的全部事实要么在存储里,
// 要么在 backend runtime 的内存里。
type SessionCatchupHandlers struct {
	deps SessionCatchupDeps
}

// NewSessionCatchupHandlers 组装补齐族 handler。
func NewSessionCatchupHandlers(deps SessionCatchupDeps) *SessionCatchupHandlers {
	if deps.RuntimeFor == nil {
		deps.RuntimeFor = agentruntime.RuntimeFor
	}
	return &SessionCatchupHandlers{deps: deps}
}

// List 返回这台 daemon 上的会话。调用方自己的对端永远在范围内;daemon 已认领且
// 调用方账号等于 daemon 账号时走 ListAll 把全部对端的会话一并列出(账号可见性)。
//
// keyword 非空时按标题的大小写不敏感子串收窄,原样下推给存储。它是**收窄**而不是
// 另一条查询:对端限定与账号可见性的判据一个字不变。
//
// 每条会话的「最新 seq」取自通知日志的 MAX(seq) —— 唯一真相源。会话一条通知都还没
// 发出时报 0。「是否正在等待输入」现算,见 waitingForInput。标题 / Agent 同步标识 /
// provider_session_id(R7 + 决策 8)原样回传;老会话缺这些字段时保持空串、如实留空。
func (h *SessionCatchupHandlers) List(ctx context.Context, keyword string) (wire.SessionListResult, error) {
	peer := peerFingerprint(ctx)
	accountWide := hasClaimedAccount(ctx, h.deps.ClaimedAccountID)
	var (
		rows []SessionRecord
		err  error
	)
	if accountWide {
		allSessions, ok := h.deps.Sessions.(interface {
			ListAll(ctx context.Context, keyword string) ([]SessionRecord, error)
		})
		if !ok {
			return wire.SessionListResult{}, fmt.Errorf("list all sessions: account visibility is not wired")
		}
		rows, err = allSessions.ListAll(ctx, keyword)
	} else {
		rows, err = h.deps.Sessions.List(ctx, peer, keyword)
	}
	if err != nil {
		// 报错而不是回一份空清单:空清单与「这台 daemon 上没有你的会话」无法区分,
		// 客户端会据此把还活着的会话当成已消失。
		return wire.SessionListResult{}, fmt.Errorf("list sessions: %w", err)
	}
	latest := map[string]int64{}
	if !accountWide {
		latest, err = h.deps.Journal.LatestSeqByPeer(ctx, peer)
		if err != nil {
			return wire.SessionListResult{}, fmt.Errorf("read latest seq: %w", err)
		}
	}
	// 这台 daemon 认识 R7 / 决策 8 的那几列(它就是落库方),如实声明 —— 未升级的
	// agentred 不认识这个字段,客户端解出来是 false,据此说明该机器需要升级。
	out := wire.SessionListResult{
		Sessions: make([]wire.SessionSummary, 0, len(rows)),
		// 这台 daemon 落库并回传会话级 ModelTarget(它就是落库方),如实声明 ——
		// 未升级的 agentred 不认识这个字段,客户端解出来是 false,据此说明这台机器
		// 记不住模型选择,而不是把每条对话都显示成「跟随 Agent 绑定」。
	}
	for _, row := range rows {
		conversationID := row.PeerSessionID
		if conversationid.Validate(conversationID) != nil {
			// 身份列存的不是一条对话 uuid,说明这一行不是本协议写的。跳过而不是整份
			// 清单失败 —— 一条坏行不该让客户端连自己的会话都看不到。
			continue
		}
		latestSeq := latest[row.PeerSessionID]
		if accountWide {
			latestSeq, err = h.deps.Journal.LatestSeq(ctx, row.PeerFingerprint, row.PeerSessionID)
			if err != nil {
				return wire.SessionListResult{}, fmt.Errorf("read latest seq: %w", err)
			}
		}
		summary := wire.SessionSummary{
			ConversationID:    conversationID,
			AgentID:           row.AgentID,
			Title:             row.Title,
			AgentSyncID:       row.AgentSyncID,
			ProjectSyncID:     row.ProjectSyncID,
			ProviderSessionID: row.ProviderSessionID,
			Cwd:               row.Cwd,
			BackendType:       row.BackendType,
			LifecycleState:    row.LifecycleState,
			WaitingForInput:   h.waitingForInput(ctx, row, conversationID),
			LatestSeq:         latestSeq,
			LastMessageAt:     row.LastMessageAt,
			ProviderKey:       row.ProviderKey,
			ModelKey:          row.ModelKey,
			ReasoningEffort:   row.ReasoningEffort,
		}
		// Origin 只标在**别的对端**发起的那些会话上。空 origin 的语义是
		// ResolveSessionPeer 的入口约定 ——「省略 = 调用方自己的对端」—— 而清单是客户端
		// 学 origin 的唯一来源:它会把这里交出的值原样带回此后每一次 attach / pull /
		// 控制请求。把调用方自己的指纹写进来,它就会在**配对鉴权**的连接上也点名一个
		// origin,而配对身份点名任何 origin 都被 ResolveSessionPeer 拒掉;桌面端同时握
		// 着两条路径(直连有配对令牌时走 auth.connect,中转恒走 auth.account,谁先到谁
		// 胜),路径一切换就连自己的会话都补不齐 —— 规格的硬不变量正是「路径切换不得使
		// 事件游标失效」。
		if accountWide && row.PeerFingerprint != peer {
			summary.PeerFingerprint = row.PeerFingerprint
		}
		out.Sessions = append(out.Sessions, summary)
	}
	return out, nil
}

// Pull 按游标取回该会话其后的通知(seq 升序),并一并交出该会话现存最老的 seq。
// The optional origin peer is resolved at the account gate before it reaches
// the composite journal key; omitted origin remains the caller's own peer.
//
// 现存最老的 seq 每页都报:日志本身已经不回收了(规格 2026-08-18 决策 8),但库可能被从
// 外部恢复或截断,客户端的游标于是落在一段已经不存在的区间里。
// 不报下界,它拉到的每一页第一条都比 游标+1 大,只能当成跳号丢弃并再拉一次同一页 ——
// 游标永远推不动,此后连实时通知也全被判成跳号,会话没有错误、没有跳号地冻住。
// 读不出下界不让整页拉取失败:内容比下界重要,客户端按 0(=不知道)处理即可。
func (h *SessionCatchupHandlers) Pull(ctx context.Context, p wire.SessionPullParams) (wire.SessionPullResult, error) {
	peer, err := ResolveSessionPeer(ctx, p.PeerFingerprint, h.deps.ClaimedAccountID)
	if err != nil {
		return wire.SessionPullResult{}, err
	}
	if err := ErrInvalidConversationID(p.ConversationID); err != nil {
		return wire.SessionPullResult{}, err
	}
	sid := p.ConversationID
	// 下界先读:两次读之间老前缀随时可能消失(库被从外部恢复或截断)。先读页、后读下界,
	// 下界就会涨到页里那些行**之上** —— 客户端拿它复位游标(复位跑在重放之前),这一整页
	// 已经拿到手的行会被当成重复全部丢掉,一段本来读得到的转录凭空消失。反过来先读下界
	// 只会偏小,偏小最多让客户端少复位一次,拉下一页时自然拿到新的下界。
	oldest, err := h.deps.Journal.OldestSeq(ctx, peer, sid)
	if err != nil {
		oldest = 0
	}
	rows, hasMore, err := h.deps.Journal.ListSince(ctx, peer, sid, p.Cursor, clampPullLimit(p.Limit))
	if err != nil {
		return wire.SessionPullResult{}, fmt.Errorf("pull notifications: %w", err)
	}
	// 空页保持游标不变:回退到 0 会让客户端把整段日志重放一遍。
	out := wire.SessionPullResult{Cursor: p.Cursor, HasMore: hasMore, OldestSeq: oldest}
	out.Notifications = make([]wire.JournaledNotification, 0, len(rows))
	for _, row := range rows {
		notification, err := protowire.DecodeNotification(row.Payload)
		if err != nil {
			return wire.SessionPullResult{}, fmt.Errorf("decode notification seq %d: %w", row.Seq, err)
		}
		protowire.SetNotificationSeq(notification, row.Seq)
		method, value, err := protowire.ProtoNotificationToWire(notification)
		if err != nil {
			return wire.SessionPullResult{}, fmt.Errorf("translate notification seq %d: %w", row.Seq, err)
		}
		out.Notifications = append(out.Notifications, wire.JournaledNotification{
			Seq:        row.Seq,
			Method:     method,
			Params:     value,
			Createtime: row.Createtime,
		})
		out.Cursor = row.Seq
	}
	return out, nil
}

// PendingWaiters 返回该会话此刻仍在阻塞的全部待决策(R7)。
//
// 快照来自 daemon 内存里的 waiter,不来自数据库;会话行只用来解「这条会话是不是调用方
// 的、跑的是哪个 backend」。不属于调用方的会话、未实现审批协议的 backend,都回空列表
// 而不是报错 —— 两者都是正常情况,报错会让客户端误判为故障。
func (h *SessionCatchupHandlers) PendingWaiters(ctx context.Context, p wire.SessionPendingWaitersParams) (wire.SessionPendingWaitersResult, error) {
	row, err := h.findSession(ctx, p.ConversationID, p.PeerFingerprint)
	if err != nil {
		return wire.SessionPendingWaitersResult{}, err
	}
	if row == nil {
		return wire.SessionPendingWaitersResult{}, nil
	}
	snap := h.pendingWaiters(ctx, *row, p.ConversationID)
	return wire.SessionPendingWaitersResult{
		ToolPermissions:  snap.ToolPermissions,
		AskUserQuestions: snap.AskUserQuestions,
	}, nil
}

// Attach 是显式接管的受理侧:校验这条会话确实是调用方的、且还接得回去,然后交回它
// 接着补齐要用的生命周期状态、backend 类型与此刻的最新 seq。
//
// 把推送目标真正改到这条连接上的动作在 daemon.go 的注册处,且只在本方法**成功返回
// 之后**执行 —— 被拒的接管不得改变任何东西。
func (h *SessionCatchupHandlers) Attach(ctx context.Context, p wire.SessionAttachParams) (wire.SessionAttachResult, error) {
	row, err := h.findSession(ctx, p.ConversationID, p.PeerFingerprint)
	if err != nil {
		return wire.SessionAttachResult{}, err
	}
	if row == nil {
		// 接管会改变通知的推送目标,允许接管别人的(或不存在的)会话等于把别人的事件流
		// 引到自己的连接上。
		return wire.SessionAttachResult{}, agentruntime.ErrSessionNotFound
	}
	if row.LifecycleState == wire.SessionLifecycleInterrupted {
		// R10「不可续跑」:那一轮的子进程随上一个 daemon 进程消亡了,接回实时流等于让
		// 客户端对着一条永远不会再产出任何东西的会话无限期等下去。历史仍可 Pull。
		return wire.SessionAttachResult{}, agentruntime.ErrNoActiveTurn
	}
	latest, err := h.deps.Journal.LatestSeq(ctx, row.PeerFingerprint, row.PeerSessionID)
	if err != nil {
		return wire.SessionAttachResult{}, fmt.Errorf("read latest seq: %w", err)
	}
	return wire.SessionAttachResult{
		ConversationID: p.ConversationID,
		BackendType:    row.BackendType,
		LifecycleState: row.LifecycleState,
		LatestSeq:      latest,
	}, nil
}

// findSession resolves an omitted origin to the caller's own peer, or an
// authorized claimed-account origin to that peer's row. Every per-session
// catch-up operation passes through this boundary.
func (h *SessionCatchupHandlers) findSession(ctx context.Context, conversationID string, originPeer string) (*SessionRecord, error) {
	if err := ErrInvalidConversationID(conversationID); err != nil {
		return nil, err
	}
	peer, err := ResolveSessionPeer(ctx, originPeer, h.deps.ClaimedAccountID)
	if err != nil {
		return nil, err
	}
	row, err := h.deps.Sessions.Find(ctx, peer, conversationID)
	if err != nil {
		return nil, fmt.Errorf("find session: %w", err)
	}
	return row, nil
}

// ResolveSessionPeer selects the caller's own peer for an omitted origin. A
// named origin is an account-level capability: pairing alone never grants it.
func ResolveSessionPeer(ctx context.Context, originPeer string, claimedAccountID func() string) (string, error) {
	ownPeer := peerFingerprint(ctx)
	if originPeer == "" {
		return ownPeer, nil
	}
	if !hasClaimedAccount(ctx, claimedAccountID) {
		return "", rpcerror.ErrUnauthorized
	}
	return originPeer, nil
}

func hasClaimedAccount(ctx context.Context, claimedAccountID func() string) bool {
	if claimedAccountID == nil {
		return false
	}
	claimed := claimedAccountID()
	if claimed == "" {
		return false
	}
	if c := connection.FromContext(ctx); c != nil {
		auth := c.Auth()
		return auth.AccountID != "" && auth.AccountID == claimed
	}
	return false
}

// waitingForInput 现算「这条会话是不是正在等用户操作」:问 backend 此刻有没有阻塞中的
// waiter(R11)。它永远不落库 —— 落库的等待标志会活过 daemon 重启,变成一个没人能回答
// 的问题(那一轮的子进程已经不在了)。
func (h *SessionCatchupHandlers) waitingForInput(ctx context.Context, row SessionRecord, conversationID string) bool {
	snap := h.pendingWaiters(ctx, row, conversationID)
	return len(snap.ToolPermissions) > 0 || len(snap.AskUserQuestions) > 0
}

// pendingWaiters 问该会话的 backend 要一份 waiter 快照。backend 没注册、或没实现审批
// 协议时回零值 —— 未实现者返回空列表而非报错是 R7 明写的。
//
// 问的是**折算过的进程内会话键**(runtimeSessionID):backend 的 waiter 表是进程内一份、
// 只按 int64 索引,而线上的身份是一个 uuid。
//
// 中断态会话一律不问 backend:那一轮的子进程随上一个 daemon 进程消亡了(R10),它不可能
// 还有活的 waiter,任何答案都只会是别人的。
func (h *SessionCatchupHandlers) pendingWaiters(ctx context.Context, row SessionRecord, conversationID string) agentruntime.WaiterSnapshot {
	if row.LifecycleState == wire.SessionLifecycleInterrupted {
		return agentruntime.WaiterSnapshot{}
	}
	rt := h.deps.RuntimeFor(agent_backend_entity.BackendType(row.BackendType))
	if rt == nil {
		return agentruntime.WaiterSnapshot{}
	}
	lister, ok := rt.(agentruntime.WaiterLister)
	if !ok {
		return agentruntime.WaiterSnapshot{}
	}
	return lister.PendingWaiters(ctx, runtimeSessionID(conversationID))
}

// clampPullLimit 把客户端报的单页条数收进 daemon 的上限。
func clampPullLimit(limit int) int {
	if limit <= 0 {
		return wire.DefaultSessionPullLimit
	}
	if limit > wire.MaxSessionPullLimit {
		return wire.MaxSessionPullLimit
	}
	return limit
}
