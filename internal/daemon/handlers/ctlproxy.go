package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/cago-frame/cago/pkg/logger"
	"github.com/google/uuid"
	"go.uber.org/zap"
	"google.golang.org/protobuf/encoding/protojson"

	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/runtimes/remote/wire"
	"github.com/agentre-hub/agentre/internal/pkg/agenttool"
	transcriptblocks "github.com/agentre-hub/agentre/internal/pkg/transcript/blocks"
	"github.com/agentre-hub/agentre/internal/pkg/transcript/ctlbind"
	"github.com/agentre-hub/agentre/internal/pkg/tunnelheader"
	"github.com/agentre-hub/agentre/pkg/wire/agentrewire"
	"github.com/agentre-hub/agentre/pkg/wire/devicefp"
)

// ctl 代理(规格 2026-09-22 agrctl-resource-management 决策 4「谁拥有会话,请求就交给
// 谁」):agentred 上 CLI 子进程里的 agrctl 打到本机 gateway 的 /ctl/v1/*,这里凭会话级
// token 认出会话,再按归属转发:
//
//   - 桌面端拥有(runtime.run 带来过桌面端的会话 token):读写都经 runtime.mcpProxy 同一条
//     反向隧道转回发起会话的那台桌面端,Authorization 换成桌面端自己签的 token;审批卡
//     出在桌面端的这个会话里。桌面端那一跳用 http.Client 重放,1xx 会被吞掉,所以写请求
//     的 102 等待行由这里先发(session=<桌面会话 id>),最终应答原样透传;
//   - 控制台拥有:读直连 server(设备 Bearer);写先 `?preview=1` 让 server 算变更清单,
//     在本会话里出一张 toolKey=ctl 的审批卡(落进转录 + 实时推出),批准后才提交 server,
//     拒绝 403、4 分钟无人处理 504。OpenClaw 后端绑在这台 agentred 上时,它的 token 由
//     这里写进本机的设备本地凭据,不交 server(见 ctlproxy_localtoken.go)。
//
// 契约与桌面端执行者一致(ctl_svc):请求是 protojson 的 CtlRequest,失败是 {"error": …}。

const (
	ctlResourcesPath = "/ctl/v1/resources"
	ctlSendPath      = "/ctl/v1/send"
	// serverResourcesPath / serverSendPath 是 server 执行者的设备 Bearer 接口。
	serverResourcesPath = "/v1/ctl/resources"
	serverSendPath      = "/v1/ctl/send"
	// serverDevicesPath 是账号设备列表(与桌面端 server_svc.ListDevices、directCredential-
	// Reconciler.fetchDevices 同一个接口),同样接受这台 agentred 自己的设备 Bearer。
	// planConsoleWrite 用它把 --device 给的名字解析成指纹,判断是不是本机
	// (ctlproxy_localtoken.go 的 selfDeviceMatches)。
	serverDevicesPath = "/v1/devices"
	// ctlApprovalPendingHeader 是挂起等审批前那个 102 里的头,与桌面端
	// ctl_svc.ApprovalPendingHeader、agrctl 的 approvalPendingHeader 同值。
	ctlApprovalPendingHeader = "Agentre-Ctl-Approval"
	// ctlBindToolCallHeader / ctlBindBackgroundHeader 把这里绑定的那条 shell 调用(ID 与是否
	// 后台)带给桌面端(spec 2026-10-27「agrctl send 子代理卡」Remote sessions 第 1 条),与桌面端
	// ctl_svc 的同名常量同值。只由本代理写:调用方自带的同名头在转发前一律去掉。
	ctlBindToolCallHeader   = "Agentre-Ctl-Bind-Tool-Call"
	ctlBindBackgroundHeader = "Agentre-Ctl-Bind-Background"
	// ctlApprovalTimeout 与桌面端会话审批卡一致(spec 决策 12)。
	ctlApprovalTimeout = 4 * time.Minute
	// ctlMaxBody 是请求正文上限:一次写入只是一份资源文档。
	ctlMaxBody = 1 << 20
	// ctlSendBindWait 是 send --wait 在父轮里还找不到那条 shell 调用时,转发前最多等它出现
	// 的时长(awaitCtlSendBinding)。实测 Codex 的 command_execution 比 send 晚约 63ms 开始
	// (运行时验证 V10),2s 留出三十倍的余量盖住本机负载与事件流转;代价只落在始终匹配不上
	// 的 send(命令写法认不出)上,而 --wait 本身要等子会话跑完整整一轮,多出的至多 2s
	// 不改变用户的体感。命中、有歧义、已绑过的 send 都不等。
	ctlSendBindWait = 2 * time.Second
)

