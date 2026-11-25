package ctlcmd

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/agentre-hub/agentre/internal/pkg/cagoenvelope"
	"github.com/agentre-hub/agentre/internal/pkg/conversationid"
	"github.com/agentre-hub/agentre/internal/pkg/ctlclient"
	"github.com/agentre-hub/agentre/internal/pkg/relaypeer"
	"github.com/agentre-hub/agentre/pkg/wire/agentrewire"
	"github.com/agentre-hub/agentre/pkg/wire/protorpc"
	"github.com/agentre-hub/agentre/pkg/wire/rpcerror"
	"github.com/agentre-hub/agentre/pkg/wire/wirecall"
)

// 账号模式的会话命令（send / wait / stop）：agrctl 自己是一个账号对端，经账号 server 的
// relay 直连承载会话的那台机器（agentred 或桌面），与控制台走同一套派发流程——
// dispatch-target 取计划、machine:<指纹> 开通道、auth.account、runtime.run、保存进账号、
// 再把模型与力度钉在会话上。flag、输出与退出码与桌面模式相同。

// accountPollInterval 是账号模式 wait 复查会话状态的间隔。状态一律以执行端的会话行为准，
// 断线重连后再查一次既不重复也不遗漏。
var accountPollInterval = time.Second

// Lifecycle 值与执行端 daemon_sessions.lifecycle_state 同值。
const (
	lifecycleRunning     = "running"
	lifecycleFailed      = "failed"
	lifecycleIdle        = "idle"
	lifecycleInterrupted = "interrupted"
)

var (
	errSessionNotVisible = errors.New("session not found or not accessible")
	errSessionBusy       = errors.New("session is running; use --steer to interject, or agrctl wait first")
)

// reasoningEfforts 是 --effort 的可选值（与执行端的六档相同，空 = 跟随后端配置）。
var reasoningEfforts = []string{"low", "medium", "high", "xhigh", "max"}

// sendInput 是 `agrctl send` 解析之后交给执行者的那一份。
type sendInput struct {
	text     string
	images   []sendImage
	session  string
	steer    bool
	wait     bool
	timeout  time.Duration
	agent    string
	agentID  int64
	project  string
	title    string
	device   string
	backend  string
	isolated bool

	model, cliModel, effort, permissionMode string
}

// accountChat 是一次调用里的账号会话执行者：一个 Ctl* 目录（名字解析）、一条 relay 链路。
type accountChat struct {
	s    *sys
	acct *ctlclient.Account
	cat  *catalog
	peer *relaypeer.Peer
}

func newAccountChat(s *sys, x *executor, args []string) *accountChat {
	return &accountChat{s: s, acct: x.acct, cat: newCatalog(x, args), peer: relaypeer.New(relaypeer.Options{
		ServerURL: x.acct.Server, Token: x.acct.Bearer, Reauthorize: x.acct.Reauthorize,
	})}
}

func (a *accountChat) close() { a.peer.Close() }

// session 是一条已经连上的会话：通道、它的摘要、承载它的设备名（不知道时为空）。
type session struct {
	conn    *relaypeer.Conn
	summary *agentrewire.SessionSummary
	device  string
}

func (h *session) origin() string { return h.summary.GetPeerFingerprint() }

// ─── send ───

func (a *accountChat) send(ctx context.Context, in sendInput) (conversationReply, error) {
	if in.effort != "" && !slices.Contains(reasoningEfforts, in.effort) {
		return conversationReply{}, usageErrorf("--effort %q is not supported (one of: %s)", in.effort, strings.Join(reasoningEfforts, ", "))
	}
	if in.session != "" {
		return a.continueSession(ctx, in)
	}
	return a.startSession(ctx, in)
}

// modelTarget 是 --model 解析出的 (provider key, model key)。
type modelTarget struct{ providerKey, modelKey string }

func (a *accountChat) resolveModel(ref string) (*modelTarget, error) {
	if ref == "" {
		return nil, nil
	}
	it, err := a.cat.locate(kindModel, ref)
	if err != nil {
		var ce *cliError
		if errors.As(err, &ce) && strings.Contains(ce.msg, "ambiguous") {
			return nil, err
		}
		return nil, usageErrorf("--model %q is not a model of this account (one of: %s)", ref, strings.Join(a.modelChoices(), ", "))
	}
	m := it.GetModel()
	p := a.cat.byID(agentrewire.CtlKind_CTL_KIND_PROVIDER, m.GetProviderId()).GetProvider()
	if p.GetSyncId() == "" {
		return nil, missingSyncID("provider")
	}
	return &modelTarget{providerKey: p.GetSyncId(), modelKey: m.GetKey()}, nil
}

func (a *accountChat) modelChoices() []string {
	items, err := a.cat.list(agentrewire.CtlKind_CTL_KIND_MODEL)
	if err != nil {
		return nil
	}
	out := make([]string, 0, len(items))
	for _, it := range items {
		if it.GetModel().GetEnabled() {
			out = append(out, a.cat.path(agentrewire.CtlKind_CTL_KIND_MODEL, it.GetModel().GetId()))
		}
	}
	return out
}

