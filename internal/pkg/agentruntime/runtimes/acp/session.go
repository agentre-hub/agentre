package acp

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"

	acpsdk "github.com/coder/acp-go-sdk"

	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"

	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
)

// agentSession 是池里的一条常驻 ACP agent 会话:一个子进程 + 一次握手敲定的
// 协商结果 + 一个 ACP session id。它是 CLISessionPool 的条目(Close/Kill/PID/
// Busy 都为池实现),也承载 update 路由与审批 waiter。
type agentSession struct {
	tr   agentTransport
	conn *acpsdk.ClientSideConnection
	caps negotiated

	// updates 承载 agent 推来的 session/update。通道在会话(进程)一生只有一个:
	// session/new 期间到达的早期帧(hermes 实测会先推 available_commands_update
	// 与 usage_update 再回 response)会被下一轮 drain 循环自然消费。
	updates chan acpsdk.SessionNotification

	inTurn       atomic.Bool
	turnSeq      atomic.Uint64
	curTurnToken atomic.Uint64
	aborting     atomic.Bool

	mu          sync.Mutex
	acpSession  string
	permSeq     int
	permWaiters map[string]*permWaiter
	turnDone    chan struct{}
	usageUsed   int
	usageSize   int

	// outMu/liveOuts 圈住当前仍在 drain 的事件出口。异步应答(SubmitToolPermission)
	// 按 waiter 记下的通道回投,emitTo 只往还在集合里的通道写 —— drain 退出时
	// detachOut 先摘除、调用方随后才 close,不会向已关闭 channel 写入。
	outMu    sync.Mutex
	liveOuts map[chan<- agentruntime.Event]struct{}

	pool    *agentruntime.CLISessionPool
	poolKey string
}

// permWaiter 是一次挂起的 session/request_permission。reply 承载用户决策
// (或 abort 时的取消应答);out 是发起这一轮的事件出口 —— Resolved 必须回到
// 同一条通道。
type permWaiter struct {
	options []acpsdk.PermissionOption
	reply   chan permDecision
	out     chan<- agentruntime.Event
}

// permDecision 是投回 waiter 的决策:canceled(agent 不认这个 kind / abort /
// 连接收尾)或选中的 optionId。
type permDecision struct {
	canceled bool
	optionID acpsdk.PermissionOptionId
}

// attachPool 在入池后由 Runtime 注入池句柄(MarkWaiting/MarkActive 用)。
func (s *agentSession) attachPool(pool *agentruntime.CLISessionPool, key string) {
	s.pool = pool
	s.poolKey = key
}

// Close 优雅收尾(关 stdin → 等退出),并把挂起的审批全部答成取消应答 ——
// 被阻塞的 RequestPermission goroutine 必须能返回。
func (s *agentSession) Close(ctx context.Context) error {
	s.cancelPendingPermissions()
	return s.tr.Close(ctx)
}

// Kill 整组 SIGKILL,是池在优雅关闭超时后的升级口。
func (s *agentSession) Kill(context.Context) error {
	return s.tr.Kill()
}

// PID 供池快照把子进程和会话对上;拿不到时为 0。
func (s *agentSession) PID() int { return s.tr.PID() }

// Busy 实现池的 sessionBusyReporter:本轮在跑或正等审批时自报正忙。
func (s *agentSession) Busy() bool {
	s.mu.Lock()
	waiting := len(s.permWaiters) > 0
	s.mu.Unlock()
	return s.inTurn.Load() || waiting
}

// dead 报告对端是否已经断开(进程退出 / stdin EOF)。
func (s *agentSession) dead() bool {
	select {
	case <-s.conn.Done():
		return true
	default:
		return false
	}
}

// stderrTail 交回进程 stderr 尾巴,错误信息用。
func (s *agentSession) stderrTail() string { return stderrTailLimited(s.tr.StderrTail()) }

func (s *agentSession) acpSessionID() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.acpSession
}

func (s *agentSession) setACPSessionID(id string) {
	s.mu.Lock()
	s.acpSession = id
	s.mu.Unlock()
}

// nextTurnToken 为新一轮入口分配自增 token 并记录为当前活跃轮。
func (s *agentSession) nextTurnToken() uint64 {
	t := s.turnSeq.Add(1)
	s.curTurnToken.Store(t)
	return t
}

