package ctlaccount

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strings"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/agentre-hub/agentre/internal/pkg/cagoenvelope"
	"github.com/agentre-hub/agentre/internal/pkg/conversationid"
	"github.com/agentre-hub/agentre/internal/pkg/relaypeer"
	"github.com/agentre-hub/agentre/pkg/wire/agentrewire"
	"github.com/agentre-hub/agentre/pkg/wire/rpcerror"
	"github.com/agentre-hub/agentre/pkg/wire/wirecall"
)

// 会话命令（send / wait / stop）：与控制台走同一套派发流程——dispatch-target 取计划、
// machine:<指纹> 开通道、auth.account、runtime.run、保存进账号、再把模型与力度钉在会话上。

// reasoningEfforts 是 --effort 的可选值（与执行端的六档相同，空 = 跟随后端配置）。
var reasoningEfforts = []string{"low", "medium", "high", "xhigh", "max"}

// Image 是一张随 send 附上的图片（与 /ctl/v1/send 的 images 同形）。
type Image struct {
	Name    string `json:"name,omitempty"`
	DataURL string `json:"dataUrl"`
}

// SendInput 是一次 send：Session 为空则按 Agent / AgentID / Project 新开一条会话。
type SendInput struct {
	Text    string
	Images  []Image
	Session string
	Steer   bool
	Wait    bool
	Timeout time.Duration
	Agent   string
	AgentID int64
	Project string
	Title   string
	Device  string
	// Engine 是本会话换用的引擎配置（名字或 id，仅新会话）；空 = 成员生效的那份。
	Engine string
	// Backend 已废弃且不再被读：agentred 的控制台代理（internal/daemon/handlers/ctlproxy_console.go）
	// 还在填它，那一侧迁到 Engine 之后删掉这一格。
	Backend  string
	Isolated bool

	Model, CLIModel, Effort, PermissionMode string
}

// Reply 是 send 与 wait 的结果，与桌面 /ctl/v1/send、/ctl/v1/wait 的响应同形。
type Reply struct {
	ConversationID string          `json:"conversationId"`
	Agent          string          `json:"agent"`
	Device         string          `json:"device"`
	Steered        bool            `json:"steered"`
	Status         string          `json:"status"`
	Text           string          `json:"text"`
	Error          string          `json:"error"`
	Pending        json.RawMessage `json:"pending"`
}

// session 是一条已经连上的会话：通道、它的摘要、承载它的设备名（不知道时为空）。
type session struct {
	conn    *relaypeer.Conn
	summary *agentrewire.SessionSummary
	device  string
	// deviceFP 是承载它的设备指纹（不知道时为空）。
	deviceFP string
}

func (h *session) origin() string { return h.summary.GetPeerFingerprint() }

// attach 让这条通道接管会话的控制面（session.attach），作答 / 中止 / 插话之前必经。
// agentred 的 runtime 控制按连接持有会话表，只认本连接起过或接管过的会话；agrctl 每条
// 命令都是一条新通道，起这一轮的那条早已关掉 —— 不先接管，这些调用一律解不出那一轮。
// 能不能动这条会话仍由 agentred 判：个人 token 接管别人发起的会话只是跟随输出，之后的
// 控制照样被拒。
func (h *session) attach(ctx context.Context) error {
	_, err := wirecall.SessionAttach(ctx, h.conn, &agentrewire.SessionAttachRequest{
		ConversationId: h.summary.GetConversationId(), PeerFingerprint: h.origin(),
	})
	return err
}

// Send 派发一条任务给某个 agent，或继续一条会话。
func (a *Client) Send(ctx context.Context, in SendInput) (Reply, error) {
	if in.Effort != "" && !slices.Contains(reasoningEfforts, in.Effort) {
		return Reply{}, Usagef("--effort %q is not supported (one of: %s)", in.Effort, strings.Join(reasoningEfforts, ", "))
	}
	if in.Session != "" {
		return a.continueSession(ctx, in)
	}
	return a.startSession(ctx, in)
}

// modelTarget 是 --model 解析出的 (provider key, model key)。
type modelTarget struct{ providerKey, modelKey string }