func missingSyncID(kind string) error {
	return fmt.Errorf("the account server did not report the %s's sync id; it needs to be upgraded", kind)
}

// dispatchPlan 是 GET /v1/workspace/dispatch-target 的应答（与控制台 lib/dispatch.ts 同形）。
type dispatchPlan struct {
	Tiers  []dispatchTier  `json:"tiers"`
	Chosen *dispatchChoice `json:"chosen"`
}

type dispatchTier struct {
	BackendSyncID string `json:"backend_sync_id"`
	DeviceID      int64  `json:"device_id"`
	DeviceName    string `json:"device_name"`
	BackendType   string `json:"backend_type"`
	Kind          string `json:"kind"`
	Availability  string `json:"availability"`
}

type dispatchChoice struct {
	DeviceFingerprint string `json:"device_fingerprint"`
	DeviceName        string `json:"device_name"`
	BackendSyncID     string `json:"backend_sync_id"`
	BackendType       string `json:"backend_type"`
	Kind              string `json:"kind"`
	Cwd               string `json:"cwd"`
}

func (a *accountChat) plan(ctx context.Context, agentSync, projectSync, targetBackend string) (*dispatchPlan, error) {
	q := url.Values{"agent_sync_id": {agentSync}}
	if projectSync != "" {
		q.Set("project_sync_id", projectSync)
	}
	if targetBackend != "" {
		q.Set("target_backend_sync_id", targetBackend)
	}
	raw, err := a.acct.GetRaw(ctx, "/v1/workspace/dispatch-target?"+q.Encode())
	if err != nil {
		return nil, serverErr(err)
	}
	var p dispatchPlan
	if err := cagoenvelope.Decode(raw, &p); err != nil {
		return nil, fmt.Errorf("decode the dispatch plan: %w", err)
	}
	return &p, nil
}

// accountDevice 是 GET /v1/devices 的一行。
type accountDevice struct {
	ID          int64  `json:"id"`
	Name        string `json:"name"`
	DisplayName string `json:"display_name"`
	Kind        string `json:"kind"`
	Fingerprint string `json:"fingerprint"`
	Online      bool   `json:"online"`
}

func (d accountDevice) label() string {
	if strings.TrimSpace(d.DisplayName) != "" {
		return d.DisplayName
	}
	return d.Name
}

func (a *accountChat) devices(ctx context.Context) ([]accountDevice, error) {
	raw, err := a.acct.GetRaw(ctx, "/v1/devices")
	if err != nil {
		return nil, serverErr(err)
	}
	var out struct {
		Devices []accountDevice `json:"devices"`
	}
	if err := cagoenvelope.Decode(raw, &out); err != nil {
		return nil, fmt.Errorf("decode the device list: %w", err)
	}
	return out.Devices, nil
}

// availabilityReason 把派发计划逐档的原因说成人话（与控制台同一组分类）。
func availabilityReason(availability string) string {
	switch availability {
	case "offline":
		return "the device is offline"
	case "unpaired":
		return "the device is not paired with this account"
	case "no_device":
		return "no device is set for this backend"
	case "project_path_missing":
		return "the project has no path on that device"
	default:
		return availability
	}
}

// pickTarget 按 --device / --backend 在 agent 的执行后端里挑一档，交回它的同步标识（空 =
// 由 server 按 agent 的顺序挑第一个可用的）。点名的不在这个 agent 的执行后端里是用法错误。
func (a *accountChat) pickTarget(ctx context.Context, agent *agentrewire.CtlAgent, projectSync string, in sendInput) (string, error) {
	switch {
	case in.backend != "":
		var options []string
		var chosen *agentrewire.CtlBackend
		for _, id := range agent.GetBackendIds() {
			b := a.cat.byID(agentrewire.CtlKind_CTL_KIND_BACKEND, id).GetBackend()
			if b == nil {
				continue
			}
			options = append(options, b.GetName())
			if chosen == nil && (b.GetName() == in.backend || strconv.FormatInt(b.GetId(), 10) == in.backend) {
				chosen = b
			}
		}
		if chosen == nil {
			return "", usageErrorf("--backend %q is not one of agent %s's backends (one of: %s)", in.backend, agent.GetName(), strings.Join(options, ", "))
		}
		if chosen.GetSyncId() == "" {
			return "", missingSyncID("backend")
		}
		return chosen.GetSyncId(), nil
	case in.device != "":
		p, err := a.plan(ctx, agent.GetSyncId(), projectSync, "")
		if err != nil {
			return "", err
		}
		devices, err := a.devices(ctx)
		if err != nil {
			return "", err
		}
		var options []string
		for _, t := range p.Tiers {
			if t.DeviceName != "" && !slices.Contains(options, t.DeviceName) {
				options = append(options, t.DeviceName)
			}
			if t.DeviceID != 0 && deviceMatches(devices, t, in.device) {
				return t.BackendSyncID, nil
			}
		}
		return "", usageErrorf("--device %q is not one of agent %s's devices (one of: %s)", in.device, agent.GetName(), strings.Join(options, ", "))
	}
	return "", nil
}

