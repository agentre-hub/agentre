package ctl_svc

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"

	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/runtimes/remote/wire"
	"github.com/agentre-hub/agentre/internal/service/chat_svc"
)

// cursorPrefix 让 tail 的游标对调用方保持不透明:今天它包着一个持久帧号,换实现时
// 只换前缀,旧游标按「失效」处理而不是被误读。
const cursorPrefix = "v1:"

func formatCursor(seq int64) string { return cursorPrefix + strconv.FormatInt(seq, 10) }

func parseCursor(s string) (int64, error) {
	raw, ok := strings.CutPrefix(strings.TrimSpace(s), cursorPrefix)
	if !ok {
		return 0, fmt.Errorf("invalid cursor %q", s)
	}
	seq, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || seq < 0 {
		return 0, fmt.Errorf("invalid cursor %q", s)
	}
	return seq, nil
}

// tailLine 是 tail 输出的一行 NDJSON(spec「跟随输出」)。Type 是稳定的事件类别;Kind 是
// 原始事件类型;Data 是事件原样。Preview=true 的是实时增量,随后同一段内容还会以持久行
// (不带 preview)再出现一次,只有持久行推进游标。
type tailLine struct {
	Cursor  string          `json:"cursor"`
	Type    string          `json:"type"`
	Kind    string          `json:"kind,omitempty"`
	Preview bool            `json:"preview,omitempty"`
	Status  string          `json:"status,omitempty"`
	Error   string          `json:"error,omitempty"`
	Data    json.RawMessage `json:"data,omitempty"`
}

// tailTypes 把运行时事件归到 spec 列出的几类;没列出的一律 "event"。
var tailTypes = map[agentruntime.EventKind]string{
	agentruntime.EventTextDelta:               "text",
	agentruntime.EventThinkingDelta:           "thinking",
	agentruntime.EventToolUseStart:            "tool_call",
	agentruntime.EventToolResult:              "tool_result",
	agentruntime.EventUserMessage:             "user_message",
	agentruntime.EventToolPermissionRequest:   "pending",
	agentruntime.EventAskUserQuestion:         "pending",
	agentruntime.EventExecApprovalRequested:   "pending",
	agentruntime.EventToolApprovalRequested:   "pending",
	agentruntime.EventToolPermissionResolved:  "pending_resolved",
	agentruntime.EventAskUserQuestionAnswered: "pending_resolved",
	agentruntime.EventExecApprovalResolved:    "pending_resolved",
	agentruntime.EventToolApprovalResolved:    "pending_resolved",
}

// toTailLine 把一条对端通知译成一行输出;cursor 是此前最后一个持久帧号,持久帧推进它。
func toTailLine(n chat_svc.CtlTailNotification, cursor *int64) tailLine {
	if n.Seq > *cursor {
		*cursor = n.Seq
	}
	switch n.Method {
	case wire.NotifyEvent, wire.NotifyAutonomousTurnEvent:
		var frame wire.EventFrame
		switch p := n.Params.(type) {
		case wire.EventFrame:
			frame = p
		case *wire.EventFrame:
			frame = *p
		}
		raw, _ := json.Marshal(frame.Event)
		var head struct {
			Kind agentruntime.EventKind `json:"kind"`
		}
		_ = json.Unmarshal(raw, &head)
		typ, ok := tailTypes[head.Kind]
		if !ok {
			typ = "event"
		}
		return tailLine{Cursor: formatCursor(*cursor), Type: typ, Kind: string(head.Kind), Preview: frame.Preview, Data: raw}
	case wire.NotifyTurnStarted, wire.NotifyAutonomousTurnStarted:
		raw, _ := json.Marshal(n.Params)
		return tailLine{Cursor: formatCursor(*cursor), Type: "turn_started", Kind: n.Method, Data: raw}
	case wire.NotifyRunResultDone, wire.NotifyAutonomousTurnDone:
		raw, _ := json.Marshal(n.Params)
		line := tailLine{Cursor: formatCursor(*cursor), Type: "turn_done", Kind: n.Method, Data: raw}
		switch p := n.Params.(type) {
		case wire.RunResultDoneFrame:
			line.Error = p.StopErrMsg
		case *wire.RunResultDoneFrame:
			line.Error = p.StopErrMsg
		}
		return line
	default:
		raw, _ := json.Marshal(n.Params)
		return tailLine{Cursor: formatCursor(*cursor), Type: "event", Kind: n.Method, Data: raw}
	}
}

// serveTail 以 NDJSON 跟随一条会话(GET /ctl/v1/tail?conversationId=&from=&untilIdle=true)。
// from 给出时先补齐它之后错过的持久帧;untilIdle 时会话空闲或进入等待输入即以 status 行收尾。
func (h *ctlHandler) serveTail(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeErr(w, http.StatusMethodNotAllowed, "tail requires GET")
		return
	}
	if h.chat == nil || h.conversations == nil {
		writeErr(w, http.StatusServiceUnavailable, "control service not ready")
		return
	}
	q := r.URL.Query()
	resume := q.Get("from") != ""
	var from int64
	if resume {
		seq, err := parseCursor(q.Get("from"))
		if err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		from = seq
	}
	untilIdle := q.Get("untilIdle") == "true" || q.Get("untilIdle") == "1"
	c, ok := h.lookupConversation(w, r, q.Get("conversationId"))
	if !ok {
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeErr(w, http.StatusInternalServerError, "streaming unsupported")
		return
	}
	tail, err := h.conversations.TailCtlConversation(r.Context(), c.SessionID, from, resume)
	if err != nil {
		if errors.Is(err, chat_svc.ErrCtlCursorInvalid) {
			writeErr(w, http.StatusGone, "cursor is no longer valid; read the history with agrctl messages")
			return
		}
		writeErr(w, http.StatusInternalServerError, "tail failed: "+err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/x-ndjson")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	enc := json.NewEncoder(w)
	write := func(line tailLine) bool {
		if err := enc.Encode(line); err != nil {
			return false
		}
		flusher.Flush()
		return true
	}
	cursor := tail.LatestSeq
	if resume {
		cursor = from
	}
	for _, n := range tail.Replay {
		if !write(toTailLine(n, &cursor)) {
			return
		}
	}
	lastStatus := ""
	// settle 复查运行态:变了就写一行 status;untilIdle 时空闲 / 等待输入即收尾(交回 true)。
	settle := func() bool {
		cur, err := h.conversations.DescribeCtlConversation(r.Context(), c.SessionID)
		if err != nil || cur == nil {
			return false
		}
		status := string(cur.Status)
		if status != lastStatus {
			lastStatus = status
			if !write(tailLine{Cursor: formatCursor(cursor), Type: "status", Status: status}) {
				return true
			}
		}
		return untilIdle && cur.Status != chat_svc.CtlStatusRunning
	}
	if settle() {
		return
	}
	tick := time.NewTicker(h.poll())
	defer tick.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case n, ok := <-tail.Live:
			if !ok {
				if tail.Lagged != nil && tail.Lagged() {
					logger.Ctx(r.Context()).Info("ctl_svc.serveTail: subscriber fell behind",
						zap.Int64("sessionId", c.SessionID))
					_ = write(tailLine{Cursor: formatCursor(cursor), Type: "error",
						Error: "tail fell behind; resume from the last cursor"})
				}
				return
			}
			line := toTailLine(n, &cursor)
			if !write(line) {
				return
			}
			switch line.Type {
			case "turn_done", "pending", "pending_resolved":
				if settle() {
					return
				}
			}
		case <-tick.C:
			if settle() {
				return
			}
		}
	}
}
