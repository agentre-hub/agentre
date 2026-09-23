package acp

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	cagoblocks "github.com/cago-frame/agents/agent/blocks"
	"github.com/cago-frame/agents/provider"
	acpsdk "github.com/coder/acp-go-sdk"

	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/capability"
)

// defaultRuntime 是包级单例,init() 时登记到 agentruntime 注册表。
var defaultRuntime = NewWithPool(agentruntime.DefaultCLISessionPool())

func init() {
	agentruntime.RegisterRuntime(agent_backend_entity.TypeACP, defaultRuntime)
}

// Default 返回包级默认 *Runtime,供宿主关机调 CloseAllSessions / 会话关闭调
// CloseSession。
func Default() *Runtime { return defaultRuntime }

// defaultAbortGrace 是 Abort 后等待 agent 自行收尾的宽限期:超时还没回
// prompt response 就整进程收掉,让 drain 解阻塞 —— 绝不让前端停在「生成中」。
const defaultAbortGrace = 10 * time.Second

// errImageNotNegotiated 是「带图的一轮打到一个没协商出图片输入的 agent 上」
// 的可读错误:明确失败,绝不静默丢图。
var errImageNotNegotiated = errors.New("acp: this agent does not accept image input (promptCapabilities.image not negotiated)")

// Runtime 是 ACP 后端的 runtime:一个 chat session 对应一个常驻 ACP agent
// 子进程(ACP Agent 的会话状态活在自己的进程里,跨轮复用连接是唯一不需要
// 依赖 session/load 的做法),经 CLISessionPool 复用。
type Runtime struct {
	// spawnLocks 按 session key 分桶串行化 get-or-spawn,只防同一 session 并发
	// 首轮 double-spawn;绝不能退回单把全局锁(某个 agent 启动期挂起会拖垮所有
	// session 的 turn,见 claudecode 的同款教训)。
	spawnLocks sync.Map // key(string) → *sync.Mutex
	cache      *agentruntime.CLISessionPool
	// abortGrace 单测覆写成毫秒级。
	abortGrace time.Duration
}

// New 造一个自带独立池的 runtime(单测要互不干扰);生产用进程级共享池。
func New() *Runtime {
	return NewWithPool(agentruntime.NewCLISessionPool(agentruntime.DefaultCLISessionIdleCap))
}

func NewWithPool(pool *agentruntime.CLISessionPool) *Runtime {
	if pool == nil {
		pool = agentruntime.NewCLISessionPool(agentruntime.DefaultCLISessionIdleCap)
	}
	return &Runtime{cache: pool, abortGrace: defaultAbortGrace}
}

// SetAbortGraceForTest 仅测试用;restore 闭包恢复默认。
func (r *Runtime) SetAbortGraceForTest(d time.Duration) func() {
	old := r.abortGrace
	r.abortGrace = d
	return func() { r.abortGrace = old }
}

func (r *Runtime) spawnLockFor(key string) *sync.Mutex {
	v, _ := r.spawnLocks.LoadOrStore(key, &sync.Mutex{})
	return v.(*sync.Mutex)
}

// sessionKey 把 chat session ID 翻成池键;形状由 agentruntime 统一决定(池是
// 进程级单例,裸会话 id 会让不同后端的同号会话互相顶掉)。
func sessionKey(id int64) string {
	return agentruntime.SessionPoolKey(agent_backend_entity.TypeACP, id)
}

// Capabilities 声明且只声明本 runtime 实际实现的能力。凡是「ACP 协议里没有
// 对应通道」的都不宣告:steer / 权限模式切换 / 反向提问 / fork / compact /
// goal / skills / 自主续轮 / 后台任务停止 / 思考力度一概不出现,chat_svc 对
// 它们返回 ErrUnsupported(诚实报不支持,不做桩实现)。三项例外:
//   - CapReportContextWindow:usage_update 的 size 是上下文窗口分母,对端不发
//     就什么都不发生(usage_update 本身是 MAY),无害;
//   - CapImageInput / CapMCPTools:静态矩阵宣告「这条通道存在」,运行时按
//     initialize 协商结果(promptCapabilities.image / mcpCapabilities.http)
//     决定用不用 —— 协商不到时图片输入明确报错,MCP 注入降级为日志。
func (r *Runtime) Capabilities() capability.Capabilities {
	return capability.Capabilities{
		Set: map[capability.Capability]bool{
			capability.CapAbort:               true, // session/cancel
			capability.CapToolPermission:      true, // session/request_permission
			capability.CapReportContextWindow: true, // session/update: usage_update 的 size
			capability.CapImageInput:          true, // 协商到 promptCapabilities.image 才真的发图
			capability.CapMCPTools:            true, // 协商到 mcpCapabilities.http 才真的注入
		},
	}
}

