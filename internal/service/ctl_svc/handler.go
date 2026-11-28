package ctl_svc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"

	"github.com/agentre-hub/agentre/internal/pkg/code"
	"github.com/agentre-hub/agentre/internal/pkg/ctlaccount"
	"github.com/agentre-hub/agentre/internal/service/chat_svc"
	"github.com/agentre-hub/agentre/pkg/wire/devicefp"
)

// routePrefix 控制 API 的版本化前缀；gateway 已把 /ctl/* 全部转到本 handler。
const routePrefix = "/ctl/v1/"

// ctlHandler 是 /ctl/* 的 HTTP handler：先过 bearer 鉴权，再按路径分发。
type ctlHandler struct {
	// token 是握手 token（全权）；sessions 校验会话级 token（读 + send）。
	token     string
	sessions  SessionTokenVerifier
	agents    AgentGateway
	projects  ProjectGateway
	chat      ChatGateway
	resources Resources
	// conversations 按 conversation_id 定位与操作会话(nil = 未就绪,会话端点 503)。
	conversations Conversations
	// pollInterval 是 wait / tail 复查会话运行态的间隔(0 = defaultPollInterval)。
	pollInterval time.Duration
	// cards 把 send --wait 的子会话升级成调用方转录里的子代理卡(nil = 不升级)。
	cards SubagentCards
	// dispatches 登记会话间的派发关系(nil = 不登记),见 dispatch.go。
	dispatches chat_svc.DispatchRecorder
	// approvals 在会话里出审批卡；external 是桌面端弹窗的待审批队列（nil = 不可用）。
	approvals       SessionApprovals
	external        ExternalApprovals
	approvalTimeout time.Duration
}

func (h *ctlHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	cred, ok := h.authenticate(r)
	if !ok {
		writeErr(w, http.StatusUnauthorized, "invalid control token")
		return
	}
	route := strings.TrimPrefix(r.URL.Path, routePrefix)
	if cred.session && !sessionRoutes[route] {
		writeErr(w, http.StatusForbidden, "a session token cannot call "+route)
		return
	}
	switch route {
	case "resources":
		h.serveResources(w, r, cred)
	case "provider-test":
		h.serveProviderTest(w, r)
	case "provider-discover":
		h.serveProviderDiscover(w, r)
	case "agents":
		h.serveAgents(w, r)
	case "projects":
		h.serveProjects(w, r)
	case "sessions":
		h.serveSessions(w, r)
	case "send":
		h.serveSend(w, r, cred)
	case "stop":
		h.serveStop(w, r)
	case "answer-permission":
		h.serveAnswerPermission(w, r)
	case "stream":
		h.serveStream(w, r)
	case "wait":
		h.serveWait(w, r)
	case "tail":
		h.serveTail(w, r)
	case "conversations":
		h.serveConversations(w, r)
	case "conversation":
		h.serveConversation(w, r)
	case "messages":
		h.serveMessages(w, r)
	case "pending":
		h.servePending(w, r)
	case "answer":
		h.serveAnswer(w, r)
	default:
		writeErr(w, http.StatusNotFound, "unknown control endpoint")
	}
}

type agentDTO struct {
	ID           int64  `json:"id"`
	Name         string `json:"name"`
	Description  string `json:"description,omitempty"`
	SystemBadge  string `json:"systemBadge,omitempty"`
	DepartmentID int64  `json:"departmentId,omitempty"`
}

