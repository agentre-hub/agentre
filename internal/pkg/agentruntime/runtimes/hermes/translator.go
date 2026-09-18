// Package hermes drives a running Hermes Agent `serve` through its TUI-gateway
// JSON-RPC protocol (newline-delimited JSON over a WebSocket).
//
// Hermes serves that protocol over both stdio and WebSocket — its
// `tui_gateway/ws.py` reuses `tui_gateway/server.py`'s dispatch — so the method
// and event catalog below is the gateway's own, not a transport-specific subset.
// The server accepts the WebSocket and immediately sends `gateway.ready`.
// Transport-independent frame coding lives in frame.go; client.go owns the
// WebSocket dial, token handshake and error classification. A turn covers
// create/resume session, prompt.submit, streaming the frames into sealed
// agentruntime events, and abort via session.interrupt. See docs/agent-backend.md
// and the Hermes source under tui_gateway/ for the method/event catalog.
package hermes

import (
	"encoding/json"
	"strings"

	"github.com/cago-frame/agents/provider"

	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/canonical"
	"github.com/agentre-hub/agentre/internal/pkg/diff"
)

// EventKind is one gateway event `params.type` (or a synthetic kind for unit
// tests). Unknown kinds translate to zero events.
type EventKind string

const (
	EventGatewayReady    EventKind = "gateway.ready"
	EventMessageStart    EventKind = "message.start"
	EventMessageDelta    EventKind = "message.delta"
	EventMessageInterim  EventKind = "message.interim"
	EventMessageComplete EventKind = "message.complete"
	EventThinkingDelta   EventKind = "thinking.delta"
	EventReasoningDelta  EventKind = "reasoning.delta"
	EventReasoningAvail  EventKind = "reasoning.available"
	EventToolStart       EventKind = "tool.start"
	EventToolGenerating  EventKind = "tool.generating"
	EventToolComplete    EventKind = "tool.complete"
	EventTodoUpdated     EventKind = "todo.updated"
	EventStatusUpdate    EventKind = "status.update"
	EventSessionUsage    EventKind = "session.usage"
	EventError           EventKind = "error"
	EventApprovalRequest EventKind = "approval.request"
	EventClarifyRequest  EventKind = "clarify.request"
)

// Event is one decoded gateway event frame.
//
// Session is the live runtime session id the gateway stamped on the frame
// (`params.session_id`). The turn path filters by it; empty means a
// session-less frame (gateway.ready) that no turn owns.
type Event struct {
	Kind    EventKind
	Session string
	Payload json.RawMessage
}

// eventParams mirrors the gateway's event envelope:
// `{"method":"event","params":{"type":..,"session_id":..,"payload":..}}`.
type eventParams struct {
	Type      string          `json:"type"`
	SessionID string          `json:"session_id"`
	Payload   json.RawMessage `json:"payload"`
}

// decodeEventFrame parses the `params` object of a gateway event frame.
// A frame without a type is not an event and reports ok=false.
func decodeEventFrame(params []byte) (Event, bool) {
	if len(params) == 0 {
		return Event{}, false
	}
	var p eventParams
	if err := json.Unmarshal(params, &p); err != nil || strings.TrimSpace(p.Type) == "" {
		return Event{}, false
	}
	return Event{Kind: EventKind(p.Type), Session: p.SessionID, Payload: p.Payload}, true
}

// decodePayload is the translator's tolerant decoder: a malformed or absent
// payload yields ok=false and the frame is dropped. The gateway is a separate
// process whose schema can drift; a bad frame must never wedge a live turn.
func decodePayload[T any](payload json.RawMessage) (T, bool) {
	var v T
	if len(payload) == 0 {
		return v, false
	}
	if err := json.Unmarshal(payload, &v); err != nil {
		return v, false
	}
	return v, true
}

type deltaPayload struct {
	Text string `json:"text"`
}

type toolStartPayload struct {
	ToolID  string          `json:"tool_id"`
	Name    string          `json:"name"`
	Context string          `json:"context"`
	Args    json.RawMessage `json:"args"`
}

type toolCompletePayload struct {
	ToolID  string          `json:"tool_id"`
	Name    string          `json:"name"`
	Args    json.RawMessage `json:"args"`
	Result  json.RawMessage `json:"result"`
	Summary string          `json:"summary"`
}

