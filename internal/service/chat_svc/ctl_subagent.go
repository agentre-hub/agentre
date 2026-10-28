package chat_svc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"sync"
	"time"

	cagoblocks "github.com/cago-frame/agents/agent/blocks"
	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"

	"github.com/agentre-hub/agentre/internal/model/entity/chat_entity"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
	"github.com/agentre-hub/agentre/internal/pkg/transcript/blocks"
	"github.com/agentre-hub/agentre/internal/pkg/transcript/ctlbind"
	"github.com/agentre-hub/agentre/internal/pkg/transcript/handlers"
	"github.com/agentre-hub/agentre/internal/pkg/transcript/turn"
	"github.com/agentre-hub/agentre/internal/repository/chat_repo"
	"github.com/agentre-hub/agentre/internal/repository/transcript_repo"
	"github.com/agentre-hub/agentre/internal/service/exec_target_svc"
)

// ctl_subagent.go 把 `agrctl send --wait` 的子会话挂到父转录里(spec 2026-10-27
// 「agrctl send 子代理卡」的「Binding」「Card lifecycle」):父 agent 跑 send 的那条外层
// shell 调用原地升级成子代理卡,子会话的工具调用 / 结果实时镜像成卡片的嵌套步骤,进度、
// 模型、用量跟着子会话走,终态在 serveSend 写响应(即 shell 调用拿到结果)之前落定。
//
// 本机父轮:前台与 Claude 后台(run_in_background)调用都绑定;父轮先结束、子任务还在跑时
// (后台 Bash、Codex 的命令在父轮结束后仍在运行),卡片在原消息里继续更新并由子任务的真实结果
// 收尾(ctlSendBinding.writeBack)。远端父轮(父会话在 agentred 上执行):父转录归 agentred,
// 绑定由它完成并随请求附来,桌面端只把子会话事件经注入 RPC 交给它(ctlSendRemote)。
//
// 写入走 transcript 的 agrctl overlay 约定(handlers/subagent.go):每一帧都带
// Info.Origin = SubagentOriginAgrctl,只落在本轮累加器里已有的 overlay 上。

// CtlSendStatus 是子代理卡的终态,取值即 SubagentStateBlock.Status 的词表。
type CtlSendStatus string

const (
	CtlSendCompleted CtlSendStatus = "completed"
	CtlSendFailed    CtlSendStatus = "failed"
	CtlSendCanceled  CtlSendStatus = "canceled"
)

// CtlSendBindRequest 是绑定一次 send 需要的调用方与目标。
type CtlSendBindRequest struct {
	// CallerSessionID 是会话级 token 绑定的那个会话,即发起 send 的父会话。
	CallerSessionID int64
	// AgentName 是解析出的目标 agent 名:卡片名;按名字派发时也是匹配依据。
	AgentName string
	// AgentID 是 send 请求里的 --agent-id(按名字派发时为 0):按 id 派发时的匹配依据,
	// 命令文本里出现的是这个 id 而不是名字。
	AgentID int64
	// Task 是任务文本:卡片描述,也用于多条候选时消歧。
	Task string
	// Isolated 子会话是 --isolated 一次性会话(不在侧栏里)。
	Isolated bool
	// HostToolCallID / HostBackground 是父轮的宿主(agentred)在它的父转录里替这次 send
	// 绑定的那条 shell 调用(spec「Remote sessions」第 1 条)。只在父会话于 agentred 上执行时
	// 采信:本机父轮由桌面端自己匹配,忽略它们。
	HostToolCallID string
	HostBackground bool
}

// CtlSendOutcome 是子轮的结果。
type CtlSendOutcome struct {
	Status CtlSendStatus
	// FinalText 成功 / 取消时是子轮的最终文本(父 agent 从 stdout 拿到的那段),失败时是原因。
	FinalText string
	// AssistantMessageID 是子轮最终那条 assistant 消息;实时那一路没拿到模型时(CLI 登录态
	// 起轮时模型未知)从这条消息补。0 = 没有。
	AssistantMessageID int64
}

// CtlSendWatch 是一次 send --wait 的子会话登记。用法:Send 之前 WatchCtlSend,起轮成功后
// Bind,拿到结果后先 Finish 再写响应,最后 Close。不绑定之后 Finish 是空操作。
type CtlSendWatch interface {
	// Bind 在调用方会话当前这一轮里找发出这次 send 的 shell 调用并把它升级成子代理卡,
	// 交回此刻是否已经绑定。本机进行中的父轮里还没有候选时(send 早于它的 shell 调用登记)
	// 绑定保持待定:父轮之后每登记一个外层调用就重新匹配,有界等待到期、父轮收口或 Finish
	// 时放弃。不能确定时不绑定(记 info 日志说明原因)。Bind 不等那条调用登记。
	Bind(ctx context.Context, req CtlSendBindRequest) bool
	// Finish 把子轮结果落成卡片终态。返回时终态已落定(累加器 + 落库 + 实时事件);
	// 绑定仍待定时就此放弃。
	Finish(ctx context.Context, out CtlSendOutcome)
	// Close 注销登记;已绑定却没 Finish 的卡片收成已取消,不让它永远停在运行中。
	Close()
}

// CtlSendGateway 是 ctl_svc 对子代理卡的窄依赖在本服务这一侧的方法集。
type CtlSendGateway interface {
	WatchCtlSend(childSessionID int64) CtlSendWatch
}

// CtlSendSvcGateway 生产用子代理卡端口(供 ctl_svc 接线)。须在 RegisterChat 之后取。
func CtlSendSvcGateway() CtlSendGateway {
	gw, _ := Chat().(CtlSendGateway)
	return gw
}

// WatchCtlSend 登记子会话:此后它的工具调用 / 结果 / 用量先攒在登记里,绑定后补投。
func (s *chatSvc) WatchCtlSend(childSessionID int64) CtlSendWatch {
	w := &ctlSendWatch{
		svc: s, child: childSessionID,
		remote:     ctlSendRemotePort{resolve: s.ctlSendInjector, backoff: ctlSendRetryBackoff, window: ctlSendRetryWindow},
		bindWindow: ctlSendBindWindow,
		wake:       make(chan struct{}, 1), quit: make(chan struct{}), exited: make(chan struct{}),
	}
	s.ctlSendWatches.Store(childSessionID, w)
	return w
}

// mirrorCtlSendChild 是子会话事件循环上的接入点(turnRun.applyLive):有登记就把事件交给
// 它。只做入队,**从不**等父轮的锁 —— 子会话的事件循环不能被父轮拖住。model 是子轮此刻
// 已知的模型(空 = 还不知道)。
func (s *chatSvc) mirrorCtlSendChild(childSessionID int64, model string, ev agentruntime.Event) {
	v, ok := s.ctlSendWatches.Load(childSessionID)
	if !ok {
		return
	}
	v.(*ctlSendWatch).enqueue(model, ev)
}

