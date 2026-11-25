package ctlcmd

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// sendFlags 是 `agrctl send` 接受的 flag;它们同时是 help 的数据源。
var sendFlags = []*flagDef{
	{name: "agent", value: "<name|id>", usage: "new session: target agent by name or id"},
	{name: "agent-id", value: "<id>", usage: "new session: target agent by id (same as --agent <id>)"},
	{name: "project", value: "<ref>", usage: "new session: project by name, parent/child path or id (default: free session)"},
	{name: "title", value: "<title>", usage: "new session: its title"},
	{name: "isolated", usage: "new session: one-shot session (desktop: not in the sidebar; account: not saved to the account)"},
	{name: "session", value: "<id>", usage: "continue this session (its full UUID) instead of starting one"},
	{name: "steer", usage: "the session is running: interject into the current turn (no --image)"},
	{name: "image", value: "<file>", usage: "attach a local image (png, jpeg, gif, webp); repeatable", repeat: true},
	{name: "model", value: "<provider>/<model>", usage: "run on this provider model from now on"},
	{name: "cli-model", value: "<model>", usage: "run the agent CLI on this model from now on"},
	{name: "effort", value: "<level>", usage: "reasoning effort from now on: low, medium, high, xhigh, max"},
	{name: "permission-mode", value: "<mode>", usage: "permission mode from now on (depends on the backend)"},
	{name: "wait", usage: "block like agrctl wait: until idle (exit 0), failed (1), waiting for input (4)"},
	{name: "timeout", value: "<duration>", usage: "with --wait: give up after this long (e.g. 90s, 10m) — exit 5"},
	{name: "device", value: "<device>", usage: "new session: run on the agent's first backend on this device (name or fingerprint; \"local\" = this desktop)"},
	{name: "backend", value: "<ref>", usage: "new session: run on exactly this backend of the agent (name or id)"},
	{name: "endpoint", value: "<url>", usage: "control endpoint URL   (env AGENTRE_CTL_ENDPOINT)"},
	{name: "token", value: "<value>", usage: "with --endpoint: its credential (env AGENTRE_CTL_TOKEN); alone: a personal token (env AGENTRE_TOKEN)"},
	outputFlag, outputFlagLong,
}

// newSessionOnly 是只对新会话有意义、与 --session 互斥的 flag。
var newSessionOnly = []string{"agent", "agent-id", "project", "title", "isolated", "device", "backend"}

// runSend 派发一条任务给某个 agent,或继续一条会话(`agrctl send`)。
func runSend(args []string, s *sys) error {
	p, err := parseArgs(args, sendFlags)
	if err != nil {
		return err
	}
	asJSON, err := wantJSON(p)
	if err != nil {
		return err
	}
	text := strings.TrimSpace(strings.Join(p.positional, " "))
	session := last(p, "session")
	if session != "" {
		if err := checkSessionRef(session); err != nil {
			return err
		}
		for _, name := range newSessionOnly {
			if p.has(name) {
				return usageErrorf("send: --session continues an existing session; it cannot be combined with --%s", name)
			}
		}
	} else if !p.has("agent") && !p.has("agent-id") {
		return usageErrorf("send: --agent is required (or --session <id> to continue a session)")
	}
	if p.has("device") && p.has("backend") {
		return usageErrorf("send: --device and --backend are mutually exclusive")
	}
	if p.has("model") && p.has("cli-model") {
		return usageErrorf("send: --model and --cli-model are mutually exclusive")
	}
	if p.on("steer") && p.has("image") {
		return usageErrorf("send: --steer cannot carry an --image")
	}
	timeout, err := waitTimeout(p, "send", p.on("wait"))
	if err != nil {
		return err
	}
	if text == "" {
		return usageErrorf("send: task text is required")
	}
	images, err := readImages(p.values("image"))
	if err != nil {
		return err
	}
	x, err := conversationExecutor(s, p, true, "sending to a session")
	if err != nil {
		return err
	}
	var out conversationReply
	if x.kind == execAccount {
		in, err := sendInputFrom(p, text, images, timeout)
		if err != nil {
			return err
		}
		chat := newAccountChat(s, x, append([]string{"send"}, args...))
		defer chat.close()
		if out, err = chat.send(context.Background(), in); err != nil {
			return err
		}
	} else if out, err = desktopSend(x, p, args, text, images, timeout); err != nil {
		return err
	}
	if p.on("wait") {
		return printOutcome(s, out.outcome(), asJSON)
	}
	if asJSON {
		return printJSON(s.stdout, out.outcome())
	}
	where := out.Device
	if where == "" {
		where = "this desktop"
	}
	verb := "dispatched to"
	if out.Steered {
		verb = "steered into the running turn of"
	}
	_, _ = fmt.Fprintf(s.stderr, "%s %s — %s\n", verb, out.Agent, where)
	_, _ = fmt.Fprintln(s.stdout, out.ConversationID)
	return nil
}

