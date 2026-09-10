package chat_svc

import (
	"context"
	"strings"

	"github.com/cago-frame/cago/pkg/i18n"
	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"

	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/canonical"
	"github.com/agentre-hub/agentre/internal/pkg/code"
	"github.com/agentre-hub/agentre/internal/repository/chat_repo"
)

// ResolvePlanActionRequest 前端计划审批/历史计划 action 按钮点击后调
// App.ResolvePlanAction 的 payload。
//
// ActionID 由 backend 装配到 canonical.Plan{Update,ApproveRequest}.Actions[].ID,
// 前端只按 plan action 语义渲染、原值回传。命名空间见 canonical/plan_action.go 注释。
//
// RequestID:
//   - plan.approve.* 必填,对应 canonical.PlanApproveRequest.RequestID
//     (实为 ToolPermissionRequest.RequestID);
//   - plan.execute 忽略(plan_update canonical 不带 RequestID);
//   - plan.refine 有 RequestID 时反馈审批请求,无 RequestID 时作为下一条 plan-mode user message。
//
// Feedback 仅 actionID == plan.refine 时使用:审批请求里作为 DenyReason 反灌给 AI;
// plan update 里空 → 默认文案 "继续完善上述计划。",非空 → 直接当下一条 user message。
type ResolvePlanActionRequest struct {
	SessionID int64  `json:"sessionId"`
	RequestID string `json:"requestId,omitempty"`
	ActionID  string `json:"actionId"`
	Feedback  string `json:"feedback,omitempty"`
}

type ResolvePlanActionResponse struct {
	SessionID          int64  `json:"sessionId,omitempty"`
	UserMessageID      int64  `json:"userMessageId,omitempty"`
	AssistantMessageID int64  `json:"assistantMessageId,omitempty"`
	Stream             string `json:"stream,omitempty"`
}

// planActionDispatch 描述 ResolvePlanAction 决定要做的事:走 AnswerToolPermission
// 还是 Send,以及对应的入参。纯结构,方便单测断言决策不调底层。
type planActionDispatch struct {
	answerPermission *AnswerToolPermissionRequest // 二选一
	send             *SendRequest                 // 二选一
	allowPlanWaiting bool
}

// planActionDecision 把 actionID + feedback + (session, request) 映射到一个 dispatch。
// 不识别 / 缺参 → 返回 errCode > 0,调用方按 code 抛 i18n 错误。
//
// 之所以拆出来:ResolvePlanAction 执行体里要么调 svc.AnswerToolPermission(走 sink + DB),
// 要么调 svc.Send(走 startTurn + runner),两条都重。把决策抽出做单测,执行体的覆盖
// 留给 AnswerToolPermission_test / chat_test 各自的端到端用例。
func planActionDecision(req *ResolvePlanActionRequest) (planActionDispatch, int) {
	if req == nil || req.SessionID <= 0 || strings.TrimSpace(req.ActionID) == "" {
		return planActionDispatch{}, code.InvalidParameter
	}
	actionID := strings.TrimSpace(req.ActionID)
	switch {
	case strings.HasPrefix(actionID, "plan.approve."):
		mode, ok := mapPlanApproveAction(actionID)
		if !ok {
			return planActionDispatch{}, code.ChatPlanActionUnknown
		}
		if req.RequestID == "" {
			return planActionDispatch{}, code.InvalidParameter
		}
		return planActionDispatch{answerPermission: &AnswerToolPermissionRequest{
			SessionID:            req.SessionID,
			RequestID:            req.RequestID,
			Allow:                true,
			TargetPermissionMode: mode,
		}}, 0
	case actionID == canonical.PlanActionIDRefine:
		if req.RequestID != "" {
			return planActionDispatch{answerPermission: &AnswerToolPermissionRequest{
				SessionID:  req.SessionID,
				RequestID:  req.RequestID,
				Allow:      false,
				DenyReason: strings.TrimSpace(req.Feedback),
			}}, 0
		}
		text := strings.TrimSpace(req.Feedback)
		if text == "" {
			text = "继续完善上述计划。"
		}
		return planActionDispatch{send: &SendRequest{
			SessionID:      req.SessionID,
			Text:           text,
			PermissionMode: "plan",
		}, allowPlanWaiting: true}, 0
	case actionID == canonical.PlanActionIDExecute:
		return planActionDispatch{send: &SendRequest{
			SessionID:      req.SessionID,
			Text:           "Implement the plan.",
			PermissionMode: "default",
		}, allowPlanWaiting: true}, 0
	}
	return planActionDispatch{}, code.ChatPlanActionUnknown
}