// deviceMatches：--device 是这一档设备的名字、账号里的备注名或指纹。
func deviceMatches(devices []accountDevice, t dispatchTier, ref string) bool {
	if t.DeviceName == ref {
		return true
	}
	for _, d := range devices {
		if d.ID == t.DeviceID && (d.Fingerprint == ref || d.Name == ref || d.DisplayName == ref) {
			return true
		}
	}
	return false
}

func unavailable(agent string, p *dispatchPlan, target string) error {
	var reasons []string
	for _, t := range p.Tiers {
		if target != "" && t.BackendSyncID != target {
			continue
		}
		where := t.DeviceName
		if where == "" {
			where = "(no device)"
		}
		reasons = append(reasons, where+": "+availabilityReason(t.Availability))
	}
	if len(reasons) == 0 {
		return fmt.Errorf("agent %s has no backend to run on", agent)
	}
	return fmt.Errorf("agent %s cannot run anywhere right now — %s", agent, strings.Join(reasons, "; "))
}

func (a *accountChat) startSession(ctx context.Context, in sendInput) (conversationReply, error) {
	var agentDoc *agentrewire.CtlResource
	var err error
	if in.agentID > 0 {
		agentDoc = a.cat.byID(agentrewire.CtlKind_CTL_KIND_AGENT, in.agentID)
		if agentDoc == nil {
			return conversationReply{}, fmt.Errorf("agent %d not found", in.agentID)
		}
	} else if agentDoc, err = a.cat.locate(kindAgent, in.agent); err != nil {
		return conversationReply{}, err
	}
	agent := agentDoc.GetAgent()
	if agent.GetSyncId() == "" {
		return conversationReply{}, missingSyncID("agent")
	}
	projectSync := ""
	if in.project != "" {
		it, err := a.cat.locate(kindProject, in.project)
		if err != nil {
			return conversationReply{}, err
		}
		if projectSync = it.GetProject().GetSyncId(); projectSync == "" {
			return conversationReply{}, missingSyncID("project")
		}
	}
	model, err := a.resolveModel(in.model)
	if err != nil {
		return conversationReply{}, err
	}
	target, err := a.pickTarget(ctx, agent, projectSync, in)
	if err != nil {
		return conversationReply{}, err
	}
	p, err := a.plan(ctx, agent.GetSyncId(), projectSync, target)
	if err != nil {
		return conversationReply{}, err
	}
	choice := p.Chosen
	if choice == nil {
		return conversationReply{}, unavailable(agent.GetName(), p, target)
	}
	conn, err := a.peer.Open(ctx, relaypeer.MachineTarget(choice.DeviceFingerprint))
	if err != nil {
		return conversationReply{}, a.reachErr(err, choice.DeviceName, "")
	}
	id, err := conversationid.New()
	if err != nil {
		return conversationReply{}, err
	}
	title := in.title
	if title == "" {
		title = deriveTitle(in.text)
	}
	backend := &agentrewire.AgentBackend{Type: choice.BackendType}
	if choice.Kind == "agentred" {
		backend.SyncId = choice.BackendSyncID
	}
	req := &agentrewire.RuntimeRunRequest{
		ConversationId: id, Title: title, AgentSyncId: agent.GetSyncId(), ProjectSyncId: projectSync,
		Cwd: choice.Cwd, Backend: backend,
	}
	a.fillTurn(req, conn, in, model)
	if choice.Kind == "agentred" && choice.BackendType == "piagent" {
		err = a.runPiGeneration(ctx, conn, req)
	} else {
		_, err = wirecall.RuntimeRun(ctx, conn, req)
	}
	if err != nil {
		return conversationReply{}, a.reachErr(err, choice.DeviceName, id)
	}
	if !in.isolated {
		a.save(ctx, choice.DeviceFingerprint, conn.SelfFingerprint, id)
	}
	h := &session{conn: conn, device: choice.DeviceName, summary: &agentrewire.SessionSummary{ConversationId: id, PeerFingerprint: conn.SelfFingerprint}}
	// 会话已经在跑了：钉不住不算派发失败（这一轮照样按覆盖跑），但要如实说。
	if err := a.pin(ctx, h, in, model); err != nil {
		_, _ = fmt.Fprintf(a.s.stderr, "warning: this turn uses the overrides, but they were not kept for later turns: %v\n", err)
	}
	out := conversationReply{ConversationID: id, Agent: agent.GetName(), Device: choice.DeviceName, Status: statusRunning}
	return a.finishSend(ctx, out, h, in)
}