// ctlSendWatch 一次 send 的登记。队列由子会话事件循环写、由镜像 goroutine(或 Finish)读,
// 只在 mu 下碰;绑定后的写入交给 binding:本机父轮在父轮的 t.mu 下写(ctlSendBinding),远端
// 父轮经注入 RPC 推给 agentred(ctlSendRemote)。绑定待定期间(pending)事件照样攒着。
//
// 锁序:父轮的 t.mu 在前,mu 在后(待定绑定在 t.mu 下解析、再挂上登记)。
type ctlSendWatch struct {
	svc   *chatSvc
	child int64

	// remote 是远端父会话的注入端口(父会话在 agentred 上执行时用)。
	remote ctlSendRemotePort
	// bindWindow 父轮里还没有候选时绑定保持待定的时长。
	bindWindow time.Duration

	mu       sync.Mutex
	queue    []agentruntime.Event
	model    string
	binding  ctlSendSink
	pending  *ctlSendPendingBind // 挂在父轮上、还没解析的绑定
	finished bool

	wake   chan struct{}
	quit   chan struct{}
	exited chan struct{}
}

// mirroredChildEvent 子会话里卡片用得上的事件:工具调用 / 结果(嵌套步骤 + 工具数)与
// 用量。其余(正文增量等)不入队,队列不随子轮输出量涨。
func mirroredChildEvent(ev agentruntime.Event) bool {
	switch ev.(type) {
	case agentruntime.ToolCall, agentruntime.ToolResult, agentruntime.UsageUpdate:
		return true
	}
	return false
}

func (w *ctlSendWatch) enqueue(model string, ev agentruntime.Event) {
	w.mu.Lock()
	if w.finished {
		w.mu.Unlock()
		return
	}
	changed := model != "" && model != w.model
	if changed {
		w.model = model
	}
	keep := mirroredChildEvent(ev)
	if keep {
		w.queue = append(w.queue, ev)
	}
	w.mu.Unlock()
	if changed || keep {
		w.signal()
	}
}

func (w *ctlSendWatch) signal() {
	select {
	case w.wake <- struct{}{}:
	default:
	}
}

func (w *ctlSendWatch) Bind(ctx context.Context, req CtlSendBindRequest) bool {
	w.mu.Lock()
	finished := w.finished
	w.mu.Unlock()
	if finished {
		return false
	}
	switch w.svc.bindCtlSend(ctx, w, req) {
	case ctlSendBound:
		return true
	case ctlSendPending:
		return false
	}
	w.release()
	return false
}

// attach 挂上已绑定的卡并起镜像 goroutine。待定绑定在父轮的 t.mu 下解析时也走这里。
func (w *ctlSendWatch) attach(ctx context.Context, b ctlSendSink) {
	w.mu.Lock()
	w.binding, w.pending = b, nil
	w.mu.Unlock()
	go w.run(context.WithoutCancel(ctx))
	w.signal() // 绑定之前攒下的那些
}

// release 不绑定:没有谁会再搬这些事件,放掉攒下的,此后子会话的事件也不再入队
// (子轮可能还要跑很久,工具结果不能一直攥到 send 返回)。幂等。
func (w *ctlSendWatch) release() {
	w.mu.Lock()
	w.finished, w.queue, w.pending = true, nil, nil
	w.mu.Unlock()
}

// withdraw 收回仍挂在父轮上的待定绑定(Finish 时)。在父轮的 t.mu 下判:此前已被解析
// (绑定或放弃)就什么都不做,此后它不会再被解析 —— Finish 读 binding 时看到的是定局。
func (w *ctlSendWatch) withdraw(ctx context.Context) {
	w.mu.Lock()
	p := w.pending
	w.mu.Unlock()
	if p != nil {
		p.turn.giveUpCtlSend(ctx, p, ctlSendGaveUpFinished)
	}
}

// run 是镜像 goroutine:有新事件就整批搬进父转录,直到 Finish 叫停。
func (w *ctlSendWatch) run(ctx context.Context) {
	defer close(w.exited)
	for {
		select {
		case <-w.quit:
			return
		case <-w.wake:
			w.drain(ctx)
		}
	}
}

func (w *ctlSendWatch) drain(ctx context.Context) {
	w.mu.Lock()
	batch, model, b := w.queue, w.model, w.binding
	w.queue = nil
	w.mu.Unlock()
	b.mirror(ctx, batch, model)
}

func (w *ctlSendWatch) Finish(ctx context.Context, out CtlSendOutcome) {
	w.withdraw(ctx)
	w.mu.Lock()
	if w.finished {
		w.mu.Unlock()
		return
	}
	w.finished = true // 此后子会话的事件不再入队
	b := w.binding
	w.mu.Unlock()
	if b == nil {
		return
	}
	close(w.quit)
	<-w.exited
	// 镜像 goroutine 已停:剩下的就地搬完,再收尾 —— 步骤不会落在终态之后。
	w.drain(ctx)
	b.finish(ctx, w.child, out)
}

func (w *ctlSendWatch) Close() {
	w.Finish(context.Background(), CtlSendOutcome{Status: CtlSendCanceled})
	w.svc.ctlSendWatches.CompareAndDelete(w.child, w)
}

// ctlSendSink 一张已绑定的卡的写入端。两个方法只由镜像 goroutine 与 Finish 调用,两者经
// ctlSendWatch 的 quit/exited 先后排开,实现不必自己再加锁。
type ctlSendSink interface {
	// mirror 把一批子会话事件(与子轮此刻已知的模型)落到卡上。
	mirror(ctx context.Context, batch []agentruntime.Event, model string)
	// finish 落终态;返回时终态已落定(远端断线时已交给重试,见 ctlSendRemote.finish)。
	finish(ctx context.Context, child int64, out CtlSendOutcome)
}

// ctlSubagentInjector 把子会话事件交给父会话所在的 agentred(*remote.Runtime 实现)。
type ctlSubagentInjector interface {
	InjectSubagentEvents(ctx context.Context, sessionID int64, toolCallID string, events []agentruntime.Event) error
}

// ctlSendRemotePort 远端父会话的注入端口。
type ctlSendRemotePort struct {
	resolve func(ctx context.Context, sessionID int64) (ctlSubagentInjector, error)
	backoff time.Duration
	window  time.Duration
}

// 不绑定的原因(ctlbind.Reason 之外由本服务判出的那几种)。
const (
	ctlSendNoActiveTurn = "no_active_turn"
	// 父轮在 agentred 上执行,但 agentred 没有附来它绑定的 shell 调用(它判定不绑)。
	ctlSendNoHostBinding = "no_host_binding"
	// 带着宿主绑定,父会话却不在 agentred 上执行(本机没有在跑的轮)。
	ctlSendParentNotRemote = "parent_not_remote"
	// 带着宿主绑定,此刻却连不上父会话所在的 agentred:无从核实,不绑。
	ctlSendParentUnreachable = "parent_unreachable"
)

// ctlSendBindState 是 bindCtlSend 的结果。
type ctlSendBindState int

const (
	ctlSendNotBound ctlSendBindState = iota
	ctlSendBound
	ctlSendPending
)

// ctlSendBindWindow 本机父轮里还没有候选时,绑定待定的最长时间。Codex 的 command_execution
// 在命令跑起来、send 已经到达之后才开始(运行时实测相差几十毫秒),留足余量;到期仍没有
// 就保持普通 shell 行。
const ctlSendBindWindow = 10 * time.Second

