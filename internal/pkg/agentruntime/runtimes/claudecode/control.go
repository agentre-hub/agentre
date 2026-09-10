package claudecode

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"

	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
	"github.com/agentre-hub/agentre/pkg/claudecode"
)

// toolNameExitPlanMode CLI 计划审批 control_request 的 tool_name。
// 与 chat_svc/handlers 的同名常量各自归属(包间不互相 import)。
const toolNameExitPlanMode = "ExitPlanMode"

// SubmitAnswer 把前端提交的 AskUserQuestion 答案反向投回 CLI。语义同顶层
// claudecode.go.SubmitAnswer:
//
//   - 找到 sessionID 对应的 claudeActive(必须 turn 在飞行)
//   - takeAskWaiter 取走对应 requestID(重复提交 / 已超时 → 错)
//   - skipped → 写 deny + message;非 skipped → 拼 answers map + RespondToControl(allow, updatedInput)
//   - 成功后 emit UserAskResolved 给 drain 通道,让 chat_svc patch ack
func (r *Runtime) SubmitAnswer(ctx context.Context, sessionID int64, requestID string, questions []agentruntime.AskQuestion, answers []agentruntime.AskAnswer, skipped bool) error {
	if sessionID <= 0 {
		return fmt.Errorf("agentruntime/runtimes/claudecode: invalid sessionID %d", sessionID)
	}
	if requestID == "" {
		return errors.New("agentruntime/runtimes/claudecode: empty requestID")
	}
	v, ok := r.cache.Get(sessionKey(sessionID))
	if !ok {
		logger.Ctx(ctx).Warn("claudecode runtime: SubmitAnswer no active turn",
			zap.Int64("sessionID", sessionID), zap.String("requestID", requestID))
		return agentruntime.ErrNoActiveTurn
	}
	a := v.(*claudeActive)
	waiter := a.takeAskWaiter(requestID)
	if waiter == nil {
		logger.Ctx(ctx).Warn("claudecode runtime: SubmitAnswer no waiting AskUserQuestion",
			zap.Int64("sessionID", sessionID), zap.String("requestID", requestID))
		return fmt.Errorf("agentruntime/runtimes/claudecode: no waiting AskUserQuestion for requestID %s: %w", requestID, agentruntime.ErrWaiterNotFound)
	}

	if skipped {
		if err := a.handle.RespondToControl(ctx, requestID, claudecode.PermissionResult{
			Behavior: "deny",
			Message:  "User skipped the AskUserQuestion prompt; continue without picking an option.",
		}); err != nil {
			return err
		}
		emitUserAskResolved(a, waiter, requestID, true, nil)
		return nil
	}

	canonicalQs := waiter.questions
	if len(questions) > 0 && len(questions) != len(canonicalQs) {
		return fmt.Errorf("agentruntime/runtimes/claudecode: client supplied %d questions but waiter recorded %d", len(questions), len(canonicalQs))
	}

	answersMap, err := agentruntime.BuildUpdatedInputAnswers(canonicalQs, answers)
	if err != nil {
		return err
	}

	updated, err := mergeAnswersIntoInput(waiter.rawInput, answersMap)
	if err != nil {
		return err
	}
	if err := a.handle.RespondToControl(ctx, requestID, claudecode.PermissionResult{
		Behavior:     "allow",
		UpdatedInput: updated,
	}); err != nil {
		return err
	}
	emitUserAskResolved(a, waiter, requestID, false, answers)
	return nil
}

// emitUserAskResolved 把答案终态 emit 回 **发起该请求的那一轮** 的 drain 通道
// (waiter.out)。用 session 全局的单槽出口做不到:自主续轮 / 后台 subagent 活动轮
// 与 user turn 各有各的通道,投错通道 = 事件被丢 = chat_svc 收不到 UserAskResolved,
// 会话永久停在 waiting(sess-2425)。那一轮已结束时 emitTo 静默丢弃。
func emitUserAskResolved(a *claudeActive, w *askWaiter, requestID string, skipped bool, answers []agentruntime.AskAnswer) {
	a.emitTo(w.out, agentruntime.UserAskResolved{
		RequestID: requestID,
		Skipped:   skipped,
		Answers:   answers,
	})
}

// SubmitToolPermission 实现非-AskUserQuestion 的工具审批反向投回。
// 语义同顶层 claudecode.go.SubmitToolPermission:take-and-delete waiter,
// allow + alwaysAllowSession=true → 附加 addRules permission update,
// allow once → updatedInput=parsed(rawInput);deny → fixed message。
func (r *Runtime) SubmitToolPermission(ctx context.Context, sessionID int64, requestID string, allow, alwaysAllowSession bool, denyReason string) error {
	if sessionID <= 0 {
		return fmt.Errorf("agentruntime/runtimes/claudecode: invalid sessionID %d", sessionID)
	}
	if requestID == "" {
		return errors.New("agentruntime/runtimes/claudecode: empty requestID")
	}
	v, ok := r.cache.Get(sessionKey(sessionID))
	if !ok {
		return agentruntime.ErrNoActiveTurn
	}
	a := v.(*claudeActive)
	waiter := a.takePermWaiter(requestID)
	if waiter == nil {
		return fmt.Errorf("agentruntime/runtimes/claudecode: no waiting tool permission for requestID %s: %w", requestID, agentruntime.ErrWaiterNotFound)
	}

	var result claudecode.PermissionResult
	if allow {
		parsed, perr := parseInputAsObject(waiter.rawInput)
		if perr != nil {
			return fmt.Errorf("agentruntime/runtimes/claudecode: parse waiter rawInput: %w", perr)
		}
		result = claudecode.PermissionResult{Behavior: "allow", UpdatedInput: parsed}
		if alwaysAllowSession {
			result.UpdatedPermissions = []claudecode.PermissionUpdate{{
				Type:        "addRules",
				Rules:       []claudecode.PermissionRule{{ToolName: waiter.toolName}},
				Behavior:    "allow",
				Destination: "session",
			}}
		}
	} else {
		msg := denyReason
		if msg == "" {
			msg = "User denied this action"
		}
		result = claudecode.PermissionResult{Behavior: "deny", Message: msg}
	}

	if err := a.handle.RespondToControl(ctx, requestID, result); err != nil {
		return err
	}
	emitToolPermissionResolved(a, waiter, requestID, allow, alwaysAllowSession, denyReason)
	return nil
}