// fillTurn 填这一轮里与调用方相关的那几格：说了什么、贴了什么图、从哪来、覆盖了什么。
func (a *accountChat) fillTurn(req *agentrewire.RuntimeRunRequest, conn *relaypeer.Conn, in sendInput, model *modelTarget) {
	req.UserText = in.text
	req.UserBlocks = userBlocks(in.images)
	req.SourceDevice = conn.SelfFingerprint
	req.SourceDeviceName = a.acct.DeviceName()
	req.PermissionMode = in.permissionMode
	req.ReasoningEffort = in.effort
	switch {
	case model != nil:
		req.LlmProviderKey, req.LlmModelKey = model.providerKey, model.modelKey
	case in.cliModel != "":
		req.CliModelMode, req.CliModel = "fixed", in.cliModel
	}
}

// runPiGeneration 是 agentred 上 piagent 的三步开局（与控制台 dispatch.ts 相同）：同一个
// generation owner 先登记、再准备出 provider 会话，最后点名它开跑；任一步失败就撤掉。
func (a *accountChat) runPiGeneration(ctx context.Context, conn *relaypeer.Conn, req *agentrewire.RuntimeRunRequest) error {
	req.PermissionMode = "web-pi-generation-" + randomHex(8)
	fail := func(err error) error {
		_, _ = wirecall.RuntimeAbort(ctx, conn, &agentrewire.RuntimeAbortRequest{ConversationId: req.GetConversationId()})
		return err
	}
	reg, err := wirecall.RuntimeRun(ctx, conn, req)
	if err != nil {
		return fail(err)
	}
	if reg.GetConversationId() != req.GetConversationId() {
		return fail(errors.New("pi registration acknowledged a different session"))
	}
	prepared, err := wirecall.RuntimeRun(ctx, conn, req)
	if err != nil {
		return fail(err)
	}
	providerSession := strings.TrimSpace(prepared.GetProviderSessionId())
	if prepared.GetConversationId() != req.GetConversationId() || providerSession == "" {
		return fail(errors.New("pi preparation returned an invalid session identity"))
	}
	start := proto.Clone(req).(*agentrewire.RuntimeRunRequest)
	start.ProviderSessionId = providerSession
	ack, err := wirecall.RuntimeRun(ctx, conn, start)
	if err != nil {
		return fail(err)
	}
	if ack.GetConversationId() != req.GetConversationId() || strings.TrimSpace(ack.GetProviderSessionId()) != providerSession {
		return fail(errors.New("pi start acknowledged a different prepared generation"))
	}
	return nil
}

// save 把新会话保存进账号（控制台与桌面据此看得到它）。会话已经在那台机器上跑起来了，
// 保存失败不算派发失败，但要如实说。
func (a *accountChat) save(ctx context.Context, device, peer, id string) {
	body, _ := json.Marshal(map[string]string{"device_fingerprint": device, "peer_fingerprint": peer, "conversation_id": id})
	if _, err := a.acct.PostRaw(ctx, "/v1/saved-sessions", body); err != nil {
		_, _ = fmt.Fprintf(a.s.stderr, "warning: the session runs but was not saved to the account (%v); it will not show up in the console\n", serverErr(err))
	}
}

// pin 把模型类覆盖钉在会话上，对之后的轮次生效（runtime.run 上的只管这一轮）。每一项
// 都试一遍，交回没钉住的那些。
func (a *accountChat) pin(ctx context.Context, h *session, in sendInput, model *modelTarget) error {
	id, origin := h.summary.GetConversationId(), h.origin()
	var errs []error
	switch {
	case model != nil:
		_, err := wirecall.SetModelTarget(ctx, h.conn, &agentrewire.SetModelTargetRequest{ConversationId: id, PeerFingerprint: origin,
			ProviderKey: model.providerKey, ModelKey: model.modelKey})
		errs = append(errs, err)
	case in.cliModel != "":
		_, err := wirecall.SetSessionCLIModel(ctx, h.conn, &agentrewire.SetSessionCLIModelRequest{ConversationId: id, PeerFingerprint: origin,
			Mode: "fixed", Value: in.cliModel})
		errs = append(errs, err)
	}
	if in.effort != "" {
		_, err := wirecall.SetSessionReasoningEffort(ctx, h.conn, &agentrewire.SetSessionReasoningEffortRequest{ConversationId: id,
			PeerFingerprint: origin, ReasoningEffort: in.effort})
		errs = append(errs, err)
	}
	if err := errors.Join(errs...); err != nil {
		return a.reachErr(err, h.device, id)
	}
	return nil
}

