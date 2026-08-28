package chat_svc

import (
	"context"
	"encoding/json"
	"time"

	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"

	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/canonical"
	"github.com/agentre-hub/agentre/internal/service/chat_svc/blocks"
	"github.com/agentre-hub/agentre/internal/service/chat_svc/view"
)

// dispatcherEmitter 把 handlers 输出的 map[string]any{kind: ..., ...} 中间形态
// 转成 typed ChatStreamEvent 后透传给 s.emitter。
//
// 设计:handlers/ 包不依赖 chat_svc(避免循环),所以它们 emit 抽象形态;
// 转换在 chat_svc 边界完成,保留前端 wire schema 不变。
//
// 不识别的 kind → 丢弃(forward-compat,允许未来新增 kind 时 chat_svc 没更新也不崩)。
type dispatcherEmitter struct {
	svc *chatSvc
}

func (d *dispatcherEmitter) Emit(ctx context.Context, stream string, raw any) {
	if d == nil || d.svc == nil {
		return
	}
	m, ok := raw.(map[string]any)
	if !ok {
		return
	}
	kind, _ := m["kind"].(string)
	if kind == "" {
		return
	}

	ev := ChatStreamEvent{Kind: ChatStreamEventKind(kind)}
	switch kind {
	case string(StreamChunk), string(StreamThinking):
		ev.Delta, _ = m["delta"].(string)

	case string(StreamOutputActivity):
		// 纯计时信号,没有附加字段 —— 只要 Kind 转发出去就够了。

	case string(StreamToolUse):
		ev.ToolCallID = stringOf(m, "toolUseId")
		ev.ToolName = stringOf(m, "toolName")
		ev.ToolInput = mapOf(m, "toolInput")
		ev.ParentToolCallID = stringOf(m, "parentToolCallId")
		ev.SubagentRunID = stringOf(m, "subagentRunId")
		// Canonical: runtime translator 算出的统一识别;handler 走 raw_tool.go 透传
		// (m["canonical"] = tc2.Canonical, 类型 canonical.CanonicalTool)。
		if c, ok := m["canonical"].(canonical.CanonicalTool); ok {
			ev.Canonical = view.FromCanonical(c)
		}

	case string(StreamToolResult):
		ev.ToolCallID = stringOf(m, "toolUseId")
		ev.ToolResult, _ = m["toolResult"].(string)
		ev.IsError, _ = m["isError"].(bool)
		ev.ParentToolCallID = stringOf(m, "parentToolCallId")
		ev.SubagentRunID = stringOf(m, "subagentRunId")
		ev.ToolResultMeta = mapOf(m, "toolResultMeta")

	case string(StreamAskUserQuestion):
		ev.AskUserQuestion = askUserQuestionFromMap(m)
		// 同步 canonical:前端 CanonicalToolRouter 读 block.canonical 路径,
		// 让 UserAsk 走与 FileWrite/FileEdit 一致的统一渲染入口。
		// Answered 必须传,否则 resolved 帧到前端时 canonical.userAsk.answered 永远 false,
		// UserAskCard 的 status pill 翻不到 ANSWERED(只有 SKIPPED 路径正常)。
		ev.Canonical = view.FromCanonical(canonical.UserAsk{
			RequestID: ev.AskUserQuestion.RequestID,
			Questions: ev.AskUserQuestion.Questions,
			Answers:   ev.AskUserQuestion.Answers,
			Answered:  ev.AskUserQuestion.Answered,
			Skipped:   ev.AskUserQuestion.Skipped,
			Expired:   ev.AskUserQuestion.Expired,
		})

	case string(StreamPlanUpdate):
		ev.Delta, _ = m["delta"].(string)
		if c, ok := m["canonical"].(canonical.CanonicalTool); ok {
			ev.Canonical = view.FromCanonical(c)
		}

	case string(StreamToolPermissionRequest):
		ev.ToolPermission = toolPermissionFromMap(m)
		// 优先吃 handler 预设的 canonical(handlers/tool_permission.go 已经按
		// ExitPlanMode / 普通工具分支构造完毕,且 ExitPlanMode 带 Actions);
		// 兜底走旧合成逻辑保历史 emitter 调用方(目前只剩这条 case 用兜底)。
		if c, ok := m["canonical"].(canonical.CanonicalTool); ok {
			ev.Canonical = view.FromCanonical(c)
		} else if ev.ToolPermission != nil {
			if ev.ToolPermission.ToolName == "ExitPlanMode" {
				planText, _ := ev.ToolPermission.ToolInput["plan"].(string)
				ev.Canonical = view.FromCanonical(canonical.PlanApproveRequest{
					RequestID: ev.ToolPermission.RequestID,
					PlanText:  planText,
					Resolved:  ev.ToolPermission.Resolved,
					Allowed:   ev.ToolPermission.Allowed,
				})
			} else {
				ev.Canonical = view.FromCanonical(canonical.ToolPermission{
					RequestID:   ev.ToolPermission.RequestID,
					ToolName:    ev.ToolPermission.ToolName,
					ToolInput:   ev.ToolPermission.ToolInput,
					Resolved:    ev.ToolPermission.Resolved,
					Allowed:     ev.ToolPermission.Allowed,
					AlwaysAllow: ev.ToolPermission.AlwaysAllow,
				})
			}
		}

	case string(StreamExecApproval):
		if block, ok := m["execApproval"].(*blocks.ExecApprovalBlock); ok && block != nil {
			ev.ExecApproval = execApprovalBlockToDTO(*block)
		}

	case string(StreamSubagentStarted), string(StreamSubagentProgress), string(StreamSubagentDone):
		ev.ToolCallID = stringOf(m, "toolUseId")
		// info → ChatBlockSubagent (复用已有投影 subagentInfoToChatBlock)
		ev.Subagent = subagentInfoMapToChatBlock(m["info"])
		if ev.Subagent != nil {
			ev.Canonical = view.FromCanonical(canonical.AgentSpawn{
				TaskID:          ev.Subagent.TaskID,
				SubagentType:    ev.Subagent.SubagentType,
				TaskDescription: ev.Subagent.TaskDescription,
				Prompt:          ev.Subagent.Prompt,
				LastToolName:    ev.Subagent.LastToolName,
				ToolUses:        ev.Subagent.ToolUses,
				TotalTokens:     ev.Subagent.TotalTokens,
				DurationMs:      ev.Subagent.DurationMs,
				Status:          ev.Subagent.Status,
				Mode:            ev.Subagent.Mode,
				Runs:            agentSpawnRunsFromRuntime(ev.Subagent.Runs),
			})
		}

	case string(StreamSubagentModel):
		// 只带 toolUseId + model,不走 subagentInfoMapToChatBlock 那套整份快照投影
		// (R4)——handler 已经只 emit 这两个 key,这里原样透传即可。
		ev.ToolCallID = stringOf(m, "toolUseId")
		ev.Model = stringOf(m, "model")

	case string(StreamRetry):
		ev.RetryAttempt = intOf(m, "retryAttempt")
		ev.RetryMaxAttempts = intOf(m, "retryMaxAttempts")
		ev.RetryMessage = stringOf(m, "retryMessage")
		ev.RetryDetails = stringOf(m, "retryDetails")
		ev.RetryAt = int64Of(m, "retryAt")
		if ev.RetryAt == 0 {
			ev.RetryAt = time.Now().UnixMilli()
		}

	case string(StreamSessionStatus):
		ev.SessionStatus = sessionStatusFromMap(m["sessionStatus"])
		// 防御日志:mid-stream 经由 handler 推 session_status 时,任何 agentStatus
		// 都不应该是 "error" —— 末端错误走 chat.go finalize / failTurn 两条专用
		// 路径,不走 dispatcher。这里出现 "error" 就是新路径漏标,必须捕获。
		if ev.SessionStatus != nil && ev.SessionStatus.AgentStatus == "error" {
			logger.Ctx(ctx).Warn("chat_svc: dispatcherEmitter forwarded session_status with agentStatus=error (unexpected mid-stream path)",
				zap.String("stream", stream),
				zap.Bool("needsAttention", ev.SessionStatus.NeedsAttention),
				zap.String("permissionMode", ev.SessionStatus.PermissionMode))
		}

	case string(StreamUsage):
		ev.Usage = usageFromMap(m["usage"])

	case string(StreamCompactBoundary):
		ev.Compact = &ChatCompactBoundary{
			MessageID: int64Of(m, "messageId"),
			Seq:       intOf(m, "seq"),
			PreTokens: intOf(m, "preTokens"),
			Trigger:   stringOf(m, "trigger"),
			At:        int64Of(m, "at"),
		}

	case string(StreamRuntimeStatus):
		ev.RuntimeStatus = &ChatRuntimeStatus{
			Status:     stringOf(m, "status"),
			Compacting: boolOf(m, "compacting"),
		}

	case string(StreamError):
		ev.Error = stringOf(m, "error")
		// 防御日志:目前 chat.go runTurn 在 ErrorEvent case 用 continue 跳过
		// dispatcher,所以 handlers.ErrorHandler.Apply 不会被触发,理论上不会
		// 走到这里。若以后路径变了让 dispatcher 重新接管 ErrorEvent,这里就
		// 能立刻看到 mid-stream "error" 帧泄出去,排查跟 finalize 重复 emit 时
		// 不用再翻代码。
		logger.Ctx(ctx).Warn("chat_svc: dispatcherEmitter forwarded mid-stream error (unexpected — ErrorHandler should not fire)",
			zap.String("stream", stream),
			zap.String("error", ev.Error))

	case "message_end":
		// handlers DoneHandler emit "message_end" 中间形态,chat_svc 在 runTurn
		// 收尾(finalize)统一 emit StreamDone/Aborted/Error;这里丢弃即可。
		return

	default:
		return
	}
	d.svc.emitter.Emit(ctx, stream, ev)
}

