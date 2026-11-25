package ctlcmd

import (
	"context"
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

// sessionFilter 是 list sessions 的筛选。
type sessionFilter struct {
	agent, project, device, status string
	mine                           bool
	limit                          int
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
	ModelKey          string `json:"model_key"`
}

// savedIndexPages 是翻索引的页数上限（一页的大小由 server 定）。
const savedIndexPages = 50

func (a *accountChat) savedIndex(ctx context.Context) ([]savedSession, error) {
	var all []savedSession
	cursor := ""
	for range savedIndexPages {
		q := url.Values{"axis": {"time"}, "scope": {"time"}}
		if cursor != "" {
			q.Set("cursor", cursor)
		}
		raw, err := a.acct.GetRaw(ctx, "/v1/agent-sessions?"+q.Encode())
		if err != nil {
			return nil, serverErr(err)
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
func (a *accountChat) savedRow(ctx context.Context, id string) (*savedSession, error) {
	raw, err := a.acct.GetRaw(ctx, "/v1/agent-sessions?conversation_id="+url.QueryEscape(id))
	if err != nil {
		return nil, serverErr(err)
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
func (a *accountChat) liveSessions(ctx context.Context, devices []accountDevice) ([]liveSession, func()) {
	var out []liveSession
	var conns []*relaypeer.Conn
	for _, d := range devices {
		if !d.Online || (d.Kind != "agentred" && d.Kind != "desktop") {
			continue
		}
		conn, err := a.peer.Open(ctx, relaypeer.MachineTarget(d.Fingerprint))
		if err != nil {
			_, _ = fmt.Fprintf(a.s.stderr, "warning: could not read the sessions of %s: %v\n", d.label(), a.reachErr(err, d.label(), ""))
			continue
		}
		conns = append(conns, conn)
		resp, err := wirecall.SessionList(ctx, conn, &agentrewire.SessionListRequest{})
		if err != nil {
			_, _ = fmt.Fprintf(a.s.stderr, "warning: could not read the sessions of %s: %v\n", d.label(), a.reachErr(err, d.label(), ""))
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
		return statusFailed
	case waiting:
		return statusWaiting
	case lifecycle == lifecycleRunning:
		return statusRunning
	default:
		return statusIdle
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

func (a *accountChat) names() names {
	n := names{agents: map[string]string{}, projects: map[string]string{}}
	if items, err := a.cat.list(agentrewire.CtlKind_CTL_KIND_AGENT); err == nil {
		for _, it := range items {
			n.agents[it.GetAgent().GetSyncId()] = it.GetAgent().GetName()
		}
	}
	if items, err := a.cat.list(agentrewire.CtlKind_CTL_KIND_PROJECT); err == nil {
		for _, it := range items {
			n.projects[it.GetProject().GetSyncId()] = it.GetProject().GetName()
		}
	}
	return n
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
	row                  sessionRow
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

func (a *accountChat) listSessions(ctx context.Context, f sessionFilter) ([]sessionRow, error) {
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
			row: sessionRow{ID: it.ConversationID, Title: it.Title, Agent: nm.agent(it.AgentSyncID), Device: label,
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
		e.row = sessionRow{ID: s.GetConversationId(), Title: s.GetTitle(), Agent: nm.agent(s.GetAgentSyncId()), Device: l.device.label(),
			Status: sessionStatus(s.GetLifecycleState(), s.GetWaitingForInput()), LastActivityAt: rfc3339Ms(s.GetLastMessageAt())}
		e.peer, e.agent, e.project, e.deviceFP, e.at = s.GetPeerFingerprint(), s.GetAgentSyncId(), s.GetProjectSyncId(), l.device.Fingerprint, s.GetLastMessageAt()
	}
	if f.mine && self == "" {
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
		case f.status != "" && e.row.Status != f.status,
			f.mine && e.peer != self,
			agentSync != "" && e.agent != agentSync,
			projectSync != "" && e.project != projectSync,
			f.device != "" && !deviceRefMatches(devices, e.deviceFP, e.row.Device, f.device):
			continue
		}
		entries = append(entries, e)
	}
	sort.SliceStable(entries, func(i, j int) bool { return entries[i].at > entries[j].at })
	if f.limit > 0 && len(entries) > f.limit {
		entries = entries[:f.limit]
	}
	rows := make([]sessionRow, 0, len(entries))
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

func (a *accountChat) filterRefs(f sessionFilter) (agentSync, projectSync string, err error) {
	if f.agent != "" {
		it, err := a.cat.locate(kindAgent, f.agent)
		if err != nil {
			return "", "", err
		}
		if agentSync = it.GetAgent().GetSyncId(); agentSync == "" {
			return "", "", missingSyncID("agent")
		}
	}
	if f.project != "" {
		it, err := a.cat.locate(kindProject, f.project)
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

// accountSessionDetail 是账号模式 get session 的响应：拿不到的字段省略，不编造。
type accountSessionDetail struct {
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
}

func (a *accountChat) getSession(ctx context.Context, id string) (*accountSessionDetail, error) {
	nm := a.names()
	h, err := a.openSession(ctx, id)
	switch {
	case err == nil:
		defer h.conn.Close()
		s := h.summary
		p, err := wirecall.SessionPendingWaiters(ctx, h.conn, &agentrewire.SessionPendingWaitersRequest{ConversationId: id, PeerFingerprint: h.origin()})
		if err != nil {
			return nil, a.reachErr(err, h.device, id)
		}
		device := h.device
		if device == "" {
			device = a.deviceOf(ctx, id)
		}
		n := len(p.GetToolPermissions()) + len(p.GetAskUserQuestions()) + len(p.GetApprovals())
		return &accountSessionDetail{ID: id, Title: s.GetTitle(), Agent: nm.agent(s.GetAgentSyncId()), Project: nm.project(s.GetProjectSyncId()),
			Device: device, Backend: s.GetBackendType(), Model: s.GetModelKey(), LastActivityAt: rfc3339Ms(s.GetLastMessageAt()),
			Status: sessionStatus(s.GetLifecycleState(), s.GetWaitingForInput() || n > 0), Pending: &n}, nil
	case errors.Is(err, relaypeer.ErrTargetOffline):
		row, rerr := a.savedRow(ctx, id)
		if rerr != nil {
			return nil, rerr
		}
		if row == nil {
			return nil, a.reachErr(err, "", id)
		}
		devices, derr := a.devices(ctx)
		if derr != nil {
			return nil, derr
		}
		label, _ := deviceByFP(devices, row.DeviceFingerprint)
		return &accountSessionDetail{ID: id, Title: row.Title, Agent: nm.agent(row.AgentSyncID), Project: nm.project(row.ProjectSyncID),
			Device: label, DeviceOffline: true, Backend: row.BackendType, Model: row.ModelKey, LastActivityAt: rfc3339Ms(row.LastMessageAt),
			Status: sessionStatus(row.LifecycleState, row.WaitingForInput)}, nil
	default:
		return nil, a.reachErr(err, "", id)
	}
}

// deviceOf 是承载一条保存过的会话的设备名（不知道时为空）。
func (a *accountChat) deviceOf(ctx context.Context, id string) string {
	row, err := a.savedRow(ctx, id)
	if err != nil || row == nil {
		return ""
	}
	devices, err := a.devices(ctx)
	if err != nil {
		return ""
	}
	label, _ := deviceByFP(devices, row.DeviceFingerprint)
	return label
}

// ─── pending / answer ───

func pendingItemsOf(session string, p *agentrewire.SessionPendingWaitersResponse) []pendingItem {
	var out []pendingItem
	for _, t := range p.GetToolPermissions() {
		out = append(out, pendingItem{ID: t.GetRequestId(), Session: session, Kind: "tool_permission", Tool: t.GetToolName(), Input: rawJSON(t.GetInput())})
	}
	for _, q := range p.GetAskUserQuestions() {
		it := pendingItem{ID: q.GetRequestId(), Session: session, Kind: "question"}
		for _, aq := range q.GetQuestions() {
			pq := pendingQuestion{Question: aq.GetQuestion()}
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
		out = append(out, pendingItem{ID: ap.GetRequestId(), Session: session, Kind: "approval", Title: ap.GetTitle(), Summary: rawJSON(ap.GetSummary())})
	}
	return out
}

func (a *accountChat) pending(ctx context.Context, id string) ([]pendingItem, error) {
	if id != "" {
		h, err := a.openSession(ctx, id)
		if err != nil {
			return nil, a.reachErr(err, "", id)
		}
		defer h.conn.Close()
		p, err := wirecall.SessionPendingWaiters(ctx, h.conn, &agentrewire.SessionPendingWaitersRequest{ConversationId: id, PeerFingerprint: h.origin()})
		if err != nil {
			return nil, a.reachErr(err, h.device, id)
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
	var out []pendingItem
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
			return nil, a.reachErr(err, l.device.label(), s.GetConversationId())
		}
		out = append(out, pendingItemsOf(s.GetConversationId(), p)...)
	}
	return out, nil
}

// answerInput 是 `agrctl answer` 解析之后交给执行者的那一份。
type answerInput struct {
	session, item string
	options       []string
	text          string
	hasText       bool
	allow, deny   bool
	message       string
}

// answer 按卡的种类把答案交给承载会话的机器，交回卡的种类。
func (a *accountChat) answer(ctx context.Context, in answerInput) (string, error) {
	h, err := a.openSession(ctx, in.session)
	if err != nil {
		return "", a.reachErr(err, "", in.session)
	}
	defer h.conn.Close()
	p, err := wirecall.SessionPendingWaiters(ctx, h.conn, &agentrewire.SessionPendingWaitersRequest{ConversationId: in.session, PeerFingerprint: h.origin()})
	if err != nil {
		return "", a.reachErr(err, h.device, in.session)
	}
	decision := in.allow || in.deny
	for _, q := range p.GetAskUserQuestions() {
		if q.GetRequestId() != in.item {
			continue
		}
		if decision {
			return "", usageErrorf("a question is answered with --option or --text, not --allow / --deny")
		}
		answers, err := questionAnswers(q, in)
		if err != nil {
			return "", err
		}
		_, err = wirecall.RuntimeSubmitAnswer(ctx, h.conn, &agentrewire.RuntimeSubmitAnswerRequest{ConversationId: in.session,
			PeerFingerprint: h.origin(), RequestId: in.item, Questions: q.GetQuestions(), Answers: answers})
		return "question", a.answerErr(err, h, in)
	}
	for _, t := range p.GetToolPermissions() {
		if t.GetRequestId() != in.item {
			continue
		}
		if !decision {
			return "", usageErrorf("a tool permission is answered with --allow or --deny, not --option / --text")
		}
		_, err := wirecall.RuntimeSubmitToolPermission(ctx, h.conn, &agentrewire.RuntimeSubmitToolPermissionRequest{ConversationId: in.session,
			PeerFingerprint: h.origin(), RequestId: in.item, Allow: in.allow, DenyReason: in.message})
		return "tool_permission", a.answerErr(err, h, in)
	}
	for _, ap := range p.GetApprovals() {
		if ap.GetRequestId() != in.item {
			continue
		}
		if !decision {
			return "", usageErrorf("an approval card is answered with --allow or --deny, not --option / --text")
		}
		if in.message != "" {
			return "", usageErrorf("an approval card takes no --message")
		}
		_, err := wirecall.ToolApprovalAnswer(ctx, h.conn, &agentrewire.ToolApprovalAnswerRequest{ConversationId: in.session, RequestId: in.item, Allow: in.allow})
		return "approval", a.answerErr(err, h, in)
	}
	return "", errors.New(errPendingGone)
}

// answerErr 把作答的失败说成人话：执行端说卡已不挂起，与展示时就找不到是同一句。
func (a *accountChat) answerErr(err error, h *session, in answerInput) error {
	if err == nil {
		return nil
	}
	var rpcErr *rpcerror.Error
	if errors.As(err, &rpcErr) && rpcErr.Code == rpcerror.CodeInvalidParams && strings.Contains(rpcErr.Message, "no pending") {
		return errors.New(errPendingGone)
	}
	return a.reachErr(err, h.device, in.session)
}

// otherAnswerLabel 是提问卡「其它」答案的标签（与执行端 agentruntime.OtherAnswerLabel 同值）。
const otherAnswerLabel = "__other__"

func questionAnswers(q *agentrewire.PendingAskUserQuestion, in answerInput) ([]*agentrewire.AskAnswer, error) {
	if len(q.GetQuestions()) != 1 {
		return nil, usageErrorf("this card asks %d questions; answer it in the console or the desktop app", len(q.GetQuestions()))
	}
	one := q.GetQuestions()[0]
	if in.hasText {
		if one.GetDisallowOther() {
			return nil, usageErrorf("this question only accepts its listed options (--option)")
		}
		return []*agentrewire.AskAnswer{{QuestionIndex: 0, Labels: []string{otherAnswerLabel}, OtherText: in.text}}, nil
	}
	if len(in.options) > 1 && !one.GetMultiSelect() {
		return nil, usageErrorf("this question takes a single option")
	}
	var labels, names []string
	for _, o := range one.GetOptions() {
		names = append(names, o.GetLabel())
	}
	for _, o := range in.options {
		label, ok := matchOption(names, o)
		if !ok {
			return nil, usageErrorf("--option %q is not one of: %s", o, strings.Join(names, ", "))
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
