package ctlaccount

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"strconv"
	"strings"
	"time"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/descriptorpb"

	"github.com/agentre-hub/agentre/internal/pkg/cagoenvelope"
	"github.com/agentre-hub/agentre/internal/pkg/relaypeer"
	"github.com/agentre-hub/agentre/pkg/wire/agentrewire"
	"github.com/agentre-hub/agentre/pkg/wire/wirecall"
)

// 账号模式的对话内容：messages（执行端在线读它的持久帧，离线退回 server 的镜像）与 tail
// （按游标跟随持久帧）。两条来路都先折成同一种中性帧，再走同一套投影，因此输出一致。

// MessageBlock 是 --full 时一条消息里的块。
type MessageBlock struct {
	Type  string         `json:"type"`
	Text  string         `json:"text,omitempty"`
	Name  string         `json:"name,omitempty"`
	Input map[string]any `json:"input,omitempty"`
	// IsError 标记出错的工具结果。
	IsError bool `json:"isError,omitempty"`
}

// MessageRow 是一条消息。
type MessageRow struct {
	Role   string         `json:"role"`
	At     string         `json:"at,omitempty"`
	Text   string         `json:"text"`
	Blocks []MessageBlock `json:"blocks,omitempty"`
}

// MessagesPage 是 messages 的一页（与桌面 /ctl/v1/messages 同形）。
type MessagesPage struct {
	Session  string       `json:"session"`
	Messages []MessageRow `json:"messages"`
	HasMore  bool         `json:"hasMore"`
	Before   string       `json:"before,omitempty"`
}

// frame 是一条持久帧里 agrctl 关心的那部分。
type frame struct {
	seq     int64
	at      int64
	kind    string // user_message / text / tool_use / tool_result / turn_done / ""（其它）
	text    string
	name    string
	input   map[string]any
	isError bool
}

// frameOfNote 折一条执行端的持久通知；预览帧不入转录。
func frameOfNote(n *agentrewire.DurableNotification) frame {
	f := frame{seq: n.GetSeq(), at: n.GetCreatetime()}
	var ev *agentrewire.RuntimeEventNotification
	switch p := n.GetPayload().GetPayload().(type) {
	case *agentrewire.RpcNotification_RuntimeEvent:
		ev = p.RuntimeEvent
	case *agentrewire.RpcNotification_AutonomousTurnEvent:
		ev = p.AutonomousTurnEvent
	case *agentrewire.RpcNotification_RunResultDone, *agentrewire.RpcNotification_AutonomousTurnDone:
		f.kind = "turn_done"
		return f
	default:
		return f
	}
	if ev.GetPreview() {
		return f
	}
	switch e := ev.GetEvent().(type) {
	case *agentrewire.RuntimeEventNotification_UserMessage:
		f.kind, f.text = "user_message", e.UserMessage.GetText()
	case *agentrewire.RuntimeEventNotification_TextDelta:
		f.kind, f.text = "text", e.TextDelta.GetText()
	case *agentrewire.RuntimeEventNotification_ToolCall:
		f.kind, f.name = "tool_use", e.ToolCall.GetName()
		_ = json.Unmarshal(e.ToolCall.GetInput(), &f.input)
	case *agentrewire.RuntimeEventNotification_ToolResult:
		f.kind, f.text, f.isError = "tool_result", e.ToolResult.GetContent(), e.ToolResult.GetIsError()
	}
	return f
}

// mirrorFrame 是 server 镜像转录里的一行（method + params 的 JSON 投影）。
type mirrorFrame struct {
	Seq        int64           `json:"seq"`
	Method     string          `json:"method"`
	Params     json.RawMessage `json:"params"`
	Createtime int64           `json:"createtime"`
}

func frameOfMirror(m mirrorFrame) frame {
	f := frame{seq: m.Seq, at: m.Createtime}
	switch m.Method {
	case "runtime.runResultDone", "runtime.autonomousTurn.done":
		f.kind = "turn_done"
		return f
	case "runtime.event", "runtime.autonomousTurn.event":
	default:
		return f
	}
	var p struct {
		Event struct {
			Kind       string         `json:"kind"`
			Text       string         `json:"text"`
			Name       string         `json:"name"`
			Input      map[string]any `json:"input"`
			Content    string         `json:"content"`
			IsError    bool           `json:"isError"`
			ToolCallID string         `json:"toolCallId"`
		} `json:"event"`
	}
	if json.Unmarshal(m.Params, &p) != nil {
		return f
	}
	e := p.Event
	switch e.Kind {
	case "user_message":
		f.kind, f.text = "user_message", e.Text
	case "text_delta":
		f.kind, f.text = "text", e.Text
	case "tool_use_start":
		f.kind, f.name, f.input = "tool_use", e.Name, e.Input
	case "tool_result":
		f.kind, f.text, f.isError = "tool_result", e.Content, e.IsError
	}
	return f
}