func (h *ctlHandler) serveAgents(w http.ResponseWriter, r *http.Request) {
	if h.agents == nil {
		writeErr(w, http.StatusServiceUnavailable, "control service not ready")
		return
	}
	list, err := h.agents.List(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := make([]agentDTO, 0, len(list))
	for _, a := range list {
		if a == nil {
			continue
		}
		out = append(out, agentDTO{
			ID:           a.ID,
			Name:         a.Name,
			Description:  a.Description,
			SystemBadge:  a.SystemBadge,
			DepartmentID: a.DepartmentID,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"agents": out})
}

func (h *ctlHandler) serveProjects(w http.ResponseWriter, r *http.Request) {
	if h.projects == nil {
		writeErr(w, http.StatusServiceUnavailable, "control service not ready")
		return
	}
	list, err := h.projects.List(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if list == nil {
		list = []ProjectInfo{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"projects": list})
}

type sendRequest struct {
	// ConversationID 非空 = 继续这条会话(不建新会话);空 = 按 agent/agentId 建新会话。
	ConversationID string      `json:"conversationId"`
	Agent          string      `json:"agent"`            // 目标 agent 名称(与 agentId 二选一;纯数字时也按 id 找)
	AgentID        int64       `json:"agentId"`          // 目标 agent id(优先于 agent 名称)
	ProjectID      int64       `json:"projectId"`        // 可选：0 = 自由会话
	Title          string      `json:"title,omitempty"`  // 新会话标题;空 = "ctl: <agent>"
	Text           string      `json:"text"`             // 任务内容
	Images         []sendImage `json:"images,omitempty"` // 附件图片(name + data URL)
	// Steer=true:会话正在跑时把 Text 插进当前这一轮;不在跑时按普通继续处理。
	Steer bool `json:"steer,omitempty"`
	// Wait=true 阻塞到会话空闲、进入等待输入或 TimeoutMs 到期(0 = 不超时),同 /wait。
	Wait      bool  `json:"wait"`
	TimeoutMs int64 `json:"timeoutMs,omitempty"`
	Isolated  bool  `json:"isolated"` // true = 一次性隔离会话(不进侧栏)；默认普通可见会话
	// 本会话设置的覆盖(自下一轮起生效);空串 = 不覆盖。
	Model          string `json:"model,omitempty"`
	CLIModel       string `json:"cliModel,omitempty"`
	Effort         string `json:"effort,omitempty"`
	PermissionMode string `json:"permissionMode,omitempty"`
	// 新会话在哪跑(都不给 = 按派发规则选设备、用成员生效的引擎配置):Device 是设备名或
	// 指纹(本机也算一台,「本机」「local」都认),Engine 是本会话换用的引擎配置的名字或 id。
	Device string `json:"device,omitempty"`
	Engine string `json:"engine,omitempty"`
}

func (r sendRequest) continuing() bool { return strings.TrimSpace(r.ConversationID) != "" }

func (r sendRequest) overrides() chat_svc.CtlOverrides {
	return chat_svc.CtlOverrides{
		Model: strings.TrimSpace(r.Model), CLIModel: strings.TrimSpace(r.CLIModel),
		Effort: strings.TrimSpace(r.Effort), PermissionMode: strings.TrimSpace(r.PermissionMode),
	}
}

// sendImage 是控制 API 收下的图片附件形状,与 chat_svc.SendImage 同形但刻意不复用
// 那个类型:两个包的 JSON 契约各自独立,避免 ctl 的 wire 形状被 chat 域内部类型绑架。
type sendImage struct {
	Name    string `json:"name,omitempty"`
	DataURL string `json:"dataUrl"`
}

// sendDTO 是 /ctl/v1/send 的响应。不等待时 Status 恒为 running;等待时与 /wait 同形。
type sendDTO struct {
	ConversationID     string `json:"conversationId"`
	Agent              string `json:"agent,omitempty"`
	Device             string `json:"device,omitempty"` // 执行设备名;空 = 本机桌面
	AssistantMessageID int64  `json:"assistantMessageId,omitempty"`
	Stream             string `json:"stream,omitempty"`
	Steered            bool   `json:"steered,omitempty"`
	QueuedID           string `json:"queuedId,omitempty"`
	outcomeDTO
	Done bool `json:"done"`
}

// serveSend 派发一次任务:新建会话,或按 conversationId 继续一条会话。cred 决定会话级
// token 的「不能向自己所在的会话发送」,以及要不要把子会话升级成调用方转录里的子代理卡
// (见 watchSubagentCard)。
func (h *ctlHandler) serveSend(w http.ResponseWriter, r *http.Request, cred credential) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "send requires POST")
		return
	}
	if h.agents == nil || h.chat == nil || h.conversations == nil {
		writeErr(w, http.StatusServiceUnavailable, "control service not ready")
		return
	}
	var req sendRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json body")
		return
	}
	if strings.TrimSpace(req.Text) == "" {
		writeErr(w, http.StatusBadRequest, "text is required")
		return
	}
	if req.continuing() && (req.Agent != "" || req.AgentID > 0 || req.ProjectID > 0 || req.Isolated || req.Title != "" ||
		req.Device != "" || req.Engine != "") {
		writeErr(w, http.StatusBadRequest, "a session (conversationId) cannot be combined with agent, project, isolated, title, device or engine")
		return
	}
	if req.Steer && len(req.Images) > 0 {
		writeErr(w, http.StatusBadRequest, "--steer cannot carry images")
		return
	}
	ctx := r.Context()
	overrides := req.overrides()

	if req.continuing() {
		c, ok := h.lookupConversation(w, r, req.ConversationID)
		if !ok {
			return
		}
		if cred.session && c.SessionID == cred.ref.SessionID {
			writeErr(w, http.StatusBadRequest, "cannot send to your own session")
			return
		}
		if err := h.conversations.CheckCtlOverrides(ctx, 0, 0, c.SessionID, overrides); err != nil {
			writeOverrideErr(w, err)
			return
		}
		if isActive(c.Status) && !req.Steer {
			writeErr(w, http.StatusConflict, errSessionRunning)
			return
		}
		// 覆盖对之后的轮次生效:插话也先写上,下一轮(含插话补发的那一轮)就用新设置。
		if err := h.conversations.ApplyCtlOverrides(ctx, c.SessionID, overrides); err != nil {
			writeOverrideErr(w, err)
			return
		}
		if isActive(c.Status) && h.steer(w, r, req, c) {
			return
		}
		// 不在跑,或插话没排上(这一轮恰好收口了):按普通继续处理。
		h.dispatch(w, r, cred, req, c)
		return
	}
	a, err := h.resolveAgent(r, req.Agent, req.AgentID)
	if err != nil {
		writeErr(w, http.StatusNotFound, err.Error())
		return
	}
	target, ok := h.pickDispatch(w, r, req, a)
	if !ok {
		return
	}
	if err := h.conversations.CheckCtlOverrides(ctx, a.ID, target.engineProfileID, 0, overrides); err != nil {
		writeOverrideErr(w, err)
		return
	}
	c, ok := h.createConversation(w, r, req, a)
	if !ok {
		return
	}
	// 钉在首轮之前:Send 此后直接用钉住的设备与引擎配置,不再重新派发(会话不能换设备)。
	if err := h.conversations.PinCtlDispatch(ctx, c.SessionID, target.engineProfileID, target.device); err != nil {
		writeErr(w, http.StatusInternalServerError, "pin the dispatched device failed: "+err.Error())
		return
	}
	if err := h.conversations.ApplyCtlOverrides(ctx, c.SessionID, overrides); err != nil {
		writeOverrideErr(w, err)
		return
	}
	h.dispatch(w, r, cred, req, c)
}