// CtlServerPort 把一次 ctl 请求交给 server 执行者:pathAndQuery 是 server 上的路径
// (可带 ?preview=1),返回 server 的状态码与正文。拿不到可用的账号凭据时返回
// ErrCtlServerUnavailable。
type CtlServerPort interface {
	Post(ctx context.Context, pathAndQuery string, body []byte) (status int, respBody []byte, err error)
	// Get 用同一份设备 Bearer 凭据发只读请求(目前只有 serverDevicesPath 用得到)。
	Get(ctx context.Context, pathAndQuery string) (status int, respBody []byte, err error)
}

// CtlProxyDeps 是 ctl 代理的依赖。
type CtlProxyDeps struct {
	Sessions *CtlSessions
	// Tunnel 解出发起会话的那条桌面端连接(daemon.tunnelTargetFor);nil = 不在线。
	Tunnel func(peer devicefp.Initiator, conversationID string) NotifierPort
	Server CtlServerPort
	// ApprovalTimeout 是控制台会话审批卡的挂起上限;0 = 4 分钟。
	ApprovalTimeout time.Duration
	// SendBindWait 是 send --wait 转发前等它那条 shell 调用出现的上限;0 = ctlSendBindWait。
	SendBindWait time.Duration
	// Self 是这台 agentred 的设备指纹:后端的 device_fingerprint 与它相同 = 绑在本机。
	Self devicefp.Carrier
	// OpenClawTokens 是本机的设备本地凭据(BackendCredentialHandlers,按后端 syncId 存
	// OpenClaw token);nil = 不在本地读写 token。
	OpenClawTokens CtlOpenClawTokens
}

// CtlOpenClawTokens 是设备本地凭据里 OpenClaw token 的读写,由 *BackendCredentialHandlers 实现。
type CtlOpenClawTokens interface {
	Status(ctx context.Context, request *agentrewire.BackendCredentialStatusRequest) (*agentrewire.BackendCredentialStatusResponse, error)
	SetOpenClawToken(ctx context.Context, request *agentrewire.OpenClawTokenSetRequest) (*agentrewire.OpenClawTokenSetResponse, error)
}

type ctlProxy struct{ deps CtlProxyDeps }

// NewCtlProxyHandler 返回挂在 daemon gateway /ctl/ 上的 ctl 代理。
func NewCtlProxyHandler(deps CtlProxyDeps) http.Handler {
	if deps.ApprovalTimeout <= 0 {
		deps.ApprovalTimeout = ctlApprovalTimeout
	}
	if deps.SendBindWait <= 0 {
		deps.SendBindWait = ctlSendBindWait
	}
	return &ctlProxy{deps: deps}
}

func (p *ctlProxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	token, _ := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	owner, ok := p.deps.Sessions.resolve(token)
	if !ok {
		writeCtlErr(w, http.StatusUnauthorized, "invalid or unknown session token")
		return
	}
	if r.Method != http.MethodPost {
		writeCtlErr(w, http.StatusMethodNotAllowed, "ctl requires POST")
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, ctlMaxBody))
	if err != nil {
		writeCtlErr(w, http.StatusBadRequest, "read request body failed")
		return
	}
	if owner.hasDesktop {
		p.forwardToDesktop(w, r, owner, body)
		return
	}
	switch r.URL.Path {
	case ctlResourcesPath:
		p.serveConsoleResources(w, r, owner, body)
	case ctlSendPath:
		p.relayServer(w, r.Context(), owner, serverSendPath, body)
	default:
		// 会话级 token 做不了 answer-permission / stop 这类控制(spec「会话级 token」)。
		writeCtlErr(w, http.StatusForbidden, "not allowed with a session token")
	}
}

// ── 桌面端拥有的会话 ─────────────────────────────────────────────────────────