// beginTurn / endTurn 圈住一轮的存活期:inTurn 标记 + 本轮的 done 通道。
func (s *agentSession) beginTurn() chan struct{} {
	s.inTurn.Store(true)
	done := make(chan struct{})
	s.mu.Lock()
	s.turnDone = done
	s.mu.Unlock()
	return done
}

func (s *agentSession) endTurn(done chan struct{}) {
	s.mu.Lock()
	if s.turnDone == done {
		close(done)
		s.turnDone = nil
	}
	s.mu.Unlock()
	s.inTurn.Store(false)
}

// currentTurnDone 取当前活跃轮的 done 通道;无活跃轮时为 nil。
func (s *agentSession) currentTurnDone() chan struct{} {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.turnDone
}

func (s *agentSession) setAborting(v bool) { s.aborting.Store(v) }
func (s *agentSession) wasAborting() bool  { return s.aborting.Load() }
func (s *agentSession) pushUpdate(p acpsdk.SessionNotification) {
	select {
	case s.updates <- p:
	default:
		logger.Default().Warn("acp runtime: update channel full, dropping session/update",
			zap.String("sessionUpdate", updateKind(p.Update)))
	}
}

// attachOut / detachOut 由 drain 循环圈住一轮的事件出口存活期。
func (s *agentSession) attachOut(out chan<- agentruntime.Event) {
	if out == nil {
		return
	}
	s.outMu.Lock()
	if s.liveOuts == nil {
		s.liveOuts = make(map[chan<- agentruntime.Event]struct{})
	}
	s.liveOuts[out] = struct{}{}
	s.outMu.Unlock()
}

func (s *agentSession) detachOut(out chan<- agentruntime.Event) {
	s.outMu.Lock()
	delete(s.liveOuts, out)
	s.outMu.Unlock()
}

// emitTo 把异步应答事件投回发起该请求那一轮的通道。通道已 detach(该轮 drain
// 已结束)或缓冲满时丢弃 —— 前端有乐观更新 + 历史回放兜底。
func (s *agentSession) emitTo(out chan<- agentruntime.Event, ev agentruntime.Event) {
	if out == nil {
		return
	}
	s.outMu.Lock()
	defer s.outMu.Unlock()
	if _, live := s.liveOuts[out]; !live {
		return
	}
	select {
	case out <- ev:
	default:
	}
}

// currentOut 取当前挂着的出口(审批请求到达时用一个就行 —— ACP 一个进程同一
// 时刻只跑一轮 prompt)。
func (s *agentSession) currentOut() chan<- agentruntime.Event {
	s.outMu.Lock()
	defer s.outMu.Unlock()
	for out := range s.liveOuts {
		return out
	}
	return nil
}

// ── 审批 waiter ──────────────────────────────────────────────────────────────

func (s *agentSession) nextPermID() string {
	s.mu.Lock()
	s.permSeq++
	n := s.permSeq
	s.mu.Unlock()
	return fmt.Sprintf("acp-perm-%d", n)
}

func (s *agentSession) registerPermWaiter(id string, w *permWaiter) {
	s.mu.Lock()
	if s.permWaiters == nil {
		s.permWaiters = make(map[string]*permWaiter)
	}
	s.permWaiters[id] = w
	s.mu.Unlock()
	if s.pool != nil && s.poolKey != "" {
		s.pool.MarkWaiting(s.poolKey)
	}
}

// takePermWaiter 取走(并删除)一个 waiter;没有时返回 nil(幂等语义由调用方处理)。
func (s *agentSession) takePermWaiter(id string) *permWaiter {
	s.mu.Lock()
	w := s.permWaiters[id]
	delete(s.permWaiters, id)
	idle := len(s.permWaiters) == 0
	s.mu.Unlock()
	if s.pool != nil && s.poolKey != "" && idle {
		s.pool.MarkActive(s.poolKey)
	}
	return w
}

// cancelPendingPermissions 把所有挂起的审批答成取消应答。协议硬要求:
// session/cancel 之后,client 必须对每个未决的 session/request_permission
// 回「已取消」outcome。
func (s *agentSession) cancelPendingPermissions() {
	s.mu.Lock()
	waiters := make([]*permWaiter, 0, len(s.permWaiters))
	for _, w := range s.permWaiters {
		waiters = append(waiters, w)
	}
	s.permWaiters = nil
	s.mu.Unlock()
	for _, w := range waiters {
		select {
		case w.reply <- permDecision{canceled: true}:
		default:
		}
	}
	if s.pool != nil && s.poolKey != "" && len(waiters) > 0 {
		s.pool.MarkActive(s.poolKey)
	}
}

