package chat_svc

import (
	"context"

	"github.com/cago-frame/cago/pkg/i18n"

	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/canonical"
	"github.com/agentre-hub/agentre/internal/pkg/code"
	"github.com/agentre-hub/agentre/internal/repository/agent_backend_repo"
	"github.com/agentre-hub/agentre/internal/repository/agent_repo"
	"github.com/agentre-hub/agentre/internal/repository/chat_repo"
	"github.com/agentre-hub/agentre/internal/service/chat_svc/blocks"
	"github.com/agentre-hub/agentre/internal/service/chat_svc/view"
)

// toolPermissionBlockToChatBlock 历史回放路径：把 cago 持久化的
// blocks.ToolPermissionBlock 投影到前端显示用的 ChatBlock。
// ExitPlanMode 一并附 canonical.PlanApproveRequest,让前端 CanonicalToolRouter
// 与 live 路径共用 PlanApproveCard。
func toolPermissionBlockToChatBlock(b blocks.ToolPermissionBlock) ChatBlock {
	cb := ChatBlock{
		Type: ChatBlockTypeToolPermissionRequest,
		ToolPermission: &ChatBlockToolPermission{
			RequestID:   b.RequestID,
			ToolName:    b.ToolName,
			ToolInput:   b.ToolInput,
			Resolved:    b.Resolved,
			Allowed:     b.Allowed,
			AlwaysAllow: b.AlwaysAllow,
		},
	}
	if b.ToolName == "ExitPlanMode" {
		planText, _ := b.ToolInput["plan"].(string)
		cb.Canonical = view.FromCanonical(canonical.PlanApproveRequest{
			RequestID: b.RequestID,
			PlanText:  planText,
			Resolved:  b.Resolved,
			Allowed:   b.Allowed,
		})
	} else {
		cb.Canonical = view.FromCanonical(canonical.ToolPermission{
			RequestID:   b.RequestID,
			ToolName:    b.ToolName,
			ToolInput:   b.ToolInput,
			Resolved:    b.Resolved,
			Allowed:     b.Allowed,
			AlwaysAllow: b.AlwaysAllow,
		})
	}
	return cb
}

// AnswerToolPermissionRequest 前端审批后调 App.AnswerToolPermission 的 payload。
// RequestID 必填——它是 runtime 端 permWaiter 表的主键，也是 CLI 端
// control_request.request_id。
//
// Allow=false 时 AlwaysAllowSession / TargetPermissionMode 字段被忽略：deny 永远是
// 单次性的，不会改变 session 的运行模式。
//
// DenyReason 仅 Allow=false 时生效，作为用户反馈注入 CLI 的
// control_response.message —— CLI 把它当 tool_result 回灌给 LLM，
// 让 AI 拿到具体反馈再规划。留空时回退到默认 "User denied this action"。
// 典型场景：ExitPlanMode 的"附反馈继续规划"。
//
// TargetPermissionMode 专给 ExitPlanMode 批准用：CLI 在 approve 回包后会自动把
// plan→default;若调用方想直接到 acceptEdits / bypassPermissions,在 Allow=true 时
// 一并带上该字段,后端接力一次 SetPermissionMode。取值同 SetPermissionModeRequest。
// 空串或 "default" → 不接力,沿用 CLI 自切。
type AnswerToolPermissionRequest struct {
	SessionID            int64  `json:"sessionId"`
	RequestID            string `json:"requestId"`
	Allow                bool   `json:"allow"`
	AlwaysAllowSession   bool   `json:"alwaysAllowSession,omitempty"`
	DenyReason           string `json:"denyReason,omitempty"`
	TargetPermissionMode string `json:"targetPermissionMode,omitempty"`
}

type AnswerToolPermissionResponse struct{}

// AnswerToolPermission 把审批决策通过 backend 的 ToolPermissionSink 投回正在等待
// 的 control_request。流程对照 AnswerUserQuestion：
//
//  1. 校验 session 存在 + 取 agent backend
//  2. s.selectRunner(ctx, be, sess.ID) 拿 runner；类型断言为 ToolPermissionSink
//     —— 当前仅 claudecode 实现
//  3. 调 sink.SubmitToolPermission(sessionID, requestID, allow, alwaysAllowSession)
//
// 持久化"已审批"到 chat_messages.blocks 走 turn 结束时的常规 SetBlocks 路径
// （后续 EventToolResult 帧会更新 acc）。
func (s *chatSvc) AnswerToolPermission(ctx context.Context, req *AnswerToolPermissionRequest) (*AnswerToolPermissionResponse, error) {
	if req == nil || req.SessionID <= 0 || req.RequestID == "" {
		return nil, i18n.NewError(ctx, code.InvalidParameter)
	}

	sess, err := chat_repo.Session().Find(ctx, req.SessionID)
	if err != nil || sess == nil {
		return nil, i18n.NewError(ctx, code.ChatSessionNotFound)
	}

	a, err := agent_repo.Agent().Find(ctx, sess.AgentID)
	if err != nil || a == nil {
		return nil, i18n.NewError(ctx, code.AgentNotFound)
	}
	backendID := a.AgentBackendID
	if sess.ExecAgentBackendID > 0 {
		backendID = sess.ExecAgentBackendID
	}
	if backendID <= 0 {
		return nil, i18n.NewError(ctx, code.AgentBackendRequired)
	}
	be, err := agent_backend_repo.AgentBackend().Find(ctx, backendID)
	if err != nil || be == nil {
		return nil, i18n.NewError(ctx, code.AgentBackendNotFound)
	}

	runner, err := s.selectRunner(ctx, be, sess.ID)
	if err != nil {
		return nil, i18n.NewError(ctx, code.AgentBackendTypeUnsupported)
	}
	sink, ok := runner.(agentruntime.ToolPermissionSink)
	if !ok {
		return nil, i18n.NewError(ctx, code.AgentBackendTypeUnsupported)
	}

	if err := sink.SubmitToolPermission(ctx, req.SessionID, req.RequestID, req.Allow, req.AlwaysAllowSession, req.DenyReason); err != nil {
		return nil, err
	}

	// ExitPlanMode 批准后接力 mode 切换:CLI 在 approve 回包后会自动把 plan→default,
	// 我们需要的目标若不是 default,在 sink 成功后再触发一次 SetPermissionMode,把
	// default 推到目标 mode (acceptEdits / bypassPermissions)。deny 路径不允许切 mode,
	// 普通工具卡前端不会带 target,所以这里是 ExitPlanMode 专用通道。
	if req.Allow && req.TargetPermissionMode != "" && req.TargetPermissionMode != "default" {
		if _, err := s.SetPermissionMode(ctx, &SetPermissionModeRequest{
			SessionID: req.SessionID,
			Mode:      req.TargetPermissionMode,
		}); err != nil {
			return nil, err
		}
	}
	return &AnswerToolPermissionResponse{}, nil
}
