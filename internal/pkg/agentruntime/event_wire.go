package agentruntime

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/cago-frame/agents/provider"

	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/canonical"
)

// Event wire 编解码。Event 是 sealed interface，无法靠 stdlib 默认反序列化跨线传递；
// 每个具体 Event 类型自己实现 MarshalJSON 把 `{"kind":"<kind>", ...fields}` 拍平到
// 顶层（reusing EventKind 常量当 discriminator），客户端 / daemon 共用 UnmarshalEvent
// 反向拆出来。
//
// 设计要点：
//   - 字段名一律 lowerCamelCase，与 canonical / frontend wire 风格对齐。
//   - 接口字段（ToolCall.Canonical = canonical.CanonicalTool）走 canonical.MarshalTool /
//     UnmarshalTool 这对外置工厂；不在 canonical 具体类型上挂 MarshalJSON，避免和
//     chat_svc/view/chat_block.go 的 CanonicalBlock 包装路径互相打架。
//   - provider.Usage 是外部包且无 JSON tag，用 usageWire 内部 mirror 保证 wire 字段稳定。
//   - ErrorEvent.Err 只走 .Error() 字符串：grep 过整个 repo 无任何 errors.Is 检查
//     ErrorEvent.Err；sentinel 走 RunResult.StopErr 通道（RunResultDoneFrame 里
//     有专门的错误码字段）。

// usageWire 是 provider.Usage 的 wire 镜像。外部 Usage 无 json tag，
// 直接 json.Marshal 会输出 PascalCase 字段；这里固定 lowerCamelCase 让 wire 稳定。
type usageWire struct {
	PromptTokens        int `json:"promptTokens"`
	CompletionTokens    int `json:"completionTokens"`
	ReasoningTokens     int `json:"reasoningTokens"`
	CachedTokens        int `json:"cachedTokens"`
	CacheCreationTokens int `json:"cacheCreationTokens"`
	TotalTokens         int `json:"totalTokens"`
}

func toUsageWire(u *provider.Usage) *usageWire {
	if u == nil {
		return nil
	}
	return &usageWire{
		PromptTokens:        u.PromptTokens,
		CompletionTokens:    u.CompletionTokens,
		ReasoningTokens:     u.ReasoningTokens,
		CachedTokens:        u.CachedTokens,
		CacheCreationTokens: u.CacheCreationTokens,
		TotalTokens:         u.TotalTokens,
	}
}

func (w *usageWire) toUsage() *provider.Usage {
	if w == nil {
		return nil
	}
	return &provider.Usage{
		PromptTokens:        w.PromptTokens,
		CompletionTokens:    w.CompletionTokens,
		ReasoningTokens:     w.ReasoningTokens,
		CachedTokens:        w.CachedTokens,
		CacheCreationTokens: w.CacheCreationTokens,
		TotalTokens:         w.TotalTokens,
	}
}

// ── MarshalJSON ─────────────────────────────────────────────────────────────

func (e TextDelta) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Kind EventKind `json:"kind"`
		Text string    `json:"text"`
	}{EventTextDelta, e.Text})
}

func (e ThinkingDelta) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Kind EventKind `json:"kind"`
		Text string    `json:"text"`
	}{EventThinkingDelta, e.Text})
}

func (e OutputActivity) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Kind EventKind `json:"kind"`
	}{EventOutputActivity})
}

func (e ToolCall) MarshalJSON() ([]byte, error) {
	canonicalBytes, err := canonical.MarshalTool(e.Canonical)
	if err != nil {
		return nil, fmt.Errorf("agentruntime: ToolCall marshal canonical: %w", err)
	}
	out := struct {
		Kind             EventKind       `json:"kind"`
		ID               string          `json:"id,omitempty"`
		Name             string          `json:"name,omitempty"`
		Input            json.RawMessage `json:"input,omitempty"`
		Canonical        json.RawMessage `json:"canonical,omitempty"`
		ParentToolCallID string          `json:"parentToolCallId,omitempty"`
		SubagentRunID    string          `json:"subagentRunId,omitempty"`
	}{
		Kind:             EventToolUseStart,
		ID:               e.ID,
		Name:             e.Name,
		Input:            e.Input,
		ParentToolCallID: e.ParentToolCallID,
		SubagentRunID:    e.SubagentRunID,
	}
	if string(canonicalBytes) != "null" {
		out.Canonical = canonicalBytes
	}
	return json.Marshal(out)
}

