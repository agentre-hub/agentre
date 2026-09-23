package handlers

import (
	"context"

	"github.com/agentre-hub/agentre/internal/pkg/wireinbound"
	"github.com/agentre-hub/agentre/pkg/wire/agentrewire"
)

// AnswerToolApproval 答 toolApproval.answer:控制台按 (对话, requestId) 回答一张挂起的
// 审批卡,唤醒等着它的那次 agrctl 写入。卡不在(答过、超时、从没有过)或不属于点名的
// 那条会话时,如实答 NoPendingToolApprovalError —— 与桌面端对过期 requestId 的答复
// 逐字相同,而不是空成功让控制台以为批准生效了。
func (c *CtlSessions) AnswerToolApproval(_ context.Context, request *agentrewire.ToolApprovalAnswerRequest) (*agentrewire.ToolApprovalAnswerResponse, error) {
	requestID := request.GetRequestId()
	c.mu.Lock()
	w, ok := c.waiters[requestID]
	if ok && w.conversationID == request.GetConversationId() {
		delete(c.waiters, requestID)
	} else {
		ok = false
	}
	c.mu.Unlock()
	if !ok {
		return nil, wireinbound.NoPendingToolApprovalError(requestID)
	}
	w.ch <- request.GetAllow() // 缓冲 1,且只投递一次(上面已摘掉)
	return &agentrewire.ToolApprovalAnswerResponse{}, nil
}