func (a *accountChat) continueSession(ctx context.Context, in sendInput) (conversationReply, error) {
	model, err := a.resolveModel(in.model)
	if err != nil {
		return conversationReply{}, err
	}
	h, err := a.openSession(ctx, in.session)
	if err != nil {
		return conversationReply{}, a.reachErr(err, "", in.session)
	}
	out := conversationReply{ConversationID: in.session, Agent: a.agentName(h.summary.GetAgentSyncId()), Device: h.device, Status: statusRunning}
	if out.Device == "" {
		out.Device = "its device"
	}
	if h.summary.GetLifecycleState() == lifecycleRunning && !in.steer {
		return conversationReply{}, errSessionBusy
	}
	if err := a.pin(ctx, h, in, model); err != nil {
		return conversationReply{}, err
	}
	if h.summary.GetLifecycleState() == lifecycleRunning {
		return a.steerInto(ctx, h, in, out)
	}
	s := h.summary
	req := &agentrewire.RuntimeRunRequest{
		ConversationId: s.GetConversationId(), PeerFingerprint: s.GetPeerFingerprint(), Cwd: s.GetCwd(), Title: s.GetTitle(),
		AgentSyncId: s.GetAgentSyncId(), ProjectSyncId: s.GetProjectSyncId(), Backend: &agentrewire.AgentBackend{Type: s.GetBackendType()},
	}
	a.fillTurn(req, h.conn, in, model)
	if _, err := wirecall.RuntimeRun(ctx, h.conn, req); err != nil {
		if isSessionRunning(err) && in.steer {
			return a.steerInto(ctx, h, in, out)
		}
		return conversationReply{}, a.reachErr(err, h.device, in.session)
	}
	return a.finishSend(ctx, out, h, in)
}

func (a *accountChat) steerInto(ctx context.Context, h *session, in sendInput, out conversationReply) (conversationReply, error) {
	_, err := wirecall.RuntimeSteer(ctx, h.conn, &agentrewire.RuntimeSteerRequest{
		ConversationId: in.session, PeerFingerprint: h.origin(), QueuedId: randomHex(16), Text: in.text,
	})
	if err != nil {
		return conversationReply{}, a.reachErr(err, h.device, in.session)
	}
	out.Steered = true
	return a.finishSend(ctx, out, h, in)
}

func (a *accountChat) finishSend(ctx context.Context, out conversationReply, h *session, in sendInput) (conversationReply, error) {
	if !in.wait {
		return out, nil
	}
	o, err := a.wait(ctx, out.ConversationID, in.timeout, h)
	if err != nil {
		return conversationReply{}, err
	}
	o.Agent, o.Device, o.Steered = out.Agent, out.Device, out.Steered
	return o, nil
}

func (a *accountChat) agentName(syncID string) string {
	items, err := a.cat.list(agentrewire.CtlKind_CTL_KIND_AGENT)
	if err == nil {
		for _, it := range items {
			if it.GetAgent().GetSyncId() == syncID && syncID != "" {
				return it.GetAgent().GetName()
			}
		}
	}
	if syncID == "" {
		return "the session's agent"
	}
	return syncID
}

// ─── 找到一条会话 ───

// openSession 连上承载会话 id 的那台机器并读出它的摘要。保存过的会话由 server 按名单
// 解析机器；没保存过的（--isolated）退回在账号的在线设备上逐台找。失败原样交回（调用方
// 据类型决定重不重连，再用 reachErr 说成人话）。
func (a *accountChat) openSession(ctx context.Context, id string) (*session, error) {
	conn, err := a.peer.Open(ctx, relaypeer.ConversationTarget(id))
	switch {
	case err == nil:
		sum, err := summaryOf(ctx, conn, id)
		if err != nil {
			_ = conn.Close()
			return nil, err
		}
		if sum == nil {
			_ = conn.Close()
			return nil, errSessionNotVisible
		}
		return &session{conn: conn, summary: sum}, nil
	case !errors.Is(err, relaypeer.ErrTargetNotFound):
		return nil, err
	}
	devices, err := a.devices(ctx)
	if err != nil {
		return nil, err
	}
	for _, d := range devices {
		if !d.Online || (d.Kind != "agentred" && d.Kind != "desktop") {
			continue
		}
		conn, err := a.peer.Open(ctx, relaypeer.MachineTarget(d.Fingerprint))
		if err != nil {
			continue
		}
		if sum, err := summaryOf(ctx, conn, id); err == nil && sum != nil {
			return &session{conn: conn, summary: sum, device: d.label()}, nil
		}
		_ = conn.Close()
	}
	return nil, errSessionNotVisible
}

func summaryOf(ctx context.Context, conn *relaypeer.Conn, id string) (*agentrewire.SessionSummary, error) {
	resp, err := wirecall.SessionList(ctx, conn, &agentrewire.SessionListRequest{ConversationIds: []string{id}})
	if err != nil {
		return nil, err
	}
	for _, s := range resp.GetSessions() {
		if s.GetConversationId() == id {
			return s, nil
		}
	}
	return nil, nil
}

// ─── wait ───