func (a *Client) resolveModel(ref string) (*modelTarget, error) {
	if ref == "" {
		return nil, nil
	}
	it, err := a.cat.Locate(agentrewire.CtlKind_CTL_KIND_MODEL, ref)
	if err != nil {
		var ae *AmbiguousError
		if errors.As(err, &ae) {
			return nil, err
		}
		return nil, Usagef("--model %q is not a model of this account (one of: %s)", ref, strings.Join(a.modelChoices(), ", "))
	}
	m := it.GetModel()
	p := a.cat.ByID(agentrewire.CtlKind_CTL_KIND_PROVIDER, m.GetProviderId()).GetProvider()
	if p.GetSyncId() == "" {
		return nil, missingSyncID("provider")
	}
	return &modelTarget{providerKey: p.GetSyncId(), modelKey: m.GetKey()}, nil
}

func (a *Client) modelChoices() []string {
	items, err := a.cat.List(agentrewire.CtlKind_CTL_KIND_MODEL)
	if err != nil {
		return nil
	}
	out := make([]string, 0, len(items))
	for _, it := range items {
		if it.GetModel().GetEnabled() {
			out = append(out, a.cat.Path(agentrewire.CtlKind_CTL_KIND_MODEL, it.GetModel().GetId()))
		}
	}
	return out
}

func missingSyncID(kind string) error {
	return fmt.Errorf("the account server did not report the %s's sync id; it needs to be upgraded", kind)
}

// dispatchPlan 是 GET /v1/workspace/dispatch-target 的应答。
//
// 契约（server 任务实现，这里是 agrctl 假定的形状）：请求带 agent_sync_id，可选
// project_sync_id、device（手动指定的设备指纹）、engine_sync_id（本会话换用的引擎配置）；
// 应答是共享派发函数（pkg/syncwire/dispatch）对每台参与设备的结果：Devices 按派发顺序，
// Found 为 true 时 Chosen 是选中的那台（含它的 cwd 与用的引擎配置）。
type dispatchPlan struct {
	Found   bool            `json:"found"`
	Engine  dispatchEngine  `json:"engine"`
	Devices []dispatchDev   `json:"devices"`
	Chosen  *dispatchChoice `json:"chosen"`
}

type dispatchEngine struct {
	SyncID  string `json:"sync_id"`
	Name    string `json:"name"`
	Runtime string `json:"runtime"`
}

type dispatchDev struct {
	DeviceID          int64  `json:"device_id"`
	DeviceFingerprint string `json:"device_fingerprint"`
	DeviceName        string `json:"device_name"`
	Kind              string `json:"kind"`
	Eligible          bool   `json:"eligible"`
	Reason            Reason `json:"reason"`
}

type dispatchChoice struct {
	DeviceFingerprint string `json:"device_fingerprint"`
	DeviceName        string `json:"device_name"`
	Kind              string `json:"kind"`
	Cwd               string `json:"cwd"`
	EngineSyncID      string `json:"engine_sync_id"`
	Runtime           string `json:"runtime"`
}

func (a *Client) plan(ctx context.Context, agentSync, projectSync, device, engineSync string) (*dispatchPlan, error) {
	q := url.Values{"agent_sync_id": {agentSync}}
	if projectSync != "" {
		q.Set("project_sync_id", projectSync)
	}
	if device != "" {
		q.Set("device", device)
	}
	if engineSync != "" {
		q.Set("engine_sync_id", engineSync)
	}
	raw, err := a.srv.GetRaw(ctx, "/v1/workspace/dispatch-target?"+q.Encode())
	if err != nil {
		return nil, ServerErr(err)
	}
	var p dispatchPlan
	if err := cagoenvelope.Decode(raw, &p); err != nil {
		return nil, fmt.Errorf("decode the dispatch plan: %w", err)
	}
	return &p, nil
}