// 待定绑定被放弃的时机(日志字段 pendingEnded)。ctlSendDecidedNow 表示不是放弃等待,
// 而是一匹配就判定不绑(歧义 / 已绑过 / 没有在跑的轮)。
const (
	ctlSendDecidedNow       = ""
	ctlSendGaveUpDeadline   = "deadline"
	ctlSendGaveUpTurnClosed = "parent_turn_closed"
	ctlSendGaveUpFinished   = "send_finished"
)

// bindCtlSend 在调用方会话当前这一轮(本机、进行中)里匹配 shell 调用并升级成 overlay。
// 匹配与升级都在父轮的 t.mu 下:与事件循环、工具审批互斥,匹配时看到的开放调用不会
// 在升级前变化。此刻还没有候选时绑定挂在父轮上待定(turnRun.resolveCtlSends)。
//
// 后台(run_in_background)调用同样绑定:它的结果会立刻返回,与 send 请求谁先到是竞争的
// (Design 9)。后台卡保留 local_bash(binding.Kind),后台面板、停止按钮与轮结束保护照常;
// 它的状态归 CLI,注入方只写子会话带来的内容(handlers.ApplyAgrctlInfo)。
//
// 父会话在 agentred 上执行时(本机那一轮只是持久帧的回放,或本机根本没有那一轮)不在本地
// 匹配,改走 bindCtlSendRemote。
func (s *chatSvc) bindCtlSend(ctx context.Context, w *ctlSendWatch, req CtlSendBindRequest) ctlSendBindState {
	t := s.activeTurn(req.CallerSessionID)
	if t == nil || t.previews != nil {
		if t == nil && req.HostToolCallID == "" {
			logCtlSendNotBound(ctx, req, w.child, ctlSendNoActiveTurn, ctlSendDecidedNow)
			return ctlSendNotBound
		}
		r, reason := s.bindCtlSendRemote(ctx, w.child, req, w.remote)
		if reason != "" {
			logCtlSendNotBound(ctx, req, w.child, reason, ctlSendDecidedNow)
			return ctlSendNotBound
		}
		w.attach(ctx, r)
		return ctlSendBound
	}
	return t.bindCtlSend(ctx, w, req)
}

func logCtlSendNotBound(ctx context.Context, req CtlSendBindRequest, child int64, reason, pendingEnded string) {
	fields := []zap.Field{
		zap.String("reason", reason),
		zap.Int64("callerSessionId", req.CallerSessionID),
		zap.Int64("childSessionId", child),
	}
	if pendingEnded != "" {
		fields = append(fields, zap.String("pendingEnded", pendingEnded))
	}
	logger.Ctx(ctx).Info("chat_svc.bindCtlSend: subagent card not bound", fields...)
}

// ctlSendPendingBind 一次待定的绑定:send 已经起轮,发出它的 shell 调用却还没登记进父轮。
// 挂在父轮上(turnRun.ctlSends),由父轮的 t.mu 守。
type ctlSendPendingBind struct {
	turn  *turnRun
	watch *ctlSendWatch
	ctx   context.Context // 绑定请求的上下文(已去掉取消):解析时的日志与镜像 goroutine 用
	req   CtlSendBindRequest
	timer *time.Timer
	state ctlSendBindState // 解析结果;ctlSendPending = 还挂着
	// waited 到达时那一次匹配之后仍在等:此后只认命令含它任务文本的调用(ctlbind.MatchPending),
	// 先登记进来的可能是另一个发往同一 agent 的 send 的调用。
	waited bool
}

// match 到达时那一次照 Binding 规则匹配;等过之后用严格版。
func (p *ctlSendPendingBind) match(calls []turn.OuterToolUse, bound map[string]bool) (ctlbind.Binding, ctlbind.Reason) {
	if p.waited {
		return ctlbind.MatchPending(calls, bound, p.target())
	}
	return ctlbind.Match(calls, bound, p.target())
}

func (p *ctlSendPendingBind) target() ctlbind.Target {
	target := ctlbind.Target{Task: p.req.Task}
	if p.req.AgentID > 0 {
		target.AgentID = p.req.AgentID
	} else {
		target.AgentName = p.req.AgentName
	}
	return target
}

// bindCtlSend 本机父轮这一侧:把这次 send 当作待定绑定挂上并立刻解析一次;仍没有候选就
// 留着等父轮登记新的外层调用,同时起有界等待。
func (t *turnRun) bindCtlSend(ctx context.Context, w *ctlSendWatch, req CtlSendBindRequest) ctlSendBindState {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.transcriptClosed { // 这一轮已经收口(closeApprovals 之后转录归 finalize)
		logCtlSendNotBound(ctx, req, w.child, ctlSendNoActiveTurn, ctlSendDecidedNow)
		return ctlSendNotBound
	}
	p := &ctlSendPendingBind{turn: t, watch: w, ctx: context.WithoutCancel(ctx), req: req, state: ctlSendPending}
	t.ctlSends = append(t.ctlSends, p)
	t.resolveCtlSends(ctx)
	if p.state != ctlSendPending {
		return p.state
	}
	p.waited = true
	w.mu.Lock()
	w.pending = p
	w.mu.Unlock()
	p.timer = time.AfterFunc(w.bindWindow, func() { t.giveUpCtlSend(p.ctx, p, ctlSendGaveUpDeadline) })
	logger.Ctx(ctx).Info("chat_svc.bindCtlSend: shell call not in the parent turn yet; binding stays pending",
		zap.Int64("callerSessionId", req.CallerSessionID),
		zap.Int64("childSessionId", w.child),
		zap.Duration("window", w.bindWindow))
	return ctlSendPending
}

// resolveCtlSends 用父轮此刻的外层调用重新匹配每个待定绑定。调用方持有 t.mu;事件循环每
// 登记一个外层调用(applyLive)以及每次新绑定都会调。
//
// 仍没有候选的继续挂着;歧义 / 已绑过照常判不绑。等过的只认命令含自己任务文本的调用
// (ctlSendPendingBind.match)。所有待定绑定对着同一份快照匹配,两个落到同一条调用上的
// 都不绑(分不清那条调用是谁发的)—— 宁可不升级,也不挂错卡。
func (t *turnRun) resolveCtlSends(ctx context.Context) {
	if len(t.ctlSends) == 0 {
		return
	}
	calls := t.acc.OuterToolUses()
	bound := ctlbind.BoundToolCallIDs(t.acc.Snapshot())
	type match struct {
		binding ctlbind.Binding
		reason  ctlbind.Reason
	}
	matches := make([]match, len(t.ctlSends))
	claims := map[string]int{}
	for i, p := range t.ctlSends {
		b, r := p.match(calls, bound)
		matches[i] = match{b, r}
		if r == "" {
			claims[b.ToolCallID]++
		}
	}
	waiting := t.ctlSends[:0:0]
	for i, p := range t.ctlSends {
		m := matches[i]
		switch {
		case m.reason == ctlbind.ReasonNoCandidate:
			waiting = append(waiting, p)
		case m.reason != "":
			t.dropCtlSend(ctx, p, string(m.reason), ctlSendDecidedNow)
		case claims[m.binding.ToolCallID] > 1:
			t.dropCtlSend(ctx, p, string(ctlbind.ReasonAmbiguous), ctlSendDecidedNow)
		default:
			t.upgradeCtlSend(ctx, p, m.binding)
		}
	}
	t.ctlSends = waiting
}

