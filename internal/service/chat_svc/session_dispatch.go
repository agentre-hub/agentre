package chat_svc

import (
	"context"
	"time"

	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"

	"github.com/agentre-hub/agentre/internal/model/entity/session_dispatch_entity"
	"github.com/agentre-hub/agentre/internal/repository/agent_repo"
	"github.com/agentre-hub/agentre/internal/repository/chat_repo"
	"github.com/agentre-hub/agentre/internal/repository/session_dispatch_repo"
)

// session_dispatch.go 记录「会话 A 把任务派给会话 B」(spec 2026-11-24 决策 21):agrctl send
// (带不带 --wait、隔离与否)与 agent 派发都登记一行,随被派发方那一轮的生命周期推进状态,
// 并按会话双向查询(派发我的 / 我派发的)。自引入起记录,不回填历史。

//go:generate mockgen -source session_dispatch.go -destination mock_session_dispatch/mock_session_dispatch.go -package mock_session_dispatch

// 派发方式,取值即 session_dispatches.method。
const (
	DispatchMethodAgrctlSend = session_dispatch_entity.MethodAgrctlSend
	DispatchMethodAgentCall  = session_dispatch_entity.MethodAgentCall
)

// DispatchBegin 是登记一次派发需要的两端与内容。
type DispatchBegin struct {
	DispatcherSessionID int64
	DispatchedSessionID int64
	Method              string
	Task                string
	Isolated            bool
}

// DispatchOutcome 是被派发方那一轮的结果;Status 用子代理卡的终态词表。
type DispatchOutcome struct {
	Status CtlSendStatus
	// ReplyMessageID 是终轮最后一条 assistant 消息;0 = 没有。
	ReplyMessageID int64
}

// DispatchOutcomeOfTurn 把被派发方那一轮的结果翻成派发终态:错误 = 失败,中止 = 已取消,
// 其余完成;回复消息取这一轮最后那条 assistant 消息。
func DispatchOutcomeOfTurn(res TurnResult) DispatchOutcome {
	status := CtlSendCompleted
	switch {
	case res.Err != nil:
		status = CtlSendFailed
	case res.Aborted:
		status = CtlSendCanceled
	}
	return DispatchOutcome{Status: status, ReplyMessageID: res.AssistantMessageID}
}

// DispatchRecorder 是派发方(ctl_svc、subagent_svc)对派发登记的窄依赖(ISP)。
// 登记是尽力而为:库写失败只记日志,不影响派发本身。
type DispatchRecorder interface {
	// Begin 在起轮之前登记一行「等待」,交回它的 ID;登记失败交回 0,此后对 0 的推进都是空操作。
	Begin(ctx context.Context, b DispatchBegin) int64
	// Started 起轮成功,转入「运行中」。
	Started(ctx context.Context, id int64)
	// Finish 收成终态并记下完成时间与最后回复。
	Finish(ctx context.Context, id int64, outcome DispatchOutcome)
}

// SessionDispatchesView 是某会话两个方向的派发关系,字段都是展示就绪的。
type SessionDispatchesView struct {
	// DispatchedBy 是派发了本会话的会话(派发我的)。
	DispatchedBy []*DispatchedByItem `json:"dispatchedBy"`
	// Dispatched 是本会话派发出去的会话(我派发的)。
	Dispatched []*DispatchedItem `json:"dispatched"`
}

// DispatchedByItem 是一条「派发我的」。
type DispatchedByItem struct {
	DispatchID int64  `json:"dispatchId"`
	SessionID  int64  `json:"sessionId"`
	Title      string `json:"title"`
	AgentID    int64  `json:"agentId"`
	AgentName  string `json:"agentName"`
	// Method 是 agrctl_send / agent_call。
	Method string `json:"method"`
	// CreatedAt 是派发时间(unix ms)。
	CreatedAt int64 `json:"createdAt"`
}

// DispatchedItem 是一条「我派发的」。
type DispatchedItem struct {
	DispatchID int64  `json:"dispatchId"`
	SessionID  int64  `json:"sessionId"`
	Task       string `json:"task"`
	AgentID    int64  `json:"agentId"`
	AgentName  string `json:"agentName"`
	Method     string `json:"method"`
	// Status 是 queued / running / completed / failed / canceled(终态同子代理卡)。
	Status string `json:"status"`
	// DurationMs 终态是完成时间 - 派发时间,否则是到现在的已用时间。
	DurationMs int64 `json:"durationMs"`
	Isolated   bool  `json:"isolated"`
	// LastReply 只对隔离会话给出(它不可打开,靠这条回复看结果)。
	LastReply string `json:"lastReply,omitempty"`
	CreatedAt int64  `json:"createdAt"`
}

// DispatchSvc 是派发关系服务:登记 + 查询。
type DispatchSvc interface {
	DispatchRecorder
	ListSessionDispatches(ctx context.Context, sessionID int64) (*SessionDispatchesView, error)
}

type dispatchSvc struct {
	now       func() int64
	replyText func(ctx context.Context, messageID int64) (string, error)
}