// Run 起一轮:取/起常驻 agent 会话,把用户输入拼成 ACP ContentBlock,然后在
// drain goroutine 里发 session/prompt 并流式翻译 session/update。
func (r *Runtime) Run(ctx context.Context, req agentruntime.RunRequest) (<-chan agentruntime.Event, *agentruntime.RunResult, error) {
	if req.Backend == nil {
		return nil, nil, errors.New("acp runtime: nil backend")
	}
	command := strings.TrimSpace(req.Backend.ACPCommand)
	if command == "" {
		return nil, nil, errors.New("acp runtime: backend has no acpCommand configured")
	}
	cwd := req.Cwd
	if cwd == "" {
		var err error
		cwd, err = agentruntime.ResolveAgentCwd(req.AgentID, req.AgentSyncID)
		if err != nil {
			return nil, nil, err
		}
	}
	envMap, err := agentruntime.BuildACPEnv(req.Backend, agentruntime.CLIDeps{Ctl: req.CtlCredentials()})
	if err != nil {
		return nil, nil, err
	}
	env := envList(envMap)

	s, err := r.acquireSession(ctx, req, cwd, env, command)
	if err != nil {
		logger.Ctx(ctx).Error("acp.Runtime: acquireSession failed",
			zap.Int64("sessionID", req.SessionID),
			zap.String("cwd", cwd),
			zap.Error(err))
		return nil, nil, err
	}

	// 图片:协商不到 promptCapabilities.image 时,带图的一轮明确失败。
	if hasImageBlock(req.UserBlocks) && !s.caps.promptImage {
		r.finishTurnBookkeeping(req.SessionID)
		return nil, nil, errImageNotNegotiated
	}
	blocks := buildPromptBlocks(req)

	token := s.nextTurnToken()
	done := s.beginTurn()
	result := &agentruntime.RunResult{
		ProviderSessionID: s.acpSessionID(),
		TurnToken:         token,
	}
	out := make(chan agentruntime.Event, 32)
	logger.Ctx(ctx).Info("acp.Runtime: turn started",
		zap.Int64("sessionID", req.SessionID),
		zap.String("providerSessionID", result.ProviderSessionID),
		zap.String("cwd", cwd))
	go r.drainTurn(ctx, s, req, out, result, blocks, done)
	return out, result, nil
}

// promptResult 是一次 session/prompt RPC 的结果。
type promptResult struct {
	resp acpsdk.PromptResponse
	err  error
}

