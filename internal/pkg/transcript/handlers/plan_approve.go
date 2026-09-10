package handlers

import (
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/canonical"
)

// BuildPlanApproveActions 根据 session 启动时的 permission mode 算出
// ExitPlanMode 审批卡上可点的按钮列表。
//
// 规则:
//   - launch == "bypassPermissions" → [bypass, manual, refine]
//   - 其它(空串 / default / acceptEdits / plan)→ [accept_edits, manual, refine]
//
// 同一组里 refine 始终最后(它带 RequiresFeedback,前端展开 feedback textarea)。
// resolved 后调用方应传 nil,前端按只读态忽略。
func BuildPlanApproveActions(launchPermissionMode string) []canonical.PlanAction {
	first := canonical.PlanAction{
		ID:   canonical.PlanActionIDApproveAcceptEdits,
		Kind: canonical.PlanActionApprove,
	}
	if launchPermissionMode == "bypassPermissions" {
		first = canonical.PlanAction{
			ID:   canonical.PlanActionIDApproveBypassPermissions,
			Kind: canonical.PlanActionApprove,
		}
	}
	return []canonical.PlanAction{
		first,
		{ID: canonical.PlanActionIDApproveManual, Kind: canonical.PlanActionApprove},
		{ID: canonical.PlanActionIDRefine, Kind: canonical.PlanActionRefine, RequiresFeedback: true},
	}
}