// wait 等到会话空闲、进入等待输入或 timeout 到期（0 = 不超时）。h 非 nil 时先用它那条通道。
// relay 断线、设备暂时离线时重连再查；设备一直不回来，超过 timeout 就按超时处理。
func (a *accountChat) wait(ctx context.Context, id string, timeout time.Duration, h *session) (conversationReply, error) {
	start := time.Now()
	expired := func() bool { return timeout > 0 && time.Since(start) >= timeout }
	timedOut := conversationReply{ConversationID: id, Status: statusRunning}
	connected := h != nil
	failures := 0
	for {
		if expired() {
			return timedOut, nil
		}
		if h == nil {
			var err error
			h, err = a.openSession(ctx, id)
			if err != nil {
				if !transient(err) || (!connected && errors.Is(err, relaypeer.ErrRelayUnavailable)) || (timeout == 0 && failures >= maxReconnects) {
					return conversationReply{}, a.reachErr(err, "", id)
				}
				failures++
				time.Sleep(reconnectDelay)
				continue
			}
			connected = true
		}
		out, done, err := a.check(ctx, h, id)
		if err != nil {
			_ = h.conn.Close()
			h = nil
			if !transient(err) || (timeout == 0 && failures >= maxReconnects) {
				return conversationReply{}, a.reachErr(err, "", id)
			}
			failures++
			time.Sleep(reconnectDelay)
			continue
		}
		failures = 0
		if done {
			return out, nil
		}
		wait := accountPollInterval
		if timeout > 0 {
			wait = min(wait, max(timeout-time.Since(start), time.Millisecond))
		}
		select {
		case <-ctx.Done():
			return conversationReply{}, ctx.Err()
		case <-h.conn.Done():
			h = nil
		case <-time.After(wait):
		}
	}
}

// state 读会话此刻的状态：等待输入（含审批卡；交回待处理项）、失败、空闲，或仍在跑。
func (a *accountChat) state(ctx context.Context, h *session, id string) (string, *agentrewire.SessionPendingWaitersResponse, error) {
	sum, err := summaryOf(ctx, h.conn, id)
	if err != nil {
		return "", nil, err
	}
	if sum == nil {
		return "", nil, errSessionNotVisible
	}
	h.summary = sum
	state := sum.GetLifecycleState()
	if state == lifecycleRunning || sum.GetWaitingForInput() {
		// agentred 的 waiting_for_input 不算 ctl 审批卡：审批卡另在 approvals 里，同样是等待输入。
		p, err := wirecall.SessionPendingWaiters(ctx, h.conn, &agentrewire.SessionPendingWaitersRequest{ConversationId: id, PeerFingerprint: h.origin()})
		if err != nil {
			return "", nil, err
		}
		if len(p.GetToolPermissions())+len(p.GetAskUserQuestions())+len(p.GetApprovals()) > 0 {
			return statusWaiting, p, nil
		}
		if state == lifecycleRunning {
			return statusRunning, nil, nil
		}
	}
	if state == lifecycleFailed {
		return statusFailed, nil, nil
	}
	return statusIdle, nil, nil
}

// check 查一次会话状态：等待输入、空闲 / 失败，或仍在跑（done = false）。
func (a *accountChat) check(ctx context.Context, h *session, id string) (conversationReply, bool, error) {
	status, p, err := a.state(ctx, h, id)
	if err != nil {
		return conversationReply{}, false, err
	}
	switch status {
	case statusRunning:
		return conversationReply{}, false, nil
	case statusWaiting:
		raw, err := json.Marshal(pendingJSON(p))
		if err != nil {
			return conversationReply{}, false, err
		}
		return conversationReply{ConversationID: id, Status: statusWaiting, Pending: raw}, true, nil
	}
	text, failure, err := lastTurn(ctx, h, id)
	if err != nil {
		return conversationReply{}, false, err
	}
	if status == statusFailed {
		if failure == "" {
			failure = "the turn failed"
		}
		return conversationReply{ConversationID: id, Status: statusFailed, Error: failure}, true, nil
	}
	return conversationReply{ConversationID: id, Status: statusIdle, Text: text}, true, nil
}

// lastTurn 从执行端的持久帧里读出最后一轮助手的最终文本与失败原因。
func lastTurn(ctx context.Context, h *session, id string) (text, failure string, err error) {
	var b strings.Builder
	reset := func() { b.Reset(); failure = "" }
	cursor := int64(0)
	for {
		resp, err := wirecall.SessionPull(ctx, h.conn, &agentrewire.SessionPullRequest{ConversationId: id, PeerFingerprint: h.origin(), Cursor: cursor, Limit: 500})
		if err != nil {
			return "", "", err
		}
		for _, n := range resp.GetNotifications() {
			switch p := n.GetPayload().GetPayload().(type) {
			case *agentrewire.RpcNotification_TurnStarted, *agentrewire.RpcNotification_AutonomousTurnStarted:
				reset()
			case *agentrewire.RpcNotification_RuntimeEvent:
				turnEvent(p.RuntimeEvent, &b, &failure, reset)
			case *agentrewire.RpcNotification_AutonomousTurnEvent:
				turnEvent(p.AutonomousTurnEvent, &b, &failure, reset)
			case *agentrewire.RpcNotification_RunResultDone:
				if m := p.RunResultDone.GetStopErrorMessage(); m != "" {
					failure = m
				}
			case *agentrewire.RpcNotification_AutonomousTurnDone:
				if m := p.AutonomousTurnDone.GetStopErrorMessage(); m != "" {
					failure = m
				}
			}
		}
		if !resp.GetHasMore() || resp.GetCursor() <= cursor {
			return b.String(), failure, nil
		}
		cursor = resp.GetCursor()
	}
}

