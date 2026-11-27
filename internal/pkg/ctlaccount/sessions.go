package ctlaccount

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/agentre-hub/agentre/internal/pkg/cagoenvelope"
	"github.com/agentre-hub/agentre/internal/pkg/relaypeer"
	"github.com/agentre-hub/agentre/pkg/wire/agentrewire"
	"github.com/agentre-hub/agentre/pkg/wire/rpcerror"
	"github.com/agentre-hub/agentre/pkg/wire/wirecall"
)

// 账号模式的会话查询：账号索引（server 保存下来的会话，控制台与 CLI 发起的都在）加上在线
// 设备上正在跑的会话；离线设备上没保存的会话看不到，保存过的按最后已知状态显示并标明设备
// 离线。flag、输出与退出码与桌面模式相同（query.go）。

// errPendingGone 与桌面执行者同一句话。
const errPendingGone = "the pending item no longer exists (already answered elsewhere or timed out)"

// SessionFilter 是 list sessions 的筛选（Project 是名字、父/子 路径或 id）。
type SessionFilter struct {
	Agent, Project, Device, Status string
	Mine                           bool
	Limit                          int
}

// SessionRow 是 list sessions 的一行（与桌面 /ctl/v1/conversations 的 sessions[] 同形）。
type SessionRow struct {
	ID             string `json:"id"`
	Title          string `json:"title"`
	Agent          string `json:"agent"`
	Device         string `json:"device"`
	Status         string `json:"status"`
	LastActivityAt string `json:"lastActivityAt,omitempty"`
	// DeviceOffline 只在账号模式出现：承载设备此刻离线，这一行是它最后已知的状态。
	DeviceOffline bool `json:"deviceOffline,omitempty"`
}

// PendingQuestion 是待处理提问卡里的一道题。
type PendingQuestion struct {
	Question string `json:"question"`
	Options  []struct {
		Label string `json:"label"`
	} `json:"options"`
}

// PendingItem 是一条待处理项（与桌面 /ctl/v1/pending 的 items[] 同形）。
type PendingItem struct {
	ID        string            `json:"id"`
	Session   string            `json:"session"`
	Kind      string            `json:"kind"`
	Tool      string            `json:"tool,omitempty"`
	Input     json.RawMessage   `json:"input,omitempty"`
	Questions []PendingQuestion `json:"questions,omitempty"`
	// Title / Summary 只有账号模式的审批卡（kind "approval"）带。
	Title   string          `json:"title,omitempty"`
	Summary json.RawMessage `json:"summary,omitempty"`
}

// AnswerInput 是一次作答。
type AnswerInput struct {
	Session, Item string
	Options       []string
	Text          string
	HasText       bool
	Allow, Deny   bool
	Message       string
}

// savedSession 是账号会话索引（GET /v1/agent-sessions）的一行。
type savedSession struct {
	ConversationID    string `json:"conversation_id"`
	PeerFingerprint   string `json:"peer_fingerprint"`
	DeviceFingerprint string `json:"device_fingerprint"`
	Title             string `json:"title"`
	AgentSyncID       string `json:"agent_sync_id"`
	ProjectSyncID     string `json:"project_sync_id"`
	BackendType       string `json:"backend_type"`
	LifecycleState    string `json:"lifecycle_state"`
	WaitingForInput   bool   `json:"waiting_for_input"`
	LastMessageAt     int64  `json:"last_message_at"`
	ProviderKey       string `json:"provider_key"`
	ModelKey          string `json:"model_key"`
	CLIModelMode      string `json:"cli_model_mode"`
	CLIModel          string `json:"cli_model"`
}

// savedIndexPages 是翻索引的页数上限（一页的大小由 server 定）。
const savedIndexPages = 50

