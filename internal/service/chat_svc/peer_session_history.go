package chat_svc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"sync"

	cagoblocks "github.com/cago-frame/agents/agent/blocks"
	"github.com/cago-frame/agents/provider"

	"github.com/agentre-hub/agentre/internal/model/entity/chat_entity"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/canonical"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/runtimes/remote/wire"
	"github.com/agentre-hub/agentre/internal/repository/chat_repo"
	chatblocks "github.com/agentre-hub/agentre/internal/service/chat_svc/blocks"
)

// peerSessionPublication owns the one ordered notification universe for a
// desktop session. It is deliberately in-memory: persisted transcript seeds
// the initial prefix, and subsequent live canonical frames are retained only
// for the running desktop process so reconnects share one dedup universe.
//
// Delivery to subscribers is serialized through a single worker goroutine
// (peerFlushLoop) rather than being written inline from the canonical event
// loop: Notify blocks on a relay WebSocket write, and doing that while holding
// the publication mutex would stall the local turn itself when an attached
// peer (or the relay path to it) is slow. Frames are only ever queued by the
// turn loop; the worker drains the queues outside the publication lock.
type peerSessionPublication struct {
	mu          sync.Mutex
	history     []wire.EventFrame
	nextSeq     int64
	initialized bool
	subscribers map[string]*peerSessionSubscription

	// wake carries a single-slot non-blocking signal for the flush worker;
	// startOnce guarantees at most one worker per publication.
	wake      chan struct{}
	startOnce sync.Once
}

type peerSessionSubscription struct {
	subscriber PeerSessionSubscriber
	highWater  int64
	cursor     int64
	pending    []wire.EventFrame
}

func (s *chatSvc) peerPublication(sessionID int64) *peerSessionPublication {
	value, _ := s.peerPublications.LoadOrStore(sessionID, &peerSessionPublication{
		subscribers: map[string]*peerSessionSubscription{},
		wake:        make(chan struct{}, 1),
	})
	publication := value.(*peerSessionPublication)
	publication.startOnce.Do(func() { go s.peerFlushLoop(publication) })
	return publication
}

// peerFlushLoop drains every subscriber's queued frames on the publication's
// own goroutine. A blocked Notify (stalled peer / relay) pauses only this
// session's peer fan-out, never the local turn's event loop.
func (s *chatSvc) peerFlushLoop(publication *peerSessionPublication) {
	for range publication.wake {
		s.flushPeerPending(publication)
	}
}

// flushPeerPending hands each ready subscriber its queued frames in order,
// outside the publication lock. A subscriber is ready once its pull cursor has
// reached the attach high-water mark; the pull path returns the history-covered
// prefix in its response and signals this worker (a single wake) at catch-up, so
// this queue only ever holds genuinely live frames and the worker is the only
// goroutine that ever calls Notify for them.
func (s *chatSvc) flushPeerPending(publication *peerSessionPublication) {
	type job struct {
		key    string
		sub    *peerSessionSubscription
		frames []wire.EventFrame
	}
	publication.mu.Lock()
	jobs := make([]job, 0, len(publication.subscribers))
	for key, sub := range publication.subscribers {
		if sub.cursor < sub.highWater || len(sub.pending) == 0 {
			continue
		}
		frames := sub.pending
		sub.pending = nil
		jobs = append(jobs, job{key: key, sub: sub, frames: frames})
	}
	publication.mu.Unlock()

	for _, j := range jobs {
		for _, frame := range j.frames {
			if err := j.sub.subscriber.Notify(wire.NotifyEvent, frame); err != nil {
				publication.mu.Lock()
				if publication.subscribers[j.key] == j.sub {
					delete(publication.subscribers, j.key)
				}
				publication.mu.Unlock()
				break
			}
		}
	}
}