func (e ToolResult) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Kind             EventKind       `json:"kind"`
		ToolCallID       string          `json:"toolCallId,omitempty"`
		Content          string          `json:"content,omitempty"`
		IsError          bool            `json:"isError,omitempty"`
		ParentToolCallID string          `json:"parentToolCallId,omitempty"`
		SubagentRunID    string          `json:"subagentRunId,omitempty"`
		Meta             json.RawMessage `json:"meta,omitempty"`
	}{EventToolResult, e.ToolCallID, e.Content, e.IsError, e.ParentToolCallID, e.SubagentRunID, e.Meta})
}

func (e SteerConsumed) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Kind   EventKind       `json:"kind"`
		Steers []ConsumedSteer `json:"steers,omitempty"`
	}{EventSteerConsumed, e.Steers})
}

func (e UserAskRequest) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Kind             EventKind     `json:"kind"`
		RequestID        string        `json:"requestId,omitempty"`
		ToolCallID       string        `json:"toolCallId,omitempty"`
		ParentToolCallID string        `json:"parentToolCallId,omitempty"`
		Questions        []AskQuestion `json:"questions,omitempty"`
	}{EventAskUserQuestion, e.RequestID, e.ToolCallID, e.ParentToolCallID, e.Questions})
}

func (e UserAskResolved) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Kind             EventKind   `json:"kind"`
		RequestID        string      `json:"requestId,omitempty"`
		ParentToolCallID string      `json:"parentToolCallId,omitempty"`
		Answers          []AskAnswer `json:"answers,omitempty"`
		Skipped          bool        `json:"skipped,omitempty"`
	}{EventAskUserQuestionAnswered, e.RequestID, e.ParentToolCallID, e.Answers, e.Skipped})
}

func (e ToolPermissionRequest) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Kind       EventKind       `json:"kind"`
		RequestID  string          `json:"requestId,omitempty"`
		ToolCallID string          `json:"toolCallId,omitempty"`
		ToolName   string          `json:"toolName,omitempty"`
		Input      json.RawMessage `json:"input,omitempty"`
	}{EventToolPermissionRequest, e.RequestID, e.ToolCallID, e.ToolName, e.Input})
}

func (e ToolPermissionResolved) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Kind        EventKind `json:"kind"`
		RequestID   string    `json:"requestId,omitempty"`
		Allowed     bool      `json:"allowed,omitempty"`
		AlwaysAllow bool      `json:"alwaysAllow,omitempty"`
		DenyReason  string    `json:"denyReason,omitempty"`
	}{EventToolPermissionResolved, e.RequestID, e.Allowed, e.AlwaysAllow, e.DenyReason})
}

func (e ExecApprovalRequested) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Kind             EventKind `json:"kind"`
		ID               string    `json:"id,omitempty"`
		CommandText      string    `json:"commandText,omitempty"`
		CommandPreview   string    `json:"commandPreview,omitempty"`
		AllowedDecisions []string  `json:"allowedDecisions,omitempty"`
		Host             string    `json:"host,omitempty"`
		NodeID           string    `json:"nodeId,omitempty"`
		AgentID          string    `json:"agentId,omitempty"`
		SessionKey       string    `json:"sessionKey,omitempty"`
		CreatedAtMs      int64     `json:"createdAtMs,omitempty"`
		ExpiresAtMs      int64     `json:"expiresAtMs,omitempty"`
	}{
		EventExecApprovalRequested, e.ID, e.CommandText, e.CommandPreview,
		e.AllowedDecisions, e.Host, e.NodeID, e.AgentID, e.SessionKey,
		e.CreatedAtMs, e.ExpiresAtMs,
	})
}

func (e ExecApprovalResolved) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Kind         EventKind `json:"kind"`
		ID           string    `json:"id,omitempty"`
		Status       string    `json:"status,omitempty"`
		Decision     string    `json:"decision,omitempty"`
		ResolvedBy   string    `json:"resolvedBy,omitempty"`
		ResolvedAtMs int64     `json:"resolvedAtMs,omitempty"`
	}{EventExecApprovalResolved, e.ID, e.Status, e.Decision, e.ResolvedBy, e.ResolvedAtMs})
}

func (e PermissionModeChanged) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Kind EventKind `json:"kind"`
		Mode string    `json:"mode,omitempty"`
	}{EventPermissionModeChanged, e.Mode})
}