// accountDevice 是 GET /v1/devices 的一行。
type accountDevice struct {
	ID           int64  `json:"id"`
	Name         string `json:"name"`
	DisplayName  string `json:"display_name"`
	Kind         string `json:"kind"`
	Platform     string `json:"platform"`
	Version      string `json:"version"`
	Fingerprint  string `json:"fingerprint"`
	LastSeenAt   int64  `json:"last_seen_at"`
	Online       bool   `json:"online"`
	IsThisDevice bool   `json:"is_this_device"`
}

// Device 是账号下的一台设备（GET /v1/devices 的一行）。
type Device = accountDevice

// Label 是设备给人看的名字：账号级备注名优先，否则自报的名字。
func (d accountDevice) Label() string { return d.label() }

func (d accountDevice) label() string {
	if strings.TrimSpace(d.DisplayName) != "" {
		return d.DisplayName
	}
	return d.Name
}

func (a *Client) devices(ctx context.Context) ([]accountDevice, error) {
	return ListDevices(ctx, a.srv)
}

// ListDevices 列出账号下的设备（agrctl 的设备管理命令不需要 relay，直接用它）。
func ListDevices(ctx context.Context, srv Server) ([]Device, error) {
	raw, err := srv.GetRaw(ctx, "/v1/devices")
	if err != nil {
		return nil, ServerErr(err)
	}
	var out struct {
		Devices []accountDevice `json:"devices"`
	}
	if err := cagoenvelope.Decode(raw, &out); err != nil {
		return nil, fmt.Errorf("decode the device list: %w", err)
	}
	return out.Devices, nil
}

// resolveEngine 把 --engine（名字或 id）解析成引擎配置的同步标识；空 = 成员生效的那份。
func (a *Client) resolveEngine(ref string) (string, error) {
	if ref == "" {
		return "", nil
	}
	it, err := a.cat.Locate(agentrewire.CtlKind_CTL_KIND_ENGINE, ref)
	if err != nil {
		var ae *AmbiguousError
		if errors.As(err, &ae) {
			return "", err
		}
		return "", Usagef("--engine %q is not an engine of this account (one of: %s)", ref, strings.Join(a.engineChoices(), ", "))
	}
	if it.GetEngine().GetSyncId() == "" {
		return "", missingSyncID("engine")
	}
	return it.GetEngine().GetSyncId(), nil
}

func (a *Client) engineChoices() []string {
	items, err := a.cat.List(agentrewire.CtlKind_CTL_KIND_ENGINE)
	if err != nil {
		return nil
	}
	out := make([]string, 0, len(items))
	for _, it := range items {
		out = append(out, it.GetEngine().GetName())
	}
	return out
}

// pickDevice 把 --device（设备名、账号里的备注名或指纹）对到派发计划里的一台设备：不是参与
// 派发的设备是用法错误（列出可选的）；是但派发规则不通过，报它的原因（退出码 1）。
// 交回它的指纹，之后带着它再问一次计划拿 cwd。
func (a *Client) pickDevice(ctx context.Context, agent string, p *dispatchPlan, ref string) (string, error) {
	devices, err := a.devices(ctx)
	if err != nil {
		return "", err
	}
	var options []string
	for _, d := range p.Devices {
		if d.DeviceName != "" && !slices.Contains(options, d.DeviceName) {
			options = append(options, d.DeviceName)
		}
		if !deviceMatches(devices, d, ref) {
			continue
		}
		if !d.Eligible {
			return "", errors.New(RequestedDeviceMessage(agent, DeviceVerdict{Name: d.DeviceName, Reason: d.Reason}))
		}
		return d.DeviceFingerprint, nil
	}
	return "", Usagef("%s", UnknownDeviceMessage(ref, agent, options))
}

// deviceMatches：--device 是这台设备的名字、账号里的备注名或指纹。
func deviceMatches(devices []accountDevice, t dispatchDev, ref string) bool {
	if t.DeviceName == ref || t.DeviceFingerprint == ref {
		return true
	}
	for _, d := range devices {
		if d.ID == t.DeviceID && (d.Fingerprint == ref || d.Name == ref || d.DisplayName == ref) {
			return true
		}
	}
	return false
}