func turnEvent(ev *agentrewire.RuntimeEventNotification, b *strings.Builder, failure *string, reset func()) {
	if ev.GetPreview() {
		return
	}
	switch e := ev.GetEvent().(type) {
	case *agentrewire.RuntimeEventNotification_UserMessage:
		reset()
	case *agentrewire.RuntimeEventNotification_TextDelta:
		b.WriteString(e.TextDelta.GetText())
	case *agentrewire.RuntimeEventNotification_Error:
		*failure = e.Error.GetMessage()
	}
}

// pendingJSON 是等待输入时打印的待处理项，形状与桌面模式 wait 的 pending 相同（桌面没有审批卡这一类）。
func pendingJSON(p *agentrewire.SessionPendingWaitersResponse) any {
	type option struct {
		Label       string `json:"label"`
		Description string `json:"description,omitempty"`
		Preview     string `json:"preview,omitempty"`
	}
	type question struct {
		ID            string   `json:"id,omitempty"`
		Question      string   `json:"question"`
		Header        string   `json:"header,omitempty"`
		MultiSelect   bool     `json:"multiSelect,omitempty"`
		IsOther       bool     `json:"isOther,omitempty"`
		IsSecret      bool     `json:"isSecret,omitempty"`
		DisallowOther bool     `json:"disallowOther,omitempty"`
		Options       []option `json:"options,omitempty"`
	}
	type ask struct {
		RequestID string     `json:"requestId"`
		Questions []question `json:"questions"`
	}
	type permission struct {
		RequestID string          `json:"requestId"`
		ToolName  string          `json:"toolName"`
		Input     json.RawMessage `json:"input,omitempty"`
	}
	type approval struct {
		RequestID  string          `json:"requestId"`
		Kind       string          `json:"kind"`
		Title      string          `json:"title"`
		Summary    json.RawMessage `json:"summary,omitempty"`
		Createtime int64           `json:"createtime"`
	}
	out := struct {
		ToolPermissions  []permission `json:"toolPermissions,omitempty"`
		AskUserQuestions []ask        `json:"askUserQuestions,omitempty"`
		Approvals        []approval   `json:"approvals,omitempty"`
	}{}
	for _, t := range p.GetToolPermissions() {
		out.ToolPermissions = append(out.ToolPermissions, permission{RequestID: t.GetRequestId(), ToolName: t.GetToolName(), Input: rawJSON(t.GetInput())})
	}
	for _, q := range p.GetAskUserQuestions() {
		a := ask{RequestID: q.GetRequestId()}
		for _, aq := range q.GetQuestions() {
			qq := question{ID: aq.GetId(), Question: aq.GetQuestion(), Header: aq.GetHeader(), MultiSelect: aq.GetMultiSelect(),
				IsOther: aq.GetIsOther(), IsSecret: aq.GetIsSecret(), DisallowOther: aq.GetDisallowOther()}
			for _, o := range aq.GetOptions() {
				qq.Options = append(qq.Options, option{Label: o.GetLabel(), Description: o.GetDescription(), Preview: o.GetPreview()})
			}
			a.Questions = append(a.Questions, qq)
		}
		out.AskUserQuestions = append(out.AskUserQuestions, a)
	}
	for _, ap := range p.GetApprovals() {
		out.Approvals = append(out.Approvals, approval{RequestID: ap.GetRequestId(), Kind: ap.GetKind(), Title: ap.GetTitle(),
			Summary: rawJSON(ap.GetSummary()), Createtime: ap.GetCreatetime()})
	}
	return out
}

// rawJSON 原样带出一段 JSON 字节；不是合法 JSON 时按字符串带出。
func rawJSON(b []byte) json.RawMessage {
	if len(b) == 0 {
		return nil
	}
	if json.Valid(b) {
		return b
	}
	s, _ := json.Marshal(string(b))
	return s
}

// ─── stop ───

type stopReply struct {
	Stopped       bool
	Running       bool
	DroppedSteers int
}