// awaitPermission 是 acpClient.RequestPermission 的本体:登记 waiter、把
// ToolPermissionRequest 发进当前轮的事件流,然后阻塞等用户决策。ctx 取自
// SDK 的连接生命周期;连接收尾时回取消应答,不让 goroutine 悬空。
func (s *agentSession) awaitPermission(ctx context.Context, params acpsdk.RequestPermissionRequest) (acpsdk.RequestPermissionResponse, error) {
	id := s.nextPermID()
	out := s.currentOut()
	w := &permWaiter{options: params.Options, reply: make(chan permDecision, 1), out: out}
	s.registerPermWaiter(id, w)

	// ToolName / Input 与 ToolCall 同一规则:显示名取 title→kind→other,
	// RawInput 原样透传字节(缺省 {})。
	s.emitTo(out, agentruntime.ToolPermissionRequest{
		RequestID:  id,
		ToolCallID: string(params.ToolCall.ToolCallId),
		ToolName:   toolDisplayName(toolCallTitle(params.ToolCall), toolCallKind(params.ToolCall)),
		Input:      rawInputBytes(params.ToolCall.RawInput),
	})
	defer s.dropPermWaiter(id)
	select {
	case d := <-w.reply:
		return decisionResponse(d), nil
	case <-ctx.Done():
		return canceledResponse(), nil
	}
}

// dropPermWaiter 清掉一个仍在登记表里的 waiter(正常路径上它已被 take 走,
// 这里是 no-op;连接收尾 / 异常返回时兜底)。
func (s *agentSession) dropPermWaiter(id string) { s.takePermWaiter(id) }

func toolCallTitle(u acpsdk.ToolCallUpdate) string {
	if u.Title != nil {
		return *u.Title
	}
	return ""
}

func toolCallKind(u acpsdk.ToolCallUpdate) acpsdk.ToolKind {
	if u.Kind != nil {
		return *u.Kind
	}
	return ""
}

func decisionResponse(d permDecision) acpsdk.RequestPermissionResponse {
	if d.canceled {
		return canceledResponse()
	}
	return acpsdk.RequestPermissionResponse{Outcome: acpsdk.NewRequestPermissionOutcomeSelected(d.optionID)}
}

func canceledResponse() acpsdk.RequestPermissionResponse {
	return acpsdk.RequestPermissionResponse{Outcome: acpsdk.NewRequestPermissionOutcomeCancelled()}
}

// submitPermission 由 Runtime.SubmitToolPermission 调用:按选项 kind 选
// optionId(绝按 optionId 字面量猜 —— 那是 Agent 自定义的),投回 waiter 并
// emit ToolPermissionResolved。denyReason 没有协议通道可回传(ACP 与 codex 同形,
// 无 deny 反馈字段)—— 丢弃,但保留在 Resolved 事件与日志里。
func (s *agentSession) submitPermission(ctx context.Context, requestID string, allow, alwaysAllowSession bool, denyReason string) error {
	w := s.takePermWaiter(requestID)
	if w == nil {
		// requestID 不存在(已回过 / 超时 / 会话已收):幂等 no-op,不报错。
		return nil
	}
	kind := permOptionKind(allow, alwaysAllowSession)
	var optID acpsdk.PermissionOptionId
	for _, opt := range w.options {
		if opt.Kind == kind {
			optID = opt.OptionId
			break
		}
	}
	if optID == "" {
		// 找不到对应 kind 的选项:回取消应答,绝不编造 optionId。
		logger.Ctx(ctx).Warn("acp runtime: permission options lack the requested kind, answering canceled",
			zap.String("requestID", requestID),
			zap.String("kind", string(kind)))
		select {
		case w.reply <- permDecision{canceled: true}:
		default:
		}
		s.emitTo(w.out, agentruntime.ToolPermissionResolved{
			RequestID:   requestID,
			Allowed:     false,
			AlwaysAllow: false,
		})
		return nil
	}
	select {
	case w.reply <- permDecision{optionID: optID}:
	default:
	}
	logger.Ctx(ctx).Info("acp runtime: tool permission resolved",
		zap.String("requestID", requestID),
		zap.Bool("allowed", allow),
		zap.Bool("alwaysAllowSession", alwaysAllowSession),
		zap.String("denyReason", denyReason))
	s.emitTo(w.out, agentruntime.ToolPermissionResolved{
		RequestID:   requestID,
		Allowed:     allow,
		AlwaysAllow: alwaysAllowSession,
		DenyReason:  denyReason,
	})
	return nil
}