func (p *ctlProxy) forwardToDesktop(w http.ResponseWriter, r *http.Request, owner ctlSession, body []byte) {
	var n NotifierPort
	if p.deps.Tunnel != nil {
		n = p.deps.Tunnel(owner.peer, owner.conversationID)
	}
	if n == nil {
		logger.Ctx(r.Context()).Warn("handlers.ctlProxy.forwardToDesktop: owning desktop offline",
			zap.String("conversationId", owner.conversationID), zap.String("path", r.URL.Path))
		writeCtlErr(w, http.StatusServiceUnavailable, "the Agentre desktop that owns this session is not connected")
		return
	}
	headers := tunnelheader.Sanitize(r.Header)
	headers["Authorization"] = []string{"Bearer " + owner.desktop.Token}
	delete(headers, ctlBindToolCallHeader)
	delete(headers, ctlBindBackgroundHeader)
	if r.URL.Path == ctlResourcesPath && isCtlWrite(body) {
		announceCtlPending(w, fmt.Sprintf("session=%d", owner.desktop.DesktopSessionID))
	}
	var bound string
	if r.URL.Path == ctlSendPath {
		bound = p.bindSend(r.Context(), owner, body, headers)
	}
	var resp wire.MCPProxyResponse
	err := n.Request(r.Context(), wire.MethodMCPProxy, wire.MCPProxyRequest{
		Path: r.URL.RequestURI(), Method: r.Method, Headers: headers, Body: body,
	}, &resp)
	if bound != "" && (err != nil || (resp.Status != 0 && resp.Status != http.StatusOK)) {
		p.deps.Sessions.unbindCtlSend(owner, bound)
	}
	if err != nil {
		logger.Ctx(r.Context()).Warn("handlers.ctlProxy.forwardToDesktop: tunnel failed",
			zap.String("conversationId", owner.conversationID), zap.String("path", r.URL.Path), zap.Error(err))
		writeCtlErr(w, http.StatusBadGateway, "lost the connection to the Agentre desktop that owns this session")
		return
	}
	for k, vs := range resp.Headers {
		for _, v := range vs {
			w.Header().Add(k, v)
		}
	}
	if resp.Status == 0 {
		resp.Status = http.StatusOK
	}
	w.WriteHeader(resp.Status)
	_, _ = w.Write(resp.Body)
}

// ctlSendBody 是 send 请求里绑定要看的那几项(与桌面端 ctl_svc.sendRequest 同形)。
type ctlSendBody struct {
	SessionID int64  `json:"sessionId"`
	Agent     string `json:"agent"`
	AgentID   int64  `json:"agentId"`
	Text      string `json:"text"`
	Wait      bool   `json:"wait"`
}

// bindSend 在转发一次 send 之前,于这条会话当前这一轮里匹配发出它的那条外层 shell 调用
// (spec 2026-10-27「agrctl send 子代理卡」Remote sessions 第 1 条、Design 12:父轮的宿主
// 负责绑定),绑上就把调用 ID 与是否后台写进转给桌面端的请求头,交回被占住的调用 ID。
// 那条调用还没出现时至多等 SendBindWait(见 awaitCtlSendBinding)。
// 不绑定时记一条 info 说明原因并交回空串 —— 桌面端照今天的流程执行,父转录不变。
// 只在桌面端拥有的路径上调用:控制台拥有的会话不绑(Design 4)。
func (p *ctlProxy) bindSend(ctx context.Context, owner ctlSession, body []byte, headers map[string][]string) string {
	notBound := func(reason string) string {
		logger.Ctx(ctx).Info("handlers.ctlProxy.forwardToDesktop: subagent card not bound",
			zap.String("conversationId", owner.conversationID), zap.String("reason", reason))
		return ""
	}
	var req ctlSendBody
	if err := json.Unmarshal(body, &req); err != nil {
		return notBound("invalid_body") // 桌面端会照样以 400 拒绝它
	}
	switch {
	case !req.Wait:
		return notBound(ctlSendNoWait)
	case req.SessionID > 0:
		return notBound(ctlSendExistingSession)
	}
	target := ctlbind.Target{Task: req.Text}
	if req.AgentID > 0 {
		target.AgentID = req.AgentID
	} else {
		target.AgentName = strings.TrimSpace(req.Agent)
	}
	binding, reason := p.deps.Sessions.awaitCtlSendBinding(ctx, owner, target, p.deps.SendBindWait)
	if reason != "" {
		return notBound(reason)
	}
	headers[ctlBindToolCallHeader] = []string{binding.ToolCallID}
	if binding.Background {
		headers[ctlBindBackgroundHeader] = []string{"true"}
	}
	logger.Ctx(ctx).Info("handlers.ctlProxy.forwardToDesktop: subagent card bound",
		zap.String("conversationId", owner.conversationID),
		zap.String("toolCallId", binding.ToolCallID),
		zap.Bool("background", binding.Background))
	return binding.ToolCallID
}