type messageCompletePayload struct {
	Text   string       `json:"text"`
	Status string       `json:"status"`
	Error  string       `json:"error"`
	Usage  usagePayload `json:"usage"`
}

type usageEnvelope struct {
	Usage usagePayload `json:"usage"`
}

type usagePayload struct {
	Model      string `json:"model"`
	Input      int    `json:"input"`
	Output     int    `json:"output"`
	Reasoning  int    `json:"reasoning"`
	Prompt     int    `json:"prompt"`
	Completion int    `json:"completion"`
	Total      int    `json:"total"`
	Calls      int    `json:"calls"`
}

type errorPayload struct {
	Message string `json:"message"`
}

type statusPayload struct {
	Kind string `json:"kind"`
	Text string `json:"text"`
}

// translate is a pure function: one gateway event in, 0/1/n sealed
// agentruntime events out. It does not read or write runtime state; usage is
// returned as the gateway's cumulative snapshot and the drain loop turns it
// into per-turn deltas.
func translate(ev Event) (events []agentruntime.Event, usage *provider.Usage, stopErr error) {
	switch ev.Kind {
	case EventMessageDelta, EventMessageInterim:
		p, ok := decodePayload[deltaPayload](ev.Payload)
		if !ok || p.Text == "" {
			return nil, nil, nil
		}
		return []agentruntime.Event{agentruntime.TextDelta{Text: p.Text}}, nil, nil

	case EventThinkingDelta, EventReasoningDelta, EventReasoningAvail:
		p, ok := decodePayload[deltaPayload](ev.Payload)
		if !ok || p.Text == "" {
			return nil, nil, nil
		}
		return []agentruntime.Event{agentruntime.ThinkingDelta{Text: p.Text}}, nil, nil

	case EventToolStart:
		p, ok := decodePayload[toolStartPayload](ev.Payload)
		if !ok || strings.TrimSpace(p.Name) == "" {
			return nil, nil, nil
		}
		call := agentruntime.ToolCall{
			ID:        strings.TrimSpace(p.ToolID),
			Name:      strings.TrimSpace(p.Name),
			Input:     cloneRaw(p.Args),
			Canonical: recognizeCanonical(p.Name, p.Args),
		}
		return []agentruntime.Event{call}, nil, nil

	case EventToolGenerating:
		// The model began producing a tool call: a pure output-activity timing
		// signal (mirrors claudecode's content_block_start).
		return []agentruntime.Event{agentruntime.OutputActivity{}}, nil, nil

	case EventToolComplete:
		p, ok := decodePayload[toolCompletePayload](ev.Payload)
		if !ok || strings.TrimSpace(p.ToolID) == "" {
			return nil, nil, nil
		}
		return []agentruntime.Event{agentruntime.ToolResult{
			ToolCallID: strings.TrimSpace(p.ToolID),
			Content:    toolResultContent(p.Result),
			IsError:    toolResultIsError(p.Result),
		}}, nil, nil

	case EventTodoUpdated:
		plan, ok := planFromTodoPayload(ev.Payload)
		if !ok {
			return nil, nil, nil
		}
		return []agentruntime.Event{agentruntime.PlanUpdated{Plan: plan}}, nil, nil

	case EventStatusUpdate:
		p, ok := decodePayload[statusPayload](ev.Payload)
		if !ok {
			return nil, nil, nil
		}
		text := strings.TrimSpace(p.Text)
		if text == "" {
			text = strings.TrimSpace(p.Kind)
		}
		if text == "" {
			return nil, nil, nil
		}
		return []agentruntime.Event{agentruntime.RuntimeStatus{Status: text}}, nil, nil

	case EventSessionUsage:
		// The gateway emits session.usage as `{"usage":{...}}`; the
		// session.usage RPC returns the inner object directly. Accept both.
		if env, ok := decodePayload[usageEnvelope](ev.Payload); ok && (env.Usage.Model != "" || env.Usage.Prompt != 0) {
			return nil, mapUsage(env.Usage), nil
		}
		p, ok := decodePayload[usagePayload](ev.Payload)
		if !ok {
			return nil, nil, nil
		}
		return nil, mapUsage(p), nil

	case EventError:
		p, _ := decodePayload[errorPayload](ev.Payload)
		msg := strings.TrimSpace(p.Message)
		if msg == "" {
			msg = "hermes gateway reported an error"
		}
		err := errGatewayTurn(msg)
		return []agentruntime.Event{agentruntime.ErrorEvent{Err: err}}, nil, err

	case EventMessageComplete:
		p, ok := decodePayload[messageCompletePayload](ev.Payload)
		if !ok {
			return nil, nil, nil
		}
		usage = mapUsage(p.Usage)
		switch strings.TrimSpace(p.Status) {
		case "error":
			msg := strings.TrimSpace(p.Error)
			if msg == "" {
				msg = strings.TrimSpace(p.Text)
			}
			if msg == "" {
				msg = "hermes turn failed"
			}
			err := errGatewayTurn(msg)
			return []agentruntime.Event{agentruntime.ErrorEvent{Err: err}}, usage, err
		case "interrupted":
			return nil, usage, agentruntime.ErrAborted
		default:
			return nil, usage, nil
		}
	}

	// approval.request / clarify.request and every unknown frame are out of
	// scope for this runtime: they produce no events, so a frame we do not
	// understand can never wedge the turn. The raw-frame sink logs them.
	return nil, nil, nil
}