// drainTurn 发 prompt、翻译 update 流、收口 RunResult。out 在本函数返回时
// 关闭;RunResult 只在 out 关闭后才可读(约定)。
func (r *Runtime) drainTurn(
	ctx context.Context,
	s *agentSession,
	req agentruntime.RunRequest,
	out chan<- agentruntime.Event,
	result *agentruntime.RunResult,
	blocks []acpsdk.ContentBlock,
	done chan struct{},
) {
	defer close(out)
	s.attachOut(out)
	defer s.detachOut(out)
	defer r.finishTurnBookkeeping(req.SessionID)
	defer s.endTurn(done)

	baseUsed, _ := s.usageBaseline()
	turn := newTurnAccumulator(baseUsed)
	// tools 是本轮的 tool call 聚合表(drain 循环私有,translator 保持纯函数)。
	tools := map[acpsdk.ToolCallId]*toolCallState{}

	promptDone := make(chan promptResult, 1)
	go func() {
		resp, err := s.conn.Prompt(ctx, acpsdk.PromptRequest{
			SessionId: acpsdk.SessionId(s.acpSessionID()),
			Prompt:    blocks,
		})
		promptDone <- promptResult{resp: resp, err: err}
	}()

	handle := func(n acpsdk.SessionNotification) {
		var events []agentruntime.Event
		var usage usageSnapshot
		switch {
		case n.Update.ToolCall != nil:
			// 创建帧:同时把聚合态登记进 drain 本地的 per-id 表。
			var ev agentruntime.ToolCall
			ev, tools[n.Update.ToolCall.ToolCallId] = translateToolCallAggregate(n.Update.ToolCall)
			events = []agentruntime.Event{ev}
		case n.Update.ToolCallUpdate != nil:
			// 更新帧:与聚合态合并后出 ToolCall(非终态,同 ID 增量)或 ToolResult(终态)。
			u := n.Update.ToolCallUpdate
			st := tools[u.ToolCallId]
			if st == nil {
				st = &toolCallState{}
				tools[u.ToolCallId] = st
			}
			events = translateToolCallUpdate(*st, u)
			st.status = toolCallUpdateStatus(u)
			if t := u.Title; t != nil && strings.TrimSpace(*t) != "" {
				st.name = *t
			}
		default:
			events, usage = translate(n.Update)
		}
		if usage.present {
			turn.applyUsage(out, s, usage)
		}
		if len(events) == 0 {
			// user_message_chunk 重放与一切不产出事件的 update:raw-frame 日志后继续,
			// 绝不能让轮次卡死。
			logger.Ctx(ctx).Debug("acp runtime: session/update produced no event",
				zap.Int64("sessionID", req.SessionID),
				zap.String("sessionUpdate", updateKind(n.Update)))
		}
		for _, ev := range events {
			out <- ev
		}
	}

	var pr promptResult
	for pending := true; pending; {
		select {
		case n := <-s.updates:
			handle(n)
		case pr = <-promptDone:
			pending = false
		}
	}
	// SDK 保证 pre-response 通知在 Prompt 返回前已全部处理完(水位线),此刻
	// 它们已经进了 updates 通道 —— 非阻塞排空即为本轮的最后一泼。
	for {
		select {
		case n := <-s.updates:
			handle(n)
		default:
			goto finished
		}
	}
finished:
	r.finishTurn(ctx, s, req, out, result, pr, turn)
}

// finishTurn 收口终态:stopReason 映射、usage 落账、Done/Error emit。
func (r *Runtime) finishTurn(
	ctx context.Context,
	s *agentSession,
	req agentruntime.RunRequest,
	out chan<- agentruntime.Event,
	result *agentruntime.RunResult,
	pr promptResult,
	turn *turnAccumulator,
) {
	if pr.err != nil {
		switch {
		case s.wasAborting():
			// agent 没有优雅回取消应答(甚至被兜底收掉):收成用户中止。
			result.StopErr = agentruntime.ErrAborted
			out <- agentruntime.Done{}
		case ctx.Err() != nil:
			result.StopErr = ctx.Err()
			out <- agentruntime.ErrorEvent{Err: ctx.Err()}
		default:
			err := pr.err
			if s.dead() {
				err = fmt.Errorf("acp agent process exited: %w", err)
				if tail := s.stderrTail(); tail != "" {
					err = fmt.Errorf("%w\n%s", err, tail)
				}
			}
			result.StopErr = err
			out <- agentruntime.ErrorEvent{Err: err}
		}
	} else {
		// end_turn / max_tokens / max_turn_requests / refusal 都收成正常结束;
		// stopReason 取消且本轮是用户 Abort → ErrAborted。
		if pr.resp.StopReason == acpsdk.StopReasonCancelled && s.wasAborting() {
			result.StopErr = agentruntime.ErrAborted
		}
		out <- agentruntime.Done{}
	}
	turn.finalize(result, pr.resp)
	logger.Ctx(ctx).Info("acp.Runtime: turn completed",
		zap.Int64("sessionID", req.SessionID),
		zap.String("providerSessionID", result.ProviderSessionID),
		zap.Int("contextWindow", result.ContextWindow),
		zap.Error(result.StopErr))
}

