package openclaw

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"

	"github.com/cago-frame/cago/pkg/i18n"
	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"

	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
	"github.com/agentre-hub/agentre/internal/pkg/code"
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
	out                  chan agentruntime.Event
	result               *agentruntime.RunResult
	// sessionDescribe 记录网关是否广播了可选的 sessions.describe —— 收轮补 usage
	// 只在广播时才调。
	sessionDescribe bool

	// runMu 守 runID / awaitingFollowUp / yieldedRuns:consume 在让出后换当前 run,
	// Abort 从别的 goroutine 读它决定 chat.abort 打哪个 run。
	runMu sync.Mutex
	// runID 是本轮当前跟踪的 run:开轮时是提交的那个,让出后换成同会话的后续 run。
	runID string
	// awaitingFollowUp 表示当前 run 以 yielded 终态结束、后续 run 还没出现 ——
	// 本轮保持进行中,但此刻网关上没有可以指名的 run。
	awaitingFollowUp bool
	// yieldedRuns 记下已让出的 run,它们的迟到帧不能被认作后续 run。
	yieldedRuns map[string]struct{}

	finishOnce sync.Once
	abortMu    sync.Mutex
	aborted    bool
	// abortRequested 在 Abort 发完 chat.abort 后关闭:等待后续 run 期间没有终态帧
	// 会来,consume 靠它收轮。
	abortRequested chan struct{}
	// turnToken 本轮的身份 token(创建时一次性赋值,发布进 r.active 后只读,Abort 比对用)。
	turnToken uint64

	approvalMu        sync.Mutex
	approvalResolveMu sync.Mutex
	approvals         map[string]*approvalState
	initialApprovals  []gatewayExecApprovalRecord
	// pluginApprovalsSupported / systemAgentApprovalsSupported 记录网关 hello 是否
	// 广播了 plugin.approval.list / openclaw.approval.list —— 只有广播了,重连对账
	// 才去问这两个方法;老网关没有它们,盲问会把方法名当硬错误。
	pluginApprovalsSupported      bool
	systemAgentApprovalsSupported bool

	questionMu        sync.Mutex
	questionResolveMu sync.Mutex
	questions         map[string]*questionState
	// questionListSupported 记录网关 hello 是否广播了 question.list —— 只有广播了,
	// 重连对账才去问;老网关没有这个方法。
	questionListSupported bool

	lastAgentSeq int64
	lastChatSeq  int64
	assistant    string
	thinking     string
}

