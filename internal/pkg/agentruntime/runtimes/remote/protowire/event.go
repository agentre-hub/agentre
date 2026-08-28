// Package protowire owns the only conversion boundary between sealed
// agentruntime values and the generated Protobuf WebSocket protocol.
package protowire

import (
	"errors"
	"fmt"

	"github.com/cago-frame/agents/provider"
	"google.golang.org/protobuf/proto"

	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/canonical"
	"github.com/agentre-hub/agentre/pkg/wire/agentrewire"
)

type EventNotification struct {
	SessionID int64
	Seq       int64
	Event     agentruntime.Event
}

func MarshalEventNotification(sessionID, seq int64, event agentruntime.Event, autonomous bool) ([]byte, error) {
	n := &agentrewire.RuntimeEventNotification{SessionId: sessionID, Seq: seq}
	err := marshalEvent(n, event)
	if err != nil {
		return nil, err
	}
	notification := &agentrewire.RpcNotification{}
	if autonomous {
		notification.Payload = &agentrewire.RpcNotification_AutonomousTurnEvent{AutonomousTurnEvent: n}
	} else {
		notification.Payload = &agentrewire.RpcNotification_RuntimeEvent{RuntimeEvent: n}
	}
	return proto.Marshal(&agentrewire.RpcFrame{Body: &agentrewire.RpcFrame_Notification{Notification: notification}})
}

func UnmarshalEventNotification(data []byte) (EventNotification, bool, error) {
	var frame agentrewire.RpcFrame
	if err := proto.Unmarshal(data, &frame); err != nil {
		return EventNotification{}, false, fmt.Errorf("protowire: decode frame: %w", err)
	}
	n := frame.GetNotification()
	if n == nil {
		return EventNotification{}, false, errors.New("protowire: 不是 runtime event 通知")
	}
	autonomous := false
	eventFrame := n.GetRuntimeEvent()
	if eventFrame == nil {
		eventFrame = n.GetAutonomousTurnEvent()
		autonomous = eventFrame != nil
	}
	if eventFrame == nil {
		return EventNotification{}, false, errors.New("protowire: 不是 runtime event 通知")
	}
	event, err := unmarshalEvent(eventFrame)
	if err != nil {
		return EventNotification{}, false, err
	}
	return EventNotification{SessionID: eventFrame.GetSessionId(), Seq: eventFrame.GetSeq(), Event: event}, autonomous, nil
}