func (e SubagentStarted) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Kind       EventKind    `json:"kind"`
		ToolCallID string       `json:"toolCallId,omitempty"`
		Info       SubagentInfo `json:"info"`
	}{EventSubagentStarted, e.ToolCallID, e.Info})
}

func (e SubagentProgress) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Kind       EventKind    `json:"kind"`
		ToolCallID string       `json:"toolCallId,omitempty"`
		Info       SubagentInfo `json:"info"`
	}{EventSubagentProgress, e.ToolCallID, e.Info})
}

func (e SubagentDone) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Kind       EventKind    `json:"kind"`
		ToolCallID string       `json:"toolCallId,omitempty"`
		Info       SubagentInfo `json:"info"`
	}{EventSubagentDone, e.ToolCallID, e.Info})
}

func (e SubagentModel) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Kind       EventKind `json:"kind"`
		ToolCallID string    `json:"toolCallId,omitempty"`
		Model      string    `json:"model,omitempty"`
	}{EventSubagentModel, e.ToolCallID, e.Model})
}

func (e Retry) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Kind    EventKind `json:"kind"`
		Message string    `json:"message,omitempty"`
		Details string    `json:"details,omitempty"`
		Attempt int       `json:"attempt,omitempty"`
		Max     int       `json:"max,omitempty"`
	}{EventRetry, e.Message, e.Details, e.Attempt, e.Max})
}

func (e UsageUpdate) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Kind             EventKind  `json:"kind"`
		Usage            *usageWire `json:"usage,omitempty"`
		TotalInputTokens int        `json:"totalInputTokens,omitempty"`
		ContextWindow    int        `json:"contextWindow,omitempty"`
	}{EventUsage, toUsageWire(e.Usage), e.TotalInputTokens, e.ContextWindow})
}

func (e ContextWindowUpdated) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Kind   EventKind `json:"kind"`
		Tokens int       `json:"tokens"`
	}{EventContextWindowUpdated, e.Tokens})
}

func (e CompactBoundary) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Kind       EventKind `json:"kind"`
		PreTokens  int       `json:"preTokens,omitempty"`
		PostTokens int       `json:"postTokens,omitempty"`
		Trigger    string    `json:"trigger,omitempty"`
		DurationMs int       `json:"durationMs,omitempty"`
	}{EventCompactBoundary, e.PreTokens, e.PostTokens, e.Trigger, e.DurationMs})
}

func (e RuntimeStatus) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Kind   EventKind `json:"kind"`
		Status string    `json:"status,omitempty"`
	}{EventRuntimeStatus, e.Status})
}

func (e PlanUpdated) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Kind EventKind            `json:"kind"`
		Plan canonical.PlanUpdate `json:"plan"`
	}{EventPlanUpdated, e.Plan})
}

func (e UnrecognizedBlock) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Kind      EventKind       `json:"kind"`
		BlockType string          `json:"blockType,omitempty"`
		Data      json.RawMessage `json:"data,omitempty"`
	}{EventUnrecognizedBlock, e.BlockType, e.Data})
}

func (e Done) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Kind         EventKind `json:"kind"`
		Model        string    `json:"model,omitempty"`
		DurationMs   int       `json:"durationMs,omitempty"`
		FirstTokenMs int       `json:"firstTokenMs,omitempty"`
		TokensPerSec float64   `json:"tokensPerSec,omitempty"`
	}{EventDone, e.Model, e.DurationMs, e.FirstTokenMs, e.TokensPerSec})
}

func (e ErrorEvent) MarshalJSON() ([]byte, error) {
	msg := ""
	if e.Err != nil {
		msg = e.Err.Error()
	}
	return json.Marshal(struct {
		Kind    EventKind `json:"kind"`
		Message string    `json:"message,omitempty"`
	}{EventError, msg})
}

func (e UserMessageEvent) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Kind             EventKind `json:"kind"`
		Text             string    `json:"text,omitempty"`
		SourceDevice     string    `json:"sourceDevice,omitempty"`
		SourceDeviceName string    `json:"sourceDeviceName,omitempty"`
	}{EventUserMessage, e.Text, e.SourceDevice, e.SourceDeviceName})
}

// EventContextWindowUpdated 给 ContextWindowUpdated 事件做 wire discriminator。
// 老 RuntimeEvent 时 ContextWindow 走 RunResult,因此 runner.go 的 EventKind 列表里
// 没这个常量,这里补上。
const EventContextWindowUpdated EventKind = "context_window_updated"

