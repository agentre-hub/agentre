package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"
	"google.golang.org/protobuf/proto"

	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/runtimes/remote/protowire"
	"github.com/agentre-hub/agentre/internal/pkg/ctlaccount"
	"github.com/agentre-hub/agentre/internal/pkg/relaypeer"
	"github.com/agentre-hub/agentre/internal/pkg/transcript/ctlbind"
	"github.com/agentre-hub/agentre/pkg/wire/agentrewire"
)

// CtlAccount 是控制台拥有的会话里 agrctl 会话命令的执行者:以这台 agentred 的设备身份作为
// 账号对端(*ctlaccount.Client,与 agrctl 账号模式同一份派发与查询)。一次请求一个,用完 Close。
type CtlAccount interface {
	Send(ctx context.Context, in ctlaccount.SendInput) (ctlaccount.Reply, error)
	Wait(ctx context.Context, id string, timeout time.Duration) (ctlaccount.Reply, error)
	Stop(ctx context.Context, id string) (ctlaccount.StopReply, error)
	Follow(ctx context.Context, id string, from int64, fn func(*agentrewire.DurableNotification)) (string, error)
	ListSessions(ctx context.Context, f ctlaccount.SessionFilter) ([]ctlaccount.SessionRow, error)
	GetSession(ctx context.Context, id string) (*ctlaccount.SessionDetail, error)
	Messages(ctx context.Context, id string, last int, before int64, full bool) (ctlaccount.MessagesPage, error)
	Pending(ctx context.Context, id string) ([]ctlaccount.PendingItem, error)
	Tail(ctx context.Context, w io.Writer, id, from string, untilIdle bool) error
	Close()
}

// CtlRelay 是账号对端的 relay 接线:server 地址、这台 agentred 的设备凭据与刷新,以及目标就是
// 本机时的进程内通道(不经 relay 自拨)。
type CtlRelay struct {
	ServerURL func() string
	// Token 是这台 agentred 的设备 access token(relay 的 Bearer 与 auth.account 的凭据)。
	Token func(ctx context.Context) (string, error)
	// Reauthorize 在 relay 拒收 stale 之后刷新一次设备凭据并交回新的。
	Reauthorize func(ctx context.Context, stale string) (string, error)
	// DialSelf 开一条到这台 agentred 自己的已鉴权通道;nil = 一律经 relay。
	DialSelf func(ctx context.Context) (*relaypeer.Conn, error)
	// HostsConversation 报告这条对话是不是在这台 agentred 上。
	HostsConversation func(ctx context.Context, conversationID string) bool
}

// 控制台拥有的会话里会话级 token 能走的会话端点(与桌面端 ctl_svc 的 sessionRoutes 同一组只读
// 端点,外加 send;stop / answer 不在其中 —— 会话里的 agent 不能停会话、不能回答待处理项)。
const (
	ctlConversationsPath = "/ctl/v1/conversations"
	ctlConversationPath  = "/ctl/v1/conversation"
	ctlMessagesPath      = "/ctl/v1/messages"
	ctlPendingPath       = "/ctl/v1/pending"
	ctlWaitPath          = "/ctl/v1/wait"
	ctlTailPath          = "/ctl/v1/tail"
)

// ctlAccountSource 是这台 agentred 代发的轮次在执行端记的来源名。
const ctlAccountSource = "agentred"

// ctlCardMaxFollow 是 send --wait 提前返回(等待输入 / 超时)之后,子代理卡跟着子会话的最长
// 时间(与桌面端 ctl_svc.detachedCardMaxWait 同值)。
const ctlCardMaxFollow = 24 * time.Hour

// ctlCardSettleWait 是子轮已经收口、卡片收尾之前等跟随把最后几步交完的上限:跟随每个轮询间隔
// 复查一次状态,正常不过一两秒。
const ctlCardSettleWait = 10 * time.Second

func isConsoleReadPath(path string) bool {
	switch path {
	case ctlConversationsPath, ctlConversationPath, ctlMessagesPath, ctlPendingPath, ctlWaitPath:
		return true
	}
	return false
}

// account 为一次请求造账号对端。
func (p *ctlProxy) account() (CtlAccount, error) {
	if p.deps.Account != nil {
		return p.deps.Account()
	}
	return newCtlAccount(p.deps)
}

// ── send ────────────────────────────────────────────────────────────────────