// upgradeCtlSend 把匹配上的 shell 调用升级成 overlay 并把卡交给登记。调用方持有 t.mu。
func (t *turnRun) upgradeCtlSend(ctx context.Context, p *ctlSendPendingBind, binding ctlbind.Binding) {
	p.stop(ctlSendBound)
	child := p.watch.child
	b := &ctlSendBinding{
		turn: t, acc: t.acc, msg: t.assistantMsg, sessionID: t.sess.ID, toolCallID: binding.ToolCallID,
		startedAt:       time.Now(),
		ctlSendProgress: newCtlSendProgress(),
	}
	b.apply(ctx, ctlSendStarted(binding, child, p.req))
	t.svc.checkpointAssistantNew(ctx, b.msg, b.acc)
	logger.Ctx(ctx).Info("chat_svc.bindCtlSend: subagent card bound",
		zap.Int64("callerSessionId", p.req.CallerSessionID),
		zap.Int64("childSessionId", child),
		zap.String("toolCallId", b.toolCallID),
		zap.String("kind", binding.Kind()))
	p.watch.attach(p.ctx, b)
}

// dropCtlSend 判定不绑并放掉登记攒下的事件。调用方持有 t.mu,负责把 p 移出 t.ctlSends。
func (t *turnRun) dropCtlSend(ctx context.Context, p *ctlSendPendingBind, reason, pendingEnded string) {
	p.stop(ctlSendNotBound)
	logCtlSendNotBound(ctx, p.req, p.watch.child, reason, pendingEnded)
	p.watch.release()
}

// giveUpCtlSend 放弃仍在等待的绑定(有界等待到期 / Finish);已解析过的不动。
func (t *turnRun) giveUpCtlSend(ctx context.Context, p *ctlSendPendingBind, why string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if p.state != ctlSendPending {
		return
	}
	for i, q := range t.ctlSends {
		if q == p {
			t.ctlSends = append(t.ctlSends[:i:i], t.ctlSends[i+1:]...)
			break
		}
	}
	t.dropCtlSend(ctx, p, string(ctlbind.ReasonNoCandidate), why)
}

// abandonCtlSends 父轮收口:不会再有新的外层调用登记,还在等的绑定一律放弃。
func (t *turnRun) abandonCtlSends(ctx context.Context) {
	t.mu.Lock()
	defer t.mu.Unlock()
	for _, p := range t.ctlSends {
		t.dropCtlSend(ctx, p, string(ctlbind.ReasonNoCandidate), ctlSendGaveUpTurnClosed)
	}
	t.ctlSends = nil
}

// stop 记下解析结果并停掉有界等待。调用方持有 t.mu。
func (p *ctlSendPendingBind) stop(state ctlSendBindState) {
	p.state = state
	if p.timer != nil {
		p.timer.Stop()
	}
}

// ctlSendStarted 是升级 overlay 的那一帧:绑定时就确定的归属、子会话、isolated、目标 agent
// 与任务文本。本机与远端父轮发的是同一帧;卡片的单运行快照(重载时的卡片名)由 handler 层
// 据此写下(handlers.bindAgrctl)。
func ctlSendStarted(binding ctlbind.Binding, child int64, req CtlSendBindRequest) agentruntime.SubagentStarted {
	return agentruntime.SubagentStarted{ToolCallID: binding.ToolCallID, Info: agentruntime.SubagentInfo{
		Kind:            binding.Kind(),
		Origin:          agentruntime.SubagentOriginAgrctl,
		ChildSessionID:  child,
		Isolated:        req.Isolated,
		SubagentType:    req.AgentName,
		TaskDescription: req.Task,
		Prompt:          req.Task,
		Status:          "running",
	}}
}

// ctlSendBinding 一张已升级的卡:它落在哪一轮、哪只累加器、哪条消息上(插话分段会换掉
// turnRun 的当前消息,卡却留在它发生的那一条里),以及注入方自己累计的进度。
//
// 卡片的写入有两段:
//   - 父轮开着:在父轮的 t.mu 下写进卡所在的累加器并 checkpoint(与事件循环、工具审批互斥)。
//   - 父轮收口之后(后台 Bash;Codex 的命令在父轮结束后仍在跑;父轮被中止):等收尾把整条
//     消息落库(交接,turnRun.persistedSignal),此后只经仓储定向改写原消息里的这张卡与它的
//     子块(writeBack),与 CLI 自己的跨消息写入同一把会话锁。
//
// 绑定自己的字段(进度、模型、去重表、detached)只由镜像 goroutine 与 Finish 读写,两者经
// ctlSendWatch 的 quit/exited 先后排开,不需要 t.mu;acc / msg 的内容只在 t.mu 下碰。
type ctlSendBinding struct {
	turn       *turnRun
	acc        *turn.Accumulator
	msg        *chat_entity.Message
	sessionID  int64
	toolCallID string
	startedAt  time.Time

	// detached 父轮已收口且收尾已落库:此后只经仓储写原消息。
	detached bool

	ctlSendProgress
}

// ctlSendProgress 注入方自己累计的子会话进度与嵌套步骤去重表(本机与远端父轮共用)。
type ctlSendProgress struct {
	seen         map[string]bool // 已计数的子会话外层工具调用 ID
	delivered    map[string]bool // 已投递的嵌套步骤:块类型 + 嵌套 ID(跨批去重)
	toolUses     int
	lastToolName string
	inputTokens  int // 子轮最近一次调用的输入规模
	outputTokens int // 子轮累计输出
	model        string
}

func newCtlSendProgress() ctlSendProgress {
	return ctlSendProgress{seen: map[string]bool{}, delivered: map[string]bool{}}
}

// steps 把一批子会话事件翻成卡上的帧:嵌套步骤(按 ID 去重)与进度(变了才发)。
func (p *ctlSendProgress) steps(toolCallID string, batch []agentruntime.Event) []agentruntime.Event {
	var events []agentruntime.Event
	progressed := false
	for _, ev := range batch {
		if nested, ok := ctlbind.NestChildEvent(toolCallID, ev); ok && p.firstDelivery(nested) {
			events = append(events, nested)
		}
		progressed = p.track(ev) || progressed
	}
	if progressed {
		events = append(events, agentruntime.SubagentProgress{ToolCallID: toolCallID, Info: agentruntime.SubagentInfo{
			Origin:       agentruntime.SubagentOriginAgrctl,
			ToolUses:     p.toolUses,
			LastToolName: p.lastToolName,
			TotalTokens:  p.inputTokens + p.outputTokens,
		}})
	}
	return events
}

// done 是收尾那一帧:子轮最终文本、工具数、用量与耗时。
func (p *ctlSendProgress) done(toolCallID string, out CtlSendOutcome, startedAt time.Time) agentruntime.SubagentDone {
	return agentruntime.SubagentDone{ToolCallID: toolCallID, Info: agentruntime.SubagentInfo{
		Origin:      agentruntime.SubagentOriginAgrctl,
		Status:      string(out.Status),
		FinalText:   out.FinalText,
		ToolUses:    p.toolUses,
		TotalTokens: p.inputTokens + p.outputTokens,
		DurationMs:  int(time.Since(startedAt).Milliseconds()),
	}}
}