// isCtlWrite 判断请求是不是一次写入(要审批、要发等待行)。解不开的正文交给执行者报错。
func isCtlWrite(body []byte) bool {
	var req agentrewire.CtlRequest
	if err := (protojson.UnmarshalOptions{DiscardUnknown: true}).Unmarshal(body, &req); err != nil {
		return false
	}
	return req.GetWrite() != nil
}

// ── 控制台拥有的会话 ─────────────────────────────────────────────────────────

func (p *ctlProxy) serveConsoleResources(w http.ResponseWriter, r *http.Request, owner ctlSession, body []byte) {
	var req agentrewire.CtlRequest
	if err := (protojson.UnmarshalOptions{DiscardUnknown: true}).Unmarshal(body, &req); err != nil {
		writeCtlErr(w, http.StatusBadRequest, "invalid request body")
		return
	}
	write := req.GetWrite()
	if write == nil {
		p.relayConsoleRead(w, r.Context(), owner, &req, body)
		return
	}
	if owner.turn == nil {
		writeCtlErr(w, http.StatusConflict, "cannot ask for approval in this session: no turn is running")
		return
	}
	p.approveConsoleWrite(w, r, owner, p.planConsoleWrite(r.Context(), write, body))
}

// approveConsoleWrite:预览(server 算变更清单,本地 token 只标 secret)→ 本会话审批卡 →
// 批准后提交 server,再写本地 token。
func (p *ctlProxy) approveConsoleWrite(w http.ResponseWriter, r *http.Request, owner ctlSession, plan consoleWritePlan) {
	ctx := r.Context()
	write := plan.req
	preview, ok := p.previewConsoleWrite(w, ctx, owner, plan)
	if !ok {
		return
	}
	requestID := uuid.NewString()
	ch := p.deps.Sessions.beginWait(owner.conversationID, requestID, owner.turn.ended())
	sessionID, err := owner.turn.beginApproval(ctx, &transcriptblocks.ToolApprovalBlock{
		ToolKey:   agenttool.KeyCtl,
		RequestID: requestID,
		ToolName:  "ctl_" + transcriptblocks.CtlOpName(write.GetOp()) + "_" + transcriptblocks.CtlKindNames[write.GetKind()],
		ToolInput: ctlApprovalInput(transcriptblocks.RedactCtlCommand(write), preview.GetChanges()).ToolInput(),
		Status:    "pending",
	})
	if err != nil {
		p.deps.Sessions.endWait(requestID)
		writeCtlErr(w, http.StatusConflict, "cannot ask for approval in this session: "+err.Error())
		return
	}
	log := logger.Ctx(ctx).With(zap.String("conversationId", owner.conversationID),
		zap.Int64("sessionId", sessionID), zap.String("requestId", requestID),
		zap.String("op", write.GetOp().String()), zap.String("kind", write.GetKind().String()))
	log.Info("handlers.ctlProxy.approveConsoleWrite: awaiting approval")
	announceCtlPending(w, fmt.Sprintf("session=%d", sessionID))

	allow, answered := p.awaitAnswer(ctx, ch, requestID, owner.turn.ended())
	switch {
	case !answered && isClosed(owner.turn.ended()):
		// 这一轮先收口了:卡已随收口记成 expired,不再提交。
		log.Info("handlers.ctlProxy.approveConsoleWrite: turn ended before an answer")
		writeCtlErr(w, http.StatusConflict, "the turn ended before the approval was answered")
	case !answered && ctx.Err() != nil:
		// agrctl 走了(被杀 / 轮次中止):卡就此作废,请求 ctx 已死,落库用新的。
		owner.turn.resolveApproval(context.WithoutCancel(ctx), requestID, "expired", "")
		log.Info("handlers.ctlProxy.approveConsoleWrite: caller gone")
	case !answered:
		owner.turn.resolveApproval(ctx, requestID, "expired", "")
		log.Info("handlers.ctlProxy.approveConsoleWrite: approval timed out")
		writeCtlErr(w, http.StatusGatewayTimeout, "approval timed out")
	case !allow:
		owner.turn.resolveApproval(ctx, requestID, "denied", "")
		log.Info("handlers.ctlProxy.approveConsoleWrite: rejected")
		writeCtlErr(w, http.StatusForbidden, fmt.Sprintf("rejected in session #%d", sessionID))
	default:
		// 人已经批准了:之后 agrctl 断开也把写入做完、把结果落到卡上(ctx 脱离请求的取消)。
		ctx := context.WithoutCancel(ctx)
		var raw []byte
		if plan.serverBody != nil {
			status, body, err := p.postServer(ctx, serverResourcesPath, plan.serverBody)
			switch {
			case err != nil:
				owner.turn.resolveApproval(ctx, requestID, "approved", "执行失败："+err.Error())
				p.writeServerErr(w, ctx, owner, err)
				return
			case status != http.StatusOK:
				owner.turn.resolveApproval(ctx, requestID, "approved", "执行失败："+ctlErrMessage(body))
				log.Warn("handlers.ctlProxy.approveConsoleWrite: server refused the write", zap.Int("status", status))
				writeCtlRaw(w, status, body)
				return
			}
			raw = body
		}
		if plan.local != nil {
			if err := p.saveLocalToken(ctx, plan.local, raw); err != nil {
				msg := "could not save the openclaw gateway secret on this agentred"
				if raw != nil {
					// 其它字段已经交 server 写好了:如实说是部分成功。
					msg = "the other changes were saved, but " + msg
				}
				owner.turn.resolveApproval(ctx, requestID, "approved", "执行失败："+msg)
				writeCtlErr(w, http.StatusInternalServerError, msg)
				return
			}
		}
		if raw == nil {
			raw, _ = protojson.Marshal(&agentrewire.CtlResponse{Result: &agentrewire.CtlResponse_Write{Write: preview}})
		}
		owner.turn.resolveApproval(ctx, requestID, "approved", ctlResultText(raw, preview))
		log.Info("handlers.ctlProxy.approveConsoleWrite: written")
		writeCtlRaw(w, http.StatusOK, raw)
	}
}