func (a *Client) savedIndex(ctx context.Context) ([]savedSession, error) {
	var all []savedSession
	cursor := ""
	for range savedIndexPages {
		q := url.Values{"axis": {"time"}, "scope": {"time"}}
		if cursor != "" {
			q.Set("cursor", cursor)
		}
		raw, err := a.srv.GetRaw(ctx, "/v1/agent-sessions?"+q.Encode())
		if err != nil {
			return nil, ServerErr(err)
		}
		var page struct {
			Items   []savedSession `json:"items"`
			Cursor  string         `json:"cursor"`
			HasMore bool           `json:"has_more"`
		}
		if err := cagoenvelope.Decode(raw, &page); err != nil {
			return nil, fmt.Errorf("decode the session index: %w", err)
		}
		all = append(all, page.Items...)
		if !page.HasMore || page.Cursor == "" {
			break
		}
		cursor = page.Cursor
	}
	return all, nil
}

// savedRow 按会话号精确读索引里的一行（没保存过 = nil）。
func (a *Client) savedRow(ctx context.Context, id string) (*savedSession, error) {
	raw, err := a.srv.GetRaw(ctx, "/v1/agent-sessions?conversation_id="+url.QueryEscape(id))
	if err != nil {
		return nil, ServerErr(err)
	}
	var page struct {
		Items []savedSession `json:"items"`
	}
	if err := cagoenvelope.Decode(raw, &page); err != nil {
		return nil, fmt.Errorf("decode the session index: %w", err)
	}
	for i := range page.Items {
		if page.Items[i].ConversationID == id {
			return &page.Items[i], nil
		}
	}
	return nil, nil
}

// liveSession 是在线设备上读到的一条会话，连同读到它的那条通道。
type liveSession struct {
	summary *agentrewire.SessionSummary
	device  accountDevice
	conn    *relaypeer.Conn
}

// liveSessions 逐台读在线设备（agentred / 桌面）上的会话。某台读不动只报警告，不挡其余的。
// 交回的 closer 关掉所有通道。
func (a *Client) liveSessions(ctx context.Context, devices []accountDevice) ([]liveSession, func()) {
	var out []liveSession
	var conns []*relaypeer.Conn
	for _, d := range devices {
		if !d.Online || (d.Kind != "agentred" && d.Kind != "desktop") {
			continue
		}
		conn, err := a.peer.Open(ctx, relaypeer.MachineTarget(d.Fingerprint))
		if err != nil {
			a.warnf("could not read the sessions of %s: %v", d.label(), a.ReachErr(err, d.label(), ""))
			continue
		}
		conns = append(conns, conn)
		resp, err := wirecall.SessionList(ctx, conn, &agentrewire.SessionListRequest{})
		if err != nil {
			a.warnf("could not read the sessions of %s: %v", d.label(), a.ReachErr(err, d.label(), ""))
			continue
		}
		for _, s := range resp.GetSessions() {
			out = append(out, liveSession{summary: s, device: d, conn: conn})
		}
	}
	return out, func() {
		for _, c := range conns {
			_ = c.Close()
		}
	}
}

// sessionStatus 把执行端的生命周期换算成 list / get 的状态词（与桌面同一组）。
func sessionStatus(lifecycle string, waiting bool) string {
	switch {
	case lifecycle == lifecycleFailed:
		return StatusFailed
	case waiting:
		return StatusWaiting
	case lifecycle == lifecycleRunning:
		return StatusRunning
	default:
		return StatusIdle
	}
}

func rfc3339Ms(ms int64) string {
	if ms <= 0 {
		return ""
	}
	return time.UnixMilli(ms).UTC().Format(time.RFC3339)
}

// names 是目录里 sync id → 名字的对照（agent、项目）；读不到时留空，显示退回 sync id。
type names struct{ agents, projects map[string]string }

