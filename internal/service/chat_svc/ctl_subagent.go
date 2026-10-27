package chat_svc

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"

	"github.com/agentre-hub/agentre/internal/model/entity/chat_entity"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
	"github.com/agentre-hub/agentre/internal/pkg/transcript/blocks"
	"github.com/agentre-hub/agentre/internal/pkg/transcript/ctlbind"
	"github.com/agentre-hub/agentre/internal/pkg/transcript/turn"
	"github.com/agentre-hub/agentre/internal/repository/transcript_repo"
)

// ctl_subagent.go 把 `agrctl send --wait` 的子会话挂到父转录里(spec 2026-10-27
// 「agrctl send 子代理卡」的「Binding」「Card lifecycle」):父 agent 跑 send 的那条外层
// shell 调用原地升级成子代理卡,子会话的工具调用 / 结果实时镜像成卡片的嵌套步骤,进度、
// 模型、用量跟着子会话走,终态在 serveSend 写响应(即 shell 调用拿到结果)之前落定。
//
// 本文件只做本机、前台、父轮进行中这一条路。后台(run_in_background)绑定、父轮结束后
// 写回原消息、远端(agentred)父轮由后续切片在 bindCtlSend 的对应分支上接手。
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
// Bind,拿到结果后先 Finish 再写响应,最后 Close。Bind 失败(不绑定)后 Finish 是空操作。
type CtlSendWatch interface {
	// Bind 在调用方会话当前这一轮里找发出这次 send 的 shell 调用并把它升级成子代理卡;
	// 不能确定时不绑定(记 info 日志说明原因)并交回 false。
	Bind(ctx context.Context, req CtlSendBindRequest) bool
	// Finish 把子轮结果落成卡片终态。返回时终态已落定(累加器 + 落库 + 实时事件)。
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

// ctlSendKey 登记表的键。表挂在包级、按服务实例分区:同一进程里的多个 chatSvc(测试)
// 互不可见。
type ctlSendKey struct {
	svc   *chatSvc
	child int64
}

// ctlSendWatches 子会话 → 它的登记(*ctlSendWatch)。子会话的事件循环经
// mirrorCtlSendChild 查这张表。
var ctlSendWatches sync.Map

// WatchCtlSend 登记子会话:此后它的工具调用 / 结果 / 用量先攒在登记里,绑定后补投。
func (s *chatSvc) WatchCtlSend(childSessionID int64) CtlSendWatch {
	w := &ctlSendWatch{
		svc: s, child: childSessionID,
		wake: make(chan struct{}, 1), quit: make(chan struct{}), exited: make(chan struct{}),
	}
	ctlSendWatches.Store(ctlSendKey{svc: s, child: childSessionID}, w)
	return w
}

// mirrorCtlSendChild 是子会话事件循环上的接入点(turnRun.applyLive):有登记就把事件交给
// 它。只做入队,**从不**等父轮的锁 —— 子会话的事件循环不能被父轮拖住。model 是子轮此刻
// 已知的模型(空 = 还不知道)。
func (s *chatSvc) mirrorCtlSendChild(childSessionID int64, model string, ev agentruntime.Event) {
	v, ok := ctlSendWatches.Load(ctlSendKey{svc: s, child: childSessionID})
	if !ok {
		return
	}
	v.(*ctlSendWatch).enqueue(model, ev)
}

// ctlSendWatch 一次 send 的登记。队列由子会话事件循环写、由镜像 goroutine(或 Finish)读,
// 只在 mu 下碰;绑定后的父转录写入一律在父轮的 t.mu 下进行(ctlSendBinding)。
type ctlSendWatch struct {
	svc   *chatSvc
	child int64

	mu       sync.Mutex
	queue    []agentruntime.Event
	model    string
	binding  *ctlSendBinding
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
	b, ok := w.svc.bindCtlSend(ctx, w.child, req)
	if !ok {
		return false
	}
	w.mu.Lock()
	w.binding = b
	w.mu.Unlock()
	go w.run(context.WithoutCancel(ctx))
	w.signal() // 绑定之前攒下的那些
	return true
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
	ctlSendWatches.CompareAndDelete(ctlSendKey{svc: w.svc, child: w.child}, w)
}

// 不绑定的原因(ctlbind.Reason 之外由本服务判出的那几种)。
const (
	ctlSendNoActiveTurn = "no_active_turn"
	ctlSendRemoteParent = "remote_parent"
	ctlSendBackground   = "background"
)

// ctlSendRunMode 卡片的单运行快照模式:目标 agent 名与任务文本存在 Runs[0] 里,
// SubagentStateBlock 不持久化 SubagentType,重载时卡片名只能从这里来。
const ctlSendRunMode = "single"

// bindCtlSend 在调用方会话当前这一轮(本机、进行中)里匹配 shell 调用并升级成 overlay。
// 匹配与升级都在父轮的 t.mu 下:与事件循环、工具审批互斥,匹配时看到的开放调用不会
// 在升级前变化。
func (s *chatSvc) bindCtlSend(ctx context.Context, child int64, req CtlSendBindRequest) (*ctlSendBinding, bool) {
	notBound := func(reason string) (*ctlSendBinding, bool) {
		logger.Ctx(ctx).Info("chat_svc.bindCtlSend: subagent card not bound",
			zap.String("reason", reason),
			zap.Int64("callerSessionId", req.CallerSessionID),
			zap.Int64("childSessionId", child))
		return nil, false
	}
	t := s.activeTurn(req.CallerSessionID)
	if t == nil {
		return notBound(ctlSendNoActiveTurn)
	}
	// 远端执行的父轮:转录归 agentred,桌面端这只累加器只是持久帧的回放。
	if t.previews != nil {
		return notBound(ctlSendRemoteParent)
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.approvalsClosed { // 这一轮已经收口(closeApprovals 之后转录归 finalize)
		return notBound(ctlSendNoActiveTurn)
	}
	target := ctlbind.Target{Task: req.Task}
	if req.AgentID > 0 {
		target.AgentID = req.AgentID
	} else {
		target.AgentName = req.AgentName
	}
	acc := t.acc
	binding, reason := ctlbind.Match(acc.OuterToolUses(), ctlbind.BoundToolCallIDs(acc.Snapshot()), target)
	if reason != "" {
		return notBound(string(reason))
	}
	if binding.Background {
		return notBound(ctlSendBackground)
	}
	b := &ctlSendBinding{
		turn: t, acc: acc, msg: t.assistantMsg, toolCallID: binding.ToolCallID,
		run: agentruntime.SubagentRun{
			ID: fmt.Sprintf("agrctl:%d", child), Agent: req.AgentName, Task: req.Task,
		},
		startedAt: time.Now(),
		seen:      map[string]bool{},
	}
	b.apply(ctx, agentruntime.SubagentStarted{ToolCallID: b.toolCallID, Info: agentruntime.SubagentInfo{
		Kind:            binding.Kind(),
		Origin:          agentruntime.SubagentOriginAgrctl,
		ChildSessionID:  child,
		Isolated:        req.Isolated,
		SubagentType:    req.AgentName,
		TaskDescription: req.Task,
		Prompt:          req.Task,
		Status:          "running",
	}})
	b.syncRun()
	s.checkpointAssistantNew(ctx, b.msg, b.acc)
	logger.Ctx(ctx).Info("chat_svc.bindCtlSend: subagent card bound",
		zap.Int64("callerSessionId", req.CallerSessionID),
		zap.Int64("childSessionId", child),
		zap.String("toolCallId", b.toolCallID),
		zap.String("kind", binding.Kind()))
	return b, true
}

// ctlSendBinding 一张已升级的卡:它落在哪一轮、哪只累加器、哪条消息上(插话分段会换掉
// turnRun 的当前消息,卡却留在它发生的那一条里),以及注入方自己累计的进度。
// 除构造外,所有字段只在 turn.mu 下读写。
type ctlSendBinding struct {
	turn       *turnRun
	acc        *turn.Accumulator
	msg        *chat_entity.Message
	toolCallID string
	run        agentruntime.SubagentRun // 身份:ID / Agent / Task
	startedAt  time.Time

	seen         map[string]bool // 已计数的子会话外层工具调用 ID
	toolUses     int
	lastToolName string
	inputTokens  int // 子轮最近一次调用的输入规模
	outputTokens int // 子轮累计输出
	model        string
}

// apply 把一帧经 dispatcher 落进卡所在的累加器(带实时事件),并照 applyLive 的口径扇出给
// 对端。调用方持有 turn.mu。
func (b *ctlSendBinding) apply(ctx context.Context, ev agentruntime.Event) {
	t := b.turn
	t.svc.publishPeerEvent(t.sess.ID, ev)
	if err := t.svc.dispatcher.Apply(ctx, ev, b.acc, t.dispEmit, t.turnCtx); err != nil {
		logger.Ctx(ctx).Warn("chat_svc.ctlSendBinding.apply: dispatcher Apply failed",
			zap.Int64("sessionId", t.sess.ID),
			zap.String("eventType", fmt.Sprintf("%T", ev)),
			zap.Error(err))
	}
}

// mirror 把一批子会话事件搬进父转录:嵌套步骤(按 ID 去重由 handler 负责)、模型、进度。
func (b *ctlSendBinding) mirror(ctx context.Context, batch []agentruntime.Event, model string) {
	t := b.turn
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.approvalsClosed {
		return // 父轮已收口:之后的写回归后续切片(写回原消息)
	}
	b.applyModel(ctx, model)
	checkpoint, progressed := false, false
	for _, ev := range batch {
		if nested, ok := ctlbind.NestChildEvent(b.toolCallID, ev); ok {
			b.apply(ctx, nested)
			checkpoint = checkpoint || shouldCheckpointAssistantAfterEvent(nested)
		}
		progressed = b.track(ev) || progressed
	}
	if progressed {
		b.apply(ctx, agentruntime.SubagentProgress{ToolCallID: b.toolCallID, Info: agentruntime.SubagentInfo{
			Origin:       agentruntime.SubagentOriginAgrctl,
			ToolUses:     b.toolUses,
			LastToolName: b.lastToolName,
			TotalTokens:  b.inputTokens + b.outputTokens,
		}})
	}
	b.syncRun()
	if checkpoint {
		t.svc.checkpointAssistantNew(ctx, b.msg, b.acc)
	}
}

// track 累计进度,交回这一帧是否改变了进度。子会话外层工具调用按 ID 计一次(同一 ID 的
// 增量更新不重复计数);用量 = 最近一次调用的输入规模 + 累计输出。
func (b *ctlSendBinding) track(ev agentruntime.Event) bool {
	switch e := ev.(type) {
	case agentruntime.ToolCall:
		if e.ParentToolCallID != "" || b.seen[e.ID] {
			return false
		}
		b.seen[e.ID] = true
		b.toolUses++
		b.lastToolName = e.Name
		return true
	case agentruntime.UsageUpdate:
		if e.Usage == nil {
			return false
		}
		b.outputTokens += e.Usage.CompletionTokens
		if e.TotalInputTokens > 0 {
			b.inputTokens = e.TotalInputTokens
		}
		return true
	}
	return false
}

// applyModel 模型一经记录不再改写(与 SubagentModelHandler 的 first-wins 一致)。
func (b *ctlSendBinding) applyModel(ctx context.Context, model string) {
	if model == "" || b.model != "" {
		return
	}
	b.model = model
	b.apply(ctx, agentruntime.SubagentModel{ToolCallID: b.toolCallID, Model: model})
}

// finish 落终态:SubagentDone 带子轮最终文本、工具数、用量与耗时,随后落库。
func (b *ctlSendBinding) finish(ctx context.Context, child int64, out CtlSendOutcome) {
	t := b.turn
	t.mu.Lock()
	known, closed := b.model != "", t.approvalsClosed
	t.mu.Unlock()
	if closed {
		return
	}
	model := ""
	if !known && out.AssistantMessageID > 0 { // 仓储 IO 不在父轮的锁里做
		if msg, err := transcript_repo.Message().Find(ctx, out.AssistantMessageID); err == nil && msg != nil {
			model = msg.Model
		}
	}

	t.mu.Lock()
	defer t.mu.Unlock()
	if t.approvalsClosed {
		return
	}
	b.applyModel(ctx, model)
	b.apply(ctx, agentruntime.SubagentDone{ToolCallID: b.toolCallID, Info: agentruntime.SubagentInfo{
		Origin:      agentruntime.SubagentOriginAgrctl,
		Status:      string(out.Status),
		FinalText:   out.FinalText,
		ToolUses:    b.toolUses,
		TotalTokens: b.inputTokens + b.outputTokens,
		DurationMs:  int(time.Since(b.startedAt).Milliseconds()),
	}})
	b.syncRun()
	t.svc.checkpointAssistantNew(ctx, b.msg, b.acc)
	logger.Ctx(ctx).Info("chat_svc.ctlSendBinding.finish: subagent card settled",
		zap.Int64("sessionId", t.sess.ID),
		zap.Int64("childSessionId", child),
		zap.String("toolCallId", b.toolCallID),
		zap.String("status", string(out.Status)))
}

// syncRun 把卡片的单运行快照(Mode/Runs)对齐到 overlay 当前的状态。overlay 的 agrctl 写入
// 路径不收 Runs,而重载时卡片名(目标 agent)只能从 Runs[0] 来,所以由注入方在每次写入后
// 从 overlay 自身派生,不另存一份会漂移的状态。
func (b *ctlSendBinding) syncRun() {
	turn.Mutate[blocks.SubagentStateBlock](b.acc, "subagent_state:"+b.toolCallID, func(sb *blocks.SubagentStateBlock) {
		if sb.Origin != agentruntime.SubagentOriginAgrctl {
			return
		}
		run := b.run
		run.Status = sb.Status
		run.Model = sb.Model
		run.ToolUses = sb.ToolUses
		run.LastToolName = sb.LastToolName
		if sb.Status == string(CtlSendFailed) {
			run.ErrorMessage = sb.FinalText
		} else {
			run.Summary = sb.FinalText
		}
		sb.Mode = ctlSendRunMode
		sb.Runs = []agentruntime.SubagentRun{run}
	})
}