// desktopSend 把一次 send 交给会话 / 桌面执行者的 /ctl/v1/send。
func desktopSend(x *executor, p parsedArgs, args []string, text string, images []sendImage, timeout time.Duration) (conversationReply, error) {
	session := last(p, "session")
	body := map[string]any{"text": text, "wait": p.on("wait")}
	if session != "" {
		body["conversationId"] = session
	} else if err := newSessionBody(body, p, x, args); err != nil {
		return conversationReply{}, err
	}
	if p.on("steer") {
		body["steer"] = true
	}
	for flagName, field := range map[string]string{"model": "model", "cli-model": "cliModel", "effort": "effort", "permission-mode": "permissionMode"} {
		if v := last(p, flagName); v != "" {
			body[field] = v
		}
	}
	if len(images) > 0 {
		body["images"] = images
	}
	if timeout > 0 {
		body["timeoutMs"] = timeout.Milliseconds()
	}
	var out conversationReply
	if err := x.ep.Post("/ctl/v1/send", body, &out); err != nil {
		return conversationReply{}, conversationErr(err)
	}
	return out, nil
}

// sendInputFrom 把解析好的 send flag 交给账号执行者。
func sendInputFrom(p parsedArgs, text string, images []sendImage, timeout time.Duration) (sendInput, error) {
	in := sendInput{
		text: text, images: images, session: last(p, "session"), steer: p.on("steer"), wait: p.on("wait"), timeout: timeout,
		agent: last(p, "agent"), project: last(p, "project"), title: last(p, "title"), device: last(p, "device"),
		backend: last(p, "backend"), isolated: p.on("isolated"), model: last(p, "model"), cliModel: last(p, "cli-model"),
		effort: last(p, "effort"), permissionMode: last(p, "permission-mode"),
	}
	if v := last(p, "agent-id"); v != "" {
		id, err := strconv.ParseInt(v, 10, 64)
		if err != nil || id <= 0 {
			return sendInput{}, usageErrorf("send: --agent-id must be a positive number, got %q", v)
		}
		in.agentID = id
	}
	return in, nil
}

// newSessionBody 填新会话的定位:agent(名字或 id)、项目(名字 / 路径 / id)、标题、隔离。
func newSessionBody(body map[string]any, p parsedArgs, x *executor, args []string) error {
	if v := last(p, "agent-id"); v != "" {
		id, err := strconv.ParseInt(v, 10, 64)
		if err != nil || id <= 0 {
			return usageErrorf("send: --agent-id must be a positive number, got %q", v)
		}
		body["agentId"] = id
	} else {
		body["agent"] = last(p, "agent")
	}
	if ref := last(p, "project"); ref != "" {
		id, err := strconv.ParseInt(ref, 10, 64)
		if err != nil || id <= 0 {
			cat := newCatalog(x, append([]string{"send"}, args...))
			if id, err = refID(cat, kindProject, ref); err != nil {
				return err
			}
		}
		body["projectId"] = id
	}
	if v := last(p, "title"); v != "" {
		body["title"] = v
	}
	for _, name := range []string{"device", "backend"} {
		if v := last(p, name); v != "" {
			body[name] = v
		}
	}
	if p.on("isolated") {
		body["isolated"] = true
	}
	return nil
}

// last 是 flag 最后一次出现的值;没出现过为空串。
func last(p parsedArgs, name string) string {
	vals := p.values(name)
	if len(vals) == 0 {
		return ""
	}
	return strings.TrimSpace(vals[len(vals)-1])
}

// waitTimeout 解析 --timeout;它只在等待时有意义。
func waitTimeout(p parsedArgs, verb string, waiting bool) (time.Duration, error) {
	v := last(p, "timeout")
	if v == "" {
		return 0, nil
	}
	if !waiting {
		return 0, usageErrorf("%s: --timeout only applies with --wait", verb)
	}
	d, err := time.ParseDuration(v)
	if err != nil || d <= 0 {
		return 0, usageErrorf("%s: --timeout takes a positive duration such as 90s or 10m, got %q", verb, v)
	}
	return d, nil
}

// sendImage 是 /ctl/v1/send 的图片附件。
type sendImage struct {
	Name    string `json:"name,omitempty"`
	DataURL string `json:"dataUrl"`
}

// supportedImageTypes 是 --image 接受的格式(按文件内容判断,不看扩展名)。
var supportedImageTypes = map[string]bool{"image/png": true, "image/jpeg": true, "image/gif": true, "image/webp": true}

// readImages 读取 --image 文件并编码成 data URL;文件读不到或不是支持的图片是用法错误。
func readImages(paths []string) ([]sendImage, error) {
	out := make([]sendImage, 0, len(paths))
	for _, path := range paths {
		data, err := os.ReadFile(path) //nolint:gosec // 读取用户在命令行上点名的文件就是本 flag 的用途。
		if err != nil {
			return nil, usageErrorf("send: --image %s: %v", path, err)
		}
		mime := http.DetectContentType(data)
		if !supportedImageTypes[mime] {
			return nil, usageErrorf("send: --image %s is not a supported image (png, jpeg, gif, webp)", path)
		}
		out = append(out, sendImage{
			Name:    filepath.Base(path),
			DataURL: "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(data),
		})
	}
	return out, nil
}