func stringOf(m map[string]any, k string) string {
	if v, ok := m[k].(string); ok {
		return v
	}
	return ""
}

func intOf(m map[string]any, k string) int {
	switch v := m[k].(type) {
	case int:
		return v
	case int64:
		return int(v)
	case float64:
		return int(v)
	}
	return 0
}

func int64Of(m map[string]any, k string) int64 {
	switch v := m[k].(type) {
	case int64:
		return v
	case int:
		return int64(v)
	case float64:
		return int64(v)
	}
	return 0
}

func mapOf(m map[string]any, k string) map[string]any {
	if v, ok := m[k].(map[string]any); ok {
		return v
	}
	return nil
}

func askUserQuestionFromMap(m map[string]any) *ChatBlockAskUserQuestion {
	// handlers/user_ask.go emit 时携带原 block 引用 (blocks.UserAskBlock pointer)
	// 通过 "askUserQuestion" key;若无则按 fallback 字段拼。
	if blk, ok := m["askUserQuestion"].(*blocks.UserAskBlock); ok && blk != nil {
		return &ChatBlockAskUserQuestion{
			RequestID: blk.RequestID,
			Questions: blk.Questions,
			Answered:  blk.Answered,
			Answers:   blk.Answers,
			Skipped:   blk.Skipped,
			Expired:   blk.Expired,
		}
	}
	out := &ChatBlockAskUserQuestion{
		RequestID: stringOf(m, "requestId"),
		Answered:  boolOf(m, "answered"),
		Skipped:   boolOf(m, "skipped"),
	}
	return out
}