// stop 停止正在运行的一轮并丢弃还没送达的插话。先取走插话、再中止：反过来的话，这一轮
// 收尾时 agentred 会把残留插话当下一轮补发出去。
func (a *accountChat) stop(ctx context.Context, id string) (stopReply, error) {
	h, err := a.openSession(ctx, id)
	if err != nil {
		return stopReply{}, a.reachErr(err, "", id)
	}
	if h.summary.GetLifecycleState() != lifecycleRunning {
		return stopReply{}, nil
	}
	drained, err := wirecall.RuntimeDrainPending(ctx, h.conn, &agentrewire.RuntimeDrainPendingRequest{ConversationId: id, PeerFingerprint: h.origin()})
	if err != nil {
		return stopReply{}, a.reachErr(err, h.device, id)
	}
	if _, err := wirecall.RuntimeAbort(ctx, h.conn, &agentrewire.RuntimeAbortRequest{ConversationId: id, PeerFingerprint: h.origin()}); err != nil {
		if rpcCode(err) == rpcerror.CodeRuntimeNoActiveTurn {
			return stopReply{DroppedSteers: len(drained.GetSteers())}, nil
		}
		return stopReply{}, a.reachErr(err, h.device, id)
	}
	return stopReply{Stopped: true, Running: true, DroppedSteers: len(drained.GetSteers())}, nil
}

// ─── 失败的说法 ───

// reachErr 把 relay / 执行端的失败说成给人看的那句话，并指明是哪台设备。
func (a *accountChat) reachErr(err error, device, session string) error {
	what := "device " + device
	if device == "" {
		what = "the device"
		if session != "" {
			what = "the device holding session " + session
		}
	}
	switch {
	case errors.Is(err, relaypeer.ErrPeerVersionMismatch):
		return fmt.Errorf("%s needs to be upgraded to the release this agrctl speaks: %v", what, err)
	case errors.Is(err, relaypeer.ErrTargetOffline):
		return fmt.Errorf("%s is offline", what)
	case errors.Is(err, relaypeer.ErrTargetNotFound), errors.Is(err, relaypeer.ErrTargetForbidden):
		return fmt.Errorf("%s cannot be reached through the account", what)
	case errors.Is(err, relaypeer.ErrForwardFailed):
		return fmt.Errorf("the account relay could not reach %s; try again", what)
	case errors.Is(err, relaypeer.ErrCredentialRejected):
		if a.acct.PersonalToken() {
			return ctlclient.ErrTokenRejected
		}
		return ctlclient.ErrLoginExpired
	case errors.Is(err, relaypeer.ErrRelayUnavailable):
		return fmt.Errorf("cannot reach the account relay at %s: %v", a.acct.Server, err)
	case errors.Is(err, protorpc.ErrConnClosed):
		return fmt.Errorf("lost the connection to %s", what)
	case isSessionRunning(err):
		return errSessionBusy
	}
	var rpcErr *rpcerror.Error
	if errors.As(err, &rpcErr) {
		return errors.New(rpcErr.Message)
	}
	return err
}

// transient 报告一次失败值不值得重连再试：执行端或 server 明确的拒绝不重试。
func transient(err error) bool {
	var rpcErr *rpcerror.Error
	var se *ctlclient.ServerError
	switch {
	case errors.As(err, &rpcErr), errors.As(err, &se),
		errors.Is(err, errSessionNotVisible),
		errors.Is(err, relaypeer.ErrPeerVersionMismatch), errors.Is(err, relaypeer.ErrCredentialRejected),
		errors.Is(err, relaypeer.ErrTargetInvalid), errors.Is(err, relaypeer.ErrTargetForbidden),
		errors.Is(err, ctlclient.ErrLoginExpired), errors.Is(err, ctlclient.ErrTokenRejected), errors.Is(err, ctlclient.ErrNotLoggedIn),
		errors.Is(err, context.Canceled):
		return false
	}
	return true
}

func rpcCode(err error) int32 {
	var rpcErr *rpcerror.Error
	if errors.As(err, &rpcErr) {
		return rpcErr.Code
	}
	return 0
}

func isSessionRunning(err error) bool { return rpcCode(err) == rpcerror.CodeRuntimeSessionRunning }

// serverErr 把账号 server 的拒绝原样透出。
func serverErr(err error) error {
	var se *ctlclient.ServerError
	if errors.As(err, &se) {
		return errors.New(se.Message)
	}
	return err
}

// ─── 小工具 ───

// deriveTitle 是没给 --title 时的标题：首行，过长截断（与控制台、桌面同一规则）。
func deriveTitle(text string) string {
	first, _, _ := strings.Cut(strings.TrimSpace(text), "\n")
	first = strings.TrimSpace(first)
	if r := []rune(first); len(r) > 60 {
		return string(r[:60]) + "…"
	}
	return first
}

// userBlocks 把 --image 编码成 runtime.run 的 userBlocks（与控制台 lib/userBlocks.ts 同形）。
func userBlocks(images []sendImage) []*agentrewire.StoredBlock {
	if len(images) == 0 {
		return nil
	}
	out := make([]*agentrewire.StoredBlock, 0, len(images))
	for _, img := range images {
		mime, payload, _ := strings.Cut(strings.TrimPrefix(img.DataURL, "data:"), ";base64,")
		data, _ := json.Marshal(map[string]any{"media_type": mime, "source": map[string]string{"inline": payload}})
		out = append(out, &agentrewire.StoredBlock{Type: "image", Data: data})
	}
	return out
}

func randomHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