// finishTurnBookkeeping 收尾池侧记账(错误路径也要走)。
func (r *Runtime) finishTurnBookkeeping(sessionID int64) {
	if sessionID <= 0 {
		return
	}
	r.cache.MarkIdle(sessionKey(sessionID))
}

// acquireSession 拿到 chat session 对应的常驻 ACP agent 会话,必要时 spawn。
func (r *Runtime) acquireSession(ctx context.Context, req agentruntime.RunRequest, cwd string, env []string, command string) (*agentSession, error) {
	key := sessionKey(req.SessionID)
	lk := r.spawnLockFor(key)
	lk.Lock()
	defer lk.Unlock()

	identity := launchIdentity(req, cwd, env)
	if req.SessionID > 0 {
		// 身份不一致(含池里那条从没记过身份)由池当场驱逐,见 GetWithIdentity。
		if v, ok := r.cache.GetWithIdentity(key, identity); ok {
			s := v.(*agentSession)
			if !s.dead() {
				r.cache.MarkActive(key)
				if err := s.ensureACPSession(ctx, req, cwd); err != nil {
					return nil, err
				}
				return s, nil
			}
			// 进程已死:驱走重开,别把一轮送给死连接。
			r.cache.Remove(key)
		}
	}
	s, err := spawnAgent(ctx, launchSpec{command: command, args: req.Backend.ACPArgs, cwd: cwd, env: env})
	if err != nil {
		return nil, err
	}
	if req.SessionID > 0 {
		s.attachPool(r.cache, key)
		r.cache.PutWithIdentity(key, identity, s)
		r.cache.MarkActive(key)
	}
	if err := s.ensureACPSession(ctx, req, cwd); err != nil {
		if req.SessionID > 0 {
			r.cache.Remove(key)
		}
		_ = s.Close(context.Background())
		return nil, err
	}
	return s, nil
}

// launchIdentity 拼出「这个 agent 进程是拿什么参数起来的」:command / args /
// cwd / env 指纹(只含 env_json 用户变量 —— ACP 不注入每轮变化的一次性 token,
// 指纹才能跨轮稳定)/ 本轮 MCP 清单(server 名 + URL + headers,照 piagent 把
// mcpServers 算进身份的先例 —— 注入清单变了必须重开会话)。比对与「未记录即
// 已变」的判定交给 CLISessionPool.GetWithIdentity。
func launchIdentity(req agentruntime.RunRequest, cwd string, env []string) string {
	sortedEnv := append([]string(nil), env...)
	sort.Strings(sortedEnv)
	command := ""
	var args []string
	if req.Backend != nil {
		command = req.Backend.ACPCommand
		args = req.Backend.ACPArgs
	}
	return strings.Join([]string{
		command,
		strings.Join(args, "\x01"),
		cwd,
		strings.Join(sortedEnv, "\x01"),
		mcpFingerprint(req.MCPServers),
	}, "\x00")
}