// PullPeerSession serves the same runtime.session.pull contract used by
// agentred. The subscriber identifies the account connection whose attach
// handoff cursor advances; it is not a new wire field.
func (s *chatSvc) PullPeerSession(_ context.Context, params wire.SessionPullParams, subscriber PeerSessionSubscriber) (wire.SessionPullResult, error) {
	if params.SessionID <= 0 || subscriber == nil {
		return wire.SessionPullResult{}, ErrPeerSessionNotFound
	}
	publication := s.peerPublication(params.SessionID)
	key := peerSubscriberKey(subscriber)
	publication.mu.Lock()

	subscription := publication.subscribers[key]
	if subscription == nil {
		publication.mu.Unlock()
		return wire.SessionPullResult{}, ErrPeerSessionNotFound
	}
	limit := clampPeerPullLimit(params.Limit)
	out := wire.SessionPullResult{Cursor: params.Cursor}
	if subscription.highWater > 0 {
		out.OldestSeq = 1
	}
	for _, frame := range publication.history {
		if frame.Seq <= params.Cursor || frame.Seq > subscription.highWater {
			continue
		}
		if len(out.Notifications) == limit {
			out.HasMore = true
			break
		}
		out.Notifications = append(out.Notifications, wire.JournaledNotification{
			Seq: frame.Seq, Method: wire.NotifyEvent, Params: &frame,
		})
		out.Cursor = frame.Seq
	}
	if out.Cursor > subscription.cursor {
		subscription.cursor = out.Cursor
	}
	caughtUp := subscription.cursor >= subscription.highWater
	publication.mu.Unlock()

	// 拉平后把 live 交付完全交给单个 flush worker：这里只发一个 wake 信号，绝不在
	// publication 锁内调用 subscriber.Notify。因此慢对端只卡自己的扇出、不卡本地
	// turn，也不会与 worker 的 out-of-lock 投递交错出乱序（worker 是唯一投递者）。
	// 不拉平的订阅保持 cursor < highWater，worker 的 flush 会照旧跳过它。
	if caughtUp {
		select {
		case publication.wake <- struct{}{}:
		default:
		}
	}
	return out, nil
}

func clampPeerPullLimit(limit int) int {
	if limit <= 0 {
		return wire.DefaultSessionPullLimit
	}
	if limit > wire.MaxSessionPullLimit {
		return wire.MaxSessionPullLimit
	}
	return limit
}

func (s *chatSvc) attachPeerTranscript(ctx context.Context, sessionID int64, subscriber PeerSessionSubscriber) (int64, func(), error) {
	publication := s.peerPublication(sessionID)
	key := peerSubscriberKey(subscriber)
	// Holding this lock across the initial repository read makes the synthesized
	// prefix and registration one publication boundary: a live event is either
	// in 1..H or assigned after H and buffered for this subscriber.
	publication.mu.Lock()
	if !publication.initialized {
		messages, err := chat_repo.Message().List(ctx, sessionID)
		if err != nil {
			publication.mu.Unlock()
			return 0, nil, operationFailedWithCause(ctx, err)
		}
		history, err := synthesizePeerHistory(sessionID, messages)
		if err != nil {
			publication.mu.Unlock()
			return 0, nil, fmt.Errorf("synthesize desktop peer history: %w", err)
		}
		for index := range history {
			history[index].Seq = int64(index + 1)
		}
		publication.history = history
		publication.nextSeq = int64(len(history))
		publication.initialized = true
	}
	highWater := publication.nextSeq
	subscription := &peerSessionSubscription{subscriber: subscriber, highWater: highWater}
	publication.subscribers[key] = subscription
	publication.mu.Unlock()

	var once sync.Once
	detach := func() {
		once.Do(func() {
			publication.mu.Lock()
			if publication.subscribers[key] == subscription {
				delete(publication.subscribers, key)
			}
			publication.mu.Unlock()
		})
	}
	return highWater, detach, nil
}