// dispatchTarget 是新会话派发的结果:用哪份引擎配置、跑在哪台设备上。
type dispatchTarget struct {
	engineProfileID int64
	device          devicefp.Carrier
}

// pickDispatch 按共享派发函数为新会话挑设备与引擎配置(spec「agrctl」):--engine 点名的引擎
// 配置不存在是用法错误(422,列出可选的);--device 不是参与派发的设备是用法错误(422,列出
// 可选的),是但规则不通过报它的原因(409);没给 --device 时取规则选中的那台,没有任何设备
// 通过时逐台列出原因(409)。失败时已写好响应并交回 false。
func (h *ctlHandler) pickDispatch(w http.ResponseWriter, r *http.Request, req sendRequest, a agentResolved) (dispatchTarget, bool) {
	ctx := r.Context()
	var engineID int64
	if ref := strings.TrimSpace(req.Engine); ref != "" {
		engine, err := h.conversations.ResolveCtlEngine(ctx, ref)
		var notFound *chat_svc.CtlEngineNotFoundError
		switch {
		case errors.As(err, &notFound):
			writeErr(w, http.StatusUnprocessableEntity, notFound.Error())
			return dispatchTarget{}, false
		case err != nil:
			writeErr(w, http.StatusInternalServerError, "resolve the engine failed: "+err.Error())
			return dispatchTarget{}, false
		}
		engineID = engine.ID
	}
	plan, err := h.conversations.CtlDispatchPlan(ctx, a.ID, req.ProjectID, engineID)
	if err != nil || plan == nil {
		writeErr(w, http.StatusInternalServerError, "plan the dispatch failed")
		return dispatchTarget{}, false
	}
	target := dispatchTarget{engineProfileID: plan.EngineProfileID, device: plan.Chosen}
	if ref := strings.TrimSpace(req.Device); ref != "" {
		var options []string
		var chosen *chat_svc.CtlDevice
		for i := range plan.Devices {
			d := &plan.Devices[i]
			options = append(options, d.Name)
			if chosen == nil && slices.Contains(d.Refs, ref) {
				chosen = d
			}
		}
		if chosen == nil {
			writeErr(w, http.StatusUnprocessableEntity, ctlaccount.UnknownDeviceMessage(ref, a.Name, options))
			return dispatchTarget{}, false
		}
		if !chosen.Eligible {
			writeErr(w, http.StatusConflict, ctlaccount.RequestedDeviceMessage(a.Name, verdictOf(*chosen)))
			return dispatchTarget{}, false
		}
		target.device = chosen.Fingerprint
		return target, true
	}
	if !plan.Found {
		verdicts := make([]ctlaccount.DeviceVerdict, 0, len(plan.Devices))
		for _, d := range plan.Devices {
			verdicts = append(verdicts, verdictOf(d))
		}
		writeErr(w, http.StatusConflict, ctlaccount.NowhereToRunMessage(a.Name, verdicts))
		return dispatchTarget{}, false
	}
	return target, true
}

