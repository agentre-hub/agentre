package openclaw

import (
	"encoding/json"
	"strings"

	"github.com/cago-frame/agents/provider"

	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
	"github.com/agentre-hub/agentre/internal/pkg/openclawgateway"
)

type agentEventPayload struct {
	RunID      string          `json:"runId"`
	SessionKey string          `json:"sessionKey"`
	Seq        int64           `json:"seq"`
	Stream     string          `json:"stream"`
	Data       json.RawMessage `json:"data"`
}

type chatEventPayload struct {
	RunID        string          `json:"runId"`
	SessionKey   string          `json:"sessionKey"`
	Seq          int64           `json:"seq"`
	State        string          `json:"state"`
	DeltaText    string          `json:"deltaText"`
	Usage        json.RawMessage `json:"usage"`
	ErrorMessage string          `json:"errorMessage"`
}

func (a *activeTurn) handleGatewayEvent(event openclawgateway.Event) (needsReconcile bool) {
	switch event.Name {
	case "agent":
		var payload agentEventPayload
		if json.Unmarshal(event.Payload, &payload) != nil || payload.RunID != a.runID {
			return false
		}
		if !a.matchesSession(payload.SessionKey) {
			return false
		}
		a.adoptSessionKey(payload.SessionKey)
		if payload.Seq > 0 {
			if payload.Seq <= a.lastAgentSeq {
				return false
			}
			needsReconcile = a.lastAgentSeq > 0 && payload.Seq > a.lastAgentSeq+1
			a.lastAgentSeq = payload.Seq
		}
		a.handleAgentPayload(payload)
	case "chat":
		var payload chatEventPayload
		if json.Unmarshal(event.Payload, &payload) != nil || payload.RunID != a.runID ||
			!a.matchesSession(payload.SessionKey) {
			return false
		}
		a.adoptSessionKey(payload.SessionKey)
		if payload.Seq > 0 {
			if payload.Seq <= a.lastChatSeq {
				return false
			}
			needsReconcile = a.lastChatSeq > 0 && payload.Seq > a.lastChatSeq+1
			a.lastChatSeq = payload.Seq
		}
		a.handleChatPayload(payload)
	case "exec.approval.requested":
		var record gatewayExecApprovalRecord
		if json.Unmarshal(event.Payload, &record) == nil {
			a.handleApprovalRequested(record)
		}
	case "exec.approval.resolved":
		a.handleApprovalResolved(event.Payload)
	}
	return needsReconcile
}

func (a *activeTurn) handleAgentPayload(payload agentEventPayload) {
	switch payload.Stream {
	case "assistant":
		var data struct {
			Text  string `json:"text"`
			Delta string `json:"delta"`
		}
		if json.Unmarshal(payload.Data, &data) == nil {
			if delta := deltaFromText(&a.assistant, data.Text, data.Delta); delta != "" {
				a.emit(agentruntime.TextDelta{Text: delta})
			}
		}
	case "thinking":
		var data struct {
			Text  string `json:"text"`
			Delta string `json:"delta"`
		}
		if json.Unmarshal(payload.Data, &data) == nil {
			if delta := deltaFromText(&a.thinking, data.Text, data.Delta); delta != "" {
				a.emit(agentruntime.ThinkingDelta{Text: delta})
			}
		}
	case "tool":
		a.handleTool(payload.Data)
	case "usage":
		a.handleUsage(payload.Data)
	case "error":
		var data struct {
			Error   string `json:"error"`
			Message string `json:"message"`
		}
		_ = json.Unmarshal(payload.Data, &data)
		if data.Error == "" {
			data.Error = data.Message
		}
		a.finish(eventError("openclaw run", data.Error))
	case "lifecycle":
		a.handleLifecycle(payload.Data)
	}
}

func (a *activeTurn) handleChatPayload(payload chatEventPayload) {
	if len(payload.Usage) > 0 && string(payload.Usage) != "null" {
		a.handleUsage(payload.Usage)
	}
	switch payload.State {
	case "delta":
		// agent RPC normally emits assistant stream frames. chat.delta is an
		// additive fallback for Gateway variants that only publish chat frames.
		if a.assistant == "" && payload.DeltaText != "" {
			a.assistant += payload.DeltaText
			a.emit(agentruntime.TextDelta{Text: payload.DeltaText})
		}
	case "final":
		a.finish(nil)
	case "aborted":
		a.finish(agentruntime.ErrAborted)
	case "error":
		a.finish(eventError("openclaw chat", payload.ErrorMessage))
	}
}

