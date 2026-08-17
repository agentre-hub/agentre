package chat_svc

import (
	"context"
	"fmt"

	"github.com/agentre-ai/agentre/internal/repository/chat_repo"
	"github.com/agentre-ai/agentre/internal/service/chat_svc/blocks"
)

// toolApprovalBlockToChatBlock 历史回放/overlay 路径：持久化 block → 前端 ChatBlock。
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

// BeginToolApproval 在 sessionID 当前活跃 turn 上登记一条 pending 审批、推流事件,
// 并返回等待 channel(buffered=1)。工具服务 select 该 channel(allow)/超时/ctx。
// 无活跃 turn 返回错误(工具 MCP handler 据此拒绝工具调用)。
func (s *chatSvc) BeginToolApproval(ctx context.Context, sessionID int64, blk *blocks.ToolApprovalBlock) (<-chan bool, error) {
	streamAny, ok := s.activeTurnStreams.Load(sessionID)
	if !ok {
		return nil, fmt.Errorf("chat_svc.BeginToolApproval: no active turn for session %d", sessionID)
	}
	stream := streamAny.(string)
	s.toolApprovalsMu.Lock()
	s.toolApprovals[sessionID] = append(s.toolApprovals[sessionID], blk)
	snapshot := *blk
	s.toolApprovalsMu.Unlock()

	ch := make(chan bool, 1)
	s.toolApprovalWaiters.Store(blk.RequestID, ch)

	s.emitter.Emit(ctx, stream, toolApprovalEventPayload(snapshot))
	if sess, err := chat_repo.Session().Find(ctx, sessionID); err == nil && sess != nil {
		s.markSessionWaiting(ctx, sess, stream)
	}
	return ch, nil
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

// AnswerToolApproval 按 requestID 唤醒挂起的写工具调用(前端审批入口的唯一后端方法)。
// 未知/重复/已超时 → error。
func (s *chatSvc) AnswerToolApproval(_ context.Context, _ int64, requestID string, allow bool) error {
	if requestID == "" {
		return fmt.Errorf("chat_svc.AnswerToolApproval: empty requestID")
	}
	chAny, ok := s.toolApprovalWaiters.LoadAndDelete(requestID)
	if !ok {
		return fmt.Errorf("chat_svc.AnswerToolApproval: request %s not found", requestID)
	}
	chAny.(chan bool) <- allow
	return nil
}

// FinishToolApproval 把审批置为终态(approved/denied/expired)并推 resolved 事件。
func (s *chatSvc) FinishToolApproval(ctx context.Context, sessionID int64, requestID, status, result string) error {
	s.toolApprovalWaiters.Delete(requestID) // 终态兜底清 waiter(超时/拒绝/ctx 死路径)
	s.toolApprovalsMu.Lock()
	var snapshot *blocks.ToolApprovalBlock
	for _, b := range s.toolApprovals[sessionID] {
		if b.RequestID == requestID {
			b.Status = status
			b.Result = result
			cp := *b
			snapshot = &cp
			break
		}
	}
	s.toolApprovalsMu.Unlock()
	if snapshot == nil {
		return fmt.Errorf("chat_svc.FinishToolApproval: request %s not found (turn finalized?)", requestID)
	}
	if streamAny, ok := s.activeTurnStreams.Load(sessionID); ok {
		stream := streamAny.(string)
		s.emitter.Emit(ctx, stream, toolApprovalEventPayload(*snapshot))
		if sess, err := chat_repo.Session().Find(ctx, sessionID); err == nil && sess != nil {
			s.markSessionRunning(ctx, sess, stream)
		}
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

// takeToolApprovals finalize 时取走本会话全部审批 block;仍 pending 的标 expired
// (turn 被 abort / 子进程死亡时挂起审批不再可决)。
func (s *chatSvc) takeToolApprovals(sessionID int64) []*blocks.ToolApprovalBlock {
	s.toolApprovalsMu.Lock()
	defer s.toolApprovalsMu.Unlock()
	out := s.toolApprovals[sessionID]
	delete(s.toolApprovals, sessionID)
	for _, b := range out {
		if b.Status == "pending" {
			b.Status = "expired"
		}
	}
	return out
}

// snapshotToolApprovals LoadSession overlay 用:拷贝当前登记的 block(不取走)。
func (s *chatSvc) snapshotToolApprovals(sessionID int64) []blocks.ToolApprovalBlock {
	s.toolApprovalsMu.Lock()
	defer s.toolApprovalsMu.Unlock()
	out := make([]blocks.ToolApprovalBlock, 0, len(s.toolApprovals[sessionID]))
	for _, b := range s.toolApprovals[sessionID] {
		out = append(out, *b)
	}
	return out
}