// unavailable：没有任何设备通过派发规则时，逐台说出原因。
func unavailable(agent string, p *dispatchPlan) error {
	verdicts := make([]DeviceVerdict, 0, len(p.Devices))
	for _, d := range p.Devices {
		verdicts = append(verdicts, DeviceVerdict{Name: d.DeviceName, Reason: d.Reason})
	}
	return errors.New(NowhereToRunMessage(agent, verdicts))
}

func (a *Client) startSession(ctx context.Context, in SendInput) (Reply, error) {
	var agentDoc *agentrewire.CtlResource
	var err error
	if in.AgentID > 0 {
		agentDoc = a.cat.ByID(agentrewire.CtlKind_CTL_KIND_AGENT, in.AgentID)
		if agentDoc == nil {
			return Reply{}, fmt.Errorf("agent %d not found", in.AgentID)
		}
	} else if agentDoc, err = a.cat.Locate(agentrewire.CtlKind_CTL_KIND_AGENT, in.Agent); err != nil {
		return Reply{}, err
	}
	agent := agentDoc.GetAgent()
	if agent.GetSyncId() == "" {
		return Reply{}, missingSyncID("agent")
	}
	projectSync := ""
	if in.Project != "" {
		it, err := a.cat.Locate(agentrewire.CtlKind_CTL_KIND_PROJECT, in.Project)
		if err != nil {
			return Reply{}, err
		}
		if projectSync = it.GetProject().GetSyncId(); projectSync == "" {
			return Reply{}, missingSyncID("project")
		}
	}
	model, err := a.resolveModel(in.Model)
	if err != nil {
		return Reply{}, err
	}
	engineSync, err := a.resolveEngine(in.Engine)
	if err != nil {
		return Reply{}, err
	}
	p, err := a.plan(ctx, agent.GetSyncId(), projectSync, "", engineSync)
	if err != nil {
		return Reply{}, err
	}
	if in.Device != "" {
		device, err := a.pickDevice(ctx, agent.GetName(), p, in.Device)
		if err != nil {
			return Reply{}, err
		}
		if p, err = a.plan(ctx, agent.GetSyncId(), projectSync, device, engineSync); err != nil {
			return Reply{}, err
		}
	}
	choice := p.Chosen
	if choice == nil {
		return Reply{}, unavailable(agent.GetName(), p)
	}
	conn, err := a.peer.Open(ctx, relaypeer.MachineTarget(choice.DeviceFingerprint))
	if err != nil {
		return Reply{}, a.ReachErr(err, choice.DeviceName, "")
	}
	id, err := conversationid.New()
	if err != nil {
		return Reply{}, err
	}
	title := in.Title
	if title == "" {
		title = deriveTitle(in.Text)
	}
	// 运行时类型暂时仍走旧的 backend 字段（协议切换前对端据它建这一轮），引擎配置由同步标识指明。
	req := &agentrewire.RuntimeRunRequest{
		ConversationId: id, Title: title, AgentSyncId: agent.GetSyncId(), ProjectSyncId: projectSync,
		Cwd: choice.Cwd, Backend: &agentrewire.AgentBackend{Type: choice.Runtime}, EngineProfileSyncId: choice.EngineSyncID,
	}
	a.fillTurn(req, conn, in, model)
	if choice.Kind == "agentred" && choice.Runtime == "piagent" {
		err = a.runPiGeneration(ctx, conn, req)
	} else {
		_, err = wirecall.RuntimeRun(ctx, conn, req)
	}
	if err != nil {
		return Reply{}, a.ReachErr(err, choice.DeviceName, id)
	}
	if !in.Isolated {
		a.save(ctx, choice.DeviceFingerprint, conn.SelfFingerprint, id)
	}
	h := &session{conn: conn, device: choice.DeviceName, summary: &agentrewire.SessionSummary{ConversationId: id, PeerFingerprint: conn.SelfFingerprint}}
	// 会话已经在跑了：钉不住不算派发失败（这一轮照样按覆盖跑），但要如实说。
	if err := a.pin(ctx, h, in, model); err != nil {
		a.warnf("this turn uses the overrides, but they were not kept for later turns: %v", err)
	}
	out := Reply{ConversationID: id, Agent: agent.GetName(), Device: choice.DeviceName, Status: StatusRunning}
	return a.finishSend(ctx, out, h, in)
}

