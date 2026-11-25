package ctlcmd

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"
)

// sessionStatuses 是 list sessions --status 接受的值。
var sessionStatuses = []string{statusRunning, statusIdle, statusWaiting, statusFailed}

var (
	listSessionsFlags = append([]*flagDef{
		{name: "agent", value: "<name|id>", usage: "only sessions of this agent"},
		{name: "project", value: "<ref>", usage: "only sessions of this project (name, parent/child path or id)"},
		{name: "device", value: "<device>", usage: "only sessions running on this device (name or fingerprint; \"local\" = this desktop)"},
		{name: "status", value: "<status>", usage: "only running, idle, waiting or failed sessions"},
		{name: "mine", usage: "only sessions started by the caller (needs an account)"},
		{name: "limit", value: "<n>", usage: "at most n sessions, most recently active first (default 50)"},
		outputFlag, outputFlagLong,
	}, connFlags...)
	getSessionFlags = append([]*flagDef{outputFlag, outputFlagLong}, connFlags...)
	messagesFlags   = append([]*flagDef{
		{name: "last", value: "<n>", usage: "the last n turns (default 20)"},
		{name: "before", value: "<cursor>", usage: "page back: only messages before this cursor (printed by the previous page)"},
		{name: "full", usage: "also print tool calls and their results (never thinking)"},
		outputFlag, outputFlagLong,
	}, connFlags...)
	pendingFlags = append([]*flagDef{outputFlag, outputFlagLong}, connFlags...)
	answerFlags  = append([]*flagDef{
		{name: "option", value: "<text|number>", usage: "question: pick this option by its text or 1-based number; repeat for multi-select", repeat: true},
		{name: "text", value: "<answer>", usage: "question: a free-text answer"},
		{name: "allow", usage: "tool permission: allow"},
		{name: "deny", usage: "tool permission: deny"},
		{name: "message", value: "<reason>", usage: "with --deny: the reason handed back to the agent"},
		outputFlag, outputFlagLong,
	}, connFlags...)
)

// isSessionResource 报告 list / get 的资源名是不是会话(会话不是 ctl 资源,走会话端点)。
func isSessionResource(name string) bool { return name == "session" || name == "sessions" }

// sessionRow 是 list sessions 一行。
type sessionRow struct {
	ID             string `json:"id"`
	Title          string `json:"title"`
	Agent          string `json:"agent"`
	Device         string `json:"device"`
	Status         string `json:"status"`
	LastActivityAt string `json:"lastActivityAt,omitempty"`
	// DeviceOffline 只在账号模式出现：承载设备此刻离线，这一行是它最后已知的状态。
	DeviceOffline bool `json:"deviceOffline,omitempty"`
}

// runListSessions:`agrctl list sessions [--agent …] [-o json]`。
func runListSessions(args []string, s *sys) error {
	p, err := parseArgs(args, listSessionsFlags)
	if err != nil {
		return err
	}
	if len(p.positional) > 0 {
		return usageErrorf("unexpected argument %q", p.positional[0])
	}
	asJSON, err := wantJSON(p)
	if err != nil {
		return err
	}
	body := map[string]any{}
	if v := last(p, "status"); v != "" {
		if !slices.Contains(sessionStatuses, v) {
			return usageErrorf("--status %q is not supported (one of: %s)", v, strings.Join(sessionStatuses, ", "))
		}
		body["status"] = v
	}
	if p.has("limit") {
		n, err := strconv.Atoi(last(p, "limit"))
		if err != nil || n <= 0 {
			return usageErrorf("--limit takes a positive number, got %q", last(p, "limit"))
		}
		body["limit"] = n
	}
	for _, name := range []string{"agent", "device"} {
		if v := last(p, name); v != "" {
			body[name] = v
		}
	}
	if p.on("mine") {
		body["mine"] = true
	}
	x, err := conversationExecutor(s, p, false, "")
	if err != nil {
		return err
	}
	if x.kind == execAccount {
		return listAccountSessions(s, x, args, p, asJSON)
	}
	ep := x.ep
	if ref := last(p, "project"); ref != "" {
		id, err := strconv.ParseInt(ref, 10, 64)
		if err != nil || id <= 0 {
			cat := newCatalog(endpointExecutor(ep), append([]string{"list", "sessions"}, args...))
			if id, err = refID(cat, kindProject, ref); err != nil {
				return err
			}
		}
		body["projectId"] = id
	}
	var out struct {
		Sessions []sessionRow `json:"sessions"`
	}
	if err := ep.Post("/ctl/v1/conversations", body, &out); err != nil {
		return conversationErr(err)
	}
	if out.Sessions == nil {
		out.Sessions = []sessionRow{}
	}
	return printSessionRows(s, out.Sessions, asJSON)
}