func (a *Client) names() names {
	n := names{agents: map[string]string{}, projects: map[string]string{}}
	if items, err := a.cat.List(agentrewire.CtlKind_CTL_KIND_AGENT); err == nil {
		for _, it := range items {
			n.agents[it.GetAgent().GetSyncId()] = it.GetAgent().GetName()
		}
	}
	if items, err := a.cat.List(agentrewire.CtlKind_CTL_KIND_PROJECT); err == nil {
		for _, it := range items {
			n.projects[it.GetProject().GetSyncId()] = it.GetProject().GetName()
		}
	}
	return n
}

// modelID 把会话记下的模型 key（服务生成的引用键）换成用户认得的模型 id；空 key（跟随
// 后端配置）与目录里已经没有的 key 交回空串 —— 不往外写一串内部 key。
func (a *Client) modelID(key string) string {
	if key == "" {
		return ""
	}
	items, err := a.cat.List(agentrewire.CtlKind_CTL_KIND_MODEL)
	if err != nil {
		return ""
	}
	for _, it := range items {
		if m := it.GetModel(); m.GetKey() == key {
			return m.GetModelId()
		}
	}
	return ""
}

func (n names) agent(syncID string) string   { return orSync(n.agents, syncID) }
func (n names) project(syncID string) string { return orSync(n.projects, syncID) }

func orSync(m map[string]string, syncID string) string {
	if v := m[syncID]; v != "" {
		return v
	}
	return syncID
}

// sessionEntry 是合并之后的一条会话。
type sessionEntry struct {
	row                  SessionRow
	peer, agent, project string
	deviceFP             string
	at                   int64
}

func deviceByFP(devices []accountDevice, fp string) (label string, online bool) {
	for _, d := range devices {
		if d.Fingerprint == fp {
			return d.label(), d.Online
		}
	}
	return "", false
}

func (a *Client) ListSessions(ctx context.Context, f SessionFilter) ([]SessionRow, error) {
	devices, err := a.devices(ctx)
	if err != nil {
		return nil, err
	}
	saved, err := a.savedIndex(ctx)
	if err != nil {
		return nil, err
	}
	live, closeAll := a.liveSessions(ctx, devices)
	defer closeAll()
	nm := a.names()

	byID := map[string]*sessionEntry{}
	var order []string
	for _, it := range saved {
		label, online := deviceByFP(devices, it.DeviceFingerprint)
		byID[it.ConversationID] = &sessionEntry{
			row: SessionRow{ID: it.ConversationID, Title: it.Title, Agent: nm.agent(it.AgentSyncID), Device: label,
				Status: sessionStatus(it.LifecycleState, it.WaitingForInput), LastActivityAt: rfc3339Ms(it.LastMessageAt), DeviceOffline: !online},
			peer: it.PeerFingerprint, agent: it.AgentSyncID, project: it.ProjectSyncID, deviceFP: it.DeviceFingerprint, at: it.LastMessageAt,
		}
		order = append(order, it.ConversationID)
	}
	self := ""
	for _, l := range live {
		self = l.conn.SelfFingerprint
		s := l.summary
		e := byID[s.GetConversationId()]
		if e == nil {
			e = &sessionEntry{}
			byID[s.GetConversationId()] = e
			order = append(order, s.GetConversationId())
		}
		e.row = SessionRow{ID: s.GetConversationId(), Title: s.GetTitle(), Agent: nm.agent(s.GetAgentSyncId()), Device: l.device.label(),
			Status: sessionStatus(s.GetLifecycleState(), s.GetWaitingForInput()), LastActivityAt: rfc3339Ms(s.GetLastMessageAt())}
		// agentred 只在别的对端发起的会话上标 peer_fingerprint，调用方自己发起的留空
		// （空 = 这条连接的调用方，见 handlers/session_catchup.go List）。
		e.peer = s.GetPeerFingerprint()
		if e.peer == "" {
			e.peer = l.conn.SelfFingerprint
		}
		e.agent, e.project, e.deviceFP, e.at = s.GetAgentSyncId(), s.GetProjectSyncId(), l.device.Fingerprint, s.GetLastMessageAt()
	}
	if f.Mine && self == "" && a.opts.SelfFingerprint != nil {
		// 没有在线设备报出调用方的身份时问宿主：账号索引里保存的会话照样认得出是谁发起的。
		if self, err = a.opts.SelfFingerprint(ctx); err != nil {
			return nil, err
		}
	}
	if f.Mine && self == "" {
		return nil, errors.New("cannot tell which sessions are yours: no online device to ask (--mine needs one)")
	}

	agentSync, projectSync, err := a.filterRefs(f)
	if err != nil {
		return nil, err
	}
	var entries []*sessionEntry
	for _, id := range order {
		e := byID[id]
		switch {
		case f.Status != "" && e.row.Status != f.Status,
			f.Mine && e.peer != self,
			agentSync != "" && e.agent != agentSync,
			projectSync != "" && e.project != projectSync,
			f.Device != "" && !deviceRefMatches(devices, e.deviceFP, e.row.Device, f.Device):
			continue
		}
		entries = append(entries, e)
	}
	sort.SliceStable(entries, func(i, j int) bool { return entries[i].at > entries[j].at })
	if f.Limit > 0 && len(entries) > f.Limit {
		entries = entries[:f.Limit]
	}
	rows := make([]SessionRow, 0, len(entries))
	for _, e := range entries {
		rows = append(rows, e.row)
	}
	return rows, nil
}

