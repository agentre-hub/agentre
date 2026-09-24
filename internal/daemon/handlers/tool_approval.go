package handlers

import (
	"context"

	"github.com/agentre-hub/agentre/internal/pkg/wireinbound"
	"github.com/agentre-hub/agentre/pkg/wire/agentrewire"
	"github.com/agentre-hub/agentre/pkg/wire/rpcerror"
)

// AnswerToolApproval 答 toolApproval.answer:控制台按 (对话, requestId) 回答一张挂起的
// 审批卡,唤醒等着它的那次 agrctl 写入。卡不在(答过、超时、从没有过)或不属于点名的
// 那条会话时,如实答 NoPendingToolApprovalError —— 与桌面端对过期 requestId 的答复
// 逐字相同,而不是空成功让控制台以为批准生效了。
//
// 作答会让 agentred 用自己的设备凭据提交写入,所以先过 answerAuth(已登录账号的调用方);
// 没接闸时一律拒绝。
func (c *CtlSessions) AnswerToolApproval(ctx context.Context, request *agentrewire.ToolApprovalAnswerRequest) (*agentrewire.ToolApprovalAnswerResponse, error) {
	if c.answerAuth == nil {
		return nil, rpcerror.ErrUnauthorized
	}
	if err := c.answerAuth(ctx); err != nil {
		return nil, err
	}
	requestID := request.GetRequestId()
	c.mu.Lock()
	w, ok := c.waiters[requestID]
	if ok && w.conversationID == request.GetConversationId() {
		delete(c.waiters, requestID)
		ok = !isClosed(w.ended) // 那一轮已经收口:卡已记成 expired,不再算数
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

// isClosed 报告 ch 是否已经关上(nil 视为没关)。
func isClosed(ch <-chan struct{}) bool {
	select {
	case <-ch:
		return true
	default:
		return false
	}
}