// previewConsoleWrite 取审批卡要展示的变更清单:要交 server 的部分由 server 预览算,本地
// 写入的 token 只补一行 secret。失败时已经写好应答,返回 false。
func (p *ctlProxy) previewConsoleWrite(w http.ResponseWriter, ctx context.Context, owner ctlSession, plan consoleWritePlan) (*agentrewire.CtlWriteResponse, bool) {
	var preview *agentrewire.CtlWriteResponse
	if plan.serverBody != nil {
		status, raw, err := p.postServer(ctx, serverResourcesPath+"?preview=1", plan.serverBody)
		if err != nil {
			p.writeServerErr(w, ctx, owner, err)
			return nil, false
		}
		if status != http.StatusOK {
			writeCtlRaw(w, status, raw)
			return nil, false
		}
		var resp agentrewire.CtlResponse
		if err := (protojson.UnmarshalOptions{DiscardUnknown: true}).Unmarshal(raw, &resp); err != nil || resp.GetWrite() == nil {
			writeCtlErr(w, http.StatusBadGateway, "the Agentre server answered the preview with an unreadable change list")
			return nil, false
		}
		preview = resp.GetWrite()
	}
	if plan.local != nil {
		preview = plan.local.markChanged(preview)
	}
	return preview, true
}

// awaitAnswer 等作答到超时、调用方断开或这一轮收口。没等到时撤下卡,再收一次:作答可能
// 恰好在撤下之前投进了 channel —— 控制台已经收到「成功」,这一票不能丢。收口的那一轮
// 不在此列:它的卡已经记成 expired,收口之后的作答 AnswerToolApproval 一律不收。
func (p *ctlProxy) awaitAnswer(ctx context.Context, ch <-chan bool, requestID string, ended <-chan struct{}) (allow, answered bool) {
	timer := time.NewTimer(p.deps.ApprovalTimeout)
	defer timer.Stop()
	select {
	case allow = <-ch:
		return allow, true
	case <-timer.C:
	case <-ctx.Done():
	case <-ended:
		p.deps.Sessions.endWait(requestID)
		return false, false
	}
	p.deps.Sessions.endWait(requestID)
	select {
	case allow = <-ch:
		return allow, true
	default:
		return false, false
	}
}