// publishPeerEvent 把一条密封事件挂进该会话的对端通知宇宙。
//
// 从前这里分成 publishPeerEvent / publishPeerEventRaw 两跳,中间隔着一次
// json.Marshal —— 那次序列化只是为了填 EventFrame 上的 json.RawMessage;帧现在
// 直接装密封值,两跳合成一跳。
func (s *chatSvc) publishPeerEvent(sessionID int64, event agentruntime.Event) {
	if sessionID <= 0 || event == nil {
		return
	}
	value, ok := s.peerPublications.Load(sessionID)
	if !ok {
		return
	}
	publication := value.(*peerSessionPublication)
	publication.mu.Lock()
	publication.nextSeq++
	frame := wire.EventFrame{SessionID: sessionID, Event: event, Seq: publication.nextSeq}
	publication.history = append(publication.history, frame)
	for _, subscription := range publication.subscribers {
		// Queue only: the flush worker performs the (potentially blocking) relay
		// write. Never Notify inline from a canonical event loop — a stalled
		// peer must not stall this desktop's own turn.
		subscription.pending = append(subscription.pending, frame)
	}
	publication.mu.Unlock()
	select {
	case publication.wake <- struct{}{}:
	default:
	}
}

func peerSubscriberKey(subscriber PeerSessionSubscriber) string {
	if keyer, ok := subscriber.(PeerSessionSubscriberKeyer); ok && keyer.PeerSessionSubscriberKey() != "" {
		return keyer.PeerSessionSubscriberKey()
	}
	value := reflect.ValueOf(subscriber)
	if value.IsValid() && value.Kind() == reflect.Pointer && !value.IsNil() {
		return fmt.Sprintf("%T:%x", subscriber, value.Pointer())
	}
	return fmt.Sprintf("%T:%v", subscriber, subscriber)
}

func synthesizePeerHistory(sessionID int64, messages []*chat_entity.Message) ([]wire.EventFrame, error) {
	sorted := append([]*chat_entity.Message(nil), messages...)
	sort.SliceStable(sorted, func(i, j int) bool {
		if sorted[i] == nil {
			return false
		}
		if sorted[j] == nil {
			return true
		}
		return sorted[i].Seq < sorted[j].Seq
	})
	frames := make([]wire.EventFrame, 0)
	appendEvent := func(event agentruntime.Event) error {
		frames = append(frames, wire.EventFrame{SessionID: sessionID, Event: event})
		return nil
	}
	for _, message := range sorted {
		if message == nil {
			continue
		}
		var stored []cagoblocks.StoredBlock
		if err := json.Unmarshal([]byte(message.BlocksJSON), &stored); err != nil {
			return nil, fmt.Errorf("message %d blocks: %w", message.ID, err)
		}
		for _, block := range stored {
			if message.Role == "assistant" && block.Type == "user_ask" {
				var data chatblocks.UserAskBlock
				if err := json.Unmarshal(block.Data, &data); err != nil {
					return nil, err
				}
				if err := appendEvent(agentruntime.UserAskRequest{RequestID: data.RequestID, ToolCallID: data.ToolCallID, Questions: peerQuestions(data.Questions)}); err != nil {
					return nil, err
				}
				if data.Answered || data.Skipped {
					if err := appendEvent(agentruntime.UserAskResolved{RequestID: data.RequestID, Answers: peerAnswers(data.Answers), Skipped: data.Skipped}); err != nil {
						return nil, err
					}
				}
				continue
			}
			if message.Role == "assistant" && block.Type == "subagent_state" {
				var data chatblocks.SubagentStateBlock
				if err := json.Unmarshal(block.Data, &data); err != nil {
					return nil, err
				}
				if err := appendEvent(agentruntime.SubagentDone{ToolCallID: data.ParentToolCallID, Info: agentruntime.SubagentInfo{
					TaskID: data.TaskID, Kind: data.Kind, TaskDescription: data.Description, LastToolName: data.LastToolName,
					ToolUses: data.ToolUses, TotalTokens: data.TotalTokens, DurationMs: data.DurationMs, Status: data.Status,
					Mode: data.Mode, Runs: data.Runs,
				}}); err != nil {
					return nil, err
				}
				if data.Model != "" {
					if err := appendEvent(agentruntime.SubagentModel{ToolCallID: data.ParentToolCallID, Model: data.Model}); err != nil {
						return nil, err
					}
				}
				continue
			}
			if message.Role == "assistant" && block.Type == "tool_permission" {
				var data chatblocks.ToolPermissionBlock
				if err := json.Unmarshal(block.Data, &data); err != nil {
					return nil, err
				}
				input, err := json.Marshal(data.ToolInput)
				if err != nil {
					return nil, err
				}
				if err := appendEvent(agentruntime.ToolPermissionRequest{RequestID: data.RequestID, ToolCallID: data.ToolCallID, ToolName: data.ToolName, Input: input}); err != nil {
					return nil, err
				}
				if data.Resolved {
					if err := appendEvent(agentruntime.ToolPermissionResolved{RequestID: data.RequestID, Allowed: data.Allowed, AlwaysAllow: data.AlwaysAllow, DenyReason: data.DenyReason}); err != nil {
						return nil, err
					}
				}
				continue
			}
			if event, ok, err := peerEventForStoredBlock(message, block); err != nil {
				return nil, err
			} else if ok {
				if err := appendEvent(event); err != nil {
					return nil, err
				}
				// 投射不出来的块原样往下送(R8)。它是一等的密封事件,所以既过得了
				// 协议边界,又不必在这里对载荷做任何解释 —— 对端可能是认得这个
				// blockType 的新版本。
			} else if err := appendEvent(agentruntime.UnrecognizedBlock{
				BlockType: block.Type,
				Data:      append(json.RawMessage(nil), block.Data...),
			}); err != nil {
				return nil, err
			}
		}
		if message.Role == "assistant" {
			if message.PromptTokens != 0 || message.CompletionTokens != 0 || message.CachedTokens != 0 || message.CacheCreationTokens != 0 || message.ReasoningTokens != 0 || message.TotalInputTokens != 0 {
				if err := appendEvent(agentruntime.UsageUpdate{Usage: &provider.Usage{
					PromptTokens: message.PromptTokens, CompletionTokens: message.CompletionTokens,
					CachedTokens: message.CachedTokens, CacheCreationTokens: message.CacheCreationTokens,
					ReasoningTokens: message.ReasoningTokens,
				}, TotalInputTokens: message.TotalInputTokens}); err != nil {
					return nil, err
				}
			}
			if message.ErrorText != "" {
				if err := appendEvent(agentruntime.ErrorEvent{Err: errors.New(message.ErrorText)}); err != nil {
					return nil, err
				}
			}
			if err := appendEvent(agentruntime.Done{}); err != nil {
				return nil, err
			}
		}
	}
	return frames, nil
}