func cloneRaw(b json.RawMessage) json.RawMessage {
	if len(b) == 0 {
		return nil
	}
	return append(json.RawMessage(nil), b...)
}

func mapUsage(u usagePayload) *provider.Usage {
	if u.Model == "" && u.Prompt == 0 && u.Completion == 0 && u.Input == 0 &&
		u.Output == 0 && u.Total == 0 && u.Calls == 0 {
		return nil
	}
	prompt := u.Prompt
	if prompt == 0 {
		// Some builds only fill `input`; it is the same canonical prompt total
		// once cache reads/writes are folded in.
		prompt = u.Input
	}
	completion := u.Completion
	if completion == 0 {
		completion = u.Output
	}
	return &provider.Usage{
		PromptTokens:     prompt,
		CompletionTokens: completion,
		ReasoningTokens:  u.Reasoning,
		TotalTokens:      u.Total,
	}
}

// usageDelta returns max(cur-prev, 0) per field. The gateway reports cumulative
// session usage, so a turn's usage is the growth over the pre-submit baseline.
func usageDelta(cur, prev *provider.Usage) *provider.Usage {
	if cur == nil {
		return nil
	}
	if prev == nil {
		prev = new(provider.Usage)
	}
	return &provider.Usage{
		PromptTokens:        max(0, cur.PromptTokens-prev.PromptTokens),
		CompletionTokens:    max(0, cur.CompletionTokens-prev.CompletionTokens),
		ReasoningTokens:     max(0, cur.ReasoningTokens-prev.ReasoningTokens),
		CachedTokens:        max(0, cur.CachedTokens-prev.CachedTokens),
		CacheCreationTokens: max(0, cur.CacheCreationTokens-prev.CacheCreationTokens),
		TotalTokens:         max(0, cur.TotalTokens-prev.TotalTokens),
	}
}

func addUsage(dst *provider.Usage, add *provider.Usage) {
	if dst == nil || add == nil {
		return
	}
	dst.PromptTokens += add.PromptTokens
	dst.CompletionTokens += add.CompletionTokens
	dst.ReasoningTokens += add.ReasoningTokens
	dst.CachedTokens += add.CachedTokens
	dst.CacheCreationTokens += add.CacheCreationTokens
	dst.TotalTokens += add.TotalTokens
}

func usageIsZero(u *provider.Usage) bool {
	return u == nil || (u.PromptTokens == 0 && u.CompletionTokens == 0 && u.ReasoningTokens == 0 &&
		u.CachedTokens == 0 && u.CacheCreationTokens == 0 && u.TotalTokens == 0)
}

// messageCompleteText extracts the final assistant text from a message.complete
// payload, used only when no streamed delta arrived.
func messageCompleteText(payload json.RawMessage) string {
	p, _ := decodePayload[messageCompletePayload](payload)
	return p.Text
}

func toolResultContent(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	return string(raw)
}