// mcpFingerprint 把注入的 MCP 清单压成稳定串:server 名 + URL + 排序后的
// header 键值。清单变化(增删 server / 换 URL / 换凭证)都会让身份变化,从而
// 重开 agent 进程并重新渲染 mcpServers。
func mcpFingerprint(specs []agentruntime.MCPServerSpec) string {
	if len(specs) == 0 {
		return ""
	}
	parts := make([]string, 0, len(specs))
	for _, spec := range specs {
		keys := make([]string, 0, len(spec.Headers))
		for k := range spec.Headers {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		pairs := make([]string, 0, len(keys))
		for _, k := range keys {
			pairs = append(pairs, k+"="+spec.Headers[k])
		}
		parts = append(parts, spec.Name+"|"+spec.URL+"|"+strings.Join(pairs, ";"))
	}
	sort.Strings(parts)
	return strings.Join(parts, "\x01")
}

// Abort 中断当前轮:先置取消信号,再把挂着的审批全部答成取消应答(协议硬
// 要求),然后发 session/cancel 通知。agent 优雅回取消 response 最好;
// 宽限期内没回就整进程收掉兜底,绝不让 drain 挂死。幂等;无进行中轮次返回
// ErrNoActiveTurn。
func (r *Runtime) Abort(ctx context.Context, sessionID int64, turnToken uint64) (agentruntime.AbortOutcome, error) {
	v, ok := r.cache.Get(sessionKey(sessionID))
	if !ok {
		return agentruntime.AbortOutcome{}, agentruntime.ErrNoActiveTurn
	}
	s := v.(*agentSession)
	if !s.inTurn.Load() {
		return agentruntime.AbortOutcome{}, agentruntime.ErrNoActiveTurn
	}
	if turnToken != 0 && s.curTurnToken.Load() != turnToken {
		// stale:该轮已不是当前活跃轮,no-op。
		return agentruntime.AbortOutcome{TurnKind: agentruntime.TurnKindNone}, nil
	}
	s.setAborting(true)
	s.cancelPendingPermissions()
	if err := s.conn.Cancel(ctx, acpsdk.CancelNotification{SessionId: acpsdk.SessionId(s.acpSessionID())}); err != nil {
		if s.dead() {
			return agentruntime.AbortOutcome{}, agentruntime.ErrNoActiveTurn
		}
		return agentruntime.AbortOutcome{}, err
	}
	if done := s.currentTurnDone(); done != nil {
		grace := r.abortGrace
		go func() {
			select {
			case <-done:
			case <-time.After(grace):
				logger.Default().Warn("acp runtime: abort grace elapsed without prompt response, tearing down agent process",
					zap.Int64("sessionID", sessionID))
				r.cache.Remove(sessionKey(sessionID))
			}
		}()
	}
	return agentruntime.AbortOutcome{TurnKind: agentruntime.TurnKindUser}, nil
}

// SubmitToolPermission 把用户决策投回阻塞中的 session/request_permission。
// requestID 不存在(已回过 / 超时 / 会话已收)时幂等 no-op —— 断连重连后调用
// 方无法判断上一次提交是否送达,报错只会误报给用户。
func (r *Runtime) SubmitToolPermission(ctx context.Context, sessionID int64, requestID string, allow, alwaysAllowSession bool, denyReason string) error {
	if strings.TrimSpace(requestID) == "" {
		return errors.New("acp runtime: empty requestID")
	}
	v, ok := r.cache.Get(sessionKey(sessionID))
	if !ok {
		return nil
	}
	s := v.(*agentSession)
	return s.submitPermission(ctx, requestID, allow, alwaysAllowSession, denyReason)
}

// CloseSession 显式释放某个 chat session 的常驻 agent 进程。
func (r *Runtime) CloseSession(_ context.Context, sessionID int64) {
	if sessionID <= 0 {
		return
	}
	r.cache.Remove(sessionKey(sessionID))
}

// CloseAllSessions 宿主关机时调,收掉所有常驻 agent 进程。
func (r *Runtime) CloseAllSessions(_ context.Context) {
	r.cache.RemoveAll()
}

// ── 输入组装 ─────────────────────────────────────────────────────────────────

// buildPromptBlocks 把用户输入拼成 ACP ContentBlock:UserBlocks 非空以它为准
// (text 一律支持,image 已在入口按协商结果把关),否则 UserText。文本与图片的
// 顺序保持 blocks 原序。只有 URL、无 inline 字节的图片跳过(ACP 走 base64
// inline),与 claudecode / piagent 的行为一致。
func buildPromptBlocks(req agentruntime.RunRequest) []acpsdk.ContentBlock {
	var out []acpsdk.ContentBlock
	for _, b := range req.UserBlocks {
		switch v := b.(type) {
		case cagoblocks.TextBlock:
			if v.Text != "" {
				out = append(out, acpsdk.TextBlock(v.Text))
			}
		case *cagoblocks.TextBlock:
			if v != nil && v.Text != "" {
				out = append(out, acpsdk.TextBlock(v.Text))
			}
		case cagoblocks.ImageBlock:
			if img, ok := sdkImageBlock(v); ok {
				out = append(out, img)
			}
		case *cagoblocks.ImageBlock:
			if v != nil {
				if img, ok := sdkImageBlock(*v); ok {
					out = append(out, img)
				}
			}
		}
	}
	if len(out) == 0 && strings.TrimSpace(req.UserText) != "" {
		out = append(out, acpsdk.TextBlock(req.UserText))
	}
	return out
}

// sdkImageBlock 把 cago 图片块转成 ACP 的 image ContentBlock(base64 inline)。
func sdkImageBlock(b cagoblocks.ImageBlock) (acpsdk.ContentBlock, bool) {
	if len(b.Source.Inline) == 0 {
		return acpsdk.ContentBlock{}, false
	}
	mime := b.MediaType
	if mime == "" {
		mime = "image/png"
	}
	return acpsdk.ImageBlock(base64.StdEncoding.EncodeToString(b.Source.Inline), mime), true
}

// hasImageBlock 报告本轮用户输入是否带图。
func hasImageBlock(blocks []cagoblocks.ContentBlock) bool {
	for _, b := range blocks {
		switch v := b.(type) {
		case cagoblocks.ImageBlock:
			return true
		case *cagoblocks.ImageBlock:
			return v != nil
		}
	}
	return false
}

// envList 把 env map 转成 "K=V" 列表(cliprocess.Options.Env 的形状)。
func envList(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k, v := range m {
		out = append(out, k+"="+v)
	}
	return out
}

// ── 轮内 usage 聚合 ──────────────────────────────────────────────────────────

// turnAccumulator 在 drain 循环里把 ACP 的累计型 usage(used = 当前上下文
// tokens)翻成本轮增量,hermes 同款口径;size 是上下文窗口分母。
type turnAccumulator struct {
	baseUsed int
	usage    provider.Usage
	have     bool
	size     int
}

func newTurnAccumulator(baseUsed int) *turnAccumulator { return &turnAccumulator{baseUsed: baseUsed} }

func (t *turnAccumulator) applyUsage(out chan<- agentruntime.Event, s *agentSession, u usageSnapshot) {
	delta := max(u.used-t.baseUsed, 0)
	t.baseUsed = u.used
	s.setUsage(u.used, u.size)
	if u.size > 0 && u.size != t.size {
		t.size = u.size
		out <- agentruntime.ContextWindowUpdated{Tokens: u.size}
	}
	if delta <= 0 {
		return
	}
	t.usage.PromptTokens += delta
	t.usage.TotalTokens += delta
	t.have = true
	out <- agentruntime.UsageUpdate{
		Usage:            &provider.Usage{PromptTokens: delta, TotalTokens: delta},
		TotalInputTokens: delta,
		ContextWindow:    u.size,
	}
}

// finalize 用 PromptResponse 里的 per-turn usage(非标准但更准)覆盖;没有时
// 用累计差额兜底。ContextWindow 探到就填,与 usage 来源无关。
func (t *turnAccumulator) finalize(result *agentruntime.RunResult, resp acpsdk.PromptResponse) {
	if t.size > 0 {
		result.ContextWindow = t.size
	}
	if u := promptResponseUsage(resp); u != nil {
		result.Usage = u
		return
	}
	if t.have {
		u := t.usage
		result.Usage = &u
	}
}

// promptResponseUsage 把 PromptResponse 的非标准 usage 字段翻成 provider.Usage;
// 全零时返回 nil(agent 没上报)。
func promptResponseUsage(resp acpsdk.PromptResponse) *provider.Usage {
	u := resp.Usage
	if u == nil {
		return nil
	}
	out := &provider.Usage{
		PromptTokens:     u.InputTokens,
		CompletionTokens: u.OutputTokens,
		TotalTokens:      u.TotalTokens,
	}
	if u.CachedReadTokens != nil {
		out.CachedTokens = *u.CachedReadTokens
	}
	if u.CachedWriteTokens != nil {
		out.CacheCreationTokens = *u.CachedWriteTokens
	}
	if u.ThoughtTokens != nil {
		out.ReasoningTokens = *u.ThoughtTokens
	}
	if out.PromptTokens == 0 && out.CompletionTokens == 0 && out.TotalTokens == 0 &&
		out.CachedTokens == 0 && out.CacheCreationTokens == 0 && out.ReasoningTokens == 0 {
		return nil
	}
	return out
}