var defaultDispatch DispatchSvc = &dispatchSvc{
	now:       func() int64 { return time.Now().UnixMilli() },
	replyText: finalAssistantText,
}

// Dispatch 取派发关系服务单例(无自有状态,依赖都是包级仓储)。
func Dispatch() DispatchSvc { return defaultDispatch }

func (s *dispatchSvc) Begin(ctx context.Context, b DispatchBegin) int64 {
	row := &session_dispatch_entity.SessionDispatch{
		DispatcherSessionID: b.DispatcherSessionID,
		DispatchedSessionID: b.DispatchedSessionID,
		Method:              b.Method,
		Task:                b.Task,
		Isolated:            b.Isolated,
		Status:              session_dispatch_entity.StatusQueued,
		Createtime:          s.now(),
	}
	if err := session_dispatch_repo.SessionDispatch().Create(ctx, row); err != nil {
		logger.Ctx(ctx).Warn("chat_svc.Dispatch.Begin: record dispatch failed",
			zap.Int64("dispatcherSessionId", b.DispatcherSessionID),
			zap.Int64("dispatchedSessionId", b.DispatchedSessionID),
			zap.Error(err))
		return 0
	}
	return row.ID
}

func (s *dispatchSvc) Started(ctx context.Context, id int64) {
	if id <= 0 {
		return
	}
	s.update(ctx, id, session_dispatch_repo.StateChange{Status: session_dispatch_entity.StatusRunning})
}

func (s *dispatchSvc) Finish(ctx context.Context, id int64, outcome DispatchOutcome) {
	if id <= 0 {
		return
	}
	s.update(ctx, id, session_dispatch_repo.StateChange{
		Status:         string(outcome.Status),
		Finishtime:     s.now(),
		ReplyMessageID: outcome.ReplyMessageID,
	})
}

func (s *dispatchSvc) update(ctx context.Context, id int64, change session_dispatch_repo.StateChange) {
	if err := session_dispatch_repo.SessionDispatch().UpdateState(ctx, id, change); err != nil {
		logger.Ctx(ctx).Warn("chat_svc.Dispatch.update: advance dispatch state failed",
			zap.Int64("dispatchId", id), zap.String("status", change.Status), zap.Error(err))
	}
}

func (s *dispatchSvc) ListSessionDispatches(ctx context.Context, sessionID int64) (*SessionDispatchesView, error) {
	repo := session_dispatch_repo.SessionDispatch()
	into, err := repo.ListByDispatched(ctx, sessionID)
	if err != nil {
		return nil, operationFailedWithCause(ctx, err)
	}
	out, err := repo.ListByDispatcher(ctx, sessionID)
	if err != nil {
		return nil, operationFailedWithCause(ctx, err)
	}

	view := &SessionDispatchesView{DispatchedBy: []*DispatchedByItem{}, Dispatched: []*DispatchedItem{}}
	for _, row := range into {
		sess, err := chat_repo.Session().Find(ctx, row.DispatcherSessionID)
		if err != nil || sess == nil {
			continue // 派发方已不在:没有可展示、可打开的对象
		}
		view.DispatchedBy = append(view.DispatchedBy, &DispatchedByItem{
			DispatchID: row.ID, SessionID: sess.ID, Title: sess.Title,
			AgentID: sess.AgentID, AgentName: s.agentName(ctx, sess.AgentID),
			Method: row.Method, CreatedAt: row.Createtime,
		})
	}
	now := s.now()
	for _, row := range out {
		item := &DispatchedItem{
			DispatchID: row.ID, SessionID: row.DispatchedSessionID, Task: row.Task, Method: row.Method,
			Status: row.Status, Isolated: row.Isolated, CreatedAt: row.Createtime,
			DurationMs: dispatchDuration(row, now),
		}
		if sess, err := chat_repo.Session().Find(ctx, row.DispatchedSessionID); err == nil && sess != nil {
			item.AgentID = sess.AgentID
			item.AgentName = s.agentName(ctx, sess.AgentID)
		}
		if row.Isolated && row.ReplyMessageID > 0 {
			if text, err := s.replyText(ctx, row.ReplyMessageID); err != nil {
				logger.Ctx(ctx).Warn("chat_svc.Dispatch.ListSessionDispatches: read last reply failed",
					zap.Int64("dispatchId", row.ID), zap.Int64("messageId", row.ReplyMessageID), zap.Error(err))
			} else {
				item.LastReply = text
			}
		}
		view.Dispatched = append(view.Dispatched, item)
	}
	return view, nil
}

func (s *dispatchSvc) agentName(ctx context.Context, agentID int64) string {
	if agentID <= 0 {
		return ""
	}
	a, err := agent_repo.Agent().Find(ctx, agentID)
	if err != nil || a == nil {
		return ""
	}
	return a.Name
}

func dispatchDuration(row *session_dispatch_entity.SessionDispatch, now int64) int64 {
	end := row.Finishtime
	if end == 0 {
		end = now
	}
	return max(end-row.Createtime, 0)
}
