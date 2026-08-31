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

// peerSubscriberQueueDepth 是**每个订阅者**的投递缓冲深度。
//
// 从前 pending 没有上限:一个写不动的对端能让它一直涨到内存吃光 —— 中继是网络入口,
// 「对面猛灌就能撑爆本机」不是可以留着的形状。
//
// 满了之后丢帧是可恢复的:帧上带 seq,对端的闸门看到跳号会从游标发起一次补齐,
// 而 publication.history + PullPeerSession 正是补齐读的那份日志。日志不参与丢弃。
//
// 与 agentred 那侧同一个数(connRegistry 的 subscriberQueueDepth):同一条纪律,
// 没有理由两边取不同的深度。
const peerSubscriberQueueDepth = 256

type peerSessionSubscription struct {
	subscriber PeerSessionSubscriber
	highWater  int64
	cursor     int64
	pending    []wire.EventFrame
	// dropped 记这个订阅者被丢过帧。只用于日志:对端靠 seq 跳号自己发现并补齐,
	// 不需要服务端告诉它。
	dropped bool
	// flushing 表示这个订阅者此刻有一条投递在飞。每个订阅者至多一条 —— 它保证
	// 这个订阅者收到的帧仍然有序,同时让**不同**订阅者彼此独立:一个卡住的对端
	// 不再拖住同一会话上的其他人。
	flushing bool
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

// flushPeerPending 把每个就绪订阅者排着的帧交出去。订阅者在拉取游标追上 attach
// 高水位之后才算就绪;pull 那条路在应答里带回日志覆盖的前缀,并在追平时叫醒本
// worker 一次,所以这个队列里只会有真正的实时帧。
//
// **每个订阅者一条独立的投递 goroutine**,而不是在这里逐个串行调阻塞的 Notify。
// Notify 写的是一条中继 websocket(跨副本时还要等一次 Redis 回执,最坏 5 秒),
// 串行意味着一台卡住的机器会让同一条对话上其它所有端一起停住。同一条纪律在
// agentred 那侧是 connRegistry 的 asyncNotifier,这里是它的对称实现。
//
// 每个订阅者至多一条投递在飞(flushing),所以它收到的帧仍然有序;投递完成后
// 如果队列里又攒了新的,worker 自己接着跑下一轮,不必再等一次 wake。
func (s *chatSvc) flushPeerPending(publication *peerSessionPublication) {
	publication.mu.Lock()
	starting := make([]string, 0, len(publication.subscribers))
	for key, sub := range publication.subscribers {
		if sub.flushing || sub.cursor < sub.highWater || len(sub.pending) == 0 {
			continue
		}
		sub.flushing = true
		starting = append(starting, key)
	}
	publication.mu.Unlock()

	for _, key := range starting {
		go s.deliverPeerPending(publication, key)
	}
}

// deliverPeerPending 是一个订阅者的投递循环:取走它排着的帧、在**锁外**逐条交付,
// 交付期间新到的帧继续入队,交付完再看一轮。写失败即认为这个订阅者不行了,摘掉它
// (与从前同一判据)。
func (s *chatSvc) deliverPeerPending(publication *peerSessionPublication, key string) {
	for {
		publication.mu.Lock()
		sub := publication.subscribers[key]
		if sub == nil || len(sub.pending) == 0 {
			if sub != nil {
				sub.flushing = false
			}
			publication.mu.Unlock()
			return
		}
		frames := sub.pending
		sub.pending = nil
		subscriber := sub.subscriber
		publication.mu.Unlock()

		for _, frame := range frames {
			if err := subscriber.Notify(wire.NotifyEvent, frame); err != nil {
				publication.mu.Lock()
				if publication.subscribers[key] == sub {
					delete(publication.subscribers, key)
				}
				sub.flushing = false
				publication.mu.Unlock()
				return
			}
		}
	}
}

// enqueuePeerFrame 把一帧排给一个订阅者。**永不阻塞**,而且封顶。
//
// 队列满说明这个订阅者已经落后这么多帧了,继续排只会无界吃内存。此时丢掉最旧的
// 那一批里的这一帧并记一次:对端按帧上的 seq 看到跳号,走既有的游标补齐把缺口
// 拉回来 —— 日志(publication.history)是完整的,补得回来正是可以丢的前提。
func enqueuePeerFrame(sub *peerSessionSubscription, frame wire.EventFrame) {
	if len(sub.pending) >= peerSubscriberQueueDepth {
		sub.dropped = true
		return
	}
	sub.pending = append(sub.pending, frame)
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
		enqueuePeerFrame(subscription, frame)
	}
	publication.mu.Unlock()
	select {
	case publication.wake <- struct{}{}:
	default:
	}
}

// publishPeerTurnDone 在一轮收口时把本轮统计随 Done 发给对端订阅者。
//
// 对端 Peer Tab 与浏览器控制台走的是同一个共享转录投影器,那边 meta 那一行
// (模型 · 耗时 · 首字 · 速率)读的正是 done 事件上的这几格。这台桌面端此刻手里
// 就有全套 —— 它自己刚算完并落了库 —— 所以送出去的是同一份数,与重连后从
// synthesizePeerHistory 读到的那一条同形。
//
// runtime 自己 emit 的 Done(只有 openclaw / piagent 有)留零,零读作「没上报」,
// 不会把这一条覆盖掉。
func (s *chatSvc) publishPeerTurnDone(sessionID int64, msg *chat_entity.Message) {
	if msg == nil {
		return
	}
	s.publishPeerEvent(sessionID, agentruntime.Done{
		Model: msg.Model, DurationMs: msg.DurationMs,
		FirstTokenMs: msg.FirstTokenMs, TokensPerSec: msg.TokensPerSec,
	})
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
			// 收口带上本轮统计:对端 Peer Tab 的 meta(模型 · 耗时 · 首字 · 速率)
			// 读的正是这几格,而它们就在手边这条消息实体上。
			if err := appendEvent(agentruntime.Done{
				Model: message.Model, DurationMs: message.DurationMs,
				FirstTokenMs: message.FirstTokenMs, TokensPerSec: message.TokensPerSec,
			}); err != nil {
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
