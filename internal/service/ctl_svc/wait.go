package ctl_svc

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/runtimes/remote/wire"
	"github.com/agentre-hub/agentre/internal/service/chat_svc"
)

// defaultPollInterval 是 wait / tail 复查会话运行态的间隔。流事件与轮次回执只是提前
// 唤醒的信号;状态一律以会话行为准,漏掉一个信号也最多晚这么久。
const defaultPollInterval = time.Second

func (h *ctlHandler) poll() time.Duration {
	if h.pollInterval > 0 {
		return h.pollInterval
	}
	return defaultPollInterval
}

// outcomeDTO 是一次等待的结果(spec「等待」):idle 带最后一轮的最终文本,failed 带错误
// 摘要,waiting 带待处理项,running 表示等到超时会话仍在跑。
type outcomeDTO struct {
	Status  string                            `json:"status"`
	Text    string                            `json:"text,omitempty"`
	Error   string                            `json:"error,omitempty"`
	Pending *wire.SessionPendingWaitersResult `json:"pending,omitempty"`
}

type waitOutcome struct {
	dto                outcomeDTO
	assistantMessageID int64
	aborted            bool
}

// settled 报告会话已经空闲(这一轮收口,也没有待送达的插话)。
func (o waitOutcome) settled() bool {
	return o.dto.Status == string(chat_svc.CtlStatusIdle) || o.dto.Status == string(chat_svc.CtlStatusFailed)
}

func (o waitOutcome) cardOutcome() chat_svc.CtlSendOutcome {
	switch {
	case o.dto.Status == string(chat_svc.CtlStatusFailed):
		return chat_svc.CtlSendOutcome{Status: chat_svc.CtlSendFailed, FinalText: o.dto.Error, AssistantMessageID: o.assistantMessageID}
	case o.aborted:
		return chat_svc.CtlSendOutcome{Status: chat_svc.CtlSendCanceled, FinalText: o.dto.Text, AssistantMessageID: o.assistantMessageID}
	default:
		return chat_svc.CtlSendOutcome{Status: chat_svc.CtlSendCompleted, FinalText: o.dto.Text, AssistantMessageID: o.assistantMessageID}
	}
}

var errConversationGone = errors.New("session disappeared while waiting")

// awaitConversation 等到会话空闲、进入等待输入或 timeout 到期(0 = 不超时)。状态以会话行为准、反复
// 复查,所以断线重连后再等一次既不重复也不遗漏。turnCh 可为 nil;非 nil 时它的回执
// 提供这一轮是否被中止 / 失败的细节。
func (h *ctlHandler) awaitConversation(ctx context.Context, sessionID int64, timeout time.Duration, turnCh <-chan chat_svc.TurnResult) (waitOutcome, error) {
	var deadline <-chan time.Time
	if timeout > 0 {
		timer := time.NewTimer(timeout)
		defer timer.Stop()
		deadline = timer.C
	}
	events, cancel := h.chat.SubscribeSessionEvents(sessionID)
	defer cancel()
	tick := time.NewTicker(h.poll())
	defer tick.Stop()
	var last *chat_svc.TurnResult
	for {
		if err := ctx.Err(); err != nil {
			return waitOutcome{}, err
		}
		c, err := h.conversations.DescribeCtlConversation(ctx, sessionID)
		if err != nil {
			return waitOutcome{}, err
		}
		if c == nil {
			return waitOutcome{}, errConversationGone
		}
		switch c.Status {
		case chat_svc.CtlStatusWaiting:
			pending, err := h.conversations.PendingCtlWaiters(ctx, sessionID)
			if err != nil {
				return waitOutcome{}, err
			}
			return waitOutcome{dto: outcomeDTO{Status: string(chat_svc.CtlStatusWaiting), Pending: &pending}}, nil
		case chat_svc.CtlStatusIdle, chat_svc.CtlStatusFailed:
			// 已经到了的回执带着「被中止 / 以错误收场」的细节,先收下再定论。
			select {
			case res := <-turnCh:
				last = &res
			default:
			}
			return h.settledOutcome(ctx, c, last)
		}
		select {
		case <-ctx.Done():
			return waitOutcome{}, ctx.Err()
		case <-deadline:
			return waitOutcome{dto: outcomeDTO{Status: string(chat_svc.CtlStatusRunning)}}, nil
		case res := <-turnCh:
			last = &res
		case _, ok := <-events:
			if !ok {
				events = nil
			}
		case <-tick.C:
		}
	}
}

func (h *ctlHandler) settledOutcome(ctx context.Context, c *chat_svc.CtlConversation, last *chat_svc.TurnResult) (waitOutcome, error) {
	out := waitOutcome{dto: outcomeDTO{Status: string(chat_svc.CtlStatusIdle)}, assistantMessageID: c.LastAssistantMessageID}
	if last != nil && last.AssistantMessageID > 0 {
		out.assistantMessageID = last.AssistantMessageID
	}
	failed := c.Status == chat_svc.CtlStatusFailed || (last != nil && last.Err != nil)
	if failed {
		out.dto.Status = string(chat_svc.CtlStatusFailed)
		out.dto.Error = c.LastError
		if last != nil && last.Err != nil {
			switch {
			case isSessionRunningErr(last.Err):
				// 执行它的 agentred 拒绝了这一轮:会话在那边还跑着另一轮。
				out.dto.Error = errSessionRunning
			case out.dto.Error == "":
				out.dto.Error = last.Err.Error()
			}
		}
		if out.dto.Error == "" {
			out.dto.Error = "the turn failed"
		}
		return out, nil
	}
	out.aborted = last != nil && last.Aborted
	text, err := h.chat.FinalAssistantText(ctx, out.assistantMessageID)
	if err != nil {
		return waitOutcome{}, err
	}
	out.dto.Text = text
	return out, nil
}

// writeAwaitErr 写一次失败的等待。调用方断开时不写(没人收)。
func writeAwaitErr(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		writeErr(w, http.StatusRequestTimeout, "canceled")
	case errors.Is(err, errConversationGone):
		writeErr(w, http.StatusNotFound, errSessionNotVisible)
	default:
		writeErr(w, http.StatusInternalServerError, "wait failed: "+err.Error())
	}
}

// ---- wait ----

type waitRequest struct {
	ConversationID string `json:"conversationId"`
	TimeoutMs      int64  `json:"timeoutMs,omitempty"`
}

type waitDTO struct {
	ConversationID string `json:"conversationId"`
	outcomeDTO
}

func (h *ctlHandler) serveWait(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "wait requires POST")
		return
	}
	if h.chat == nil || h.conversations == nil {
		writeErr(w, http.StatusServiceUnavailable, "control service not ready")
		return
	}
	var req waitRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json body")
		return
	}
	c, ok := h.lookupConversation(w, r, req.ConversationID)
	if !ok {
		return
	}
	res, err := h.awaitConversation(r.Context(), c.SessionID, millis(req.TimeoutMs), nil)
	if err != nil {
		writeAwaitErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, waitDTO{ConversationID: c.ConversationID, outcomeDTO: res.dto})
}