// serveConsoleSend:控制台拥有的会话里的 `agrctl send`(spec「控制台会话里的 agrctl send」)——
// 这台 agentred 以账号对端身份为目标 agent 选设备(可以就是自己)、发起子会话并保存进账号;
// --wait 时把父转录里那条 shell 调用升级成子代理卡,子会话跑完再回;承载它的连接断开时停掉子会话。
func (p *ctlProxy) serveConsoleSend(w http.ResponseWriter, r *http.Request, owner ctlSession, body []byte) {
	ctx := r.Context()
	var req ctlSendBody
	if err := json.Unmarshal(body, &req); err != nil {
		writeCtlErr(w, http.StatusBadRequest, "invalid json body")
		return
	}
	if strings.TrimSpace(req.Text) == "" {
		writeCtlErr(w, http.StatusBadRequest, "text is required")
		return
	}
	if strings.TrimSpace(req.ConversationID) == owner.conversationID {
		writeCtlErr(w, http.StatusBadRequest, "cannot send to the session you are in")
		return
	}
	acct, err := p.account()
	if err != nil {
		p.writeAccountErr(w, ctx, owner, err)
		return
	}
	const where = "handlers.ctlProxy.serveConsoleSend"
	binding, bound := p.matchSend(ctx, where, owner, req)
	reply, err := acct.Send(ctx, req.input())
	if err != nil {
		if bound {
			p.deps.Sessions.unbindCtlSend(owner, binding.ToolCallID)
		}
		acct.Close()
		p.writeAccountErr(w, ctx, owner, err)
		return
	}
	child := reply.ConversationID
	logger.Ctx(ctx).Info(where+": dispatched",
		zap.String("conversationId", owner.conversationID), zap.String("childConversationId", child),
		zap.Bool("continue", req.continuing()), zap.Bool("steered", reply.Steered), zap.Bool("wait", req.Wait))
	if !req.Wait {
		acct.Close()
		writeCtlJSON(w, http.StatusOK, reply)
		return
	}
	var card *consoleCard
	if bound {
		card = p.startConsoleCard(ctx, owner, binding, req, reply, acct)
	}
	res, err := acct.Wait(ctx, child, millis(req.TimeoutMs))
	if ctx.Err() != nil {
		// 承载 send 的连接断开(agrctl 被杀、父轮被停、shell 超时):与桌面端拥有的会话相同,停掉
		// 子会话;请求 ctx 已死,收尾用脱离取消的那份。
		stopCtx := context.WithoutCancel(ctx)
		if _, serr := acct.Stop(stopCtx, child); serr != nil {
			logger.Ctx(stopCtx).Warn(where+": stop the child after the caller left failed",
				zap.String("conversationId", owner.conversationID), zap.String("childConversationId", child), zap.Error(serr))
		}
		card.cancel(stopCtx)
		acct.Close()
		logger.Ctx(stopCtx).Info(where+": the connection carrying the send dropped; child stopped",
			zap.String("conversationId", owner.conversationID), zap.String("childConversationId", child))
		return
	}
	if err != nil {
		if card != nil {
			card.detach() // 子会话还在跑:卡片照样跟到它真正收口
		} else {
			acct.Close()
		}
		p.writeAccountErr(w, ctx, owner, err)
		return
	}
	res.Agent, res.Device, res.Steered = reply.Agent, reply.Device, reply.Steered
	switch {
	case card == nil:
		acct.Close()
	case settledStatus(res.Status):
		// 卡片的终态在 shell 调用拿到结果之前落定。
		card.finish(ctx, res)
		acct.Close()
	default:
		// 等待输入或超时:这一轮还在跑,卡片跟着它直到真正收口。
		card.detach()
	}
	writeCtlJSON(w, http.StatusOK, res)
}

// input 是交给账号对端的那一份:flag 与 agrctl 账号模式相同;等待由代理自己做(要在中间绑卡)。
func (r ctlSendBody) input() ctlaccount.SendInput {
	in := ctlaccount.SendInput{
		Text: r.Text, Images: r.Images, Session: strings.TrimSpace(r.ConversationID), Steer: r.Steer,
		Agent: strings.TrimSpace(r.Agent), AgentID: r.AgentID, Title: r.Title, Device: r.Device, Engine: r.Engine,
		Isolated: r.Isolated, Model: r.Model, CLIModel: r.CLIModel, Effort: r.Effort, PermissionMode: r.PermissionMode,
	}
	if r.ProjectID > 0 {
		in.Project = strconv.FormatInt(r.ProjectID, 10)
	}
	return in
}