func toolPermissionFromMap(m map[string]any) *ChatBlockToolPermission {
	if blk, ok := m["toolPermission"].(*blocks.ToolPermissionBlock); ok && blk != nil {
		return &ChatBlockToolPermission{
			RequestID:   blk.RequestID,
			ToolName:    blk.ToolName,
			ToolInput:   blk.ToolInput,
			Resolved:    blk.Resolved,
			Allowed:     blk.Allowed,
			AlwaysAllow: blk.AlwaysAllow,
		}
	}
	return &ChatBlockToolPermission{
		RequestID:   stringOf(m, "requestId"),
		ToolName:    stringOf(m, "toolName"),
		ToolInput:   mapOf(m, "toolInput"),
		Resolved:    boolOf(m, "resolved"),
		Allowed:     boolOf(m, "allowed"),
		AlwaysAllow: boolOf(m, "alwaysAllow"),
	}
}

func boolOf(m map[string]any, k string) bool {
	if v, ok := m[k].(bool); ok {
		return v
	}
	return false
}

// subagentInfoMapToChatBlock 解析 handler emit 的 "info" 字段(agentruntime.SubagentInfo)
// 到 ChatBlockSubagent 投影。
func subagentInfoMapToChatBlock(raw any) *ChatBlockSubagent {
	switch info := raw.(type) {
	case nil:
		return nil
	default:
		// 通过 JSON round-trip 跨包转(SubagentInfo 在 agentruntime,ChatBlockSubagent
		// 在 chat_svc;字段同形)。性能可接受 —— Subagent 事件 turn 内频率低。
		buf, err := json.Marshal(info)
		if err != nil {
			return nil
		}
		out := &ChatBlockSubagent{}
		if err := json.Unmarshal(buf, out); err != nil {
			return nil
		}
		return out
	}
}

func sessionStatusFromMap(raw any) *ChatSessionStatusPatch {
	m, ok := raw.(map[string]any)
	if !ok {
		return nil
	}
	out := &ChatSessionStatusPatch{
		AgentStatus:    stringOf(m, "agentStatus"),
		NeedsAttention: boolOf(m, "needsAttention"),
		BgRunning:      boolOf(m, "bgRunning"),
		PermissionMode: stringOf(m, "permissionMode"),
	}
	if v, ok := m["contextWindow"]; ok {
		switch vv := v.(type) {
		case int:
			out.ContextWindow = vv
		case int64:
			out.ContextWindow = int(vv)
		case float64:
			out.ContextWindow = int(vv)
		}
	}
	return out
}

func usageFromMap(raw any) *ChatStreamUsage {
	m, ok := raw.(map[string]any)
	if !ok {
		return nil
	}
	return &ChatStreamUsage{
		MessageID:           int64Of(m, "messageId"),
		PromptTokens:        intOf(m, "promptTokens"),
		CompletionTokens:    intOf(m, "completionTokens"),
		CachedTokens:        intOf(m, "cachedTokens"),
		CacheCreationTokens: intOf(m, "cacheCreationTokens"),
		ReasoningTokens:     intOf(m, "reasoningTokens"),
		TotalInputTokens:    intOf(m, "totalInputTokens"),
		ContextWindow:       intOf(m, "contextWindow"),
	}
}