// marshalEvent / unmarshalEvent 是 sealed Event 与 oneof 之间**逐字段**的一对
// 映射。两个 switch 必须成对改动：给 agentruntime 加一个 Event 而不在这里补两
// 个分支，编码侧会走到 default 报错 —— 编译器拦不住,这是 sealed interface 的
// 代价。
//
// 这里刻意不走「Event → JSON → protojson → proto」那条中转。它看着省事,实际
// 是错的:agentruntime 的 JSON 形态由各类型自己的 MarshalJSON 决定,而嵌套值
// (AskQuestion / AskAnswer)根本没有 json tag,序列化出来是 Go 字段名
// (`"Question"`);protojson 认的是 proto 的 JSON 名(`question`),配上
// DiscardUnknown 就是**静默丢光**。反向同样有坑:protojson 把 int64 写成字符串,
// 喂回 encoding/json 的 int64 字段直接报错,整条事件解不出来。
//
// 两种失效都不是理论问题,populated 往返用例一跑就是四条红:提问卡与答案在远端
// 全空、两条审批事件根本解不出来。逐字段映射换来的不只是省掉两次序列化。
func marshalEvent(frame *agentrewire.RuntimeEventNotification, event agentruntime.Event) error {
	switch value := event.(type) {
	case agentruntime.TextDelta:
		frame.Event = &agentrewire.RuntimeEventNotification_TextDelta{TextDelta: &agentrewire.TextDelta{Text: value.Text}}
	case agentruntime.ThinkingDelta:
		frame.Event = &agentrewire.RuntimeEventNotification_ThinkingDelta{ThinkingDelta: &agentrewire.ThinkingDelta{Text: value.Text}}
	case agentruntime.OutputActivity:
		frame.Event = &agentrewire.RuntimeEventNotification_OutputActivity{OutputActivity: &agentrewire.OutputActivity{}}
	case agentruntime.ToolCall:
		canonicalBytes, err := canonical.MarshalTool(value.Canonical)
		if err != nil {
			return err
		}
		if string(canonicalBytes) == "null" {
			canonicalBytes = nil
		}
		frame.Event = &agentrewire.RuntimeEventNotification_ToolCall{ToolCall: &agentrewire.ToolCall{Id: value.ID, Name: value.Name, Input: value.Input, Canonical: canonicalBytes, ParentToolCallId: value.ParentToolCallID, SubagentRunId: value.SubagentRunID}}
	case agentruntime.ToolResult:
		frame.Event = &agentrewire.RuntimeEventNotification_ToolResult{ToolResult: &agentrewire.ToolResult{ToolCallId: value.ToolCallID, Content: value.Content, IsError: value.IsError, ParentToolCallId: value.ParentToolCallID, SubagentRunId: value.SubagentRunID, Meta: value.Meta}}
	case agentruntime.SteerConsumed:
		m := &agentrewire.SteerConsumed{}
		for _, steer := range value.Steers {
			m.Steers = append(m.Steers, &agentrewire.ConsumedSteer{QueuedId: steer.QueuedID, Text: steer.Text, SourcePeer: steer.SourcePeer, SourceName: steer.SourceName})
		}
		frame.Event = &agentrewire.RuntimeEventNotification_SteerConsumed{SteerConsumed: m}
	case agentruntime.UserAskRequest:
		frame.Event = &agentrewire.RuntimeEventNotification_UserAskRequest{UserAskRequest: &agentrewire.UserAskRequest{
			RequestId: value.RequestID, ToolCallId: value.ToolCallID, ParentToolCallId: value.ParentToolCallID,
			Questions: AskQuestionsToProto(value.Questions),
		}}
	case agentruntime.UserAskResolved:
		frame.Event = &agentrewire.RuntimeEventNotification_UserAskResolved{UserAskResolved: &agentrewire.UserAskResolved{
			RequestId: value.RequestID, ParentToolCallId: value.ParentToolCallID,
			Answers: AskAnswersToProto(value.Answers), Skipped: value.Skipped,
		}}
	case agentruntime.ToolPermissionRequest:
		frame.Event = &agentrewire.RuntimeEventNotification_ToolPermissionRequest{ToolPermissionRequest: &agentrewire.ToolPermissionRequest{RequestId: value.RequestID, ToolCallId: value.ToolCallID, ToolName: value.ToolName, Input: value.Input}}
	case agentruntime.ToolPermissionResolved:
		frame.Event = &agentrewire.RuntimeEventNotification_ToolPermissionResolved{ToolPermissionResolved: &agentrewire.ToolPermissionResolved{RequestId: value.RequestID, Allowed: value.Allowed, AlwaysAllow: value.AlwaysAllow, DenyReason: value.DenyReason}}
	case agentruntime.ExecApprovalRequested:
		frame.Event = &agentrewire.RuntimeEventNotification_ExecApprovalRequested{ExecApprovalRequested: &agentrewire.ExecApprovalRequested{
			Id: value.ID, CommandText: value.CommandText, CommandPreview: value.CommandPreview,
			AllowedDecisions: value.AllowedDecisions, Host: value.Host, NodeId: value.NodeID,
			AgentId: value.AgentID, SessionKey: value.SessionKey,
			CreatedAtMs: value.CreatedAtMs, ExpiresAtMs: value.ExpiresAtMs,
		}}
	case agentruntime.ExecApprovalResolved:
		frame.Event = &agentrewire.RuntimeEventNotification_ExecApprovalResolved{ExecApprovalResolved: &agentrewire.ExecApprovalResolved{Id: value.ID, Status: value.Status, Decision: value.Decision, ResolvedBy: value.ResolvedBy, ResolvedAtMs: value.ResolvedAtMs}}
	case agentruntime.PermissionModeChanged:
		frame.Event = &agentrewire.RuntimeEventNotification_PermissionModeChanged{PermissionModeChanged: &agentrewire.PermissionModeChanged{Mode: value.Mode}}
	case agentruntime.SubagentStarted:
		frame.Event = &agentrewire.RuntimeEventNotification_SubagentStarted{SubagentStarted: subagentEventToProto(value.ToolCallID, value.Info)}
	case agentruntime.SubagentProgress:
		frame.Event = &agentrewire.RuntimeEventNotification_SubagentProgress{SubagentProgress: subagentEventToProto(value.ToolCallID, value.Info)}
	case agentruntime.SubagentDone:
		frame.Event = &agentrewire.RuntimeEventNotification_SubagentDone{SubagentDone: subagentEventToProto(value.ToolCallID, value.Info)}
	case agentruntime.SubagentModel:
		frame.Event = &agentrewire.RuntimeEventNotification_SubagentModel{SubagentModel: &agentrewire.SubagentModel{ToolCallId: value.ToolCallID, Model: value.Model}}
	case agentruntime.Retry:
		frame.Event = &agentrewire.RuntimeEventNotification_Retry{Retry: &agentrewire.Retry{Message: value.Message, Details: value.Details, Attempt: int32(value.Attempt), Max: int32(value.Max)}}
	case agentruntime.UsageUpdate:
		frame.Event = &agentrewire.RuntimeEventNotification_UsageUpdate{UsageUpdate: &agentrewire.UsageUpdate{
			Usage: usageToProto(value.Usage), TotalInputTokens: int32(value.TotalInputTokens), ContextWindow: int32(value.ContextWindow),
		}}
	case agentruntime.ContextWindowUpdated:
		frame.Event = &agentrewire.RuntimeEventNotification_ContextWindowUpdated{ContextWindowUpdated: &agentrewire.ContextWindowUpdated{Tokens: int32(value.Tokens)}}
	case agentruntime.CompactBoundary:
		frame.Event = &agentrewire.RuntimeEventNotification_CompactBoundary{CompactBoundary: &agentrewire.CompactBoundary{PreTokens: int32(value.PreTokens), PostTokens: int32(value.PostTokens), Trigger: value.Trigger, DurationMs: int32(value.DurationMs)}}
	case agentruntime.RuntimeStatus:
		frame.Event = &agentrewire.RuntimeEventNotification_RuntimeStatus{RuntimeStatus: &agentrewire.RuntimeStatus{Status: value.Status}}
	case agentruntime.PlanUpdated:
		m := &agentrewire.PlanUpdated{Text: value.Plan.Text}
		for _, step := range value.Plan.Steps {
			m.Steps = append(m.Steps, &agentrewire.PlanStep{Id: step.ID, Step: step.Step, Status: string(step.Status)})
		}
		for _, action := range value.Plan.Actions {
			m.Actions = append(m.Actions, &agentrewire.PlanAction{Id: action.ID, Kind: string(action.Kind), RequiresFeedback: action.RequiresFeedback})
		}
		frame.Event = &agentrewire.RuntimeEventNotification_PlanUpdated{PlanUpdated: m}
	case agentruntime.UnrecognizedBlock:
		frame.Event = &agentrewire.RuntimeEventNotification_UnrecognizedBlock{UnrecognizedBlock: &agentrewire.UnrecognizedBlock{BlockType: value.BlockType, Data: value.Data}}
	case agentruntime.Done:
		frame.Event = &agentrewire.RuntimeEventNotification_Done{Done: &agentrewire.Done{}}
	case agentruntime.ErrorEvent:
		message := ""
		if value.Err != nil {
			message = value.Err.Error()
		}
		frame.Event = &agentrewire.RuntimeEventNotification_Error{Error: &agentrewire.ErrorEvent{Message: message}}
	case agentruntime.UserMessageEvent:
		frame.Event = &agentrewire.RuntimeEventNotification_UserMessage{UserMessage: &agentrewire.UserMessage{Text: value.Text, SourceDevice: value.SourceDevice, SourceDeviceName: value.SourceDeviceName}}
	default:
		return fmt.Errorf("protowire: 不支持的 runtime event %T", event)
	}
	return nil
}