func deviceRefMatches(devices []accountDevice, fp, label, ref string) bool {
	if fp == ref || label == ref {
		return true
	}
	for _, d := range devices {
		if d.Fingerprint == fp && (d.Name == ref || d.DisplayName == ref) {
			return true
		}
	}
	return false
}

func (a *Client) filterRefs(f SessionFilter) (agentSync, projectSync string, err error) {
	if f.Agent != "" {
		it, err := a.cat.Locate(agentrewire.CtlKind_CTL_KIND_AGENT, f.Agent)
		if err != nil {
			return "", "", err
		}
		if agentSync = it.GetAgent().GetSyncId(); agentSync == "" {
			return "", "", missingSyncID("agent")
		}
	}
	if f.Project != "" {
		it, err := a.cat.Locate(agentrewire.CtlKind_CTL_KIND_PROJECT, f.Project)
		if err != nil {
			return "", "", err
		}
		if projectSync = it.GetProject().GetSyncId(); projectSync == "" {
			return "", "", missingSyncID("project")
		}
	}
	return agentSync, projectSync, nil
}

// ─── get ───

// SessionDetail 是账号模式 get session 的响应：拿不到的字段省略，不编造。
type SessionDetail struct {
	ID             string `json:"id"`
	Title          string `json:"title"`
	Agent          string `json:"agent"`
	Project        string `json:"project,omitempty"`
	Device         string `json:"device,omitempty"`
	DeviceOffline  bool   `json:"deviceOffline,omitempty"`
	Backend        string `json:"backend,omitempty"`
	Model          string `json:"model,omitempty"`
	Status         string `json:"status"`
	LastActivityAt string `json:"lastActivityAt,omitempty"`
	Pending        *int   `json:"pending,omitempty"`
	// Initiator 是发起这条会话的人话标签，InitiatorKind 是它的种类
	// （desktop | console | cli | token | agentred）；认不出发起方时两者都省略，
	// 认得出是 token 却看不到它的名字时只省略 Initiator。
	Initiator     string `json:"initiator,omitempty"`
	InitiatorKind string `json:"initiatorKind,omitempty"`
}