// verdictOf 把一台设备的不通过原因交给命令行的措辞(与账号模式同一份)。
func verdictOf(d chat_svc.CtlDevice) ctlaccount.DeviceVerdict {
	v := ctlaccount.DeviceVerdict{Name: d.Name}
	if r := d.Reason; r != nil {
		v.Reason = ctlaccount.Reason{Code: r.Code, LastSeen: r.LastSeen, Runtime: r.Runtime, Labels: r.Labels,
			Project: r.Project, Provider: r.Provider, Engine: r.Engine}
	}
	return v
}

// createConversation 为一次新派发建会话并带回它的对话身份。
func (h *ctlHandler) createConversation(
	w http.ResponseWriter, r *http.Request, req sendRequest, a agentResolved,
) (*chat_svc.CtlConversation, bool) {
	purpose := chat_svc.SessionPurposeUserChat
	if req.Isolated {
		purpose = chat_svc.SessionPurposeSubagentCall
	}
	title := strings.TrimSpace(req.Title)
	if title == "" {
		title = "ctl: " + a.Name
	}
	ensured, err := h.chat.EnsureSession(r.Context(), &chat_svc.EnsureSessionRequest{
		Purpose: purpose, AgentID: a.ID, ProjectID: req.ProjectID, Title: title,
	})
	if err != nil || ensured == nil || ensured.SessionID <= 0 {
		writeErr(w, http.StatusInternalServerError, "create session failed")
		return nil, false
	}
	c, err := h.conversations.DescribeCtlConversation(r.Context(), ensured.SessionID)
	if err != nil || c == nil {
		writeErr(w, http.StatusInternalServerError, "read created session failed")
		return nil, false
	}
	c.AgentName = a.Name
	return c, true
}