func printSessionRows(s *sys, sessions []sessionRow, asJSON bool) error {
	if asJSON {
		return printJSON(s.stdout, sessions)
	}
	rows := make([][]string, 0, len(sessions))
	for _, r := range sessions {
		device := r.Device
		if r.DeviceOffline {
			device += " (offline)"
		}
		rows = append(rows, []string{r.ID, r.Title, r.Agent, device, r.Status, r.LastActivityAt})
	}
	return printTable(s.stdout, []string{"ID", "TITLE", "AGENT", "DEVICE", "STATUS", "LAST ACTIVITY"}, rows)
}

// listAccountSessions 是账号执行者的 list sessions：筛选在客户端做（账号索引加在线设备的会话）。
func listAccountSessions(s *sys, x *executor, args []string, p parsedArgs, asJSON bool) error {
	f := sessionFilter{agent: last(p, "agent"), project: last(p, "project"), device: last(p, "device"),
		status: last(p, "status"), mine: p.on("mine"), limit: 50}
	if p.has("limit") {
		f.limit, _ = strconv.Atoi(last(p, "limit"))
	}
	chat := newAccountChat(s, x, append([]string{"list", "sessions"}, args...))
	defer chat.close()
	rows, err := chat.listSessions(context.Background(), f)
	if err != nil {
		return err
	}
	return printSessionRows(s, rows, asJSON)
}

// runGetSession:`agrctl get session <id>`,JSON 详情(执行者省略的字段不补)。
func runGetSession(args []string, s *sys) error {
	p, err := parseArgs(args, getSessionFlags)
	if err != nil {
		return err
	}
	if _, err := wantJSON(p); err != nil {
		return err
	}
	if len(p.positional) != 1 {
		return usageErrorf("get session needs exactly one session id")
	}
	id := p.positional[0]
	if err := checkSessionRef(id); err != nil {
		return err
	}
	x, err := conversationExecutor(s, p, false, "")
	if err != nil {
		return err
	}
	if x.kind == execAccount {
		chat := newAccountChat(s, x, append([]string{"get", "session"}, args...))
		defer chat.close()
		d, err := chat.getSession(context.Background(), id)
		if err != nil {
			return err
		}
		return printJSON(s.stdout, d)
	}
	ep := x.ep
	var out json.RawMessage
	if err := ep.Post("/ctl/v1/conversation", map[string]any{"conversationId": id}, &out); err != nil {
		return conversationErr(err)
	}
	return printJSON(s.stdout, out)
}

type messageBlock struct {
	Type  string         `json:"type"`
	Text  string         `json:"text,omitempty"`
	Name  string         `json:"name,omitempty"`
	Input map[string]any `json:"input,omitempty"`
	// IsError 标记出错的工具结果。
	IsError bool `json:"isError,omitempty"`
}

type messageRow struct {
	Role   string         `json:"role"`
	At     string         `json:"at,omitempty"`
	Text   string         `json:"text"`
	Blocks []messageBlock `json:"blocks,omitempty"`
}

type messagesPage struct {
	Session  string       `json:"session"`
	Messages []messageRow `json:"messages"`
	HasMore  bool         `json:"hasMore"`
	Before   string       `json:"before,omitempty"`
}