func settledStatus(status string) bool {
	return status == ctlaccount.StatusIdle || status == ctlaccount.StatusFailed
}

func millis(ms int64) time.Duration {
	if ms <= 0 {
		return 0
	}
	return time.Duration(ms) * time.Millisecond
}

// ── 子代理卡 ────────────────────────────────────────────────────────────────

// consoleCard 是控制台拥有的父会话里一张已绑定的子代理卡(spec 2026-10-27「Card lifecycle」):
// 这台 agentred 既是父轮的宿主又是子会话的发起方,所以由它自己跟随子会话的持久帧,把工具调用
// / 结果镜像成卡片的嵌套步骤,进度与模型跟着子会话走,终态由子轮的真实结果写入。写入走与
// 桌面端注入同一个入口(SubagentCardInjector):父轮还开着写在途那一轮,收口之后写回原消息。
type consoleCard struct {
	inj        *SubagentCardInjector
	owner      ctlSession
	toolCallID string
	child      string
	acct       CtlAccount
	ctx        context.Context
	stop       context.CancelFunc
	startedAt  time.Time
	progress   ctlCardProgress

	followed chan struct{} // 跟随结束时关上;status 此后可读
	status   string
}

// startConsoleCard 写卡片的起始帧,并起跟随子会话的 goroutine。ctx 脱离请求的取消:前台 send
// 断开由 cancel 显式收尾,提前返回的 send 之后卡片还要跟着子会话。
func (p *ctlProxy) startConsoleCard(
	ctx context.Context, owner ctlSession, binding ctlbind.Binding, req ctlSendBody, reply ctlaccount.Reply, acct CtlAccount,
) *consoleCard {
	cctx, stop := context.WithTimeout(context.WithoutCancel(ctx), ctlCardMaxFollow)
	agent := reply.Agent
	if agent == "" {
		agent = strings.TrimSpace(req.Agent)
	}
	c := &consoleCard{
		inj: p.deps.Cards, owner: owner, toolCallID: binding.ToolCallID, child: reply.ConversationID, acct: acct,
		ctx: cctx, stop: stop, startedAt: time.Now(), progress: newCtlCardProgress(), followed: make(chan struct{}),
	}
	c.inject(agentruntime.SubagentStarted{ToolCallID: c.toolCallID, Info: agentruntime.SubagentInfo{
		Kind: binding.Kind(), Origin: agentruntime.SubagentOriginAgrctl, Isolated: req.Isolated,
		SubagentType: agent, TaskDescription: req.Text, Prompt: req.Text, Status: "running",
	}})
	go func() {
		defer close(c.followed)
		status, err := acct.Follow(cctx, c.child, 0, c.mirror)
		if err != nil {
			logger.Ctx(cctx).Warn("handlers.consoleCard.follow: following the child failed; steps until it ends may be missing",
				zap.String("conversationId", owner.conversationID), zap.String("childConversationId", c.child), zap.Error(err))
		}
		c.status = status
	}()
	return c
}

// mirror 把子会话的一条持久帧翻成卡上的帧:嵌套步骤、进度、模型。
func (c *consoleCard) mirror(n *agentrewire.DurableNotification) {
	if events := c.progress.frame(c.toolCallID, n); len(events) > 0 {
		c.inject(events...)
	}
}

// finish 等跟随把子轮的最后几步交完,再写终态。
func (c *consoleCard) finish(ctx context.Context, res ctlaccount.Reply) {
	select {
	case <-c.followed:
	case <-time.After(ctlCardSettleWait):
		c.stop()
		<-c.followed
	}
	c.stop()
	c.done(res)
	logger.Ctx(ctx).Info("handlers.consoleCard.finish: subagent card settled",
		zap.String("conversationId", c.owner.conversationID), zap.String("toolCallId", c.toolCallID), zap.String("status", res.Status))
}

// cancel:承载 send 的连接断开,子会话已停,卡片收成已取消(后台卡的状态仍归 CLI)。
func (c *consoleCard) cancel(ctx context.Context) {
	if c == nil {
		return
	}
	c.stop()
	<-c.followed
	c.inject(agentruntime.SubagentDone{ToolCallID: c.toolCallID, Info: c.progress.doneInfo("canceled", "", c.startedAt)})
	logger.Ctx(ctx).Info("handlers.consoleCard.cancel: subagent card settled as canceled",
		zap.String("conversationId", c.owner.conversationID), zap.String("toolCallId", c.toolCallID))
}