// steer 把插话排进正在跑的这一轮并写响应;这一轮恰好已收口时什么都不写、交回 false,
// 由调用方按普通继续处理。
func (h *ctlHandler) steer(w http.ResponseWriter, r *http.Request, req sendRequest, c *chat_svc.CtlConversation) bool {
	queuedID, err := h.conversations.SteerCtlConversation(r.Context(), c.SessionID, req.Text)
	if err != nil {
		if hasErrCode(err, code.ChatSteerNoActive) {
			return false
		}
		writeErr(w, http.StatusInternalServerError, "steer failed: "+err.Error())
		return true
	}
	logger.Ctx(r.Context()).Info("ctl_svc.serveSend: steered",
		zap.Int64("sessionId", c.SessionID), zap.String("queuedId", queuedID))
	out := sendDTO{
		ConversationID: c.ConversationID, Agent: c.AgentName, Device: c.Device,
		Steered: true, QueuedID: queuedID, outcomeDTO: outcomeDTO{Status: string(chat_svc.CtlStatusRunning)},
	}
	if req.Wait {
		res, err := h.awaitConversation(r.Context(), c.SessionID, millis(req.TimeoutMs), nil)
		if err != nil {
			writeAwaitErr(w, err)
			return true
		}
		out.outcomeDTO, out.Done = res.dto, res.settled()
	}
	writeJSON(w, http.StatusOK, out)
	return true
}