// turn 是一个用户回合：用户那句话加上助手的回答。seq 是它第一行的帧号。
type turn struct {
	seq  int64
	rows []MessageRow
}

// projectTurns 把按帧号升序的帧折成回合。一轮助手回答合成一条消息：文本是全部文本块的
// 拼接；工具调用与结果只在 full 时作为块带出。
func projectTurns(frames []frame, full bool) []turn {
	var turns []turn
	var cur *turn
	var asst *MessageRow
	var asstSeq int64
	flush := func() {
		if asst == nil {
			return
		}
		if cur == nil {
			turns = append(turns, turn{seq: asstSeq})
			cur = &turns[len(turns)-1]
		}
		cur.rows = append(cur.rows, *asst)
		asst = nil
	}
	ensure := func(f frame) {
		if asst == nil {
			asst = &MessageRow{Role: "assistant", At: rfc3339Ms(f.at)}
			asstSeq = f.seq
		}
	}
	for _, f := range frames {
		switch f.kind {
		case "user_message":
			flush()
			turns = append(turns, turn{seq: f.seq, rows: []MessageRow{{Role: "user", At: rfc3339Ms(f.at), Text: f.text}}})
			cur = &turns[len(turns)-1]
		case "text":
			ensure(f)
			asst.Text += f.text
			if full {
				if n := len(asst.Blocks); n > 0 && asst.Blocks[n-1].Type == "text" {
					asst.Blocks[n-1].Text += f.text
				} else {
					asst.Blocks = append(asst.Blocks, MessageBlock{Type: "text", Text: f.text})
				}
			}
		case "tool_use":
			ensure(f)
			if full {
				asst.Blocks = append(asst.Blocks, MessageBlock{Type: "tool_use", Name: f.name, Input: f.input})
			}
		case "tool_result":
			ensure(f)
			if full {
				asst.Blocks = append(asst.Blocks, MessageBlock{Type: "tool_result", Text: f.text, IsError: f.isError})
			}
		case "turn_done":
			flush()
		}
	}
	flush()
	return turns
}

// pageTurns 取 before 之前的最后 n 个回合（before = 0 不设上界）。more 报告 have 之外是否
// 还有更早的回合（moreBefore：来源自己说还有）。
func pageTurns(turns []turn, n int, moreBefore bool) (page MessagesPage) {
	hasMore := moreBefore
	if len(turns) > n {
		turns, hasMore = turns[len(turns)-n:], true
	}
	page.Messages = []MessageRow{}
	for _, t := range turns {
		page.Messages = append(page.Messages, t.rows...)
	}
	page.HasMore = hasMore
	if hasMore && len(turns) > 0 {
		page.Before = strconv.FormatInt(turns[0].seq, 10)
	}
	return page
}

// defaultLastTurns 是 messages --last 的缺省回合数。
const defaultLastTurns = 20

// Messages 读一条会话的最后 n 个回合：执行端在线就读它的持久帧；设备离线、会话保存过就读
// server 的镜像；镜像里没有内容时如实说设备离线。
func (a *Client) Messages(ctx context.Context, id string, last int, before int64, full bool) (MessagesPage, error) {
	if last <= 0 {
		last = defaultLastTurns
	}
	h, err := a.openSession(ctx, id)
	switch {
	case err == nil:
		defer func() { _ = h.conn.Close() }()
		frames, err := pullAll(ctx, h, id, before)
		if err != nil {
			return MessagesPage{}, a.ReachErr(err, h.device, id)
		}
		page := pageTurns(projectTurns(frames, full), last, false)
		page.Session = id
		return page, nil
	case errors.Is(err, relaypeer.ErrTargetOffline):
		page, ok, merr := a.mirrorMessages(ctx, id, last, before, full)
		if merr != nil {
			return MessagesPage{}, merr
		}
		if !ok {
			return MessagesPage{}, a.ReachErr(err, a.deviceOf(ctx, id), id)
		}
		return page, nil
	default:
		return MessagesPage{}, a.ReachErr(err, "", id)
	}
}