// apply 把一帧经 dispatcher 落进卡所在的累加器(带实时事件),并照 applyLive 的口径扇出给
// 对端。调用方持有 turn.mu。
func (b *ctlSendBinding) apply(ctx context.Context, ev agentruntime.Event) {
	t := b.turn
	t.svc.publishPeerEvent(b.sessionID, ev)
	if err := t.svc.dispatcher.Apply(ctx, ev, b.acc, t.dispEmit, t.turnCtx); err != nil {
		logger.Ctx(ctx).Warn("chat_svc.ctlSendBinding.apply: dispatcher Apply failed",
			zap.Int64("sessionId", b.sessionID),
			zap.String("eventType", fmt.Sprintf("%T", ev)),
			zap.Error(err))
	}
}

// mirror 把一批子会话事件搬进父转录:模型、嵌套步骤(按 ID 去重)、进度。
func (b *ctlSendBinding) mirror(ctx context.Context, batch []agentruntime.Event, model string) {
	var events []agentruntime.Event
	if m := b.takeModel(model); m != "" {
		events = append(events, agentruntime.SubagentModel{ToolCallID: b.toolCallID, Model: m})
	}
	events = append(events, b.steps(b.toolCallID, batch)...)
	if len(events) > 0 {
		b.write(ctx, events, false)
	}
}

// firstDelivery 嵌套步骤是否头一次投递。父轮那一段 handler 自己也按 ID 去重,但写回那一段
// 每批用一只新累加器,跨批、跨两段的重复只能在这里认出来。
func (p *ctlSendProgress) firstDelivery(nested agentruntime.Event) bool {
	key := nestedStepKey(nested)
	if key == "" {
		return true
	}
	if p.delivered[key] {
		return false
	}
	p.delivered[key] = true
	return true
}

// nestedStepKey 嵌套步骤的去重键:块类型 + 嵌套 ID;不是嵌套调用 / 结果时为空。
func nestedStepKey(ev agentruntime.Event) string {
	switch e := ev.(type) {
	case agentruntime.ToolCall:
		return "use:" + e.ID
	case agentruntime.ToolResult:
		return "result:" + e.ToolCallID
	}
	return ""
}

// track 累计进度,交回这一帧是否改变了进度。子会话外层工具调用按 ID 计一次(同一 ID 的
// 增量更新不重复计数);用量 = 最近一次调用的输入规模 + 累计输出。
func (p *ctlSendProgress) track(ev agentruntime.Event) bool {
	switch e := ev.(type) {
	case agentruntime.ToolCall:
		if e.ParentToolCallID != "" || p.seen[e.ID] {
			return false
		}
		p.seen[e.ID] = true
		p.toolUses++
		p.lastToolName = e.Name
		return true
	case agentruntime.UsageUpdate:
		if e.Usage == nil {
			return false
		}
		p.outputTokens += e.Usage.CompletionTokens
		if e.TotalInputTokens > 0 {
			p.inputTokens = e.TotalInputTokens
		}
		return true
	}
	return false
}

// takeModel 模型一经记录不再改写(与 SubagentModelHandler 的 first-wins 一致);交回这次
// 要写的模型,不写时为空。
func (p *ctlSendProgress) takeModel(model string) string {
	if model == "" || p.model != "" {
		return ""
	}
	p.model = model
	return model
}

// finalModel 子轮没报过模型时,收尾取它最终那条消息上的模型;已报过或取不到时为空。
// 仓储 IO,不在父轮的锁里做。
func (p *ctlSendProgress) finalModel(ctx context.Context, out CtlSendOutcome) string {
	if p.model != "" || out.AssistantMessageID <= 0 {
		return ""
	}
	msg, err := transcript_repo.Message().Find(ctx, out.AssistantMessageID)
	if err != nil || msg == nil {
		return ""
	}
	return msg.Model
}

// finish 落终态:SubagentDone 带子轮最终文本、工具数、用量与耗时。
func (b *ctlSendBinding) finish(ctx context.Context, child int64, out CtlSendOutcome) {
	var events []agentruntime.Event
	if m := b.takeModel(b.finalModel(ctx, out)); m != "" {
		events = append(events, agentruntime.SubagentModel{ToolCallID: b.toolCallID, Model: m})
	}
	events = append(events, b.done(b.toolCallID, out, b.startedAt))
	b.write(ctx, events, true)
	logger.Ctx(ctx).Info("chat_svc.ctlSendBinding.finish: subagent card settled",
		zap.Int64("sessionId", b.sessionID),
		zap.Int64("childSessionId", child),
		zap.String("toolCallId", b.toolCallID),
		zap.String("status", string(out.Status)),
		zap.Bool("afterParentTurn", b.detached))
}

// write 把一组帧落到卡上:父轮还开着就写进它的累加器,否则写回原消息。settle 表示这是
// 收尾那一发(无论有没有嵌套步骤都 checkpoint)。
func (b *ctlSendBinding) write(ctx context.Context, events []agentruntime.Event, settle bool) {
	if b.underTurn(ctx, func() { b.applyLive(ctx, events, settle) }) {
		return
	}
	b.writeBack(ctx, events)
}

// underTurn 父轮仍开着时在 t.mu 下跑 fn 并交回 true。父轮已收口时先等收尾把整条消息
// 落库(交接,同审批卡归 finalize 的那一刻)再交回 false,此后这张卡只经仓储写原消息 ——
// 收口到落库之间写进库里的块会被收尾那一发整条覆盖掉。只在镜像 goroutine / Finish 上
// 等,不持任何锁。
func (b *ctlSendBinding) underTurn(ctx context.Context, fn func()) bool {
	if b.detached {
		return false
	}
	t := b.turn
	t.mu.Lock()
	if !t.transcriptClosed {
		defer t.mu.Unlock()
		fn()
		return true
	}
	t.mu.Unlock()
	<-t.persistedSignal()
	b.detached = true
	logger.Ctx(ctx).Info("chat_svc.ctlSendBinding: parent turn ended; card continues in the original message",
		zap.Int64("sessionId", b.sessionID),
		zap.Int64("messageId", b.msg.ID),
		zap.String("toolCallId", b.toolCallID))
	return false
}

// applyLive 父轮那一段:经 dispatcher 写进卡所在的累加器,按需 checkpoint。调用方持有
// turn.mu。
func (b *ctlSendBinding) applyLive(ctx context.Context, events []agentruntime.Event, settle bool) {
	checkpoint := settle
	for _, ev := range events {
		b.apply(ctx, ev)
		checkpoint = checkpoint || shouldCheckpointAssistantAfterEvent(ev)
	}
	if checkpoint {
		b.turn.svc.checkpointAssistantNew(ctx, b.msg, b.acc)
	}
}