// dispatch 在 target 上起一轮,按 req.Wait 立即返回或等到结果。
func (h *ctlHandler) dispatch(w http.ResponseWriter, r *http.Request, cred credential, req sendRequest, target *chat_svc.CtlConversation) {
	sessionID := target.SessionID
	// 派发关系:登记同样先于起轮;只有会话 token 才有派发方。
	dispatch := h.beginDispatch(r.Context(), cred, req, sessionID)

	// --wait 要等结果,要登记派发的也要知道这一轮何时收尾：订阅必须在 Send 之前(快 turn 的回执会丢)。
	var (
		turnCh        <-chan chat_svc.TurnResult
		cancelObserve = func() {}
	)
	if req.Wait || dispatch.recorded() {
		turnCh, cancelObserve = h.chat.ObserveTurn(sessionID)
	}
	// 子代理卡:登记同样必须在 Send 之前(子会话起轮第一时间的步骤要攒住)。
	card := h.watchSubagentCard(r.Context(), cred, req, sessionID)
	// 交给后台 goroutine 的那一份由它释放:卡片与订阅各自记着是否已移交。
	cardDetached, observeDetached := false, false
	defer func() {
		if !cardDetached {
			card.Close()
		}
		if !observeDetached {
			cancelObserve()
		}
	}()

	agentID := int64(0)
	if !req.continuing() {
		agentID = target.AgentID
	}
	sendResp, err := h.chat.Send(r.Context(), &chat_svc.SendRequest{
		SessionID:             sessionID,
		AgentID:               agentID,
		Text:                  req.Text,
		Images:                toSendImages(req.Images),
		EmitTurnStartedBypass: req.Isolated, // 隔离会话不需要前端正常轮起始事件；可见会话要
	})
	if err != nil {
		dispatch.finish(r.Context(), chat_svc.DispatchOutcome{Status: chat_svc.CtlSendFailed})
		if isSessionRunningErr(err) {
			writeErr(w, http.StatusConflict, errSessionRunning)
			return
		}
		writeErr(w, http.StatusInternalServerError, "start turn failed: "+err.Error())
		return
	}
	dispatch.started(r.Context())
	logger.Ctx(r.Context()).Info("ctl_svc.serveSend: dispatched",
		zap.Int64("agentId", target.AgentID),
		zap.Int64("sessionId", sessionID),
		zap.Bool("continue", req.continuing()),
		zap.Bool("wait", req.Wait))

	out := sendDTO{
		ConversationID: target.ConversationID, Agent: target.AgentName, Device: target.Device,
		AssistantMessageID: sendResp.AssistantMessageID, Stream: sendResp.Stream,
		outcomeDTO: outcomeDTO{Status: string(chat_svc.CtlStatusRunning)},
	}
	if !req.Wait {
		if dispatch.recorded() {
			// 响应不等收尾:后台盯着这一轮,收尾时把派发行落成终态。
			observeDetached = true
			go dispatch.track(context.WithoutCancel(r.Context()), turnCh, cancelObserve)
		}
		writeJSON(w, http.StatusOK, out)
		return
	}

	// 绑定以起轮成功为准(spec「Binding」规则 4)。不绑定时句柄的后续操作都是空操作。
	card.Bind(r.Context(), chat_svc.CtlSendBindRequest{
		CallerSessionID: cred.ref.SessionID,
		AgentName:       target.AgentName,
		AgentID:         req.AgentID,
		Task:            req.Text,
		Isolated:        req.Isolated,
		HostToolCallID:  r.Header.Get(HostBindToolCallHeader),
		HostBackground:  r.Header.Get(HostBindBackgroundHeader) == "true",
	})

	res, err := h.awaitConversation(r.Context(), sessionID, millis(req.TimeoutMs), turnCh)
	if err != nil {
		if r.Context().Err() != nil {
			// CLI 断开/取消 → 中止该轮，不留悬空 turn（用 Background：请求 ctx 已取消）。
			_, _ = h.chat.Stop(context.Background(), &chat_svc.StopRequest{SessionID: sessionID})
			dispatch.finish(context.WithoutCancel(r.Context()), chat_svc.DispatchOutcome{Status: chat_svc.CtlSendCanceled})
			card.Finish(context.WithoutCancel(r.Context()), chat_svc.CtlSendOutcome{Status: chat_svc.CtlSendCanceled})
		} else {
			dispatch.finish(r.Context(), chat_svc.DispatchOutcome{Status: chat_svc.CtlSendFailed})
		}
		writeAwaitErr(w, err)
		return
	}
	if res.settled() {
		// 派发行与卡片的终态一律在写响应之前落定:前台 shell 调用拿到结果时,两者都已收尾。
		outcome := res.cardOutcome()
		dispatch.finish(r.Context(), chat_svc.DispatchOutcome{Status: outcome.Status, ReplyMessageID: outcome.AssistantMessageID})
		card.Finish(r.Context(), outcome)
	} else {
		// 等待输入或超时返回时这一轮还在跑:派发行与卡片跟着它直到真正收口。
		cardDetached, observeDetached = true, true
		go h.finishCardLater(context.WithoutCancel(r.Context()), card, dispatch, turnCh, cancelObserve, sessionID)
	}
	out.outcomeDTO, out.Done = res.dto, res.settled()
	if res.assistantMessageID > 0 {
		out.AssistantMessageID = res.assistantMessageID
	}
	writeJSON(w, http.StatusOK, out)
}

// finishCardLater 在 send --wait 提前返回(等待输入 / 超时)之后,等子会话这一轮真正
// 收口再把子代理卡与派发行落成终态。
// ctx 不随请求结束而取消:响应已经写出,卡片还要跟着这一轮。
func (h *ctlHandler) finishCardLater(
	ctx context.Context, card chat_svc.CtlSendWatch, dispatch dispatchHandle,
	turnCh <-chan chat_svc.TurnResult, cancel func(), sessionID int64,
) {
	defer cancel()
	defer card.Close()
	timer := time.NewTimer(detachedCardMaxWait)
	defer timer.Stop()
	select {
	case res := <-turnCh:
		dispatch.finish(ctx, chat_svc.DispatchOutcomeOfTurn(res))
		text, _ := h.chat.FinalAssistantText(ctx, res.AssistantMessageID)
		status := chat_svc.CtlSendCompleted
		switch {
		case res.Err != nil:
			status, text = chat_svc.CtlSendFailed, res.Err.Error()
		case res.Aborted:
			status = chat_svc.CtlSendCanceled
		}
		card.Finish(ctx, chat_svc.CtlSendOutcome{Status: status, FinalText: text, AssistantMessageID: res.AssistantMessageID})
	case <-timer.C:
		logger.Ctx(ctx).Info("ctl_svc.finishCardLater: gave up waiting for the turn",
			zap.Int64("sessionId", sessionID))
	}
}