// initiatorOf 把会话的发起对端指纹（空 = 调用方自己）换成人话标签与种类；认不出时交回两个空串。
// token 的名字由宿主按指纹查（登录态读 token 列表，个人 token 只看得到自己）；查不到时名字省略、
// 种类仍是 token —— 不拿占位串冒充 token 名。
func (a *Client) initiatorOf(ctx context.Context, peer string, devices []accountDevice) (label, kind string) {
	self := ""
	if a.opts.SelfFingerprint != nil {
		self, _ = a.opts.SelfFingerprint(ctx)
	}
	if peer == "" {
		peer = self
	}
	if peer == "" {
		return "", ""
	}
	if a.opts.ConsolePeer != nil {
		if console, err := a.opts.ConsolePeer(ctx); err == nil && console != "" && console == peer {
			return "console", "console"
		}
	}
	if strings.HasPrefix(peer, "pat:") {
		if a.opts.TokenName != nil {
			if name, err := a.opts.TokenName(ctx, peer); err == nil && name != "" {
				return name, "token"
			}
		}
		return "", "token"
	}
	for _, d := range devices {
		if d.Fingerprint != peer {
			continue
		}
		if peer == self && d.Kind == "cli" {
			return "this agrctl (" + d.label() + ")", "cli"
		}
		return d.label(), d.Kind
	}
	return "", ""
}

// initiatorFields 读设备名单后解出发起方；名单读不到只是认不出，不让 get 失败。
func (a *Client) initiatorFields(ctx context.Context, peer string) (label, kind string) {
	devices, err := a.devices(ctx)
	if err != nil {
		devices = nil
	}
	return a.initiatorOf(ctx, peer, devices)
}

func (a *Client) GetSession(ctx context.Context, id string) (*SessionDetail, error) {
	nm := a.names()
	h, err := a.openSession(ctx, id)
	switch {
	case err == nil:
		defer func() { _ = h.conn.Close() }()
		s := h.summary
		p, err := wirecall.SessionPendingWaiters(ctx, h.conn, &agentrewire.SessionPendingWaitersRequest{ConversationId: id, PeerFingerprint: h.origin()})
		if err != nil {
			return nil, a.ReachErr(err, h.device, id)
		}
		device, deviceFP := h.device, h.deviceFP
		if device == "" || deviceFP == "" {
			label, fp := a.savedDevice(ctx, id)
			device, deviceFP = cmp.Or(device, label), cmp.Or(deviceFP, fp)
		}
		model := a.sessionModel(modelChoice{providerKey: s.GetProviderKey(), modelKey: s.GetModelKey(),
			cliMode: s.GetCliModelMode(), cliModel: s.GetCliModel()}, s.GetAgentSyncId(), s.GetBackendType(), deviceFP)
		n := len(p.GetToolPermissions()) + len(p.GetAskUserQuestions()) + len(p.GetApprovals())
		who, whoKind := a.initiatorFields(ctx, h.origin())
		return &SessionDetail{Initiator: who, InitiatorKind: whoKind, ID: id, Title: s.GetTitle(), Agent: nm.agent(s.GetAgentSyncId()), Project: nm.project(s.GetProjectSyncId()),
			Device: device, Backend: s.GetBackendType(), Model: model, LastActivityAt: rfc3339Ms(s.GetLastMessageAt()),
			Status: sessionStatus(s.GetLifecycleState(), s.GetWaitingForInput() || n > 0), Pending: &n}, nil
	case errors.Is(err, relaypeer.ErrTargetOffline):
		row, rerr := a.savedRow(ctx, id)
		if rerr != nil {
			return nil, rerr
		}
		if row == nil {
			return nil, a.ReachErr(err, "", id)
		}
		devices, derr := a.devices(ctx)
		if derr != nil {
			return nil, derr
		}
		label, _ := deviceByFP(devices, row.DeviceFingerprint)
		who, whoKind := a.initiatorOf(ctx, row.PeerFingerprint, devices)
		model := a.sessionModel(modelChoice{providerKey: row.ProviderKey, modelKey: row.ModelKey,
			cliMode: row.CLIModelMode, cliModel: row.CLIModel}, row.AgentSyncID, row.BackendType, row.DeviceFingerprint)
		return &SessionDetail{Initiator: who, InitiatorKind: whoKind, ID: id, Title: row.Title, Agent: nm.agent(row.AgentSyncID), Project: nm.project(row.ProjectSyncID),
			Device: label, DeviceOffline: true, Backend: row.BackendType, Model: model, LastActivityAt: rfc3339Ms(row.LastMessageAt),
			Status: sessionStatus(row.LifecycleState, row.WaitingForInput)}, nil
	default:
		return nil, a.ReachErr(err, "", id)
	}
}