// ── UnmarshalEvent ──────────────────────────────────────────────────────────

// UnmarshalEvent 反向解出具体 Event 类型。data 是 `{"kind":"<kind>", ...}` 的整段 JSON。
//
// 任何 kind 必须在下面的 switch 里有对应分支;新增 sealed Event 类型时编译器虽然不
// 会提醒(没有 exhaustiveness 检查),event_wire_test.go::TestUnmarshalEvent_AllKindsCovered
// 会兜底失败。
func UnmarshalEvent(data []byte) (Event, error) {
	if len(data) == 0 {
		return nil, errors.New("agentruntime: UnmarshalEvent: empty data")
	}
	var head struct {
		Kind EventKind `json:"kind"`
	}
	if err := json.Unmarshal(data, &head); err != nil {
		return nil, fmt.Errorf("agentruntime: UnmarshalEvent: read kind: %w", err)
	}
	if head.Kind == "" {
		return nil, errors.New("agentruntime: UnmarshalEvent: missing kind")
	}
	switch head.Kind {
	case EventOutputActivity:
		return OutputActivity{}, nil
	case EventTextDelta:
		var w struct {
			Text string `json:"text"`
		}
		if err := json.Unmarshal(data, &w); err != nil {
			return nil, err
		}
		return TextDelta{Text: w.Text}, nil
	case EventThinkingDelta:
		var w struct {
			Text string `json:"text"`
		}
		if err := json.Unmarshal(data, &w); err != nil {
			return nil, err
		}
		return ThinkingDelta{Text: w.Text}, nil
	case EventToolUseStart:
		var w struct {
			ID               string          `json:"id"`
			Name             string          `json:"name"`
			Input            json.RawMessage `json:"input"`
			Canonical        json.RawMessage `json:"canonical"`
			ParentToolCallID string          `json:"parentToolCallId"`
			SubagentRunID    string          `json:"subagentRunId"`
		}
		if err := json.Unmarshal(data, &w); err != nil {
			return nil, err
		}
		var c canonical.CanonicalTool
		if len(w.Canonical) > 0 && string(w.Canonical) != "null" {
			parsed, err := canonical.UnmarshalTool(w.Canonical)
			if err != nil {
				return nil, fmt.Errorf("agentruntime: ToolCall unmarshal canonical: %w", err)
			}
			c = parsed
		}
		return ToolCall{
			ID:               w.ID,
			Name:             w.Name,
			Input:            w.Input,
			Canonical:        c,
			ParentToolCallID: w.ParentToolCallID,
			SubagentRunID:    w.SubagentRunID,
		}, nil
	case EventToolResult:
		var w struct {
			ToolCallID       string          `json:"toolCallId"`
			Content          string          `json:"content"`
			IsError          bool            `json:"isError"`
			ParentToolCallID string          `json:"parentToolCallId"`
			SubagentRunID    string          `json:"subagentRunId"`
			Meta             json.RawMessage `json:"meta"`
		}
		if err := json.Unmarshal(data, &w); err != nil {
			return nil, err
		}
		return ToolResult{
			ToolCallID:       w.ToolCallID,
			Content:          w.Content,
			IsError:          w.IsError,
			ParentToolCallID: w.ParentToolCallID,
			SubagentRunID:    w.SubagentRunID,
			Meta:             w.Meta,
		}, nil
	case EventSteerConsumed:
		var w struct {
			Steers []ConsumedSteer `json:"steers"`
		}
		if err := json.Unmarshal(data, &w); err != nil {
			return nil, err
		}
		return SteerConsumed{Steers: w.Steers}, nil
	case EventAskUserQuestion:
		var w struct {
			RequestID        string        `json:"requestId"`
			ToolCallID       string        `json:"toolCallId"`
			ParentToolCallID string        `json:"parentToolCallId"`
			Questions        []AskQuestion `json:"questions"`
		}
		if err := json.Unmarshal(data, &w); err != nil {
			return nil, err
		}
		return UserAskRequest{
			RequestID:        w.RequestID,
			ToolCallID:       w.ToolCallID,
			ParentToolCallID: w.ParentToolCallID,
			Questions:        w.Questions,
		}, nil
	case EventAskUserQuestionAnswered:
		var w struct {
			RequestID        string      `json:"requestId"`
			ParentToolCallID string      `json:"parentToolCallId"`
			Answers          []AskAnswer `json:"answers"`
			Skipped          bool        `json:"skipped"`
		}
		if err := json.Unmarshal(data, &w); err != nil {
			return nil, err
		}
		return UserAskResolved{
			RequestID:        w.RequestID,
			ParentToolCallID: w.ParentToolCallID,
			Answers:          w.Answers,
			Skipped:          w.Skipped,
		}, nil
	case EventToolPermissionRequest:
		var w struct {
			RequestID  string          `json:"requestId"`
			ToolCallID string          `json:"toolCallId"`
			ToolName   string          `json:"toolName"`
			Input      json.RawMessage `json:"input"`
		}
		if err := json.Unmarshal(data, &w); err != nil {
			return nil, err
		}
		return ToolPermissionRequest{
			RequestID:  w.RequestID,
			ToolCallID: w.ToolCallID,
			ToolName:   w.ToolName,
			Input:      w.Input,
		}, nil
	case EventToolPermissionResolved:
		var w struct {
			RequestID   string `json:"requestId"`
			Allowed     bool   `json:"allowed"`
			AlwaysAllow bool   `json:"alwaysAllow"`
			DenyReason  string `json:"denyReason"`
		}
		if err := json.Unmarshal(data, &w); err != nil {
			return nil, err
		}
		return ToolPermissionResolved{
			RequestID:   w.RequestID,
			Allowed:     w.Allowed,
			AlwaysAllow: w.AlwaysAllow,
			DenyReason:  w.DenyReason,
		}, nil
	case EventExecApprovalRequested:
		var w struct {
			ID               string   `json:"id"`
			CommandText      string   `json:"commandText"`
			CommandPreview   string   `json:"commandPreview"`
			AllowedDecisions []string `json:"allowedDecisions"`
			Host             string   `json:"host"`
			NodeID           string   `json:"nodeId"`
			AgentID          string   `json:"agentId"`
			SessionKey       string   `json:"sessionKey"`
			CreatedAtMs      int64    `json:"createdAtMs"`
			ExpiresAtMs      int64    `json:"expiresAtMs"`
		}
		if err := json.Unmarshal(data, &w); err != nil {
			return nil, err
		}
		return ExecApprovalRequested{
			ID: w.ID, CommandText: w.CommandText, CommandPreview: w.CommandPreview,
			AllowedDecisions: w.AllowedDecisions, Host: w.Host, NodeID: w.NodeID,
			AgentID: w.AgentID, SessionKey: w.SessionKey,
			CreatedAtMs: w.CreatedAtMs, ExpiresAtMs: w.ExpiresAtMs,
		}, nil
	case EventExecApprovalResolved:
		var w struct {
			ID           string `json:"id"`
			Status       string `json:"status"`
			Decision     string `json:"decision"`
			ResolvedBy   string `json:"resolvedBy"`
			ResolvedAtMs int64  `json:"resolvedAtMs"`
		}
		if err := json.Unmarshal(data, &w); err != nil {
			return nil, err
		}
		return ExecApprovalResolved{
			ID: w.ID, Status: w.Status, Decision: w.Decision,
			ResolvedBy: w.ResolvedBy, ResolvedAtMs: w.ResolvedAtMs,
		}, nil
	case EventPermissionModeChanged:
		var w struct {
			Mode string `json:"mode"`
		}
		if err := json.Unmarshal(data, &w); err != nil {
			return nil, err
		}
		return PermissionModeChanged{Mode: w.Mode}, nil
	case EventSubagentStarted:
		var w struct {
			ToolCallID string       `json:"toolCallId"`
			Info       SubagentInfo `json:"info"`
		}
		if err := json.Unmarshal(data, &w); err != nil {
			return nil, err
		}
		return SubagentStarted{ToolCallID: w.ToolCallID, Info: w.Info}, nil
	case EventSubagentProgress:
		var w struct {
			ToolCallID string       `json:"toolCallId"`
			Info       SubagentInfo `json:"info"`
		}
		if err := json.Unmarshal(data, &w); err != nil {
			return nil, err
		}
		return SubagentProgress{ToolCallID: w.ToolCallID, Info: w.Info}, nil
	case EventSubagentDone:
		var w struct {
			ToolCallID string       `json:"toolCallId"`
			Info       SubagentInfo `json:"info"`
		}
		if err := json.Unmarshal(data, &w); err != nil {
			return nil, err
		}
		return SubagentDone{ToolCallID: w.ToolCallID, Info: w.Info}, nil
	case EventSubagentModel:
		var w struct {
			ToolCallID string `json:"toolCallId"`
			Model      string `json:"model"`
		}
		if err := json.Unmarshal(data, &w); err != nil {
			return nil, err
		}
		return SubagentModel{ToolCallID: w.ToolCallID, Model: w.Model}, nil
	case EventRetry:
		var w struct {
			Message string `json:"message"`
			Details string `json:"details"`
			Attempt int    `json:"attempt"`
			Max     int    `json:"max"`
		}
		if err := json.Unmarshal(data, &w); err != nil {
			return nil, err
		}
		return Retry{Message: w.Message, Details: w.Details, Attempt: w.Attempt, Max: w.Max}, nil
	case EventUsage:
		var w struct {
			Usage            *usageWire `json:"usage"`
			TotalInputTokens int        `json:"totalInputTokens"`
			ContextWindow    int        `json:"contextWindow"`
		}
		if err := json.Unmarshal(data, &w); err != nil {
			return nil, err
		}
		return UsageUpdate{
			Usage:            w.Usage.toUsage(),
			TotalInputTokens: w.TotalInputTokens,
			ContextWindow:    w.ContextWindow,
		}, nil
	case EventContextWindowUpdated:
		var w struct {
			Tokens int `json:"tokens"`
		}
		if err := json.Unmarshal(data, &w); err != nil {
			return nil, err
		}
		return ContextWindowUpdated{Tokens: w.Tokens}, nil
	case EventCompactBoundary:
		var w struct {
			PreTokens  int    `json:"preTokens"`
			PostTokens int    `json:"postTokens"`
			Trigger    string `json:"trigger"`
			DurationMs int    `json:"durationMs"`
		}
		if err := json.Unmarshal(data, &w); err != nil {
			return nil, err
		}
		return CompactBoundary{
			PreTokens:  w.PreTokens,
			PostTokens: w.PostTokens,
			Trigger:    w.Trigger,
			DurationMs: w.DurationMs,
		}, nil
	case EventRuntimeStatus:
		var w struct {
			Status string `json:"status"`
		}
		if err := json.Unmarshal(data, &w); err != nil {
			return nil, err
		}
		return RuntimeStatus{Status: w.Status}, nil
	case EventPlanUpdated:
		var w struct {
			Plan canonical.PlanUpdate `json:"plan"`
		}
		if err := json.Unmarshal(data, &w); err != nil {
			return nil, err
		}
		return PlanUpdated{Plan: w.Plan}, nil
	case EventUnrecognizedBlock:
		var w struct {
			BlockType string          `json:"blockType"`
			Data      json.RawMessage `json:"data"`
		}
		if err := json.Unmarshal(data, &w); err != nil {
			return nil, err
		}
		return UnrecognizedBlock{BlockType: w.BlockType, Data: w.Data}, nil
	case EventDone:
		var w struct {
			Model        string  `json:"model"`
			DurationMs   int     `json:"durationMs"`
			FirstTokenMs int     `json:"firstTokenMs"`
			TokensPerSec float64 `json:"tokensPerSec"`
		}
		if err := json.Unmarshal(data, &w); err != nil {
			return nil, err
		}
		return Done{Model: w.Model, DurationMs: w.DurationMs, FirstTokenMs: w.FirstTokenMs, TokensPerSec: w.TokensPerSec}, nil
	case EventUserMessage:
		var w struct {
			Text             string `json:"text"`
			SourceDevice     string `json:"sourceDevice"`
			SourceDeviceName string `json:"sourceDeviceName"`
		}
		if err := json.Unmarshal(data, &w); err != nil {
			return nil, err
		}
		return UserMessageEvent{
			Text:             w.Text,
			SourceDevice:     w.SourceDevice,
			SourceDeviceName: w.SourceDeviceName,
		}, nil
	case EventError:
		var w struct {
			Message string `json:"message"`
		}
		if err := json.Unmarshal(data, &w); err != nil {
			return nil, err
		}
		var ev ErrorEvent
		if w.Message != "" {
			ev.Err = errors.New(w.Message)
		}
		return ev, nil
	default:
		return nil, fmt.Errorf("agentruntime: UnmarshalEvent: unknown kind %q", head.Kind)
	}
}
