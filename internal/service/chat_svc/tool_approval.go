package chat_svc

import (
	"context"
	"errors"
	"fmt"

	"github.com/agentre-hub/agentre/internal/model/entity/chat_entity"
	"github.com/agentre-hub/agentre/internal/pkg/transcript/blocks"
	"github.com/agentre-hub/agentre/internal/pkg/transcript/turn"
	"github.com/agentre-hub/agentre/internal/repository/chat_repo"
)

// toolApprovalBlockToChatBlock 历史回放路径：持久化 block → 前端 ChatBlock。
func toolApprovalBlockToChatBlock(b blocks.ToolApprovalBlock) ChatBlock {
	return ChatBlock{
		Type: ChatBlockTypeToolApproval,
		ToolApproval: &ChatBlockToolApproval{
			ToolKey:   b.ToolKey,
			RequestID: b.RequestID,
			ToolName:  b.ToolName,
			ToolInput: b.ToolInput,
			Status:    b.Status,
			Result:    b.Result,
		},
	}
}

// BeginToolApproval 在 sessionID 当前活跃 turn 上登记一条 pending 审批:卡落进这一轮
// 的转录(checkpoint 落库 + 持久帧发给对端,控制台在挂起期间就看得到、答得了),
// 推桌面端流事件,并返回等待 channel(buffered=1)。工具服务 select 该 channel(allow)/
// 超时/ctx。无活跃 turn(或这一轮已收口)返回错误(工具 MCP handler 据此拒绝工具调用)。
func (s *chatSvc) BeginToolApproval(ctx context.Context, sessionID int64, blk *blocks.ToolApprovalBlock) (<-chan bool, error) {
	t := s.activeTurn(sessionID)
	if t == nil {
		return nil, fmt.Errorf("chat_svc.BeginToolApproval: no active turn for session %d", sessionID)
	}
	// waiter 先于卡登记:卡一落库就已经发给对端,控制台可能在这一行之后立刻作答。
	ch := make(chan bool, 1)
	s.toolApprovalWaiters.Store(blk.RequestID, ch)
	snapshot, err := t.beginApproval(ctx, blk)
	if err != nil {
		s.toolApprovalWaiters.Delete(blk.RequestID)
		return nil, fmt.Errorf("chat_svc.BeginToolApproval: session %d: %w", sessionID, err)
	}

	s.emitter.Emit(ctx, t.stream, toolApprovalEventPayload(snapshot))
	if sess, err := chat_repo.Session().Find(ctx, sessionID); err == nil && sess != nil {
		s.markSessionWaiting(ctx, sess, t.stream)
	}
	return ch, nil
}

// activeTurn 交回 sessionID 此刻在跑的那一轮,没有时为 nil。
func (s *chatSvc) activeTurn(sessionID int64) *turnRun {
	v, ok := s.activeTurns.Load(sessionID)
	if !ok {
		return nil
	}
	return v.(*turnRun)
}

// AnswerToolApprovalRequest 前端审批入口(wails binding)。org / hook
// 等内置写工具的审批决策统一走此请求(按 requestID 路由,SessionID 仅作前端上下文)。
type AnswerToolApprovalRequest struct {
	SessionID int64  `json:"sessionId"`
	RequestID string `json:"requestId"`
	Allow     bool   `json:"allow"`
}

// AnswerToolApprovalResponse 应答返回(无字段)。
type AnswerToolApprovalResponse struct{}

// AnswerToolApproval 按 requestID 唤醒挂起的写工具调用(前端审批入口与控制台 relay 共用)。
// 未知/重复/已超时 → error;sessionID > 0 时这张卡还必须挂在该会话上,否则同样 error ——
// 控制台经某条会话作答,不能答到别的会话的卡上。
func (s *chatSvc) AnswerToolApproval(_ context.Context, sessionID int64, requestID string, allow bool) error {
	if requestID == "" {
		return fmt.Errorf("chat_svc.AnswerToolApproval: empty requestID")
	}
	if sessionID > 0 && !s.toolApprovalInSession(sessionID, requestID) {
		return fmt.Errorf("chat_svc.AnswerToolApproval: request %s not pending in session %d", requestID, sessionID)
	}
	chAny, ok := s.toolApprovalWaiters.LoadAndDelete(requestID)
	if !ok {
		return fmt.Errorf("chat_svc.AnswerToolApproval: request %s not found", requestID)
	}
	chAny.(chan bool) <- allow
	return nil
}

// toolApprovalInSession 报告 requestID 是否是 sessionID 在跑那一轮上仍可决的审批。
func (s *chatSvc) toolApprovalInSession(sessionID int64, requestID string) bool {
	t := s.activeTurn(sessionID)
	return t != nil && t.hasApproval(requestID)
}