// detach:send 已经回了,子会话还在跑。卡片跟到子会话收口,以它的真实结果收尾,再放掉账号对端。
func (c *consoleCard) detach() {
	go func() {
		defer c.acct.Close()
		<-c.followed
		defer c.stop()
		if !settledStatus(c.status) {
			c.inject(agentruntime.SubagentDone{ToolCallID: c.toolCallID, Info: c.progress.doneInfo("canceled", "", c.startedAt)})
			return
		}
		res, err := c.acct.Wait(c.ctx, c.child, 0)
		if err != nil {
			logger.Ctx(c.ctx).Warn("handlers.consoleCard.detach: reading the child's outcome failed",
				zap.String("conversationId", c.owner.conversationID), zap.String("childConversationId", c.child), zap.Error(err))
			res = ctlaccount.Reply{Status: ctlaccount.StatusFailed, Error: err.Error()}
		}
		c.done(res)
		logger.Ctx(c.ctx).Info("handlers.consoleCard.detach: subagent card settled after the send returned",
			zap.String("conversationId", c.owner.conversationID), zap.String("toolCallId", c.toolCallID), zap.String("status", res.Status))
	}()
}

// done 按子轮结果写终态:空闲 = 完成(最终文本),失败 = 失败(原因)。
func (c *consoleCard) done(res ctlaccount.Reply) {
	status, text := "completed", res.Text
	if res.Status == ctlaccount.StatusFailed {
		status, text = "failed", res.Error
	}
	c.inject(agentruntime.SubagentDone{ToolCallID: c.toolCallID, Info: c.progress.doneInfo(status, text, c.startedAt)})
}

func (c *consoleCard) inject(events ...agentruntime.Event) {
	if c.inj == nil {
		return
	}
	if err := c.inj.Inject(c.ctx, c.owner.peer, c.owner.conversationID, c.toolCallID, events); err != nil {
		logger.Ctx(c.ctx).Warn("handlers.consoleCard.inject: writing the subagent card failed",
			zap.String("conversationId", c.owner.conversationID), zap.String("toolCallId", c.toolCallID), zap.Error(err))
	}
}

// ctlCardProgress 是卡片自己累计的进度(只由跟随 goroutine 与之后的收尾先后读写)。
type ctlCardProgress struct {
	seen         map[string]bool // 已计数的子会话外层工具调用
	toolUses     int
	lastToolName string
	inputTokens  int // 子轮最近一次调用的输入规模
	outputTokens int // 子轮累计输出
	model        string
}

func newCtlCardProgress() ctlCardProgress { return ctlCardProgress{seen: map[string]bool{}} }

// frame 把子会话的一条持久帧翻成卡上的帧:工具调用 / 结果嵌套到 toolCallID 下(注入方按 ID
// 去重),进度变了补一帧,子轮收口帧带来的模型只记一次。
func (p *ctlCardProgress) frame(toolCallID string, n *agentrewire.DurableNotification) []agentruntime.Event {
	switch payload := n.GetPayload().GetPayload().(type) {
	case *agentrewire.RpcNotification_RunResultDone:
		if m := payload.RunResultDone.GetModel(); m != "" && p.model == "" {
			p.model = m
			return []agentruntime.Event{agentruntime.SubagentModel{ToolCallID: toolCallID, Model: m}}
		}
		return nil
	case *agentrewire.RpcNotification_RuntimeEvent, *agentrewire.RpcNotification_AutonomousTurnEvent:
	default:
		return nil
	}
	raw, err := proto.Marshal(&agentrewire.RpcFrame{Body: &agentrewire.RpcFrame_Notification{Notification: n.GetPayload()}})
	if err != nil {
		return nil
	}
	decoded, _, err := protowire.UnmarshalEventNotification(raw)
	if err != nil {
		return nil
	}
	var events []agentruntime.Event
	if nested, ok := ctlbind.NestChildEvent(toolCallID, decoded.Event); ok {
		events = append(events, nested)
	}
	if p.track(decoded.Event) {
		events = append(events, agentruntime.SubagentProgress{ToolCallID: toolCallID, Info: agentruntime.SubagentInfo{
			Origin: agentruntime.SubagentOriginAgrctl, ToolUses: p.toolUses, LastToolName: p.lastToolName,
			TotalTokens: p.inputTokens + p.outputTokens,
		}})
	}
	return events
}

