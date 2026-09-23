package handlers

import (
	"context"

	"github.com/agentre-hub/agentre/internal/pkg/wireinbound"
	"github.com/agentre-hub/agentre/pkg/wire/agentrewire"
)

// AnswerToolApproval 答 toolApproval.answer:控制台按 (对话, requestId) 回答一张
// tool_approval 卡(规格 2026-09-22-agrctl-resource-management 决策 13)。
//
// agentred 上此刻还没有任何一方挂起 tool_approval 卡 —— 工具审批的生产方(ctl 代理)
// 由后续任务接上,届时这里换成按对话与 requestId 查挂起的那张卡。在那之前,任何作答
// 点名的都是一张不挂起的卡,如实答 NoPendingToolApprovalError(与桌面端对过期
// requestId 的答复逐字相同),而不是空成功让控制台以为批准生效了。
//
// 方法仍然要注册:控制台按会话目标机拨号而不筛 kind,缺了它调用方收到的是
// method not found,读不出「这张卡已经不在了」。
func AnswerToolApproval(_ context.Context, request *agentrewire.ToolApprovalAnswerRequest) (*agentrewire.ToolApprovalAnswerResponse, error) {
	return nil, wireinbound.NoPendingToolApprovalError(request.GetRequestId())
}