// relayConsoleRead 把读交给 server;读后端时,绑在本机的 openclaw 后端的 token 状态由
// 本机的设备本地凭据补上(server 看不到它,只会报 unknown)。
func (p *ctlProxy) relayConsoleRead(w http.ResponseWriter, ctx context.Context, owner ctlSession, req *agentrewire.CtlRequest, body []byte) {
	status, raw, err := p.postServer(ctx, serverResourcesPath, body)
	if err != nil {
		p.writeServerErr(w, ctx, owner, err)
		return
	}
	if status == http.StatusOK && (req.GetList().GetKind() == agentrewire.CtlKind_CTL_KIND_BACKEND || req.GetGet().GetKind() == agentrewire.CtlKind_CTL_KIND_BACKEND) {
		raw = p.overlayLocalTokenState(ctx, raw)
	}
	writeCtlRaw(w, status, raw)
}

func (p *ctlProxy) relayServer(w http.ResponseWriter, ctx context.Context, owner ctlSession, path string, body []byte) {
	status, raw, err := p.postServer(ctx, path, body)
	if err != nil {
		p.writeServerErr(w, ctx, owner, err)
		return
	}
	writeCtlRaw(w, status, raw)
}

func (p *ctlProxy) postServer(ctx context.Context, path string, body []byte) (int, []byte, error) {
	if p.deps.Server == nil {
		return 0, nil, ErrCtlServerUnavailable
	}
	return p.deps.Server.Post(ctx, path, body)
}

func (p *ctlProxy) getServer(ctx context.Context, path string) (int, []byte, error) {
	if p.deps.Server == nil {
		return 0, nil, ErrCtlServerUnavailable
	}
	return p.deps.Server.Get(ctx, path)
}

func (p *ctlProxy) writeServerErr(w http.ResponseWriter, ctx context.Context, owner ctlSession, err error) {
	logger.Ctx(ctx).Warn("handlers.ctlProxy: server executor unreachable",
		zap.String("conversationId", owner.conversationID), zap.Error(err))
	if isCtlServerUnavailable(err) {
		writeCtlErr(w, http.StatusServiceUnavailable, "this agentred is not signed in to an Agentre account")
		return
	}
	writeCtlErr(w, http.StatusBadGateway, "cannot reach the Agentre server")
}

// ── 审批卡内容 ───────────────────────────────────────────────────────────────

// ctlApprovalInput 把 server 算出的变更清单转成审批卡的 ToolInput(前后值不取自客户端)。
// 级联数量只拿得到 server 的附注,按约定句式还原。
func ctlApprovalInput(command string, changes []*agentrewire.CtlChange) transcriptblocks.CtlApprovalInput {
	in := transcriptblocks.CtlApprovalInput{Command: command}
	for _, c := range changes {
		in.Changes = append(in.Changes, transcriptblocks.NewCtlApprovalChange(c, transcriptblocks.ParseCtlCascadeNote(c.GetNote())))
	}
	return in
}

// ctlResultText 是批准后卡上的结果行。create 的新 id 取自 server 的写入应答;读不出来时
// 退回预览里的那条变更。
func ctlResultText(raw []byte, preview *agentrewire.CtlWriteResponse) string {
	change := firstChange(preview)
	var resp agentrewire.CtlResponse
	if err := (protojson.UnmarshalOptions{DiscardUnknown: true}).Unmarshal(raw, &resp); err == nil {
		if c := firstChange(resp.GetWrite()); c != nil {
			change = c
		}
	}
	return transcriptblocks.CtlResultText(change)
}

func firstChange(resp *agentrewire.CtlWriteResponse) *agentrewire.CtlChange {
	if changes := resp.GetChanges(); len(changes) > 0 {
		return changes[0]
	}
	return nil
}

// ── HTTP 小工具 ──────────────────────────────────────────────────────────────

// announceCtlPending 在挂起等审批前先发 102,告诉 agrctl 去哪里批准;最终响应不带这个头。
func announceCtlPending(w http.ResponseWriter, where string) {
	w.Header().Set(ctlApprovalPendingHeader, where)
	w.WriteHeader(http.StatusProcessing)
	w.Header().Del(ctlApprovalPendingHeader)
}

func writeCtlErr(w http.ResponseWriter, status int, msg string) {
	raw, _ := json.Marshal(map[string]string{"error": msg})
	writeCtlRaw(w, status, raw)
}

func writeCtlRaw(w http.ResponseWriter, status int, raw []byte) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(raw)
}

// ctlErrMessage 从 {"error": "..."} 里取消息,取不到就回原文。
func ctlErrMessage(raw []byte) string {
	var e struct {
		Error string `json:"error"`
	}
	if json.Unmarshal(raw, &e) == nil && e.Error != "" {
		return e.Error
	}
	return strings.TrimSpace(string(raw))
}