// pullAll 从头读完一条会话的持久帧（before > 0 时只要它之前的）。
func pullAll(ctx context.Context, h *session, id string, before int64) ([]frame, error) {
	var frames []frame
	cursor := int64(0)
	for {
		resp, err := wirecall.SessionPull(ctx, h.conn, &agentrewire.SessionPullRequest{ConversationId: id, PeerFingerprint: h.origin(), Cursor: cursor, Limit: 500})
		if err != nil {
			return nil, err
		}
		for _, n := range resp.GetNotifications() {
			if before > 0 && n.GetSeq() >= before {
				return frames, nil
			}
			frames = append(frames, frameOfNote(n))
		}
		if !resp.GetHasMore() || resp.GetCursor() <= cursor {
			return frames, nil
		}
		cursor = resp.GetCursor()
	}
}

// mirrorPages 是翻 server 镜像的页数上限。
const mirrorPages = 200

// mirrorMessages 从 server 镜像里从最新往回翻，直到手里有 last+1 个回合（最老那个可能被页
// 边界截断，不算完整）或没有更早的了。ok = false 表示镜像里没有这条会话的任何内容。
func (a *Client) mirrorMessages(ctx context.Context, id string, last int, before int64, full bool) (page MessagesPage, ok bool, err error) {
	var frames []frame
	upper := before
	hasBefore := false
	for range mirrorPages {
		q := url.Values{"conversation_id": {id}, "direction": {"backward"}, "cursor": {strconv.FormatInt(upper, 10)}}
		raw, gerr := a.srv.GetRaw(ctx, "/v1/agent-sessions/transcript?"+q.Encode())
		if gerr != nil {
			return page, false, ServerErr(gerr)
		}
		var resp struct {
			Frames    []mirrorFrame `json:"frames"`
			OldestSeq int64         `json:"oldest_seq"`
			HasBefore bool          `json:"has_before"`
		}
		if derr := cagoenvelope.Decode(raw, &resp); derr != nil {
			return page, false, fmt.Errorf("decode the transcript: %w", derr)
		}
		older := make([]frame, 0, len(resp.Frames)+len(frames))
		for _, m := range resp.Frames {
			older = append(older, frameOfMirror(m))
		}
		frames = append(older, frames...)
		hasBefore = resp.HasBefore
		if len(resp.Frames) == 0 || !resp.HasBefore || resp.OldestSeq <= 0 || len(projectTurns(frames, false)) > last {
			break
		}
		upper = resp.OldestSeq
	}
	if len(frames) == 0 {
		return page, false, nil
	}
	page = pageTurns(projectTurns(frames, full), last, hasBefore)
	page.Session = id
	return page, true, nil
}

// ─── tail ───

const tailCursorPrefix = "v1:"

// TailCursor 是 seq 这一帧之后的 tail 游标（与桌面执行者同一写法）。
func TailCursor(seq int64) string { return tailCursorPrefix + strconv.FormatInt(seq, 10) }

func parseTailCursor(s string) (int64, bool) {
	raw, ok := strings.CutPrefix(strings.TrimSpace(s), tailCursorPrefix)
	if !ok {
		return 0, false
	}
	seq, err := strconv.ParseInt(raw, 10, 64)
	return seq, err == nil && seq >= 0
}

// TailLine 是 tail 输出的一行（形状与桌面执行者相同）。
type TailLine struct {
	Cursor string          `json:"cursor"`
	Type   string          `json:"type"`
	Kind   string          `json:"kind,omitempty"`
	Status string          `json:"status,omitempty"`
	Error  string          `json:"error,omitempty"`
	Data   json.RawMessage `json:"data,omitempty"`
}

// tailTypes 把运行时事件归到稳定的类别；没列出的一律 "event"。
var tailTypes = map[string]string{
	"text_delta": "text", "thinking_delta": "thinking", "tool_use_start": "tool_call", "tool_result": "tool_result",
	"user_message": "user_message", "tool_permission_request": "pending", "ask_user_question": "pending",
	"exec_approval_requested": "pending", "tool_approval_requested": "pending",
	"tool_permission_resolved": "pending_resolved", "ask_user_question_answered": "pending_resolved",
	"exec_approval_resolved": "pending_resolved", "tool_approval_resolved": "pending_resolved",
}

// eventKindOf 读出事件 oneof 当前那一格的 event_kind 标注。
func eventKindOf(ev *agentrewire.RuntimeEventNotification) (string, proto.Message) {
	m := ev.ProtoReflect()
	fd := m.WhichOneof(m.Descriptor().Oneofs().ByName("event"))
	if fd == nil {
		return "", nil
	}
	kind := ""
	if opts, ok := fd.Options().(*descriptorpb.FieldOptions); ok {
		kind, _ = proto.GetExtension(opts, agentrewire.E_EventKind).(string)
	}
	return kind, m.Get(fd).Message().Interface()
}