// deviceOf 是承载一条保存过的会话的设备名（不知道时为空）。
func (a *Client) deviceOf(ctx context.Context, id string) string {
	label, _ := a.savedDevice(ctx, id)
	return label
}

// savedDevice 是承载一条保存过的会话的设备名与指纹（不知道时为空）。
func (a *Client) savedDevice(ctx context.Context, id string) (label, fingerprint string) {
	row, err := a.savedRow(ctx, id)
	if err != nil || row == nil {
		return "", ""
	}
	devices, err := a.devices(ctx)
	if err != nil {
		return "", row.DeviceFingerprint
	}
	label, _ = deviceByFP(devices, row.DeviceFingerprint)
	return label, row.DeviceFingerprint
}

// modelChoice 是一条会话自己钉的模型（机器上的会话摘要与账号索引那一行记的是同一组）。
type modelChoice struct {
	providerKey, modelKey string
	cliMode, cliModel     string
}

// sessionModel 是 get session 的 model：会话钉了 CLI 模型就是它；钉了固定模型就是那个模型 id；
// 什么都没钉（跟随后端配置）就是后端的固定模型。钉了供应商默认或 CLI 自身默认时这里不知道
// 是哪个，交回空串 —— 不猜。
func (a *Client) sessionModel(c modelChoice, agentSyncID, backendType, deviceFP string) string {
	switch {
	case c.cliMode == "fixed":
		return c.cliModel
	case c.cliMode != "":
		return ""
	case c.modelKey != "":
		return a.modelID(c.modelKey)
	case c.providerKey != "":
		return ""
	}
	return a.followedModelID(agentSyncID, backendType, deviceFP)
}

// followedModelID 是一条没钉模型（跟随后端配置）的会话实际跑的固定模型 id：它那位 agent 的
// 后端里，类型相同、跑在这台设备上的那一个（设备不知道时只认唯一同类型的那个）的固定模型。
// 后端跟随供应商默认模型、找不到或认不出唯一一个时交回空串 —— 不猜。
func (a *Client) followedModelID(agentSyncID, backendType, deviceFP string) string {
	if agentSyncID == "" {
		return ""
	}
	agents, err := a.cat.List(agentrewire.CtlKind_CTL_KIND_AGENT)
	if err != nil {
		return ""
	}
	var agent *agentrewire.CtlAgent
	for _, it := range agents {
		if it.GetAgent().GetSyncId() == agentSyncID {
			agent = it.GetAgent()
		}
	}
	var match *agentrewire.CtlBackend
	for _, id := range agent.GetBackendIds() {
		b := a.cat.ByID(agentrewire.CtlKind_CTL_KIND_BACKEND, id).GetBackend()
		if b == nil || b.GetType() != backendType || (deviceFP != "" && b.GetDeviceFingerprint() != deviceFP) {
			continue
		}
		if match != nil {
			return ""
		}
		match = b
	}
	if match.GetModelId() == 0 {
		return ""
	}
	return a.cat.ByID(agentrewire.CtlKind_CTL_KIND_MODEL, match.GetModelId()).GetModel().GetModelId()
}

// ─── pending / answer ───