func unmarshalEvent(frame *agentrewire.RuntimeEventNotification) (agentruntime.Event, error) {
	switch value := frame.GetEvent().(type) {
	case *agentrewire.RuntimeEventNotification_TextDelta:
		return agentruntime.TextDelta{Text: value.TextDelta.GetText()}, nil
	case *agentrewire.RuntimeEventNotification_ThinkingDelta:
		return agentruntime.ThinkingDelta{Text: value.ThinkingDelta.GetText()}, nil
	case *agentrewire.RuntimeEventNotification_OutputActivity:
		return agentruntime.OutputActivity{}, nil
	case *agentrewire.RuntimeEventNotification_ToolCall:
		var c canonical.CanonicalTool
		var err error
		if len(value.ToolCall.GetCanonical()) > 0 {
			c, err = canonical.UnmarshalTool(value.ToolCall.GetCanonical())
			if err != nil {
				return nil, err
			}
		}
		return agentruntime.ToolCall{ID: value.ToolCall.GetId(), Name: value.ToolCall.GetName(), Input: value.ToolCall.GetInput(), Canonical: c, ParentToolCallID: value.ToolCall.GetParentToolCallId(), SubagentRunID: value.ToolCall.GetSubagentRunId()}, nil
	case *agentrewire.RuntimeEventNotification_ToolResult:
		v := value.ToolResult
		return agentruntime.ToolResult{ToolCallID: v.GetToolCallId(), Content: v.GetContent(), IsError: v.GetIsError(), ParentToolCallID: v.GetParentToolCallId(), SubagentRunID: v.GetSubagentRunId(), Meta: v.GetMeta()}, nil
	case *agentrewire.RuntimeEventNotification_SteerConsumed:
		out := agentruntime.SteerConsumed{}
		for _, steer := range value.SteerConsumed.GetSteers() {
			out.Steers = append(out.Steers, agentruntime.ConsumedSteer{QueuedID: steer.GetQueuedId(), Text: steer.GetText(), SourcePeer: steer.GetSourcePeer(), SourceName: steer.GetSourceName()})
		}
		return out, nil
	case *agentrewire.RuntimeEventNotification_UserAskRequest:
		v := value.UserAskRequest
		out := agentruntime.UserAskRequest{RequestID: v.GetRequestId(), ToolCallID: v.GetToolCallId(), ParentToolCallID: v.GetParentToolCallId()}
		for _, question := range v.GetQuestions() {
			out.Questions = append(out.Questions, decodeAskQuestion(question))
		}
		return out, nil
	case *agentrewire.RuntimeEventNotification_UserAskResolved:
		v := value.UserAskResolved
		out := agentruntime.UserAskResolved{RequestID: v.GetRequestId(), ParentToolCallID: v.GetParentToolCallId(), Skipped: v.GetSkipped()}
		for _, answer := range v.GetAnswers() {
			out.Answers = append(out.Answers, agentruntime.AskAnswer{QuestionIndex: int(answer.GetQuestionIndex()), Labels: answer.GetLabels(), OtherText: answer.GetOtherText()})
		}
		return out, nil
	case *agentrewire.RuntimeEventNotification_ToolPermissionRequest:
		v := value.ToolPermissionRequest
		return agentruntime.ToolPermissionRequest{RequestID: v.GetRequestId(), ToolCallID: v.GetToolCallId(), ToolName: v.GetToolName(), Input: v.GetInput()}, nil
	case *agentrewire.RuntimeEventNotification_ToolPermissionResolved:
		v := value.ToolPermissionResolved
		return agentruntime.ToolPermissionResolved{RequestID: v.GetRequestId(), Allowed: v.GetAllowed(), AlwaysAllow: v.GetAlwaysAllow(), DenyReason: v.GetDenyReason()}, nil
	case *agentrewire.RuntimeEventNotification_ExecApprovalRequested:
		v := value.ExecApprovalRequested
		return agentruntime.ExecApprovalRequested{
			ID: v.GetId(), CommandText: v.GetCommandText(), CommandPreview: v.GetCommandPreview(),
			AllowedDecisions: v.GetAllowedDecisions(), Host: v.GetHost(), NodeID: v.GetNodeId(),
			AgentID: v.GetAgentId(), SessionKey: v.GetSessionKey(),
			CreatedAtMs: v.GetCreatedAtMs(), ExpiresAtMs: v.GetExpiresAtMs(),
		}, nil
	case *agentrewire.RuntimeEventNotification_ExecApprovalResolved:
		v := value.ExecApprovalResolved
		return agentruntime.ExecApprovalResolved{ID: v.GetId(), Status: v.GetStatus(), Decision: v.GetDecision(), ResolvedBy: v.GetResolvedBy(), ResolvedAtMs: v.GetResolvedAtMs()}, nil
	case *agentrewire.RuntimeEventNotification_PermissionModeChanged:
		return agentruntime.PermissionModeChanged{Mode: value.PermissionModeChanged.GetMode()}, nil
	case *agentrewire.RuntimeEventNotification_SubagentStarted:
		v := value.SubagentStarted
		return agentruntime.SubagentStarted{ToolCallID: v.GetToolCallId(), Info: subagentInfoFromProto(v.GetInfo())}, nil
	case *agentrewire.RuntimeEventNotification_SubagentProgress:
		v := value.SubagentProgress
		return agentruntime.SubagentProgress{ToolCallID: v.GetToolCallId(), Info: subagentInfoFromProto(v.GetInfo())}, nil
	case *agentrewire.RuntimeEventNotification_SubagentDone:
		v := value.SubagentDone
		return agentruntime.SubagentDone{ToolCallID: v.GetToolCallId(), Info: subagentInfoFromProto(v.GetInfo())}, nil
	case *agentrewire.RuntimeEventNotification_SubagentModel:
		v := value.SubagentModel
		return agentruntime.SubagentModel{ToolCallID: v.GetToolCallId(), Model: v.GetModel()}, nil
	case *agentrewire.RuntimeEventNotification_Retry:
		v := value.Retry
		return agentruntime.Retry{Message: v.GetMessage(), Details: v.GetDetails(), Attempt: int(v.GetAttempt()), Max: int(v.GetMax())}, nil
	case *agentrewire.RuntimeEventNotification_UsageUpdate:
		v := value.UsageUpdate
		return agentruntime.UsageUpdate{Usage: usageFromProto(v.GetUsage()), TotalInputTokens: int(v.GetTotalInputTokens()), ContextWindow: int(v.GetContextWindow())}, nil
	case *agentrewire.RuntimeEventNotification_ContextWindowUpdated:
		return agentruntime.ContextWindowUpdated{Tokens: int(value.ContextWindowUpdated.GetTokens())}, nil
	case *agentrewire.RuntimeEventNotification_CompactBoundary:
		v := value.CompactBoundary
		return agentruntime.CompactBoundary{PreTokens: int(v.GetPreTokens()), PostTokens: int(v.GetPostTokens()), Trigger: v.GetTrigger(), DurationMs: int(v.GetDurationMs())}, nil
	case *agentrewire.RuntimeEventNotification_RuntimeStatus:
		return agentruntime.RuntimeStatus{Status: value.RuntimeStatus.GetStatus()}, nil
	case *agentrewire.RuntimeEventNotification_PlanUpdated:
		v := value.PlanUpdated
		plan := canonical.PlanUpdate{Text: v.GetText()}
		for _, step := range v.GetSteps() {
			plan.Steps = append(plan.Steps, canonical.PlanStep{ID: step.GetId(), Step: step.GetStep(), Status: canonical.PlanStepStatus(step.GetStatus())})
		}
		for _, action := range v.GetActions() {
			plan.Actions = append(plan.Actions, canonical.PlanAction{ID: action.GetId(), Kind: canonical.PlanActionKind(action.GetKind()), RequiresFeedback: action.GetRequiresFeedback()})
		}
		return agentruntime.PlanUpdated{Plan: plan}, nil
	case *agentrewire.RuntimeEventNotification_UnrecognizedBlock:
		v := value.UnrecognizedBlock
		return agentruntime.UnrecognizedBlock{BlockType: v.GetBlockType(), Data: v.GetData()}, nil
	case *agentrewire.RuntimeEventNotification_Done:
		return agentruntime.Done{}, nil
	case *agentrewire.RuntimeEventNotification_Error:
		// 空 message 还原成 nil Err,与 ErrorEvent 的 JSON 形态同义:那边
		// `message,omitempty` 也是「没有错误文本就不带这个字段」。
		out := agentruntime.ErrorEvent{}
		if message := value.Error.GetMessage(); message != "" {
			out.Err = errors.New(message)
		}
		return out, nil
	case *agentrewire.RuntimeEventNotification_UserMessage:
		v := value.UserMessage
		return agentruntime.UserMessageEvent{Text: v.GetText(), SourceDevice: v.GetSourceDevice(), SourceDeviceName: v.GetSourceDeviceName()}, nil
	default:
		return nil, fmt.Errorf("protowire: 不支持的 runtime event %T", frame.GetEvent())
	}
}