// detachedCardMaxWait 是提前返回之后子代理卡最多跟一轮多久;过了就收成已取消。
const detachedCardMaxWait = 24 * time.Hour

// resolveAgent 优先按 id，否则按名称(名称是纯数字且没有同名 agent 时按 id);
// 找不到返回错误(供上层转 404)。
func (h *ctlHandler) resolveAgent(r *http.Request, agentName string, agentID int64) (agentResolved, error) {
	if agentID > 0 {
		a, err := h.agents.Find(r.Context(), agentID)
		if err != nil || a == nil {
			return agentResolved{}, errAgentNotFound(agentName, agentID)
		}
		return agentResolved{ID: a.ID, Name: a.Name}, nil
	}
	name := strings.TrimSpace(agentName)
	if name == "" {
		return agentResolved{}, errAgentNotFound(agentName, agentID)
	}
	a, err := h.agents.FindByName(r.Context(), name)
	if err == nil && a != nil {
		return agentResolved{ID: a.ID, Name: a.Name}, nil
	}
	if id, perr := strconv.ParseInt(name, 10, 64); perr == nil && id > 0 {
		if a, err := h.agents.Find(r.Context(), id); err == nil && a != nil {
			return agentResolved{ID: a.ID, Name: a.Name}, nil
		}
	}
	return agentResolved{}, errAgentNotFound(agentName, agentID)
}

type agentResolved struct {
	ID   int64
	Name string
}

// toSendImages 把控制 API 的图片 DTO 映射到 chat_svc 的 SendImage(同形,显式转换)。
func toSendImages(images []sendImage) []chat_svc.SendImage {
	if len(images) == 0 {
		return nil
	}
	out := make([]chat_svc.SendImage, 0, len(images))
	for _, img := range images {
		out = append(out, chat_svc.SendImage{Name: img.Name, DataURL: img.DataURL})
	}
	return out
}

// ---- sessions ----

// sessionsRequest 是「新建一个 Agentre 会话」的入参(ACP session/new 的落脚点)。
type sessionsRequest struct {
	Agent     string `json:"agent"`
	AgentID   int64  `json:"agentId"`
	ProjectID int64  `json:"projectId"`
}

func (h *ctlHandler) serveSessions(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "sessions requires POST")
		return
	}
	if h.agents == nil || h.chat == nil || h.conversations == nil {
		writeErr(w, http.StatusServiceUnavailable, "control service not ready")
		return
	}
	var req sessionsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json body")
		return
	}
	a, err := h.resolveAgent(r, req.Agent, req.AgentID)
	if err != nil {
		writeErr(w, http.StatusNotFound, err.Error())
		return
	}
	c, ok := h.createConversation(w, r, sendRequest{ProjectID: req.ProjectID}, a)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"conversationId": c.ConversationID})
}

// ---- stop ----

type stopRequest struct {
	ConversationID string `json:"conversationId"`
}

// stopDTO:Running=false 表示会话本就没在跑(什么都没做);DroppedSteers 是被丢弃的、
// 尚未送达的插话条数。
type stopDTO struct {
	Stopped       bool `json:"stopped"`
	Running       bool `json:"running"`
	DroppedSteers int  `json:"droppedSteers"`
}