func pendingItemsOf(session string, p *agentrewire.SessionPendingWaitersResponse) []PendingItem {
	out := make([]PendingItem, 0, len(p.GetToolPermissions())+len(p.GetAskUserQuestions())+len(p.GetApprovals()))
	for _, t := range p.GetToolPermissions() {
		out = append(out, PendingItem{ID: t.GetRequestId(), Session: session, Kind: "tool_permission", Tool: t.GetToolName(), Input: rawJSON(t.GetInput())})
	}
	for _, q := range p.GetAskUserQuestions() {
		it := PendingItem{ID: q.GetRequestId(), Session: session, Kind: "question"}
		for _, aq := range q.GetQuestions() {
			pq := PendingQuestion{Question: aq.GetQuestion()}
			for _, o := range aq.GetOptions() {
				pq.Options = append(pq.Options, struct {
					Label string `json:"label"`
				}{Label: o.GetLabel()})
			}
			it.Questions = append(it.Questions, pq)
		}
		out = append(out, it)
	}
	for _, ap := range p.GetApprovals() {
		out = append(out, PendingItem{ID: ap.GetRequestId(), Session: session, Kind: "approval", Title: ap.GetTitle(), Summary: rawJSON(ap.GetSummary())})
	}
	return out
}

func (a *Client) Pending(ctx context.Context, id string) ([]PendingItem, error) {
	if id != "" {
		h, err := a.openSession(ctx, id)
		if err != nil {
			return nil, a.ReachErr(err, "", id)
		}
		defer func() { _ = h.conn.Close() }()
		p, err := wirecall.SessionPendingWaiters(ctx, h.conn, &agentrewire.SessionPendingWaitersRequest{ConversationId: id, PeerFingerprint: h.origin()})
		if err != nil {
			return nil, a.ReachErr(err, h.device, id)
		}
		return pendingItemsOf(id, p), nil
	}
	devices, err := a.devices(ctx)
	if err != nil {
		return nil, err
	}
	live, closeAll := a.liveSessions(ctx, devices)
	defer closeAll()
	sort.SliceStable(live, func(i, j int) bool { return live[i].summary.GetLastMessageAt() > live[j].summary.GetLastMessageAt() })
	var out []PendingItem
	for _, l := range live {
		s := l.summary
		if s.GetLifecycleState() != lifecycleRunning && !s.GetWaitingForInput() {
			continue
		}
		p, err := wirecall.SessionPendingWaiters(ctx, l.conn, &agentrewire.SessionPendingWaitersRequest{ConversationId: s.GetConversationId(), PeerFingerprint: s.GetPeerFingerprint()})
		if err != nil {
			if rpcCode(err) == rpcerror.CodeScopeDenied { // 个人 token 看不到别人发起的会话的待处理项
				continue
			}
			return nil, a.ReachErr(err, l.device.label(), s.GetConversationId())
		}
		out = append(out, pendingItemsOf(s.GetConversationId(), p)...)
	}
	return out, nil
}