// ResolvePlanAction 统一分发计划 actionId 到底层服务方法:
//
//   - plan.approve.{bypass_permissions|accept_edits|manual} -> AnswerToolPermission(allow=true, mode=...)
//   - plan.refine + requestId                              -> AnswerToolPermission(allow=false, denyReason=feedback)
//   - plan.execute                                         -> Send("Implement the plan.", mode=default)
//   - plan.refine 无 requestId                              -> Send(feedback or 默认, mode=plan)
//
// 前端不再分支 backendType/source,统一调本方法;backend 内部依 actionID 语义路由。
func (s *chatSvc) ResolvePlanAction(ctx context.Context, req *ResolvePlanActionRequest) (*ResolvePlanActionResponse, error) {
	d, errCode := planActionDecision(req)
	if errCode != 0 {
		return nil, i18n.NewError(ctx, errCode)
	}
	switch {
	case d.answerPermission != nil:
		if _, err := s.AnswerToolPermission(ctx, d.answerPermission); err != nil {
			return nil, err
		}
	case d.send != nil:
		resp, err := s.send(ctx, d.send, sendOptions{allowPlanWaiting: d.allowPlanWaiting})
		if err != nil {
			return nil, err
		}
		if d.clearsPlanActions(req) {
			if err := s.clearLatestActionablePlanActions(context.WithoutCancel(ctx), req.SessionID); err != nil {
				logger.Ctx(ctx).Warn("clear latest actionable plan actions failed",
					zap.Int64("sessionID", req.SessionID),
					zap.String("actionID", req.ActionID),
					zap.Error(err))
			}
		}
		return &ResolvePlanActionResponse{
			SessionID:          resp.SessionID,
			UserMessageID:      resp.UserMessageID,
			AssistantMessageID: resp.AssistantMessageID,
			Stream:             resp.Stream,
		}, nil
	}
	return &ResolvePlanActionResponse{}, nil
}

func (d planActionDispatch) clearsPlanActions(req *ResolvePlanActionRequest) bool {
	if req == nil || d.send == nil || strings.TrimSpace(req.RequestID) != "" {
		return false
	}
	actionID := strings.TrimSpace(req.ActionID)
	return actionID == canonical.PlanActionIDExecute || actionID == canonical.PlanActionIDRefine
}

func (s *chatSvc) clearLatestActionablePlanActions(ctx context.Context, sessionID int64) error {
	msgs, err := chat_repo.Message().List(ctx, sessionID)
	if err != nil {
		return err
	}
	for i := len(msgs) - 1; i >= 0; i-- {
		msg := msgs[i]
		if msg == nil || msg.Role != "assistant" {
			continue
		}
		bs, err := msg.GetBlocks()
		if err != nil {
			return err
		}
		changed := false
		for idx, b := range bs {
			switch p := b.(type) {
			case PlanBlock:
				if len(p.Actions) > 0 {
					p.Actions = nil
					bs[idx] = p
					changed = true
				}
			case *PlanBlock:
				if p != nil && len(p.Actions) > 0 {
					p.Actions = nil
					changed = true
				}
			}
		}
		if !changed {
			continue
		}
		if err := msg.SetBlocks(bs); err != nil {
			return err
		}
		return chat_repo.Message().Update(ctx, msg)
	}
	return nil
}

// mapPlanApproveAction 把 plan.approve.* 映射成
// AnswerToolPermissionRequest.TargetPermissionMode 的合法值。
func mapPlanApproveAction(actionID string) (string, bool) {
	switch actionID {
	case canonical.PlanActionIDApproveBypassPermissions:
		return "bypassPermissions", true
	case canonical.PlanActionIDApproveAcceptEdits:
		return "acceptEdits", true
	case canonical.PlanActionIDApproveManual:
		// AnswerToolPermission 内部对 ""/"default" 走 CLI 自切(approve 后回 default),
		// 不接力 SetPermissionMode。
		return "default", true
	}
	return "", false
}