// fillTurn 填这一轮里与调用方相关的那几格：说了什么、贴了什么图、从哪来、覆盖了什么
// （只改给了覆盖的那几项，其余保留 req 上已有的值）。
func (a *Client) fillTurn(req *agentrewire.RuntimeRunRequest, conn *relaypeer.Conn, in SendInput, model *modelTarget) {
	req.UserText = in.Text
	req.UserBlocks = userBlocks(in.Images)
	req.SourceDevice = conn.SelfFingerprint
	req.SourceDeviceName = a.opts.DeviceName
	req.PermissionMode = in.PermissionMode
	if in.Effort != "" {
		req.ReasoningEffort = in.Effort
	}
	switch {
	case model != nil:
		req.LlmProviderKey, req.LlmModelKey = model.providerKey, model.modelKey
	case in.CLIModel != "":
		req.CliModelMode, req.CliModel = "fixed", in.CLIModel
	}
}

// runPiGeneration 是 agentred 上 piagent 的三步开局（与控制台 dispatch.ts 相同）：同一个
// generation owner 先登记、再准备出 provider 会话，最后点名它开跑；任一步失败就撤掉。
func (a *Client) runPiGeneration(ctx context.Context, conn *relaypeer.Conn, req *agentrewire.RuntimeRunRequest) error {
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
func (a *Client) save(ctx context.Context, device, peer, id string) {
	body, _ := json.Marshal(map[string]string{"device_fingerprint": device, "peer_fingerprint": peer, "conversation_id": id})
	if _, err := a.srv.PostRaw(ctx, "/v1/saved-sessions", body); err != nil {
		a.warnf("the session runs but was not saved to the account (%v); it will not show up in the console", ServerErr(err))
	}
}

// pin 把模型类覆盖钉在会话上，对之后的轮次生效（runtime.run 上的只管这一轮）。每一项
// 都试一遍，交回没钉住的那些。
func (a *Client) pin(ctx context.Context, h *session, in SendInput, model *modelTarget) error {
	id, origin := h.summary.GetConversationId(), h.origin()
	var errs []error
	switch {
	case model != nil:
		_, err := wirecall.SetModelTarget(ctx, h.conn, &agentrewire.SetModelTargetRequest{ConversationId: id, PeerFingerprint: origin,
			ProviderKey: model.providerKey, ModelKey: model.modelKey})
		errs = append(errs, err)
	case in.CLIModel != "":
		_, err := wirecall.SetSessionCLIModel(ctx, h.conn, &agentrewire.SetSessionCLIModelRequest{ConversationId: id, PeerFingerprint: origin,
			Mode: "fixed", Value: in.CLIModel})
		errs = append(errs, err)
	}
	if in.Effort != "" {
		_, err := wirecall.SetSessionReasoningEffort(ctx, h.conn, &agentrewire.SetSessionReasoningEffortRequest{ConversationId: id,
			PeerFingerprint: origin, ReasoningEffort: in.Effort})
		errs = append(errs, err)
	}
	if err := errors.Join(errs...); err != nil {
		return a.ReachErr(err, h.device, id)
	}
	return nil
}

func (a *Client) continueSession(ctx context.Context, in SendInput) (Reply, error) {
	model, err := a.resolveModel(in.Model)
	if err != nil {
		return Reply{}, err
	}
	h, err := a.openSession(ctx, in.Session)
	if err != nil {
		return Reply{}, a.ReachErr(err, "", in.Session)
	}
	out := Reply{ConversationID: in.Session, Agent: a.agentName(h.summary.GetAgentSyncId()), Device: h.device, Status: StatusRunning}
	if out.Device == "" {
		out.Device = "its device"
	}
	if h.summary.GetLifecycleState() == lifecycleRunning && !in.Steer {
		return Reply{}, ErrSessionBusy
	}
	if err := a.pin(ctx, h, in, model); err != nil {
		return Reply{}, err
	}
	if h.summary.GetLifecycleState() == lifecycleRunning {
		return a.steerInto(ctx, h, in, out)
	}
	s := h.summary
	// 这一轮照会话记下的设置跑（与控制台每轮带上会话的有效设置同一条）：runtime.run 只按
	// 请求上的值执行，不带就退回 agent 绑定，此前钉住的覆盖就在之后的轮次里丢了。
	req := &agentrewire.RuntimeRunRequest{
		ConversationId: s.GetConversationId(), PeerFingerprint: s.GetPeerFingerprint(), Cwd: s.GetCwd(), Title: s.GetTitle(),
		AgentSyncId: s.GetAgentSyncId(), ProjectSyncId: s.GetProjectSyncId(), Backend: &agentrewire.AgentBackend{Type: s.GetBackendType()},
		LlmProviderKey: s.GetProviderKey(), LlmModelKey: s.GetModelKey(),
		CliModelMode: s.GetCliModelMode(), CliModel: s.GetCliModel(), ReasoningEffort: s.GetReasoningEffort(),
	}
	a.fillTurn(req, h.conn, in, model)
	if _, err := wirecall.RuntimeRun(ctx, h.conn, req); err != nil {
		if isSessionRunning(err) && in.Steer {
			return a.steerInto(ctx, h, in, out)
		}
		return Reply{}, a.ReachErr(err, h.device, in.Session)
	}
	return a.finishSend(ctx, out, h, in)
}

func (a *Client) steerInto(ctx context.Context, h *session, in SendInput, out Reply) (Reply, error) {
	if err := h.attach(ctx); err != nil {
		return Reply{}, a.ReachErr(err, h.device, in.Session)
	}
	_, err := wirecall.RuntimeSteer(ctx, h.conn, &agentrewire.RuntimeSteerRequest{
		ConversationId: in.Session, PeerFingerprint: h.origin(), QueuedId: randomHex(16), Text: in.Text,
	})
	if err != nil {
		return Reply{}, a.ReachErr(err, h.device, in.Session)
	}
	out.Steered = true
	return a.finishSend(ctx, out, h, in)
}

func (a *Client) finishSend(ctx context.Context, out Reply, h *session, in SendInput) (Reply, error) {
	if !in.Wait {
		return out, nil
	}
	o, err := a.wait(ctx, out.ConversationID, in.Timeout, h)
	if err != nil {
		return Reply{}, err
	}
	o.Agent, o.Device, o.Steered = out.Agent, out.Device, out.Steered
	return o, nil
}

func (a *Client) agentName(syncID string) string {
	items, err := a.cat.List(agentrewire.CtlKind_CTL_KIND_AGENT)
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
func (a *Client) openSession(ctx context.Context, id string) (*session, error) {
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
			return nil, ErrSessionNotVisible
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
			return &session{conn: conn, summary: sum, device: d.label(), deviceFP: d.Fingerprint}, nil
		}
		_ = conn.Close()
	}
	return nil, ErrSessionNotVisible
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

// Wait 等到会话空闲、进入等待输入或 timeout 到期（0 = 不超时）。
func (a *Client) Wait(ctx context.Context, id string, timeout time.Duration) (Reply, error) {
	return a.wait(ctx, id, timeout, nil)
}

// wait 同 Wait；h 非 nil 时先用它那条通道。
// relay 断线、设备暂时离线时重连再查；设备一直不回来，超过 timeout 就按超时处理。
func (a *Client) wait(ctx context.Context, id string, timeout time.Duration, h *session) (Reply, error) {
	start := time.Now()
	expired := func() bool { return timeout > 0 && time.Since(start) >= timeout }
	timedOut := Reply{ConversationID: id, Status: StatusRunning}
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
				if !transient(err) || (!connected && errors.Is(err, relaypeer.ErrRelayUnavailable)) || (timeout == 0 && failures >= MaxReconnects) {
					return Reply{}, a.ReachErr(err, "", id)
				}
				failures++
				time.Sleep(a.opts.ReconnectDelay)
				continue
			}
			connected = true
		}
		out, done, err := a.check(ctx, h, id)
		if err != nil {
			_ = h.conn.Close()
			h = nil
			if !transient(err) || (timeout == 0 && failures >= MaxReconnects) {
				return Reply{}, a.ReachErr(err, "", id)
			}
			failures++
			time.Sleep(a.opts.ReconnectDelay)
			continue
		}
		failures = 0
		if done {
			return out, nil
		}
		wait := a.opts.PollInterval
		if timeout > 0 {
			wait = min(wait, max(timeout-time.Since(start), time.Millisecond))
		}
		select {
		case <-ctx.Done():
			return Reply{}, ctx.Err()
		case <-h.conn.Done():
			h = nil
		case <-time.After(wait):
		}
	}
}