func peerEventForStoredBlock(message *chat_entity.Message, block cagoblocks.StoredBlock) (agentruntime.Event, bool, error) {
	if message.Role == "user" && (block.Type == "text" || block.Type == "display_text") {
		var data struct {
			Text             string `json:"text"`
			SourceDevice     string `json:"sourceDevice"`
			SourceDeviceName string `json:"sourceDeviceName"`
		}
		if err := json.Unmarshal(block.Data, &data); err != nil {
			return nil, false, err
		}
		return agentruntime.UserMessageEvent{
			Text: data.Text, SourceDevice: data.SourceDevice, SourceDeviceName: data.SourceDeviceName,
		}, true, nil
	}
	if message.Role != "assistant" {
		return nil, false, nil
	}
	switch block.Type {
	case "text", "display_text":
		var data struct {
			Text string `json:"text"`
		}
		if err := json.Unmarshal(block.Data, &data); err != nil {
			return nil, false, err
		}
		return agentruntime.TextDelta{Text: data.Text}, true, nil
	case "thinking":
		var data struct {
			Text string `json:"text"`
		}
		if err := json.Unmarshal(block.Data, &data); err != nil {
			return nil, false, err
		}
		return agentruntime.ThinkingDelta{Text: data.Text}, true, nil
	case "tool_use":
		var data struct {
			ID    string          `json:"id"`
			Name  string          `json:"name"`
			Input json.RawMessage `json:"input"`
		}
		if err := json.Unmarshal(block.Data, &data); err != nil {
			return nil, false, err
		}
		return agentruntime.ToolCall{ID: data.ID, Name: data.Name, Input: data.Input}, true, nil
	case "tool_result":
		var data struct {
			ToolCallID string                   `json:"tool_use_id"`
			Content    []cagoblocks.StoredBlock `json:"content"`
			IsError    bool                     `json:"is_error"`
		}
		if err := json.Unmarshal(block.Data, &data); err != nil {
			return nil, false, err
		}
		return agentruntime.ToolResult{ToolCallID: data.ToolCallID, Content: peerTextFromStoredBlocks(data.Content), IsError: data.IsError}, true, nil
	case "permission_mode_change":
		var data chatblocks.PermissionModeChangeBlock
		if err := json.Unmarshal(block.Data, &data); err != nil {
			return nil, false, err
		}
		return agentruntime.PermissionModeChanged{Mode: data.To}, true, nil
	case "plan":
		var data PlanBlock
		if err := json.Unmarshal(block.Data, &data); err != nil {
			return nil, false, err
		}
		steps := make([]canonical.PlanStep, 0, len(data.Steps))
		for _, step := range data.Steps {
			steps = append(steps, canonical.PlanStep{Step: step.Step, Status: canonical.PlanStepStatus(step.Status)})
		}
		return agentruntime.PlanUpdated{Plan: canonical.PlanUpdate{Steps: steps, Text: data.Text, Actions: data.Actions}}, true, nil
	case "compact_boundary":
		var data chatblocks.CompactBoundaryBlock
		if err := json.Unmarshal(block.Data, &data); err != nil {
			return nil, false, err
		}
		return agentruntime.CompactBoundary{PreTokens: data.PreTokens, Trigger: data.Trigger}, true, nil
	case "exec_approval":
		var data chatblocks.ExecApprovalBlock
		if err := json.Unmarshal(block.Data, &data); err != nil {
			return nil, false, err
		}
		if data.Status == "resolved" || data.Status == "expired" {
			return agentruntime.ExecApprovalResolved{ID: data.ID, Status: data.Status, Decision: data.Decision, ResolvedBy: data.ResolvedBy, ResolvedAtMs: data.ResolvedAtMs}, true, nil
		}
		return agentruntime.ExecApprovalRequested{ID: data.ID, CommandText: data.CommandText, CommandPreview: data.CommandPreview, AllowedDecisions: data.AllowedDecisions, Host: data.Host, NodeID: data.NodeID, AgentID: data.AgentID, CreatedAtMs: data.CreatedAtMs, ExpiresAtMs: data.ExpiresAtMs}, true, nil
	default:
		return nil, false, nil
	}
}