func (p *ctlCardProgress) track(ev agentruntime.Event) bool {
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

func (p *ctlCardProgress) doneInfo(status, finalText string, startedAt time.Time) agentruntime.SubagentInfo {
	return agentruntime.SubagentInfo{
		Origin: agentruntime.SubagentOriginAgrctl, Status: status, FinalText: finalText,
		ToolUses: p.toolUses, TotalTokens: p.inputTokens + p.outputTokens,
		DurationMs: int(time.Since(startedAt).Milliseconds()),
	}
}

// ── 只读的会话端点 ──────────────────────────────────────────────────────────

// serveConsoleRead:会话级 token 的读取、等待(spec Design 18)—— 范围是账号下这台 agentred
// 看得见的全部会话,由它代为查询。形状与桌面端 ctl_svc 的同名端点相同。
func (p *ctlProxy) serveConsoleRead(w http.ResponseWriter, r *http.Request, owner ctlSession, path string, body []byte) {
	ctx := r.Context()
	var req struct {
		ConversationID string `json:"conversationId"`
		Agent          string `json:"agent"`
		AgentID        int64  `json:"agentId"`
		ProjectID      int64  `json:"projectId"`
		Device         string `json:"device"`
		Status         string `json:"status"`
		Mine           bool   `json:"mine"`
		Limit          int    `json:"limit"`
		Last           int    `json:"last"`
		Before         string `json:"before"`
		Full           bool   `json:"full"`
		TimeoutMs      int64  `json:"timeoutMs"`
	}
	if len(body) > 0 {
		if err := json.Unmarshal(body, &req); err != nil {
			writeCtlErr(w, http.StatusBadRequest, "invalid json body")
			return
		}
	}
	id := strings.TrimSpace(req.ConversationID)
	if id == "" && path != ctlConversationsPath && path != ctlPendingPath {
		writeCtlErr(w, http.StatusBadRequest, "conversationId is required")
		return
	}
	var before int64
	if v := strings.TrimSpace(req.Before); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n <= 0 {
			writeCtlErr(w, http.StatusBadRequest, "invalid before cursor "+strconv.Quote(v))
			return
		}
		before = n
	}
	acct, err := p.account()
	if err != nil {
		p.writeAccountErr(w, ctx, owner, err)
		return
	}
	defer acct.Close()
	var out any
	switch path {
	case ctlConversationsPath:
		f := ctlaccount.SessionFilter{Agent: req.Agent, Device: req.Device, Status: req.Status, Mine: req.Mine, Limit: req.Limit}
		if req.AgentID > 0 {
			f.Agent = strconv.FormatInt(req.AgentID, 10)
		}
		if req.ProjectID > 0 {
			f.Project = strconv.FormatInt(req.ProjectID, 10)
		}
		if f.Limit <= 0 {
			f.Limit = ctlDefaultSessionLimit
		}
		rows, lerr := acct.ListSessions(ctx, f)
		if rows == nil {
			rows = []ctlaccount.SessionRow{}
		}
		out, err = map[string]any{"sessions": rows}, lerr
	case ctlConversationPath:
		out, err = acct.GetSession(ctx, id)
	case ctlMessagesPath:
		page, merr := acct.Messages(ctx, id, req.Last, before, req.Full)
		if page.Messages == nil {
			page.Messages = []ctlaccount.MessageRow{}
		}
		out, err = page, merr
	case ctlPendingPath:
		items, perr := acct.Pending(ctx, id)
		if items == nil {
			items = []ctlaccount.PendingItem{}
		}
		out, err = map[string]any{"items": items}, perr
	case ctlWaitPath:
		res, werr := acct.Wait(ctx, id, millis(req.TimeoutMs))
		out, err = ctlWaitReply{ConversationID: id, Status: res.Status, Text: res.Text, Error: res.Error, Pending: res.Pending}, werr
	}
	if err != nil {
		p.writeAccountErr(w, ctx, owner, err)
		return
	}
	writeCtlJSON(w, http.StatusOK, out)
}