// state 读会话此刻的状态：等待输入（含审批卡；交回待处理项）、失败、空闲，或仍在跑。
func (a *Client) state(ctx context.Context, h *session, id string) (string, *agentrewire.SessionPendingWaitersResponse, error) {
	sum, err := summaryOf(ctx, h.conn, id)
	if err != nil {
		return "", nil, err
	}
	if sum == nil {
		return "", nil, ErrSessionNotVisible
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
			return StatusWaiting, p, nil
		}
		if state == lifecycleRunning {
			return StatusRunning, nil, nil
		}
	}
	if state == lifecycleFailed {
		return StatusFailed, nil, nil
	}
	return StatusIdle, nil, nil
}

// check 查一次会话状态：等待输入、空闲 / 失败，或仍在跑（done = false）。
func (a *Client) check(ctx context.Context, h *session, id string) (Reply, bool, error) {
	status, p, err := a.state(ctx, h, id)
	if err != nil {
		return Reply{}, false, err
	}
	switch status {
	case StatusRunning:
		return Reply{}, false, nil
	case StatusWaiting:
		raw, err := json.Marshal(pendingJSON(p))
		if err != nil {
			return Reply{}, false, err
		}
		return Reply{ConversationID: id, Status: StatusWaiting, Pending: raw}, true, nil
	}
	text, failure, err := lastTurn(ctx, h, id)
	if err != nil {
		return Reply{}, false, err
	}
	if status == StatusFailed {
		if failure == "" {
			failure = "the turn failed"
		}
		return Reply{ConversationID: id, Status: StatusFailed, Error: failure}, true, nil
	}
	return Reply{ConversationID: id, Status: StatusIdle, Text: text}, true, nil
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

// StopReply 是 stop 的结果（与桌面 /ctl/v1/stop 同形）。
type StopReply struct {
	Stopped       bool `json:"stopped"`
	Running       bool `json:"running"`
	DroppedSteers int  `json:"droppedSteers"`
}

// Stop 停止正在运行的一轮并丢弃还没送达的插话。先取走插话、再中止：反过来的话，这一轮
// 收尾时 agentred 会把残留插话当下一轮补发出去。
func (a *Client) Stop(ctx context.Context, id string) (StopReply, error) {
	h, err := a.openSession(ctx, id)
	if err != nil {
		return StopReply{}, a.ReachErr(err, "", id)
	}
	if h.summary.GetLifecycleState() != lifecycleRunning {
		return StopReply{}, nil
	}
	if err := h.attach(ctx); err != nil {
		return StopReply{}, a.ReachErr(err, h.device, id)
	}
	drained, err := wirecall.RuntimeDrainPending(ctx, h.conn, &agentrewire.RuntimeDrainPendingRequest{ConversationId: id, PeerFingerprint: h.origin()})
	if err != nil {
		return StopReply{}, a.ReachErr(err, h.device, id)
	}
	if _, err := wirecall.RuntimeAbort(ctx, h.conn, &agentrewire.RuntimeAbortRequest{ConversationId: id, PeerFingerprint: h.origin()}); err != nil {
		if rpcCode(err) == rpcerror.CodeRuntimeNoActiveTurn {
			return StopReply{DroppedSteers: len(drained.GetSteers())}, nil
		}
		return StopReply{}, a.ReachErr(err, h.device, id)
	}
	return StopReply{Stopped: true, Running: true, DroppedSteers: len(drained.GetSteers())}, nil
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
func userBlocks(images []Image) []*agentrewire.StoredBlock {
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