// FinishToolApproval 把审批置为终态(approved/denied/expired):决议落库、发持久帧,
// 并推 resolved 流事件。这一轮已收口(卡已由 finalize 置为 expired)→ error。
func (s *chatSvc) FinishToolApproval(ctx context.Context, sessionID int64, requestID, status, result string) error {
	s.toolApprovalWaiters.Delete(requestID) // 终态兜底清 waiter(超时/拒绝/ctx 死路径)
	t := s.activeTurn(sessionID)
	if t == nil {
		return fmt.Errorf("chat_svc.FinishToolApproval: request %s not found (turn finalized?)", requestID)
	}
	snapshot, ok := t.resolveApproval(ctx, requestID, status, result)
	if !ok {
		return fmt.Errorf("chat_svc.FinishToolApproval: request %s not found (turn finalized?)", requestID)
	}
	s.emitter.Emit(ctx, t.stream, toolApprovalEventPayload(snapshot))
	if sess, err := chat_repo.Session().Find(ctx, sessionID); err == nil && sess != nil {
		s.markSessionRunning(ctx, sess, t.stream)
	}
	return nil
}

func toolApprovalEventPayload(b blocks.ToolApprovalBlock) map[string]any {
	return map[string]any{
		"kind":      "tool_approval",
		"toolKey":   b.ToolKey,
		"requestId": b.RequestID,
		"toolName":  b.ToolName,
		"toolInput": b.ToolInput,
		"status":    b.Status,
		"result":    b.Result,
	}
}

// errTurnFinished:这一轮已经收口,审批卡无处可落、也不再可决。
var errTurnFinished = errors.New("the turn has finished")

// turnApproval 是这一轮上登记的一张审批卡,连同它落进的那条 assistant 消息与累加器:
// 插话分段会换掉 turnRun 的当前消息,卡却留在它发生的那一条里,决议要回写那一条。
type turnApproval struct {
	blk *blocks.ToolApprovalBlock
	msg *chat_entity.Message
	acc *turn.Accumulator
}

// beginApproval 把一张 pending 的审批卡落进这一轮的转录:追加进累加器(停在命令发生处)、
// checkpoint 落库,并随之作为持久帧推出(投影成 tool_approval_requested)。
// agentred 做宿主时是同一形态(turnTranscript.beginApproval)。
func (t *turnRun) beginApproval(ctx context.Context, blk *blocks.ToolApprovalBlock) (blocks.ToolApprovalBlock, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.approvalsClosed {
		return blocks.ToolApprovalBlock{}, errTurnFinished
	}
	t.acc.AddBlock(blk, "")
	t.approvals = append(t.approvals, &turnApproval{blk: blk, msg: t.assistantMsg, acc: t.acc})
	t.svc.checkpointAssistantNew(ctx, t.assistantMsg, t.acc)
	return *blk, nil
}

// resolveApproval 把卡原地改成终态并 checkpoint 它所在的那条消息:决议随之作为持久帧
// 推出(tool_approval_resolved)。这一轮已收口或没有这张卡时交回 false。
func (t *turnRun) resolveApproval(ctx context.Context, requestID, status, result string) (blocks.ToolApprovalBlock, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.approvalsClosed {
		return blocks.ToolApprovalBlock{}, false
	}
	for _, a := range t.approvals {
		if a.blk.RequestID == requestID {
			a.blk.Status, a.blk.Result = status, result
			t.svc.checkpointAssistantNew(ctx, a.msg, a.acc)
			return *a.blk, true
		}
	}
	return blocks.ToolApprovalBlock{}, false
}

// hasApproval 报告 requestID 是否登记在这一轮上且这一轮尚未收口。
func (t *turnRun) hasApproval(requestID string) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.approvalsClosed {
		return false
	}
	for _, a := range t.approvals {
		if a.blk.RequestID == requestID {
			return true
		}
	}
	return false
}

// closeApprovals 在收口时关上这一轮的审批:仍 pending 的卡原地标 expired(turn 被
// abort / 子进程死亡 / 正常结束时挂起审批都不再可决)。卡在当前 assistant 里的,随
// finalize 的落库与 publishPeerTurnDone 出去;早先分段留在旧消息里的,就地回写那一条。
func (t *turnRun) closeApprovals(ctx context.Context) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.approvalsClosed = true
	for _, a := range t.approvals {
		if a.blk.Status != "pending" {
			continue
		}
		a.blk.Status = "expired"
		if a.msg != t.assistantMsg {
			t.svc.checkpointAssistantNew(ctx, a.msg, a.acc)
		}
	}
}