func (h *ctlHandler) serveStop(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "stop requires POST")
		return
	}
	if h.chat == nil || h.conversations == nil {
		writeErr(w, http.StatusServiceUnavailable, "control service not ready")
		return
	}
	var req stopRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json body")
		return
	}
	c, ok := h.lookupConversation(w, r, req.ConversationID)
	if !ok {
		return
	}
	if !isActive(c.Status) {
		writeJSON(w, http.StatusOK, stopDTO{})
		return
	}
	// 先撤插话再停:被中止的这一轮收尾不会再取走它们,留着就会混进下一轮。
	dropped, err := h.conversations.DiscardCtlSteers(r.Context(), c.SessionID)
	if err != nil {
		logger.Ctx(r.Context()).Warn("ctl_svc.serveStop: discard steers failed",
			zap.Int64("sessionId", c.SessionID), zap.Error(err))
		dropped = 0
	}
	if _, err := h.chat.Stop(r.Context(), &chat_svc.StopRequest{SessionID: c.SessionID}); err != nil {
		if hasErrCode(err, code.ChatStopNoActive) {
			writeJSON(w, http.StatusOK, stopDTO{DroppedSteers: dropped})
			return
		}
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	logger.Ctx(r.Context()).Info("ctl_svc.serveStop: stopped",
		zap.Int64("sessionId", c.SessionID), zap.Int("droppedSteers", dropped))
	writeJSON(w, http.StatusOK, stopDTO{Stopped: true, Running: true, DroppedSteers: dropped})
}

// ---- answer-permission ----

// answerPermissionRequest 是 agrctl acp 把 ACP client 的权限决策回灌给目标 agent 的
// 入参。Allow=false 时 AlwaysAllowSession 被忽略(deny 永远单次);DenyReason 非空时
// 作为用户反馈注入目标 agent。
type answerPermissionRequest struct {
	ConversationID     string `json:"conversationId"`
	RequestID          string `json:"requestId"`
	Allow              bool   `json:"allow"`
	AlwaysAllowSession bool   `json:"alwaysAllowSession,omitempty"`
	DenyReason         string `json:"denyReason,omitempty"`
}

func (h *ctlHandler) serveAnswerPermission(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "answer-permission requires POST")
		return
	}
	if h.chat == nil || h.conversations == nil {
		writeErr(w, http.StatusServiceUnavailable, "control service not ready")
		return
	}
	var req answerPermissionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json body")
		return
	}
	if strings.TrimSpace(req.RequestID) == "" {
		writeErr(w, http.StatusBadRequest, "requestId is required")
		return
	}
	c, ok := h.lookupConversation(w, r, req.ConversationID)
	if !ok {
		return
	}
	if _, err := h.chat.AnswerToolPermission(r.Context(), &chat_svc.AnswerToolPermissionRequest{
		SessionID:          c.SessionID,
		RequestID:          req.RequestID,
		Allow:              req.Allow,
		AlwaysAllowSession: req.AlwaysAllowSession,
		DenyReason:         req.DenyReason,
	}); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"answered": true})
}

// ---- stream (SSE) ----

// serveStream 把某会话的流事件逐条以 SSE 推给订阅者(agrctl acp 消费)。
//
// 订阅必须先于响应头:调用方的顺序是「先打开这条流、再发 send」,它把「收到响应头」
// 当作「订阅已生效」。若先写头后订阅,首片会在那个窗口里丢掉。
func (h *ctlHandler) serveStream(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeErr(w, http.StatusMethodNotAllowed, "stream requires GET")
		return
	}
	if h.chat == nil || h.conversations == nil {
		writeErr(w, http.StatusServiceUnavailable, "control service not ready")
		return
	}
	c, ok := h.lookupConversation(w, r, r.URL.Query().Get("conversationId"))
	if !ok {
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeErr(w, http.StatusInternalServerError, "streaming unsupported")
		return
	}
	ch, cancel := h.chat.SubscribeSessionEvents(c.SessionID)
	defer cancel()

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	for {
		select {
		case <-r.Context().Done():
			return
		case ev, ok := <-ch:
			if !ok {
				return
			}
			payload, err := json.Marshal(ev)
			if err != nil {
				continue
			}
			if _, err := fmt.Fprintf(w, "data: %s\n\n", payload); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}