// writeBack 父轮结束之后那一段:扇出给对端,再经 writeBackCard 写回原消息。只在镜像
// goroutine / Finish 上跑,从不碰 CLI 的读循环,也不持会话锁去等任何东西。
func (b *ctlSendBinding) writeBack(ctx context.Context, events []agentruntime.Event) {
	svc := b.turn.svc
	for _, ev := range events {
		svc.publishPeerEvent(b.sessionID, ev)
	}
	svc.writeBackCard(ctx, cardWriteBack{
		sessionID: b.sessionID, launchMsgID: b.msg.ID, toolCallID: b.toolCallID, apply: applyCardFrame,
	}, events)
}

// cardWriteBack 一张卡在它所在的原消息里的位置(发起它的那一轮早已收尾),以及卡片帧落到
// 卡上的规则:apply 交回实际生效部分的实时事件载荷,未生效为 nil。liveStream 是写回方自己
// 那一轮还开着的流:原消息恰是它的前一个分段时两者同名,这时不发活动起止 —— 那会让前端把
// 进行中的那一轮当成结束。
type cardWriteBack struct {
	sessionID   int64
	launchMsgID int64
	toolCallID  string
	liveStream  string
	apply       func(*blocks.SubagentStateBlock, agentruntime.Event) map[string]any
}

// writeBackCard 把一组帧写回卡所在的原消息,与后台 subagent 的空闲活动同一条路
// (driveSubagentActivity):经会话级旁路告诉前端「发起卡所在的消息又有活动」,嵌套步骤经
// dispatcher 产出实时事件并跨消息追加进原消息,卡片本身在仓储的会话锁下读-改-写(patch)。
// 调用方负责嵌套步骤的去重。
func (s *chatSvc) writeBackCard(ctx context.Context, w cardWriteBack, events []agentruntime.Event) {
	ctx = context.WithoutCancel(ctx) // 已经流出去的内容必须落库
	stream := StreamName(w.sessionID, w.launchMsgID)
	activity := stream != w.liveStream
	if activity {
		s.emitter.Emit(ctx, AutonomousStreamName(w.sessionID), ChatStreamEvent{
			Kind:            StreamSubagentActivityStarted,
			Stream:          stream,
			LaunchMessageID: w.launchMsgID,
			ToolCallID:      w.toolCallID,
		})
	}
	emit := &subagentActivityEmitter{inner: &dispatcherEmitter{svc: s}, sessionID: w.sessionID}
	acc := turn.New()
	tc := &turn.TurnContext{Stream: stream}
	var frames []agentruntime.Event
	for _, ev := range events {
		switch ev.(type) {
		case agentruntime.ToolCall, agentruntime.ToolResult:
			if err := s.dispatcher.Apply(ctx, ev, acc, emit, tc); err != nil {
				logger.Ctx(ctx).Warn("chat_svc.writeBackCard: dispatcher Apply failed",
					zap.Int64("sessionId", w.sessionID),
					zap.String("eventType", fmt.Sprintf("%T", ev)),
					zap.Error(err))
			}
		default:
			frames = append(frames, ev)
		}
	}
	w.appendChildren(ctx, acc)
	w.patch(ctx, emit, stream, frames)
	if !activity {
		return
	}
	s.emitter.Emit(ctx, stream, ChatStreamEvent{Kind: StreamDone})
	s.emitter.Emit(ctx, AutonomousStreamName(w.sessionID), ChatStreamEvent{
		Kind:            StreamAutonomousFinished,
		LaunchMessageID: w.launchMsgID,
	})
}

// appendChildren 把这一批的嵌套块追加进原消息(块行 + 卡片的 nested_tool_call_ids,同一事务)。
func (w cardWriteBack) appendChildren(ctx context.Context, acc *turn.Accumulator) {
	children, ids := subagentChildBlocks(acc.Finalize(), w.toolCallID)
	if len(children) == 0 {
		return
	}
	childJSON, err := encodeStoredBlocks(children)
	if err == nil {
		err = transcript_repo.Message().AppendSubagentChildren(ctx, w.sessionID, w.toolCallID, childJSON, ids)
	}
	if err != nil {
		logger.Ctx(ctx).Warn("chat_svc.writeBackCard: write back nested steps failed",
			zap.Int64("sessionId", w.sessionID),
			zap.String("toolCallId", w.toolCallID),
			zap.Int("childBlocks", len(children)),
			zap.Error(err))
	}
}

// patch 把卡片帧(模型 / 进度 / 收尾)按 w.apply 落到原消息里那张卡上。在仓储的会话锁下
// 读-改-写,所以比的是库里此刻的那一份 —— 别人刚写下的状态不会被这里手上的旧值盖掉。实际
// 生效的部分随后作为实时事件发出。
func (w cardWriteBack) patch(ctx context.Context, emit turn.Emitter, stream string, frames []agentruntime.Event) {
	if len(frames) == 0 {
		return
	}
	var payloads []map[string]any
	found, err := transcript_repo.Message().PatchSubagentState(ctx, w.sessionID, w.toolCallID,
		func(sb *blocks.SubagentStateBlock) bool {
			payloads = payloads[:0]
			for _, ev := range frames {
				if p := w.apply(sb, ev); p != nil {
					p["toolUseId"] = w.toolCallID
					payloads = append(payloads, p)
				}
			}
			return len(payloads) > 0
		})
	if err != nil || !found {
		logger.Ctx(ctx).Warn("chat_svc.writeBackCard: card not written back",
			zap.Int64("sessionId", w.sessionID),
			zap.String("toolCallId", w.toolCallID),
			zap.Bool("found", found),
			zap.Error(err))
		return
	}
	for _, p := range payloads {
		emit.Emit(ctx, stream, p)
	}
}

// applyCardFrame 本机注入方的写回规则(handlers.ApplyAgrctlInfo):后台卡的状态归 CLI;已收尾
// (含被中止标成取消)的卡不被写回运行中。交回实时事件载荷(未生效为 nil),形态与父轮那一段
// handler 发的一致。
func applyCardFrame(sb *blocks.SubagentStateBlock, ev agentruntime.Event) map[string]any {
	switch e := ev.(type) {
	case agentruntime.SubagentModel:
		if sb.Origin != agentruntime.SubagentOriginAgrctl || !handlers.RecordSubagentModel(sb, e.Model) {
			return nil
		}
		return map[string]any{"kind": string(StreamSubagentModel), "model": e.Model}
	case agentruntime.SubagentProgress:
		if _, ok, applied := handlers.ApplyAgrctlInfo(sb, e.Info, ""); ok {
			return map[string]any{"kind": string(StreamSubagentProgress), "info": applied}
		}
	case agentruntime.SubagentDone:
		if _, ok, applied := handlers.ApplyAgrctlInfo(sb, e.Info, string(CtlSendCompleted)); ok {
			return map[string]any{"kind": string(StreamSubagentDone), "info": applied}
		}
	}
	return nil
}

// ── 持久帧里落在更早消息的 agrctl 卡(远端父会话)──────────────────────────────────────
//
// 远端父会话的转录归 agentred:父轮结束后它在原消息里改写卡片(注入方的内容、CLI 的状态),
// 变化的块以新 seq 的持久帧发来。桌面端此刻的那一轮(进行中的轮、补齐轮)不拥有这张卡,
// durableCards.restore 把这些帧交到这里写回原消息。