// emitToolPermissionResolved 把决策终态 emit 回发起该请求的那一轮的 drain 通道
// (理由同 emitUserAskResolved)。DenyReason 透传(review finding #11 修复后,
// 新 sealed Event 携带 DenyReason)。
func emitToolPermissionResolved(a *claudeActive, w *permWaiter, requestID string, allowed, alwaysAllow bool, denyReason string) {
	a.emitTo(w.out, agentruntime.ToolPermissionResolved{
		RequestID:   requestID,
		Allowed:     allowed,
		AlwaysAllow: alwaysAllow,
		DenyReason:  denyReason,
	})
}

// PendingWaiters implements agentruntime.WaiterLister (R7): a reconnecting
// client needs to rebuild the approval / question cards for whatever this
// session is currently blocked on, not just answer an already-known
// requestID. sessionID unknown to this runner (never spawned / already
// evicted) returns the zero-value snapshot — same as "nothing pending",
// never an error.
func (r *Runtime) PendingWaiters(_ context.Context, sessionID int64) agentruntime.WaiterSnapshot {
	if sessionID <= 0 {
		return agentruntime.WaiterSnapshot{}
	}
	v, ok := r.cache.Get(sessionKey(sessionID))
	if !ok {
		return agentruntime.WaiterSnapshot{}
	}
	return v.(*claudeActive).pendingWaiters()
}

// handleControlRequest 按 tool_name 分派 control_request:
//   - AskUserQuestion → 走 askWaiter 路径,emit UserAskRequest
//   - 其它工具 → 默认走 permWaiter 路径 emit ToolPermissionRequest
//     bypassPermissions 模式下立即放行(防御性兜底)
//   - ExitPlanMode 豁免于 bypass 短路:计划审批是流程门禁不是工具权限,
//     永远走审批卡。否则快照失同步(或会话真在 bypass)时计划被静默放行,
//     CLI 随后自切 mode,用户全程没有审批机会。
//
// RespondToControl 的写动作放后台 goroutine,避免阻塞 drain 主循环。
func handleControlRequest(req *claudecode.ControlRequestEvent, active *claudeActive, out chan<- agentruntime.Event) {
	if isAskUserQuestionToolName(req.ToolName) {
		handleAskUserQuestion(req, active, out)
		return
	}

	parsed, perr := parseInputAsObject(req.Input)
	if perr != nil {
		go func() {
			_ = active.handle.RespondToControl(context.Background(), req.RequestID, claudecode.PermissionResult{
				Behavior: "deny",
				Message:  "agentre: invalid tool input: " + perr.Error(),
			})
		}()
		return
	}

	if active.permissionModeSnapshot() == "bypassPermissions" && req.ToolName != toolNameExitPlanMode {
		go func() {
			_ = active.handle.RespondToControl(context.Background(), req.RequestID, claudecode.PermissionResult{
				Behavior:     "allow",
				UpdatedInput: parsed,
			})
		}()
		return
	}

	active.registerPermWaiter(req.RequestID, req.ToolName, req.Input, out)
	out <- agentruntime.ToolPermissionRequest{
		RequestID:  req.RequestID,
		ToolCallID: "", // claudecode CLI 在 control_request 里不直接给 tool_use_id;前端按 RequestID merge
		ToolName:   req.ToolName,
		Input:      append([]byte(nil), req.Input...),
	}
}

// handleAskUserQuestion AskUserQuestion 路径。语义同顶层
// claudecode.go.handleClaudeAskUserQuestion。
func handleAskUserQuestion(req *claudecode.ControlRequestEvent, active *claudeActive, out chan<- agentruntime.Event) {
	questions, err := agentruntime.ParseAskUserQuestionInput(req.Input)
	if err != nil {
		go func() {
			_ = active.handle.RespondToControl(context.Background(), req.RequestID, claudecode.PermissionResult{
				Behavior: "deny",
				Message:  "agentre: failed to parse AskUserQuestion input: " + err.Error(),
			})
		}()
		return
	}
	active.registerAskWaiter(req.RequestID, questions, req.Input, out)
	out <- agentruntime.UserAskRequest{
		RequestID: req.RequestID,
		Questions: questions,
	}
}

// parseInputAsObject 把 control_request.input 反序列化为 map[string]any。
func parseInputAsObject(raw json.RawMessage) (map[string]any, error) {
	out := map[string]any{}
	if len(raw) == 0 {
		return out, nil
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// mergeAnswersIntoInput 把 BuildUpdatedInputAnswers 出来的 {qText: csv} 合并
// 到 control_request 的原始 input bytes,保留所有原字段。
func mergeAnswersIntoInput(raw json.RawMessage, answers map[string]string) (map[string]any, error) {
	out := make(map[string]any)
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &out); err != nil {
			return nil, fmt.Errorf("agentruntime/runtimes/claudecode: parse raw input for merge: %w", err)
		}
	}
	out["answers"] = answers
	return out, nil
}