// toolResultIsError recognizes the common Hermes error shapes: an `error` key,
// or an explicit success/ok=false.
func toolResultIsError(raw json.RawMessage) bool {
	if len(raw) == 0 {
		return false
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return false
	}
	if e, ok := m["error"]; ok && e != nil {
		if s, isString := e.(string); !isString || strings.TrimSpace(s) != "" {
			return true
		}
	}
	if v, ok := m["success"].(bool); ok && !v {
		return true
	}
	if v, ok := m["ok"].(bool); ok && !v {
		return true
	}
	return false
}

func planFromTodoPayload(payload json.RawMessage) (canonical.PlanUpdate, bool) {
	var p struct {
		Todos []map[string]any `json:"todos"`
	}
	if err := json.Unmarshal(payload, &p); err != nil || len(p.Todos) == 0 {
		return canonical.PlanUpdate{}, false
	}
	steps := make([]canonical.PlanStep, 0, len(p.Todos))
	for _, todo := range p.Todos {
		step := firstString(todo, "content", "task", "text", "step")
		if strings.TrimSpace(step) == "" {
			continue
		}
		steps = append(steps, canonical.PlanStep{
			ID:     firstString(todo, "id"),
			Step:   step,
			Status: normalizeTodoStatus(firstString(todo, "status")),
		})
	}
	if len(steps) == 0 {
		return canonical.PlanUpdate{}, false
	}
	return canonical.PlanUpdate{Steps: steps}, true
}

func firstString(m map[string]any, keys ...string) string {
	for _, k := range keys {
		if v, ok := m[k].(string); ok && strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func normalizeTodoStatus(status string) canonical.PlanStepStatus {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "in_progress", "inprogress", "active", "running":
		return canonical.StepInProgress
	case "completed", "complete", "done":
		return canonical.StepCompleted
	case "cancelled", "canceled": //nolint:misspell // accept both spellings from the gateway
		return canonical.StepCancelled
	default:
		return canonical.StepPending
	}
}

// recognizeCanonical maps Hermes' file-mutating tools onto the shared canonical
// shapes, then falls back to the cross-backend recognizer (agent/task spawn).
// Everything else stays a raw tool card.
func recognizeCanonical(name string, rawInput json.RawMessage) canonical.CanonicalTool {
	if len(rawInput) == 0 {
		return nil
	}
	var m map[string]any
	if err := json.Unmarshal(rawInput, &m); err != nil {
		return nil
	}
	switch name {
	case "write_file":
		return fileWriteCanonical(m)
	case "patch":
		return patchCanonical(m)
	}
	if c, ok := canonical.FromToolUse(name, m); ok {
		return c
	}
	return nil
}

func fileWriteCanonical(m map[string]any) canonical.CanonicalTool {
	path, _ := m["path"].(string)
	content, ok := m["content"].(string)
	if !ok {
		return nil
	}
	bytes := len(content)
	truncated := false
	if bytes > canonical.WriteContentByteCap {
		content = content[:canonical.WriteContentByteCap]
		truncated = true
	}
	lines := 0
	if content != "" {
		lines = strings.Count(content, "\n")
		if !strings.HasSuffix(content, "\n") {
			lines++
		}
	}
	return canonical.FileWrite{
		Path:      path,
		Content:   content,
		Lines:     lines,
		Bytes:     bytes,
		Truncated: truncated,
	}
}

// patchCanonical maps Hermes' `patch` tool (mode=replace) onto FileEdit. Other
// modes (create/append/multi_replace) are left raw: their wire shape does not
// map cleanly onto a single diff and a half-rendered DiffCard is worse than a
// raw tool card.
func patchCanonical(m map[string]any) canonical.CanonicalTool {
	mode, _ := m["mode"].(string)
	if mode != "replace" {
		return nil
	}
	path, _ := m["path"].(string)
	oldString, _ := m["old_string"].(string)
	newString, _ := m["new_string"].(string)
	if strings.TrimSpace(path) == "" || oldString == "" {
		return nil
	}
	payload := diff.FromEdit(map[string]any{
		"file_path":  path,
		"old_string": oldString,
		"new_string": newString,
	})
	totalHunks := 0
	for _, f := range payload.Files {
		totalHunks += len(f.Hunks)
	}
	if len(payload.Files) == 0 || totalHunks == 0 {
		return nil
	}
	return canonical.FileEdit{Files: canonical.PatchesFromDiff(payload)}
}