// ctlDefaultSessionLimit 是 list sessions 没给 --limit 时的条数(与 agrctl 账号模式相同)。
const ctlDefaultSessionLimit = 50

// ctlWaitReply 是 /ctl/v1/wait 的响应(与桌面端 ctl_svc.waitDTO 同形)。
type ctlWaitReply struct {
	ConversationID string          `json:"conversationId"`
	Status         string          `json:"status"`
	Text           string          `json:"text,omitempty"`
	Error          string          `json:"error,omitempty"`
	Pending        json.RawMessage `json:"pending,omitempty"`
}

// serveConsoleTail 以 NDJSON 跟随一条会话(GET /ctl/v1/tail?conversationId=&from=&untilIdle=true),
// 行的形状与桌面端相同。还没写出任何一行时的失败照常以错误应答。
func (p *ctlProxy) serveConsoleTail(w http.ResponseWriter, r *http.Request, owner ctlSession) {
	ctx := r.Context()
	q := r.URL.Query()
	id := strings.TrimSpace(q.Get("conversationId"))
	if id == "" {
		writeCtlErr(w, http.StatusBadRequest, "conversationId is required")
		return
	}
	untilIdle := q.Get("untilIdle") == "true" || q.Get("untilIdle") == "1"
	acct, err := p.account()
	if err != nil {
		p.writeAccountErr(w, ctx, owner, err)
		return
	}
	defer acct.Close()
	out := &ndjsonWriter{w: w}
	err = acct.Tail(ctx, out, id, q.Get("from"), untilIdle)
	var waiting *ctlaccount.WaitingError
	switch {
	case err == nil, errors.As(err, &waiting):
		if !out.started {
			out.start()
		}
	case !out.started:
		p.writeAccountErr(w, ctx, owner, err)
	default:
		logger.Ctx(ctx).Warn("handlers.ctlProxy.serveConsoleTail: the tail ended early",
			zap.String("conversationId", owner.conversationID), zap.String("tailedConversationId", id), zap.Error(err))
	}
}

// ndjsonWriter 在第一行到来时才写出 200 与流的头,每行之后 flush。
type ndjsonWriter struct {
	w       http.ResponseWriter
	started bool
}

func (n *ndjsonWriter) start() {
	n.started = true
	n.w.Header().Set("Content-Type", "application/x-ndjson")
	n.w.WriteHeader(http.StatusOK)
}

func (n *ndjsonWriter) Write(b []byte) (int, error) {
	if !n.started {
		n.start()
	}
	k, err := n.w.Write(b)
	if f, ok := n.w.(http.Flusher); ok {
		f.Flush()
	}
	return k, err
}

// writeAccountErr 把账号对端的失败换算成与桌面端相同的状态码(agrctl 据此给出同样的退出码):
// 用法错误 422(退出码 2),看不到 404,运行中 409,没登录 503,其余连不上 / 被拒 502。
func (p *ctlProxy) writeAccountErr(w http.ResponseWriter, ctx context.Context, owner ctlSession, err error) {
	var usage *ctlaccount.UsageError
	var ambiguous *ctlaccount.AmbiguousError
	status := http.StatusBadGateway
	switch {
	case isCtlServerUnavailable(err):
		logger.Ctx(ctx).Warn("handlers.ctlProxy: server executor unreachable",
			zap.String("conversationId", owner.conversationID), zap.Error(err))
		writeCtlErr(w, http.StatusServiceUnavailable, "this agentred is not signed in to an Agentre account")
		return
	case errors.As(err, &usage), errors.As(err, &ambiguous):
		status = http.StatusUnprocessableEntity
	case errors.Is(err, ctlaccount.ErrSessionNotVisible):
		status = http.StatusNotFound
	case errors.Is(err, ctlaccount.ErrSessionBusy):
		status = http.StatusConflict
	case errors.Is(err, ctlaccount.ErrCursorGone):
		status = http.StatusGone
	}
	logger.Ctx(ctx).Info("handlers.ctlProxy: the account peer refused",
		zap.String("conversationId", owner.conversationID), zap.Int("status", status), zap.Error(err))
	writeCtlErr(w, status, err.Error())
}

func writeCtlJSON(w http.ResponseWriter, status int, v any) {
	raw, err := json.Marshal(v)
	if err != nil {
		writeCtlErr(w, http.StatusInternalServerError, "encode response failed")
		return
	}
	writeCtlRaw(w, status, raw)
}
