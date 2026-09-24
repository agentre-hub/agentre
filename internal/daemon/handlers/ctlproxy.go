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
	// ctlApprovalPendingHeader 是挂起等审批前那个 102 里的头,与桌面端
	// ctl_svc.ApprovalPendingHeader、agrctl 的 approvalPendingHeader 同值。
	ctlApprovalPendingHeader = "Agentre-Ctl-Approval"
	// ctlApprovalTimeout 与桌面端会话审批卡一致(spec 决策 12)。
	ctlApprovalTimeout = 4 * time.Minute
	// ctlMaxBody 是请求正文上限:一次写入只是一份资源文档。
	ctlMaxBody = 1 << 20
)

// CtlServerPort 把一次 ctl 请求交给 server 执行者:pathAndQuery 是 server 上的路径
// (可带 ?preview=1),返回 server 的状态码与正文。拿不到可用的账号凭据时返回
// ErrCtlServerUnavailable。
type CtlServerPort interface {
	Post(ctx context.Context, pathAndQuery string, body []byte) (status int, respBody []byte, err error)
}

// CtlProxyDeps 是 ctl 代理的依赖。
type CtlProxyDeps struct {
	Sessions *CtlSessions
	// Tunnel 解出发起会话的那条桌面端连接(daemon.tunnelTargetFor);nil = 不在线。
	Tunnel func(peer devicefp.Initiator, conversationID string) NotifierPort
	Server CtlServerPort
	// ApprovalTimeout 是控制台会话审批卡的挂起上限;0 = 4 分钟。
	ApprovalTimeout time.Duration
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
	if r.URL.Path == ctlResourcesPath && isCtlWrite(body) {
		announceCtlPending(w, fmt.Sprintf("session=%d", owner.desktop.DesktopSessionID))
	}
	var resp wire.MCPProxyResponse
	err := n.Request(r.Context(), wire.MethodMCPProxy, wire.MCPProxyRequest{
		Path: r.URL.RequestURI(), Method: r.Method, Headers: headers, Body: body,
	}, &resp)
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
	ch := p.deps.Sessions.beginWait(owner.conversationID, requestID)
	sessionID, err := owner.turn.beginApproval(ctx, &transcriptblocks.ToolApprovalBlock{
		ToolKey:   agenttool.KeyCtl,
		RequestID: requestID,
		ToolName:  "ctl_" + ctlOpName(write.GetOp()) + "_" + ctlKindNames[write.GetKind()],
		ToolInput: ctlApprovalInput(redactCtlCommand(write), preview.GetChanges()).ToolInput(),
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

	allow, answered := p.awaitAnswer(ctx, ch, requestID)
	switch {
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

// awaitAnswer 等作答到超时或调用方断开。没等到时撤下卡,再收一次:作答可能恰好在撤下
// 之前投进了 channel —— 控制台已经收到「成功」,这一票不能丢。
func (p *ctlProxy) awaitAnswer(ctx context.Context, ch <-chan bool, requestID string) (allow, answered bool) {
	timer := time.NewTimer(p.deps.ApprovalTimeout)
	defer timer.Stop()
	select {
	case allow = <-ch:
		return allow, true
	case <-timer.C:
	case <-ctx.Done():
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

// ctlKindNames 是审批卡与结果行里的资源名,与 agrctl / 桌面端执行者一致。
var ctlKindNames = map[agentrewire.CtlKind]string{
	agentrewire.CtlKind_CTL_KIND_AGENT:      "agent",
	agentrewire.CtlKind_CTL_KIND_DEPARTMENT: "department",
	agentrewire.CtlKind_CTL_KIND_PROJECT:    "project",
	agentrewire.CtlKind_CTL_KIND_PROVIDER:   "provider",
	agentrewire.CtlKind_CTL_KIND_MODEL:      "model",
	agentrewire.CtlKind_CTL_KIND_BACKEND:    "backend",
}

func ctlOpName(op agentrewire.CtlOp) string {
	switch op {
	case agentrewire.CtlOp_CTL_OP_CREATE:
		return "create"
	case agentrewire.CtlOp_CTL_OP_UPDATE:
		return "update"
	case agentrewire.CtlOp_CTL_OP_DELETE:
		return "delete"
	}
	return op.String()
}

// redactCtlCommand 不信客户端已经脱敏:请求里带的密钥明文若出现在命令行里,一律换成 …。
func redactCtlCommand(req *agentrewire.CtlWriteRequest) string {
	command := req.GetCommand()
	for _, secret := range []string{req.GetResource().GetProvider().GetApiKey(), req.GetResource().GetBackend().GetToken()} {
		if secret != "" {
			command = strings.ReplaceAll(command, secret, "…")
		}
	}
	return command
}

// ctlApprovalInput 把 server 算出的变更清单转成审批卡的 ToolInput(前后值不取自客户端)。
func ctlApprovalInput(command string, changes []*agentrewire.CtlChange) transcriptblocks.CtlApprovalInput {
	in := transcriptblocks.CtlApprovalInput{Command: command}
	for _, c := range changes {
		ch := transcriptblocks.CtlApprovalChange{Op: ctlOpName(c.GetOp()), Kind: ctlKindNames[c.GetKind()], ID: c.GetId(), Name: c.GetName(),
			Cascade: transcriptblocks.ParseCtlCascadeNote(c.GetNote())}
		for _, f := range c.GetFields() {
			ch.Fields = append(ch.Fields, transcriptblocks.CtlApprovalField{Field: f.GetField(), Before: f.Before, After: f.After, Secret: f.GetSecret()})
		}
		in.Changes = append(in.Changes, ch)
	}
	return in
}

// ctlResultText 是批准后卡上的结果行(与桌面端执行者同一组措辞)。create 的新 id 取自
// server 的写入应答;读不出来时退回预览里的那条变更。
func ctlResultText(raw []byte, preview *agentrewire.CtlWriteResponse) string {
	change := firstChange(preview)
	var resp agentrewire.CtlResponse
	if err := (protojson.UnmarshalOptions{DiscardUnknown: true}).Unmarshal(raw, &resp); err == nil {
		if c := firstChange(resp.GetWrite()); c != nil {
			change = c
		}
	}
	if change == nil {
		return ""
	}
	subject := ctlKindNames[change.GetKind()] + " " + change.GetName()
	switch change.GetOp() {
	case agentrewire.CtlOp_CTL_OP_CREATE:
		return fmt.Sprintf("已创建 %s（id %d）", subject, change.GetId())
	case agentrewire.CtlOp_CTL_OP_UPDATE:
		return "已更新 " + subject
	default:
		return "已删除 " + subject
	}
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