// earlierCardOf 交回这一帧要改的那张卡的发起调用 ID:仅当它可能是 agrctl 卡、且发起调用
// 不在本轮;否则为空。SubagentDone 自带归属(Info.Origin),模型帧与嵌套步骤不带,要看库里
// 那张卡。
func earlierCardOf(acc *turn.Accumulator, ev agentruntime.Event) string {
	var id string
	switch e := ev.(type) {
	case agentruntime.SubagentDone:
		if e.Info.Origin == agentruntime.SubagentOriginAgrctl {
			id = e.ToolCallID
		}
	case agentruntime.SubagentModel:
		id = e.ToolCallID
	case agentruntime.ToolCall:
		id = e.ParentToolCallID
	case agentruntime.ToolResult:
		id = e.ParentToolCallID
	}
	if id == "" || acc.HasToolUse(id) {
		return ""
	}
	return id
}

// writeBackEarlierCard 把一帧写回 toolCallID 那张 agrctl 卡所在的原消息,交回是否已处理。
// 那里没有这张卡、或它不是 agrctl 卡时记进 notAgrctl 并交回 false,走原有路径。原消息里
// 已有的嵌套步骤不再追加(补齐会重放、改写会重发)。定位失败时这一帧丢掉并记 warn:缺一步
// 好过把它落进不拥有它的这一轮。
func (c *durableCards) writeBackEarlierCard(ctx context.Context, toolCallID string, ev agentruntime.Event) bool {
	msg, err := transcript_repo.Message().FindAssistantBySubagentToolCallID(ctx, c.sessionID, toolCallID)
	var card *heldAgrctlCard
	if err == nil {
		card, err = agrctlCardIn(msg, toolCallID)
	}
	if err != nil {
		logger.Ctx(ctx).Warn("chat_svc.writeBackEarlierCard: locate the card's message failed; frame dropped",
			zap.Int64("sessionId", c.sessionID),
			zap.String("toolCallId", toolCallID),
			zap.String("eventType", fmt.Sprintf("%T", ev)),
			zap.Error(err))
		return true
	}
	if card == nil {
		c.notAgrctl[toolCallID] = true
		return false
	}
	if key := nestedStepKey(ev); key != "" && card.steps[key] {
		return true
	}
	c.svc.writeBackCard(ctx, cardWriteBack{
		sessionID: c.sessionID, launchMsgID: msg.ID, toolCallID: toolCallID, liveStream: c.liveStream,
		apply: applyDurableCardFrame,
	}, []agentruntime.Event{ev})
	return true
}

// heldAgrctlCard 原消息里那张 agrctl 卡已有的嵌套步骤(去重键同 nestedStepKey)。
type heldAgrctlCard struct {
	steps map[string]bool
}

// agrctlCardIn 在 msg 里找 toolCallID 那张 agrctl 卡;没有这张卡或它不是 agrctl 卡时为 nil。
func agrctlCardIn(msg *chat_entity.Message, toolCallID string) (*heldAgrctlCard, error) {
	if msg == nil {
		return nil, nil
	}
	var stored []cagoblocks.StoredBlock
	if err := json.Unmarshal([]byte(msg.BlocksJSON), &stored); err != nil {
		return nil, err
	}
	var card *heldAgrctlCard
	steps := map[string]bool{}
	for _, b := range stored {
		switch b.Type {
		case "subagent_state":
			var sb blocks.SubagentStateBlock
			if err := json.Unmarshal(b.Data, &sb); err != nil {
				return nil, err
			}
			if sb.ParentToolCallID == toolCallID && sb.Origin == agentruntime.SubagentOriginAgrctl {
				card = &heldAgrctlCard{steps: steps}
			}
		case "nested_tool_use":
			var nb blocks.NestedToolUseBlock
			if json.Unmarshal(b.Data, &nb) == nil && nb.ParentToolCallID == toolCallID {
				steps["use:"+nb.ID] = true
			}
		case "nested_tool_result":
			var nb blocks.NestedToolResultBlock
			if json.Unmarshal(b.Data, &nb) == nil && nb.ParentToolCallID == toolCallID {
				steps["result:"+nb.ToolCallID] = true
			}
		}
	}
	return card, nil
}

// applyDurableCardFrame 持久帧的写回规则:SubagentDone 是 agentred 落库之后的整份卡片
// (注入方与 CLI 的写入分工已在那边定好,单运行快照随它一起来),原样当快照写上 —— 与本轮
// 里的还原(restoreSubagentSnapshot)同一条规则,桌面端与 agentred 看到同一份;没变时不写。
// 模型帧照注入方的规则(first-wins)。
func applyDurableCardFrame(sb *blocks.SubagentStateBlock, ev agentruntime.Event) map[string]any {
	done, ok := ev.(agentruntime.SubagentDone)
	if !ok {
		return applyCardFrame(sb, ev)
	}
	before := *sb
	before.Runs = slices.Clone(sb.Runs)
	applySubagentSnapshot(sb, done)
	if reflect.DeepEqual(before, *sb) {
		return nil
	}
	return map[string]any{"kind": string(StreamSubagentDone), "info": done.Info}
}

// ── 远端父会话(父会话在 agentred 上执行)─────────────────────────────────────

// 远端收尾帧的重试节奏:断线时 Finish 不等重连,收尾交给后台按退避重试,直到送达或超出窗口。
const (
	ctlSendRetryBackoff    = time.Second
	ctlSendRetryMaxBackoff = 30 * time.Second
	ctlSendRetryWindow     = 30 * time.Minute
)

// ctlSendInjector 是生产的注入端口解析:父会话此刻在 agentred 上执行时交回那台 agentred 的
// 运行时,在本机执行时交回 nil。与 StopBackgroundTask 同一条解析路径(会话 → 执行目标 →
// runner);断线重连后连接会换,所以推送失败后要重新解析,不能攥着旧的。
func (s *chatSvc) ctlSendInjector(ctx context.Context, sessionID int64) (ctlSubagentInjector, error) {
	sess, err := chat_repo.Session().Find(ctx, sessionID)
	if err != nil || sess == nil {
		return nil, err
	}
	_, be, _, err := s.resolveAgentBackend(ctx, sess, sess.AgentID, sess.ProjectID)
	if err != nil {
		return nil, err
	}
	if !exec_target_svc.BackendTargetsRemote(be) {
		return nil, nil
	}
	rt, err := s.borrowRemoteRuntime(ctx, be, sessionID)
	if err != nil {
		return nil, err // 不把 nil 的 *remote.Runtime 包成非 nil 的接口交出去
	}
	return rt, nil
}

