package openclaw

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"

	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
	"github.com/agentre-hub/agentre/internal/pkg/openclawgateway"
)

const gatewayCancelledStatus = "cancelled" //nolint:misspell // OpenClaw may emit this British spelling.

type activeTurn struct {
	runtime *Runtime
	ctx     context.Context
	client  *openclawgateway.Client

	sessionID int64
	// sessionKey 是本轮当前认定的会话 key:开轮时是我们请求的那个,认领到网关规范化
	// 后的 key(agent:<agentId>:<key>)就换成后者。sessionKeyCanonical 标记它是否
	// 已经是规范形式 —— chat.send 的应答不回报 key,只能从事件里认领。
	sessionKey          string
	sessionKeyCanonical bool
	agentID             string
	// subscribeMessages 记录本轮是否靠 sessions.messages.subscribe 收事件 ——
	// 断线重连后订阅会丢,Ready 时必须补订。
	subscribeMessages bool
	// usageBaselineEndedAt 是开轮前会话记录的 endedAt:收轮补 usage 时要等它变大,
	// 否则读到的还是上一轮的数字。
	usageBaselineEndedAt int64
	runID                string
	out                  chan agentruntime.Event
	result               *agentruntime.RunResult
	// sessionDescribe 记录网关是否广播了可选的 sessions.describe —— 收轮补 usage
	// 只在广播时才调。
	sessionDescribe bool

	finishOnce sync.Once
	abortMu    sync.Mutex
	aborted    bool
	// turnToken 本轮的身份 token(创建时一次性赋值,发布进 r.active 后只读,Abort 比对用)。
	turnToken uint64

	approvalMu        sync.Mutex
	approvalResolveMu sync.Mutex
	approvals         map[string]*approvalState
	initialApprovals  []gatewayExecApprovalRecord

	lastAgentSeq int64
	lastChatSeq  int64
	assistant    string
	thinking     string
}

func (a *activeTurn) consume() {
	for _, record := range a.initialApprovals {
		a.handleApprovalRequested(record)
	}
	for {
		select {
		case event := <-a.client.Events():
			if a.handleGatewayEvent(event) {
				a.reconcile()
			}
		case <-a.client.Gaps():
			a.reconcileApprovals()
			a.reconcile()
		case <-a.client.Ready():
			a.resubscribe()
			a.reconcileApprovals()
			a.reconcile()
		case <-a.client.Errors():
			// A transport error is not terminal. Client reconnects; Ready then
			// triggers agent.wait. Never resubmit the user message here.
		case <-a.ctx.Done():
			a.abortMu.Lock()
			aborted := a.aborted
			a.abortMu.Unlock()
			if aborted {
				a.finish(agentruntime.ErrAborted)
			} else {
				a.finish(a.ctx.Err())
			}
			return
		}
		if a.finished() {
			return
		}
	}
}

func (a *activeTurn) abort(ctx context.Context) error {
	a.abortMu.Lock()
	if a.aborted {
		a.abortMu.Unlock()
		return nil
	}
	a.aborted = true
	a.abortMu.Unlock()
	var response struct {
		OK      bool `json:"ok"`
		Aborted bool `json:"aborted"`
	}
	params := map[string]any{"sessionKey": a.sessionKey, "runId": a.runID}
	if a.agentID != "" {
		// 还没认领到规范化 key 时,agentId 让网关自己把 key 解析到正确的会话。
		params["agentId"] = a.agentID
	}
	err := a.client.Call(ctx, "chat.abort", params, &response)
	if err != nil {
		return err
	}
	return nil
}

func (a *activeTurn) reconcile() {
	if a.finished() {
		return
	}
	var response struct {
		RunID      string `json:"runId"`
		Status     string `json:"status"`
		Error      string `json:"error"`
		StopReason string `json:"stopReason"`
	}
	err := a.client.Call(a.ctx, "agent.wait", map[string]any{
		"runId":     a.runID,
		"timeoutMs": 0,
	}, &response)
	if err != nil {
		return
	}
	switch strings.ToLower(strings.TrimSpace(response.Status)) {
	case "ok", "completed", "done":
		a.finish(nil)
	case "error", "failed":
		message := strings.TrimSpace(response.Error)
		if message == "" {
			message = "OpenClaw run failed"
		}
		a.finish(errors.New(message))
	case "aborted", gatewayCancelledStatus:
		a.finish(agentruntime.ErrAborted)
	}
}

func (a *activeTurn) emit(event agentruntime.Event) bool {
	select {
	case a.out <- event:
		return true
	case <-a.ctx.Done():
		return false
	}
}

func (a *activeTurn) finish(stopErr error) {
	a.finishOnce.Do(func() {
		a.expirePendingApprovals()
		a.publishSessionUsage()
		a.result.StopErr = stopErr
		switch {
		case stopErr == nil:
			a.emit(agentruntime.Done{})
		case errors.Is(stopErr, agentruntime.ErrAborted):
			a.emit(agentruntime.Done{})
		default:
			a.emit(agentruntime.ErrorEvent{Err: stopErr})
		}
		a.runtime.unregister(a)
		close(a.out)
		a.client.Close()
	})
}

func (a *activeTurn) finished() bool {
	a.runtime.mu.RLock()
	active := a.runtime.active[a.sessionID]
	a.runtime.mu.RUnlock()
	return active != a
}

func eventError(prefix, message string) error {
	message = strings.TrimSpace(message)
	if message == "" {
		message = "unknown error"
	}
	return fmt.Errorf("%s: %s", prefix, message)
}

// matchesSession 判断一条帧是否属于本轮。网关把请求 key 规范化成
// agent:<agentId>:<key>,而 chat.send 的应答不回报规范化结果 —— 认领之前用后缀
// 匹配认自己的会话(请求 key 形如 agentre:<backendID>:<sessionID>,全局唯一)。
func (a *activeTurn) matchesSession(key string) bool {
	key = strings.TrimSpace(key)
	if key == "" || key == a.sessionKey {
		return true
	}
	if a.sessionKeyCanonical {
		return false
	}
	return strings.HasSuffix(key, ":"+a.sessionKey)
}

// adoptSessionKey 记下网关规范化后的会话 key,并同步到 RunResult —— chat_svc 据此
// 持久化 provider_session_id,下一轮才能复用同一个网关会话。
// 只从带 runId 的本轮事件认领:审批事件没有 runId,不作为认领依据。
func (a *activeTurn) adoptSessionKey(key string) {
	key = strings.TrimSpace(key)
	if key == "" || a.sessionKeyCanonical || key == a.sessionKey {
		return
	}
	if !strings.HasSuffix(key, ":"+a.sessionKey) {
		return
	}
	a.sessionKey = key
	a.sessionKeyCanonical = true
	a.result.ProviderSessionID = key
}

// resubscribe 在重连后补订会话消息 —— 订阅是连接级的,不补就再也收不到本轮事件。
func (a *activeTurn) resubscribe() {
	if !a.subscribeMessages {
		return
	}
	if _, err := subscribeSessionMessages(a.ctx, a.client, a.sessionKey, a.agentID); err != nil {
		logger.Ctx(a.ctx).Warn("openclaw runtime: resubscribe session messages failed",
			zap.String("sessionKey", a.sessionKey), zap.Error(err))
	}
}