// Answer 按卡的种类把答案交给承载会话的机器，交回卡的种类。
func (a *Client) Answer(ctx context.Context, in AnswerInput) (string, error) {
	h, err := a.openSession(ctx, in.Session)
	if err != nil {
		return "", a.ReachErr(err, "", in.Session)
	}
	defer func() { _ = h.conn.Close() }()
	p, err := wirecall.SessionPendingWaiters(ctx, h.conn, &agentrewire.SessionPendingWaitersRequest{ConversationId: in.Session, PeerFingerprint: h.origin()})
	if err != nil {
		return "", a.ReachErr(err, h.device, in.Session)
	}
	decision := in.Allow || in.Deny
	for _, q := range p.GetAskUserQuestions() {
		if q.GetRequestId() != in.Item {
			continue
		}
		if decision {
			return "", Usagef("a question is answered with --option or --text, not --allow / --deny")
		}
		answers, err := questionAnswers(q, in)
		if err != nil {
			return "", err
		}
		if err := h.attach(ctx); err != nil {
			return "", a.answerErr(err, h, in)
		}
		_, err = wirecall.RuntimeSubmitAnswer(ctx, h.conn, &agentrewire.RuntimeSubmitAnswerRequest{ConversationId: in.Session,
			PeerFingerprint: h.origin(), RequestId: in.Item, Questions: q.GetQuestions(), Answers: answers})
		return "question", a.answerErr(err, h, in)
	}
	for _, t := range p.GetToolPermissions() {
		if t.GetRequestId() != in.Item {
			continue
		}
		if !decision {
			return "", Usagef("a tool permission is answered with --allow or --deny, not --option / --text")
		}
		if err := h.attach(ctx); err != nil {
			return "", a.answerErr(err, h, in)
		}
		_, err := wirecall.RuntimeSubmitToolPermission(ctx, h.conn, &agentrewire.RuntimeSubmitToolPermissionRequest{ConversationId: in.Session,
			PeerFingerprint: h.origin(), RequestId: in.Item, Allow: in.Allow, DenyReason: in.Message})
		return "tool_permission", a.answerErr(err, h, in)
	}
	for _, ap := range p.GetApprovals() {
		if ap.GetRequestId() != in.Item {
			continue
		}
		if !decision {
			return "", Usagef("an approval card is answered with --allow or --deny, not --option / --text")
		}
		if in.Message != "" {
			return "", Usagef("an approval card takes no --message")
		}
		_, err := wirecall.ToolApprovalAnswer(ctx, h.conn, &agentrewire.ToolApprovalAnswerRequest{ConversationId: in.Session, RequestId: in.Item, Allow: in.Allow})
		return "approval", a.answerErr(err, h, in)
	}
	return "", errors.New(errPendingGone)
}

// answerErr 把作答的失败说成人话：执行端说卡已不挂起，与展示时就找不到是同一句。
func (a *Client) answerErr(err error, h *session, in AnswerInput) error {
	if err == nil {
		return nil
	}
	var rpcErr *rpcerror.Error
	if errors.As(err, &rpcErr) && rpcErr.Code == rpcerror.CodeInvalidParams && strings.Contains(rpcErr.Message, "no pending") {
		return errors.New(errPendingGone)
	}
	return a.ReachErr(err, h.device, in.Session)
}

// otherAnswerLabel 是提问卡「其它」答案的标签（与执行端 agentruntime.OtherAnswerLabel 同值）。
const otherAnswerLabel = "__other__"

func questionAnswers(q *agentrewire.PendingAskUserQuestion, in AnswerInput) ([]*agentrewire.AskAnswer, error) {
	if len(q.GetQuestions()) != 1 {
		return nil, Usagef("this card asks %d questions; answer it in the console or the desktop app", len(q.GetQuestions()))
	}
	one := q.GetQuestions()[0]
	if in.HasText {
		if one.GetDisallowOther() {
			return nil, Usagef("this question only accepts its listed options (--option)")
		}
		return []*agentrewire.AskAnswer{{QuestionIndex: 0, Labels: []string{otherAnswerLabel}, OtherText: in.Text}}, nil
	}
	if len(in.Options) > 1 && !one.GetMultiSelect() {
		return nil, Usagef("this question takes a single option")
	}
	var labels, names []string
	for _, o := range one.GetOptions() {
		names = append(names, o.GetLabel())
	}
	for _, o := range in.Options {
		label, ok := matchOption(names, o)
		if !ok {
			return nil, Usagef("--option %q is not one of: %s", o, strings.Join(names, ", "))
		}
		labels = append(labels, label)
	}
	return []*agentrewire.AskAnswer{{QuestionIndex: 0, Labels: labels}}, nil
}

// matchOption 先按选项文本精确匹配，再按 1 起的序号。
func matchOption(options []string, v string) (string, bool) {
	v = strings.TrimSpace(v)
	if slices.Contains(options, v) {
		return v, true
	}
	if n, err := strconv.Atoi(v); err == nil && n >= 1 && n <= len(options) {
		return options[n-1], true
	}
	return "", false
}