// runMessages:`agrctl messages <session> [--last n] [--before cursor] [--full] [-o json]`。
func runMessages(args []string, s *sys) error {
	p, err := parseArgs(args, messagesFlags)
	if err != nil {
		return err
	}
	asJSON, err := wantJSON(p)
	if err != nil {
		return err
	}
	if len(p.positional) != 1 {
		return usageErrorf("messages takes exactly one session id")
	}
	id := p.positional[0]
	if err := checkSessionRef(id); err != nil {
		return err
	}
	body := map[string]any{"conversationId": id}
	if p.has("last") {
		n, err := strconv.Atoi(last(p, "last"))
		if err != nil || n <= 0 {
			return usageErrorf("--last takes a positive number of turns, got %q", last(p, "last"))
		}
		body["last"] = n
	}
	if v := last(p, "before"); v != "" {
		body["before"] = v
	}
	if p.on("full") {
		body["full"] = true
	}
	x, err := conversationExecutor(s, p, false, "")
	if err != nil {
		return err
	}
	var page messagesPage
	if x.kind == execAccount {
		chat := newAccountChat(s, x, append([]string{"messages"}, args...))
		defer chat.close()
		n, _ := body["last"].(int)
		var beforeSeq int64
		if v, ok := body["before"].(string); ok {
			if beforeSeq, err = strconv.ParseInt(v, 10, 64); err != nil || beforeSeq <= 0 {
				return usageErrorf("invalid --before cursor %q", v)
			}
		}
		if page, err = chat.messages(context.Background(), id, n, beforeSeq, p.on("full")); err != nil {
			return err
		}
	} else if err := x.ep.Post("/ctl/v1/messages", body, &page); err != nil {
		return conversationErr(err)
	}
	if page.Messages == nil {
		page.Messages = []messageRow{}
	}
	if asJSON {
		return printJSON(s.stdout, page)
	}
	for _, m := range page.Messages {
		writeMessage(s.stdout, m, p.on("full"))
	}
	if page.HasMore && page.Before != "" {
		_, _ = fmt.Fprintf(s.stderr, "earlier messages exist: agrctl messages %s --before %s\n", id, page.Before)
	}
	return nil
}

func writeMessage(w io.Writer, m messageRow, full bool) {
	if !full || len(m.Blocks) == 0 {
		_, _ = fmt.Fprintf(w, "%s: %s\n", m.Role, m.Text)
		return
	}
	_, _ = fmt.Fprintf(w, "%s:\n", m.Role)
	for _, b := range m.Blocks {
		switch b.Type {
		case "tool_use":
			input, _ := json.Marshal(b.Input)
			_, _ = fmt.Fprintf(w, "  tool_use %s %s\n", b.Name, input)
		case "tool_result":
			label := "tool_result"
			if b.IsError {
				label = "tool_result (error)"
			}
			_, _ = fmt.Fprintf(w, "  %s %s\n", label, b.Text)
		default:
			_, _ = fmt.Fprintf(w, "  %s\n", b.Text)
		}
	}
}

type pendingQuestion struct {
	Question string `json:"question"`
	Options  []struct {
		Label string `json:"label"`
	} `json:"options"`
}

type pendingItem struct {
	ID        string            `json:"id"`
	Session   string            `json:"session"`
	Kind      string            `json:"kind"`
	Tool      string            `json:"tool,omitempty"`
	Input     json.RawMessage   `json:"input,omitempty"`
	Questions []pendingQuestion `json:"questions,omitempty"`
	// Title / Summary 只有账号模式的审批卡（kind "approval"）带。
	Title   string          `json:"title,omitempty"`
	Summary json.RawMessage `json:"summary,omitempty"`
}

// summary 是 pending 表格最后一列:提问的第一道题,或工具名加输入。
func (it pendingItem) summary() string {
	if len(it.Questions) > 0 {
		return it.Questions[0].Question
	}
	if it.Title != "" {
		return it.Title
	}
	if len(it.Input) > 0 {
		return strings.TrimSpace(it.Tool + " " + string(it.Input))
	}
	return it.Tool
}