func marshalData(m proto.Message, kind string) json.RawMessage {
	raw, err := protojson.Marshal(m)
	if err != nil {
		return nil
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil {
		fields = map[string]json.RawMessage{}
	}
	if kind != "" {
		fields["kind"], _ = json.Marshal(kind)
	}
	out, _ := json.Marshal(fields)
	return out
}

// tailLineOf 把一条持久通知译成一行输出；不是会话事件的通知交回 ok = false。
func tailLineOf(n *agentrewire.DurableNotification) (line TailLine, ok bool) {
	line.Cursor = tailCursorPrefix + strconv.FormatInt(n.GetSeq(), 10)
	var ev *agentrewire.RuntimeEventNotification
	switch p := n.GetPayload().GetPayload().(type) {
	case *agentrewire.RpcNotification_RuntimeEvent:
		ev = p.RuntimeEvent
	case *agentrewire.RpcNotification_AutonomousTurnEvent:
		ev = p.AutonomousTurnEvent
	case *agentrewire.RpcNotification_TurnStarted:
		line.Type, line.Kind, line.Data = "turn_started", "runtime.turnStarted", marshalData(p.TurnStarted, "")
		return line, true
	case *agentrewire.RpcNotification_AutonomousTurnStarted:
		line.Type, line.Kind, line.Data = "turn_started", "runtime.autonomousTurn.started", marshalData(p.AutonomousTurnStarted, "")
		return line, true
	case *agentrewire.RpcNotification_RunResultDone:
		line.Type, line.Kind, line.Error, line.Data = "turn_done", "runtime.runResultDone", p.RunResultDone.GetStopErrorMessage(), marshalData(p.RunResultDone, "")
		return line, true
	case *agentrewire.RpcNotification_AutonomousTurnDone:
		line.Type, line.Kind, line.Error, line.Data = "turn_done", "runtime.autonomousTurn.done", p.AutonomousTurnDone.GetStopErrorMessage(), marshalData(p.AutonomousTurnDone, "")
		return line, true
	default:
		return line, false
	}
	if ev.GetPreview() {
		return line, false
	}
	kind, inner := eventKindOf(ev)
	line.Kind = kind
	if line.Type = tailTypes[kind]; line.Type == "" {
		line.Type = "event"
	}
	switch e := inner.(type) {
	case *agentrewire.ToolCall:
		line.Data, _ = json.Marshal(map[string]any{"kind": kind, "id": e.GetId(), "name": e.GetName(), "input": rawJSON(e.GetInput())})
	case *agentrewire.ToolResult:
		line.Data, _ = json.Marshal(map[string]any{"kind": kind, "toolCallId": e.GetToolCallId(), "content": e.GetContent(), "isError": e.GetIsError()})
	case nil:
	default:
		line.Data = marshalData(e, kind)
	}
	return line, true
}

// Tail 按游标跟随一条会话的持久帧到 w：from 给出时先补它之后错过的，之后是新的；untilIdle 时
// 空闲 / 失败收尾（nil）、等待输入以 *WaitingError 收尾。断线从最后一个游标重连续接。
func (a *Client) Tail(ctx context.Context, w io.Writer, id, from string, untilIdle bool) error {
	cursor, resume := int64(0), false
	if from != "" {
		seq, ok := parseTailCursor(from)
		if !ok {
			return Usagef("invalid cursor %q: use one printed by agrctl tail, or read the history with agrctl messages %s", from, id)
		}
		cursor, resume = seq, true
	}
	lastStatus := ""
	status, err := a.follow(ctx, id, cursor, resume, followSink{
		note: func(n *agentrewire.DurableNotification) error {
			if line, ok := tailLineOf(n); ok {
				return writeLine(w, line)
			}
			return nil
		},
		status: func(cursor int64, status string) error {
			if status == lastStatus {
				return nil
			}
			lastStatus = status
			return writeLine(w, TailLine{Cursor: tailCursorPrefix + strconv.FormatInt(cursor, 10), Type: "status", Status: status})
		},
		done: func(status string) bool { return untilIdle && status != StatusRunning },
	})
	if err != nil {
		return err
	}
	if status == StatusWaiting {
		return &WaitingError{Session: id}
	}
	return nil
}

// Follow 把一条会话 from 之后的每一条持久帧按序交给 fn，直到这条会话空闲或失败（等待输入
// 不算收口），交回那时的状态。断线从最后一个帧号重连续接；ctx 取消时交回空状态、nil。
func (a *Client) Follow(ctx context.Context, id string, from int64, fn func(*agentrewire.DurableNotification)) (string, error) {
	return a.follow(ctx, id, from, true, followSink{
		note:   func(n *agentrewire.DurableNotification) error { fn(n); return nil },
		status: func(int64, string) error { return nil },
		done:   func(status string) bool { return status == StatusIdle || status == StatusFailed },
	})
}

// LatestSeq 是一条会话此刻最新的持久帧号（续聊前取，之后只跟随它之后的帧）。
func (a *Client) LatestSeq(ctx context.Context, id string) (int64, error) {
	h, err := a.openSession(ctx, id)
	if err != nil {
		return 0, a.ReachErr(err, "", id)
	}
	defer func() { _ = h.conn.Close() }()
	return h.summary.GetLatestSeq(), nil
}

// followSink 是 follow 的出口：每条持久帧、每次读到的状态、读到哪个状态就收尾。
type followSink struct {
	note   func(*agentrewire.DurableNotification) error
	status func(cursor int64, status string) error
	done   func(status string) bool
}

// follow 是 Tail 与 Follow 共用的跟随循环：resume = false 时从会话此刻最新的帧之后开始。
// 交回收尾时的状态；ctx 取消交回空状态、nil。
func (a *Client) follow(ctx context.Context, id string, cursor int64, resume bool, sink followSink) (string, error) {
	var h *session
	defer func() {
		if h != nil {
			_ = h.conn.Close()
		}
	}()
	failures := 0
	connected := false
	for {
		if ctx.Err() != nil {
			return "", nil
		}
		if h == nil {
			var err error
			if h, err = a.openSession(ctx, id); err != nil {
				if ctx.Err() != nil {
					return "", nil
				}
				if !transient(err) || (!connected && errors.Is(err, relaypeer.ErrRelayUnavailable)) || failures >= MaxReconnects {
					return "", a.ReachErr(err, "", id)
				}
				failures++
				time.Sleep(a.opts.ReconnectDelay)
				continue
			}
			connected = true
			if !resume {
				cursor, resume = h.summary.GetLatestSeq(), true
			}
		}
		status, err := a.followCycle(ctx, h, id, &cursor, sink)
		if err != nil {
			_ = h.conn.Close()
			h = nil
			if ctx.Err() != nil {
				return "", nil
			}
			if errors.Is(err, ErrCursorGone) {
				return "", err
			}
			if !transient(err) || failures >= MaxReconnects {
				return "", a.ReachErr(err, "", id)
			}
			failures++
			time.Sleep(a.opts.ReconnectDelay)
			continue
		}
		failures = 0
		if sink.done(status) {
			return status, nil
		}
		select {
		case <-ctx.Done():
			return "", nil
		case <-h.conn.Done():
			h = nil
		case <-time.After(a.opts.PollInterval):
		}
	}
}

// followCycle 读一次状态、补齐游标之后的持久帧、再报状态。先读状态再读帧：状态说空闲时，
// 那之前的帧都已经在手里。
func (a *Client) followCycle(ctx context.Context, h *session, id string, cursor *int64, sink followSink) (string, error) {
	status, _, err := a.state(ctx, h, id)
	if err != nil {
		return "", err
	}
	for {
		prev := *cursor
		resp, err := wirecall.SessionPull(ctx, h.conn, &agentrewire.SessionPullRequest{ConversationId: id, PeerFingerprint: h.origin(), Cursor: *cursor, Limit: 500})
		if err != nil {
			return "", err
		}
		if o := resp.GetOldestSeq(); o > 0 && len(resp.GetNotifications()) > 0 && o > *cursor+1 {
			return "", ErrCursorGone
		}
		for _, n := range resp.GetNotifications() {
			if err := sink.note(n); err != nil {
				return "", err
			}
			*cursor = max(*cursor, n.GetSeq())
		}
		if !resp.GetHasMore() || resp.GetCursor() <= prev {
			break
		}
		*cursor = max(*cursor, resp.GetCursor())
	}
	if err := sink.status(*cursor, status); err != nil {
		return "", err
	}
	return status, nil
}

func writeLine(w io.Writer, line TailLine) error {
	raw, err := json.Marshal(line)
	if err != nil {
		return err
	}
	_, err = w.Write(append(raw, '\n'))
	return err
}