// permOptionKind 把 (allow, alwaysAllowSession) 翻译成 ACP 的选项 kind。
func permOptionKind(allow, alwaysAllowSession bool) acpsdk.PermissionOptionKind {
	switch {
	case allow && alwaysAllowSession:
		return acpsdk.PermissionOptionKindAllowAlways
	case allow:
		return acpsdk.PermissionOptionKindAllowOnce
	case alwaysAllowSession:
		return acpsdk.PermissionOptionKindRejectAlways
	default:
		return acpsdk.PermissionOptionKindRejectOnce
	}
}

// ── ACP 会话管理 ─────────────────────────────────────────────────────────────

// ensureACPSession 保证这个进程上挂着本轮该续的那条 ACP 会话:
//   - 活着的会话与 ProviderSessionID 一致(或请求没带)→ 直接复用;
//   - ProviderSessionID 非空且协商到 loadSession → session/load 恢复
//     (mcpServers 必须重新传);
//   - 协商不到 loadSession → session/new 起一条新会话,记 warn(上下文不延续);
//   - FreshSession → 强制 session/new,不许 load。
func (s *agentSession) ensureACPSession(ctx context.Context, req agentruntime.RunRequest, cwd string) error {
	live := s.acpSessionID()
	want := strings.TrimSpace(req.ProviderSessionID)
	if live != "" && !req.FreshSession && (want == "" || want == live) {
		return nil
	}
	servers, skipped := renderMCPServers(s.caps, req.MCPServers)
	if skipped > 0 {
		logger.Ctx(ctx).Warn("acp runtime: agent did not negotiate mcpCapabilities.http, skipping MCP injection for this turn",
			zap.Int64("sessionID", req.SessionID),
			zap.Int("skippedServers", skipped))
	}
	if want != "" && !req.FreshSession && s.caps.loadSession {
		if _, err := s.conn.LoadSession(ctx, acpsdk.LoadSessionRequest{
			SessionId:  acpsdk.SessionId(want),
			Cwd:        cwd,
			McpServers: servers,
		}); err != nil {
			// load 失败几乎总意味着 agent 侧那条会话已经不在了:带 ErrSessionNotFound
			// 哨兵让 chat_svc 清掉落库的 provider_session_id,当前轮失败而不是静默
			// 换一条新会话把上下文丢掉。
			return fmt.Errorf("%w: acp session/load %q: %w", agentruntime.ErrSessionNotFound, want, err)
		}
		s.setACPSessionID(want)
		return nil
	}
	if want != "" && !req.FreshSession && !s.caps.loadSession {
		logger.Ctx(ctx).Warn("acp runtime: agent has no loadSession capability, starting a new ACP session (context will not continue)",
			zap.Int64("sessionID", req.SessionID),
			zap.String("providerSessionID", want))
	}
	resp, err := s.conn.NewSession(ctx, acpsdk.NewSessionRequest{Cwd: cwd, McpServers: servers})
	if err != nil {
		// 把 agent 回的错误原文抛出去,不要吞掉换一句通用文案。
		return fmt.Errorf("acp session/new failed: %w", requestErrorMessage(err))
	}
	s.setACPSessionID(string(resp.SessionId))
	return nil
}

// requestErrorMessage 抽出 JSON-RPC 错误的可读正文,别把整包 JSON 壳给用户。
func requestErrorMessage(err error) error {
	var re *acpsdk.RequestError
	if errors.As(err, &re) {
		return errors.New(re.Message)
	}
	return err
}

// usage 快照读写(drain 循环用)。
func (s *agentSession) usageBaseline() (used, size int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.usageUsed, s.usageSize
}

func (s *agentSession) setUsage(used, size int) {
	s.mu.Lock()
	s.usageUsed, s.usageSize = used, size
	s.mu.Unlock()
}