// bindCtlSendRemote 远端父会话的绑定:父转录归 agentred,匹配也由它在转发 send 时做完,
// 这里只采信它附来的 shell 调用(spec「Remote sessions」第 1 条、Design 12)。没有附来、父会话
// 并不在 agentred 上执行、或此刻连不上它时不绑定 —— 宁可不升级,也不挂错卡。
func (s *chatSvc) bindCtlSendRemote(ctx context.Context, child int64, req CtlSendBindRequest, port ctlSendRemotePort) (*ctlSendRemote, string) {
	if req.HostToolCallID == "" {
		return nil, ctlSendNoHostBinding
	}
	inj, err := port.resolve(ctx, req.CallerSessionID)
	if err != nil {
		logger.Ctx(ctx).Warn("chat_svc.bindCtlSend: cannot reach the remote parent",
			zap.Int64("callerSessionId", req.CallerSessionID),
			zap.Int64("childSessionId", child),
			zap.Error(err))
		return nil, ctlSendParentUnreachable
	}
	if inj == nil {
		return nil, ctlSendParentNotRemote
	}
	binding := ctlbind.Binding{ToolCallID: req.HostToolCallID, Background: req.HostBackground}
	r := &ctlSendRemote{
		port: port, inj: inj, sessionID: req.CallerSessionID, child: child, toolCallID: binding.ToolCallID,
		startedAt:       time.Now(),
		ctlSendProgress: newCtlSendProgress(),
		pending:         []agentruntime.Event{ctlSendStarted(binding, child, req)},
	}
	logger.Ctx(ctx).Info("chat_svc.bindCtlSend: subagent card bound on the remote parent",
		zap.Int64("callerSessionId", req.CallerSessionID),
		zap.Int64("childSessionId", child),
		zap.String("toolCallId", binding.ToolCallID),
		zap.String("kind", binding.Kind()))
	return r, ""
}

// ctlSendRemote 远端父会话上的一张卡:子会话的事件经注入 RPC 成批推给 agentred,由它写进
// 父转录(进行中的轮直接写,已结束的轮写回原消息),再以预览帧与持久帧回传 —— 桌面端、
// agentred、重连追帧看到的是同一份。推送只在镜像 goroutine / Finish(以及 Finish 交出的重试)
// 上做,子会话的事件循环从不等它。
//
// 断线(spec「Remote sessions」断线期间):子会话不受影响;断线期间的嵌套步骤与进度帧随失败
// 的那一批丢弃(可以缺几条,记一条 warn),但 overlay 起始帧、模型帧与收尾帧留在 pending 里随
// 下一次推送重发 —— 没有起始帧 agentred 不认这张卡,模型只推一次(first-wins),没有收尾帧
// 卡片会一直停在运行中。Finish 时仍连不上就交给后台按退避重试,重连后终态与最终文本照样送达。
type ctlSendRemote struct {
	port       ctlSendRemotePort
	inj        ctlSubagentInjector // 上一次解析出的注入端口;推送失败后作废,下次重新解析
	sessionID  int64               // 父会话
	child      int64
	toolCallID string
	startedAt  time.Time

	ctlSendProgress

	pending []agentruntime.Event // 还没送达、也不能丢的帧(起始 / 模型 / 收尾)
	outage  bool                 // 正处在一段断线里(这段的 warn 已经记过)
}

// mirror 推一批子会话事件:模型(先到为准,只推一次)、嵌套步骤与进度。
func (r *ctlSendRemote) mirror(ctx context.Context, batch []agentruntime.Event, model string) {
	r.push(ctx, append(r.modelFrame(model), r.steps(r.toolCallID, batch)...))
}

// finish 推收尾帧(带子轮最终文本);子轮没报过模型时先推它最终那条消息上的模型。连不上时
// 不等:交给后台重试后返回,子会话与 send 的应答都不被断线拖住。
func (r *ctlSendRemote) finish(ctx context.Context, _ int64, out CtlSendOutcome) {
	ctx = context.WithoutCancel(ctx)
	events := append(r.modelFrame(r.finalModel(ctx, out)), r.done(r.toolCallID, out, r.startedAt))
	if r.push(ctx, events) {
		r.logSettled(ctx, out, "chat_svc.ctlSendRemote.finish: subagent card settled")
		return
	}
	go r.retry(ctx, out)
}

// modelFrame 这次要推的模型帧(takeModel 的 first-wins);不推时为空。
func (r *ctlSendRemote) modelFrame(model string) []agentruntime.Event {
	if m := r.takeModel(model); m != "" {
		return []agentruntime.Event{agentruntime.SubagentModel{ToolCallID: r.toolCallID, Model: m}}
	}
	return nil
}

// retry 按退避重推 pending(起始 / 模型 / 收尾帧),直到送达或超出窗口。
func (r *ctlSendRemote) retry(ctx context.Context, out CtlSendOutcome) {
	backoff := r.port.backoff
	deadline := time.Now().Add(r.port.window)
	for {
		timer := time.NewTimer(backoff)
		<-timer.C
		if r.push(ctx, nil) {
			r.logSettled(ctx, out, "chat_svc.ctlSendRemote.retry: subagent card settled after reconnect")
			return
		}
		if time.Now().After(deadline) {
			logger.Ctx(ctx).Warn("chat_svc.ctlSendRemote.retry: gave up; the card's final state never reached the remote parent",
				zap.Int64("sessionId", r.sessionID),
				zap.Int64("childSessionId", r.child),
				zap.String("toolCallId", r.toolCallID),
				zap.String("status", string(out.Status)))
			return
		}
		backoff = min(backoff*2, ctlSendRetryMaxBackoff)
	}
}

// push 把 pending 连同这一批推给 agentred,交回是否送达。失败时只留下不能丢的帧。
func (r *ctlSendRemote) push(ctx context.Context, events []agentruntime.Event) bool {
	batch := append(r.pending[:len(r.pending):len(r.pending)], events...)
	if len(batch) == 0 {
		return true
	}
	if err := r.deliver(ctx, batch); err != nil {
		if !r.outage {
			r.outage = true
			logger.Ctx(ctx).Warn("chat_svc.ctlSendRemote: injecting into the remote parent failed; steps until it recovers may be missing",
				zap.Int64("sessionId", r.sessionID),
				zap.Int64("childSessionId", r.child),
				zap.String("toolCallId", r.toolCallID),
				zap.Error(err))
		}
		var keep []agentruntime.Event
		for _, ev := range batch {
			switch ev.(type) {
			case agentruntime.SubagentStarted, agentruntime.SubagentModel, agentruntime.SubagentDone:
				keep = append(keep, ev)
			}
		}
		r.pending = keep
		return false
	}
	if r.outage {
		r.outage = false
		logger.Ctx(ctx).Info("chat_svc.ctlSendRemote: the remote parent is reachable again",
			zap.Int64("sessionId", r.sessionID),
			zap.String("toolCallId", r.toolCallID))
	}
	r.pending = nil
	return true
}

// deliver 一次注入往返;端口失效(断线 / 父会话已不在 agentred 上)时作废缓存,下次重新解析。
func (r *ctlSendRemote) deliver(ctx context.Context, batch []agentruntime.Event) error {
	if r.inj == nil {
		inj, err := r.port.resolve(ctx, r.sessionID)
		if err != nil {
			return err
		}
		if inj == nil {
			return errors.New("the parent session no longer runs on agentred")
		}
		r.inj = inj
	}
	if err := r.inj.InjectSubagentEvents(ctx, r.sessionID, r.toolCallID, batch); err != nil {
		r.inj = nil
		return err
	}
	return nil
}

func (r *ctlSendRemote) logSettled(ctx context.Context, out CtlSendOutcome, msg string) {
	logger.Ctx(ctx).Info(msg,
		zap.Int64("sessionId", r.sessionID),
		zap.Int64("childSessionId", r.child),
		zap.String("toolCallId", r.toolCallID),
		zap.String("status", string(out.Status)))
}