func peerTextFromStoredBlocks(blocks []cagoblocks.StoredBlock) string {
	var out strings.Builder
	for _, block := range blocks {
		if block.Type != "text" && block.Type != "display_text" {
			continue
		}
		var data struct {
			Text string `json:"text"`
		}
		if json.Unmarshal(block.Data, &data) == nil {
			out.WriteString(data.Text)
		}
	}
	return out.String()
}

func peerQuestions(in []chatblocks.AskQuestionDTO) []agentruntime.AskQuestion {
	out := make([]agentruntime.AskQuestion, 0, len(in))
	for _, question := range in {
		options := make([]agentruntime.AskOption, 0, len(question.Options))
		for _, option := range question.Options {
			options = append(options, agentruntime.AskOption{Label: option.Label, Description: option.Description, Preview: option.Preview})
		}
		out = append(out, agentruntime.AskQuestion{ID: question.ID, Question: question.Question, Header: question.Header, MultiSelect: question.MultiSelect, IsOther: question.IsOther, IsSecret: question.IsSecret, Options: options})
	}
	return out
}

func peerAnswers(in []chatblocks.AskAnswerDTO) []agentruntime.AskAnswer {
	out := make([]agentruntime.AskAnswer, 0, len(in))
	for _, answer := range in {
		out = append(out, agentruntime.AskAnswer{QuestionIndex: answer.QuestionIndex, Labels: answer.Labels, OtherText: answer.OtherText})
	}
	return out
}