func (a *activeTurn) consume() {
	for _, record := range a.initialApprovals {
		a.handleApprovalRequested(record)
	}
	abortRequested := a.abortRequested
	for {
		select {
		case event := <-a.client.Events():
			// run 内的 seq 缺口只关乎这条帧所属的 run;它若刚让出,缺口已无意义,
			// 后续 run 会自己带首帧出现 —— 不能当作丢了后续 run 的帧。
			if a.handleGatewayEvent(event) {
				if _, awaiting := a.currentRun(); !awaiting {
					a.reconcile(reconcileSeqGap)
				}
			}
		case <-a.client.Gaps():
			a.reconcileApprovals()
			a.reconcileQuestions()
			a.reconcile(reconcileFramesLost)
		case hello := <-a.client.Ready():
			// 重连给的是新连接自己的 hello:老网关重连到新网关(或反过来)理论上可能
			// 发生,plugin/system-agent 对账是否可问要按这条新 hello 重新判断,不能
			// 沿用开轮时那条。
			a.applyApprovalFeatures(hello)
			a.resubscribe()
			a.reconcileApprovals()
			a.reconcileQuestions()
			a.reconcile(reconcileFramesLost)
		case <-a.client.Errors():
			// A transport error is not terminal. Client reconnects; Ready then
			// triggers agent.wait. Never resubmit the user message here.
		case <-abortRequested:
			abortRequested = nil
			if _, awaiting := a.currentRun(); awaiting {
				a.finish(agentruntime.ErrAborted)
			}
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
	defer close(a.abortRequested)
	var response struct {
		OK      bool `json:"ok"`
		Aborted bool `json:"aborted"`
	}
	params := map[string]any{"sessionKey": a.sessionKey}
	// 等待后续 run 期间没有可指名的 run:不带 runId,让网关中止该会话此刻在跑的一切
	// (包括我们还没看到首帧的后续 run)。
	if runID, awaiting := a.currentRun(); !awaiting {
		params["runId"] = runID
	}
	if a.agentID != "" {
		// 还没认领到规范化 key 时,agentId 让网关自己把 key 解析到正确的会话。
		params["agentId"] = a.agentID
	}
	return a.client.Call(ctx, "chat.abort", params, &response)
}

// currentRun 返回本轮当前跟踪的 run,以及是否正处于「已让出、后续 run 未出现」。
func (a *activeTurn) currentRun() (runID string, awaitingFollowUp bool) {
	a.runMu.Lock()
	defer a.runMu.Unlock()
	return a.runID, a.awaitingFollowUp
}

// acceptRun 判断一条已确认属于本会话的帧是否属于本轮当前的 run。让出后第一条
// 不是心跳、也不是已让出 run 的帧,就是同会话的后续 run:本轮改跟它,seq 与流式
// 快照按新 run 重新计。
func (a *activeTurn) acceptRun(runID string, heartbeat bool) bool {
	a.runMu.Lock()
	defer a.runMu.Unlock()
	if runID == a.runID {
		return true
	}
	if !a.awaitingFollowUp || runID == "" || heartbeat {
		return false
	}
	if _, yielded := a.yieldedRuns[runID]; yielded {
		return false
	}
	logger.Ctx(a.ctx).Info("openclaw.activeTurn.acceptRun: following up yielded run",
		zap.Int64("sessionId", a.sessionID), zap.String("yieldedRunId", a.runID), zap.String("runId", runID))
	a.runID = runID
	a.awaitingFollowUp = false
	a.lastAgentSeq, a.lastChatSeq = 0, 0
	a.assistant, a.thinking = "", ""
	return true
}

// yieldRun 处理当前 run 的 yielded 终态:本轮不收,等同会话的后续 run。
// lifecycle end 与 chat final 都会带 yielded,重复调用是幂等的。
func (a *activeTurn) yieldRun() {
	a.runMu.Lock()
	if !a.awaitingFollowUp {
		logger.Ctx(a.ctx).Info("openclaw.activeTurn.yieldRun: run yielded, waiting for follow-up run",
			zap.Int64("sessionId", a.sessionID), zap.String("runId", a.runID))
	}
	a.awaitingFollowUp = true
	a.yieldedRuns[a.runID] = struct{}{}
	a.runMu.Unlock()
	a.abortMu.Lock()
	aborted := a.aborted
	a.abortMu.Unlock()
	if aborted {
		// 用户已点停止,当前 run 却以让出而非中止收尾:没有别的终态会来了。
		a.finish(agentruntime.ErrAborted)
	}
}

// finishFollowUpUnconfirmed 在让出后丢了帧(断线 / 序号缺口)时收轮:后续 run
// 可能已开始甚至结束,我们无从指名去对账,只能以可读错误结束而不是无限等待。
func (a *activeTurn) finishFollowUpUnconfirmed() {
	runID, _ := a.currentRun()
	logger.Ctx(a.ctx).Warn("openclaw.activeTurn.finishFollowUpUnconfirmed: follow-up run of yielded turn cannot be confirmed",
		zap.Int64("sessionId", a.sessionID), zap.String("yieldedRunId", runID))
	a.finish(i18n.NewError(a.ctx, code.OpenClawYieldFollowUpUnconfirmed))
}

// reconcileCause 区分两种对齐场景:连接完好时某个 run 的 seq 有缺口,和连接层
// 真的丢了帧(断线重连 / 网关丢弃)。让出后的判断完全取决于这个区别。
type reconcileCause int

const (
	// reconcileSeqGap:连接完好。真实网关按 run 编号事件却只投递其中一部分,
	// 同一个 run 的 seq 天然带缺口,对齐只是补上这段里可能漏掉的终态。
	reconcileSeqGap reconcileCause = iota
	// reconcileFramesLost:断线重连或网关丢帧 —— 这段窗口里发生了什么无从得知。
	reconcileFramesLost
)

func (a *activeTurn) reconcile(cause reconcileCause) {
	if a.finished() {
		return
	}
	runID, awaiting := a.currentRun()
	if awaiting {
		// 连接完好时「等后续 run」只是说此刻没有可指名的 run,不是出了问题:
		// 后续 run 会自己带首帧出现。
		if cause == reconcileSeqGap {
			return
		}
		a.finishFollowUpUnconfirmed()
		return
	}
	var response struct {
		RunID      string `json:"runId"`
		Status     string `json:"status"`
		Error      string `json:"error"`
		StopReason string `json:"stopReason"`
		Yielded    bool   `json:"yielded"`
	}
	err := a.client.Call(a.ctx, "agent.wait", map[string]any{
		"runId":     runID,
		"timeoutMs": 0,
	}, &response)
	if err != nil {
		return
	}
	switch strings.ToLower(strings.TrimSpace(response.Status)) {
	case "ok", "completed", "done":
		if response.Yielded {
			a.yieldRun()
			// 连接完好时这只是轮询撞上了让出:让出帧还在收件箱里排队,后续 run
			// 也照常推流,本轮继续等它。断线期间的让出才是真的可能错过了帧。
			if cause == reconcileFramesLost && !a.finished() {
				a.finishFollowUpUnconfirmed()
			}
			return
		}
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
		a.expirePendingQuestions()
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

// applyApprovalFeatures 按(重)连时那条 hello 更新 plugin/system-agent 审批与提问对账是否可问。
func (a *activeTurn) applyApprovalFeatures(hello openclawgateway.Hello) {
	a.pluginApprovalsSupported = slices.Contains(hello.Features.Methods, pluginApprovalListMethod)
	a.systemAgentApprovalsSupported = slices.Contains(hello.Features.Methods, systemAgentApprovalListMethod)
	a.questionListSupported = slices.Contains(hello.Features.Methods, questionListMethod)
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