func subagentEventToProto(toolCallID string, info agentruntime.SubagentInfo) *agentrewire.SubagentEvent {
	out := &agentrewire.SubagentInfo{
		TaskId: info.TaskID, SubagentType: info.SubagentType, Kind: info.Kind,
		TaskDescription: info.TaskDescription, Prompt: info.Prompt, LastToolName: info.LastToolName,
		ToolUses: int32(info.ToolUses), TotalTokens: int32(info.TotalTokens), DurationMs: int32(info.DurationMs),
		Status: info.Status, Mode: info.Mode,
	}
	for _, run := range info.Runs {
		out.Runs = append(out.Runs, &agentrewire.SubagentRun{
			Id: run.ID, Index: int32(run.Index), Agent: run.Agent, Profile: run.Profile,
			AgentSource: run.AgentSource, Task: run.Task, RequestedModel: run.RequestedModel,
			Model: run.Model, Status: run.Status, LastToolName: run.LastToolName,
			ToolUses: int32(run.ToolUses), Summary: run.Summary, ErrorMessage: run.ErrorMessage,
		})
	}
	return &agentrewire.SubagentEvent{ToolCallId: toolCallID, Info: out}
}

func subagentInfoFromProto(info *agentrewire.SubagentInfo) agentruntime.SubagentInfo {
	out := agentruntime.SubagentInfo{
		TaskID: info.GetTaskId(), SubagentType: info.GetSubagentType(), Kind: info.GetKind(),
		TaskDescription: info.GetTaskDescription(), Prompt: info.GetPrompt(), LastToolName: info.GetLastToolName(),
		ToolUses: int(info.GetToolUses()), TotalTokens: int(info.GetTotalTokens()), DurationMs: int(info.GetDurationMs()),
		Status: info.GetStatus(), Mode: info.GetMode(),
	}
	for _, run := range info.GetRuns() {
		out.Runs = append(out.Runs, agentruntime.SubagentRun{
			ID: run.GetId(), Index: int(run.GetIndex()), Agent: run.GetAgent(), Profile: run.GetProfile(),
			AgentSource: run.GetAgentSource(), Task: run.GetTask(), RequestedModel: run.GetRequestedModel(),
			Model: run.GetModel(), Status: run.GetStatus(), LastToolName: run.GetLastToolName(),
			ToolUses: int(run.GetToolUses()), Summary: run.GetSummary(), ErrorMessage: run.GetErrorMessage(),
		})
	}
	return out
}

// usageToProto / usageFromProto 保住 nil 语义:UsageUpdate.Usage 是指针,
// 「这一帧没带 usage」与「带了一份全零 usage」是两件事。
func usageToProto(usage *provider.Usage) *agentrewire.Usage {
	if usage == nil {
		return nil
	}
	return &agentrewire.Usage{
		PromptTokens: int32(usage.PromptTokens), CompletionTokens: int32(usage.CompletionTokens),
		ReasoningTokens: int32(usage.ReasoningTokens), CachedTokens: int32(usage.CachedTokens),
		CacheCreationTokens: int32(usage.CacheCreationTokens), TotalTokens: int32(usage.TotalTokens),
	}
}

func usageFromProto(usage *agentrewire.Usage) *provider.Usage {
	if usage == nil {
		return nil
	}
	return &provider.Usage{
		PromptTokens: int(usage.GetPromptTokens()), CompletionTokens: int(usage.GetCompletionTokens()),
		ReasoningTokens: int(usage.GetReasoningTokens()), CachedTokens: int(usage.GetCachedTokens()),
		CacheCreationTokens: int(usage.GetCacheCreationTokens()), TotalTokens: int(usage.GetTotalTokens()),
	}
}