func (a *activeTurn) handleTool(raw json.RawMessage) {
	var data struct {
		Phase      string          `json:"phase"`
		Name       string          `json:"name"`
		ToolCallID string          `json:"toolCallId"`
		Args       json.RawMessage `json:"args"`
		Result     json.RawMessage `json:"result"`
		IsError    bool            `json:"isError"`
	}
	if json.Unmarshal(raw, &data) != nil || data.ToolCallID == "" {
		return
	}
	switch data.Phase {
	case "start":
		if len(data.Args) == 0 {
			data.Args = json.RawMessage(`{}`)
		}
		a.emit(agentruntime.ToolCall{ID: data.ToolCallID, Name: data.Name, Input: data.Args})
	case "result":
		a.emit(agentruntime.ToolResult{
			ToolCallID: data.ToolCallID,
			Content:    rawResultText(data.Result),
			IsError:    data.IsError,
		})
	}
}

func (a *activeTurn) handleLifecycle(raw json.RawMessage) {
	var data struct {
		Phase      string          `json:"phase"`
		Error      json.RawMessage `json:"error"`
		Aborted    bool            `json:"aborted"`
		Status     string          `json:"status"`
		StopReason string          `json:"stopReason"`
		Usage      json.RawMessage `json:"usage"`
	}
	if json.Unmarshal(raw, &data) != nil {
		return
	}
	if len(data.Usage) > 0 && string(data.Usage) != "null" {
		a.handleUsage(data.Usage)
	}
	switch data.Phase {
	case "end":
		if data.Aborted || strings.EqualFold(data.Status, gatewayCancelledStatus) || strings.EqualFold(data.Status, "aborted") {
			a.finish(agentruntime.ErrAborted)
		} else {
			a.finish(nil)
		}
	case "error":
		a.finish(eventError("openclaw run", rawResultText(data.Error)))
	}
}

func (a *activeTurn) handleUsage(raw json.RawMessage) {
	a.applyUsage(decodeUsage(raw))
}

// applyUsage 是 usage 的唯一出口:帧里带的 usage 和收轮时从会话记录补的 usage
// 都走这里,保证 RunResult 与 UsageUpdate 始终一致。
func (a *activeTurn) applyUsage(usage *provider.Usage) {
	if usage == nil {
		return
	}
	a.result.Usage = usage
	a.emit(agentruntime.UsageUpdate{
		Usage:            usage,
		TotalInputTokens: usage.PromptTokens + usage.CachedTokens + usage.CacheCreationTokens,
	})
}

func deltaFromText(accumulator *string, snapshot, delta string) string {
	if delta != "" {
		*accumulator += delta
		if snapshot != "" {
			*accumulator = snapshot
		}
		return delta
	}
	if snapshot == "" || snapshot == *accumulator {
		return ""
	}
	if strings.HasPrefix(snapshot, *accumulator) {
		delta = strings.TrimPrefix(snapshot, *accumulator)
	} else {
		delta = snapshot
	}
	*accumulator = snapshot
	return delta
}

func rawResultText(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var text string
	if json.Unmarshal(raw, &text) == nil {
		return text
	}
	var structured struct {
		Message string `json:"message"`
	}
	if json.Unmarshal(raw, &structured) == nil && structured.Message != "" {
		return structured.Message
	}
	return string(raw)
}

func decodeUsage(raw json.RawMessage) *provider.Usage {
	var value struct {
		Input               int `json:"input"`
		InputTokens         int `json:"inputTokens"`
		PromptTokens        int `json:"promptTokens"`
		Output              int `json:"output"`
		OutputTokens        int `json:"outputTokens"`
		CompletionTokens    int `json:"completionTokens"`
		Reasoning           int `json:"reasoning"`
		ReasoningTokens     int `json:"reasoningTokens"`
		CacheRead           int `json:"cacheRead"`
		CachedTokens        int `json:"cachedTokens"`
		CacheWrite          int `json:"cacheWrite"`
		CacheCreationTokens int `json:"cacheCreationTokens"`
		Total               int `json:"total"`
		TotalTokens         int `json:"totalTokens"`
	}
	if json.Unmarshal(raw, &value) != nil {
		return nil
	}
	usage := &provider.Usage{
		PromptTokens:        firstPositive(value.PromptTokens, value.InputTokens, value.Input),
		CompletionTokens:    firstPositive(value.CompletionTokens, value.OutputTokens, value.Output),
		ReasoningTokens:     firstPositive(value.ReasoningTokens, value.Reasoning),
		CachedTokens:        firstPositive(value.CachedTokens, value.CacheRead),
		CacheCreationTokens: firstPositive(value.CacheCreationTokens, value.CacheWrite),
		TotalTokens:         firstPositive(value.TotalTokens, value.Total),
	}
	if usage.TotalTokens == 0 {
		usage.TotalTokens = usage.PromptTokens + usage.CompletionTokens + usage.ReasoningTokens + usage.CachedTokens + usage.CacheCreationTokens
	}
	if usage.TotalTokens == 0 {
		return nil
	}
	return usage
}

func firstPositive(values ...int) int {
	for _, value := range values {
		if value > 0 {
			return value
		}
	}
	return 0
}