// runPending:`agrctl pending [<session>] [-o json]`。
func runPending(args []string, s *sys) error {
	p, err := parseArgs(args, pendingFlags)
	if err != nil {
		return err
	}
	asJSON, err := wantJSON(p)
	if err != nil {
		return err
	}
	if len(p.positional) > 1 {
		return usageErrorf("pending takes at most one session id")
	}
	body := map[string]any{}
	if len(p.positional) == 1 {
		if err := checkSessionRef(p.positional[0]); err != nil {
			return err
		}
		body["conversationId"] = p.positional[0]
	}
	x, err := conversationExecutor(s, p, false, "")
	if err != nil {
		return err
	}
	var out struct {
		Items []pendingItem `json:"items"`
	}
	if x.kind == execAccount {
		chat := newAccountChat(s, x, append([]string{"pending"}, args...))
		defer chat.close()
		id, _ := body["conversationId"].(string)
		if out.Items, err = chat.pending(context.Background(), id); err != nil {
			return err
		}
	} else if err := x.ep.Post("/ctl/v1/pending", body, &out); err != nil {
		return conversationErr(err)
	}
	if out.Items == nil {
		out.Items = []pendingItem{}
	}
	if asJSON {
		return printJSON(s.stdout, out)
	}
	rows := make([][]string, 0, len(out.Items))
	for _, it := range out.Items {
		rows = append(rows, []string{it.ID, it.Session, it.Kind, it.summary()})
	}
	return printTable(s.stdout, []string{"ID", "SESSION", "KIND", "SUMMARY"}, rows)
}

// runAnswer:`agrctl answer <session> <id> (--option … | --text … | --allow | --deny [--message …])`。
func runAnswer(args []string, s *sys) error {
	p, err := parseArgs(args, answerFlags)
	if err != nil {
		return err
	}
	asJSON, err := wantJSON(p)
	if err != nil {
		return err
	}
	if len(p.positional) != 2 {
		return usageErrorf("answer takes a session id and a pending item id")
	}
	session, item := p.positional[0], p.positional[1]
	if err := checkSessionRef(session); err != nil {
		return err
	}
	body := map[string]any{"conversationId": session, "requestId": item}
	allow, deny := p.on("allow"), p.on("deny")
	options := p.values("option")
	text := last(p, "text")
	switch {
	case allow && deny:
		return usageErrorf("answer: --allow and --deny are mutually exclusive")
	case (allow || deny) && (len(options) > 0 || p.has("text")):
		return usageErrorf("answer: --allow / --deny answer a tool permission; --option / --text answer a question")
	case len(options) > 0 && p.has("text"):
		return usageErrorf("answer: give --option or --text, not both")
	case p.has("message") && !deny:
		return usageErrorf("answer: --message only goes with --deny")
	case !allow && !deny && len(options) == 0 && !p.has("text"):
		return usageErrorf("answer: give --option / --text (a question) or --allow / --deny (a tool permission)")
	}
	switch {
	case allow:
		body["decision"] = "allow"
	case deny:
		body["decision"] = "deny"
		if v := last(p, "message"); v != "" {
			body["message"] = v
		}
	case len(options) > 0:
		body["options"] = options
	default:
		body["text"] = text
	}
	x, err := conversationExecutor(s, p, true, "answering a pending item")
	if err != nil {
		return err
	}
	var out struct {
		Answered bool   `json:"answered"`
		Kind     string `json:"kind"`
	}
	if x.kind == execAccount {
		chat := newAccountChat(s, x, append([]string{"answer"}, args...))
		defer chat.close()
		out.Kind, err = chat.answer(context.Background(), answerInput{session: session, item: item, options: options, text: text,
			hasText: p.has("text"), allow: allow, deny: deny, message: last(p, "message")})
		if err != nil {
			return err
		}
		out.Answered = true
	} else if err := x.ep.Post("/ctl/v1/answer", body, &out); err != nil {
		return conversationErr(err)
	}
	if asJSON {
		return printJSON(s.stdout, map[string]any{"session": session, "id": item, "answered": out.Answered, "kind": out.Kind})
	}
	_, _ = fmt.Fprintln(s.stdout, "answered")
	return nil
}
