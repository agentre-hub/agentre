package ctlcmd

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"time"

	"github.com/agentre-hub/agentre/internal/pkg/conversationid"
	"github.com/agentre-hub/agentre/internal/pkg/ctlclient"
)

// reconnectDelay 是 wait / tail 断线后重连前的停顿。
var reconnectDelay = time.Second

// maxReconnects 是连续重连的上限:执行者一直回不来时最终按执行失败退出。
const maxReconnects = 60

// setReconnectDelay 供测试缩短重连停顿,交回还原函数。
func setReconnectDelay(d time.Duration) func() {
	prev := reconnectDelay
	reconnectDelay = d
	return func() { reconnectDelay = prev }
}

// 会话状态(与执行者 /ctl/v1/wait 的 status 同值)。
const (
	statusIdle    = "idle"
	statusRunning = "running"
	statusWaiting = "waiting"
	statusFailed  = "failed"
)

// connFlags 是会话命令共有的连接 flag。
var connFlags = []*flagDef{
	{name: "endpoint", value: "<url>", usage: "control endpoint URL   (env AGENTRE_CTL_ENDPOINT)"},
	{name: "token", value: "<value>", usage: "with --endpoint: its credential (env AGENTRE_CTL_TOKEN); alone: a personal token (env AGENTRE_TOKEN)"},
}

var (
	waitFlags = append([]*flagDef{
		{name: "timeout", value: "<duration>", usage: "give up after this long (e.g. 90s, 10m) — exit 5; default: no timeout"},
		outputFlag, outputFlagLong,
	}, connFlags...)
	tailFlags = append([]*flagDef{
		{name: "from", value: "<cursor>", usage: "resume after this cursor (missed events first, then live ones)"},
		{name: "until-idle", usage: "exit when the session goes idle (exit 0) or waits for input (exit 4)"},
	}, connFlags...)
	stopFlags = append([]*flagDef{outputFlag, outputFlagLong}, connFlags...)
)

// checkSessionRef:会话一律用完整的 conversation UUID 指称;别的写法是用法错误。
func checkSessionRef(id string) error {
	if conversationid.Validate(id) != nil {
		return usageErrorf("%q is not a session id: give the full session UUID (agrctl send prints it)", id)
	}
	return nil
}

// conversationErr 把执行者的拒绝换算成退出码:400 / 422(向自己发送、值不被支持等)是用法
// 错误,其余(运行中、看不到、越权、连不上)是执行失败。消息原样透出。
func conversationErr(err error) error {
	var se *ctlclient.ServerError
	if !errors.As(err, &se) {
		return err
	}
	if se.Status == http.StatusBadRequest || se.Status == http.StatusUnprocessableEntity {
		return usageErrorf("%s", se.Message)
	}
	return errors.New(se.Message)
}

// conversationReply 是 /ctl/v1/send 与 /ctl/v1/wait 的响应。
type conversationReply struct {
	ConversationID string          `json:"conversationId"`
	Agent          string          `json:"agent"`
	Device         string          `json:"device"`
	Steered        bool            `json:"steered"`
	Status         string          `json:"status"`
	Text           string          `json:"text"`
	Error          string          `json:"error"`
	Pending        json.RawMessage `json:"pending"`
}

func (r conversationReply) outcome() outcome {
	return outcome{Session: r.ConversationID, Status: r.Status, Text: r.Text, Error: r.Error, Pending: r.Pending, Steered: r.Steered}
}

// outcome 是一次派发或等待的结果,也是它的 -o json 形状(字段名稳定)。
type outcome struct {
	Session string          `json:"session"`
	Status  string          `json:"status"`
	Text    string          `json:"text,omitempty"`
	Error   string          `json:"error,omitempty"`
	Pending json.RawMessage `json:"pending,omitempty"`
	Steered bool            `json:"steered,omitempty"`
}

// printOutcome 按 spec「等待」输出结果并换算退出码:空闲 0(stdout 最终文本)、失败 1、
// 等待输入 4(stdout 待处理项 JSON)、超时 5。
func printOutcome(s *sys, o outcome, asJSON bool) error {
	if asJSON {
		if err := printJSON(s.stdout, o); err != nil {
			return err
		}
	}
	switch o.Status {
	case statusIdle:
		if !asJSON {
			_, _ = fmt.Fprintln(s.stdout, o.Text)
		}
		return nil
	case statusFailed:
		return &cliError{code: exitFailed, msg: "the turn failed: " + o.Error}
	case statusWaiting:
		if !asJSON {
			pending := o.Pending
			if len(pending) == 0 {
				pending = json.RawMessage("{}")
			}
			if err := printJSON(s.stdout, pending); err != nil {
				return err
			}
		}
		return &cliError{code: exitWaiting, msg: "session " + o.Session + " is waiting for input (pending items on stdout); once they are answered, run agrctl wait " + o.Session + " again"}
	case statusRunning:
		return &cliError{code: exitTimeout, msg: "still running — the session keeps going; run agrctl wait " + o.Session + " again later"}
	default:
		return fmt.Errorf("unexpected session status %q", o.Status)
	}
}

// isDialErr 报告一次失败是不是根本没连上执行者(区别于连上之后被断开)。
func isDialErr(err error) bool {
	var opErr *net.OpError
	return errors.As(err, &opErr) && opErr.Op == "dial"
}

// retryable 判断一次失败值不值得重连:执行者明确的拒绝不重连;第一次就拨不通说明执行者
// 不在,也不重连(报「连不上」);连上过之后的断线一律重连。
func retryable(err error, connected bool, attempt int) bool {
	var se *ctlclient.ServerError
	if errors.As(err, &se) || attempt >= maxReconnects {
		return false
	}
	return connected || !isDialErr(err)
}

// runWait:`agrctl wait <session> [--timeout <duration>] [-o json]`。
func runWait(args []string, s *sys) error {
	p, err := parseArgs(args, waitFlags)
	if err != nil {
		return err
	}
	asJSON, err := wantJSON(p)
	if err != nil {
		return err
	}
	if len(p.positional) != 1 {
		return usageErrorf("wait takes exactly one session id")
	}
	id := p.positional[0]
	if err := checkSessionRef(id); err != nil {
		return err
	}
	timeout, err := waitTimeout(p, "wait", true)
	if err != nil {
		return err
	}
	ep, err := conversationEndpoint(s, p, false, "")
	if err != nil {
		return err
	}
	start := time.Now()
	connected := false
	for attempt := 0; ; attempt++ {
		body := map[string]any{"conversationId": id}
		if timeout > 0 {
			remaining := timeout - time.Since(start)
			if remaining <= 0 {
				return printOutcome(s, outcome{Session: id, Status: statusRunning}, asJSON)
			}
			body["timeoutMs"] = max(remaining.Milliseconds(), 1)
		}
		var out conversationReply
		err := ep.Post("/ctl/v1/wait", body, &out)
		if err == nil {
			return printOutcome(s, out.outcome(), asJSON)
		}
		if !retryable(err, connected, attempt) {
			return conversationErr(err)
		}
		connected = connected || !isDialErr(err)
		time.Sleep(reconnectDelay)
	}
}

// tailEvent 是 tail 一行里 agrctl 自己要看的字段;整行原样写到 stdout。
type tailEvent struct {
	Cursor string `json:"cursor"`
	Type   string `json:"type"`
	Status string `json:"status"`
}

// runTail:`agrctl tail <session> [--from <cursor>] [--until-idle]`,NDJSON 原样转写到
// stdout。断线自动重连,从最后一个游标续接。
func runTail(args []string, s *sys) error {
	p, err := parseArgs(args, tailFlags)
	if err != nil {
		return err
	}
	if len(p.positional) != 1 {
		return usageErrorf("tail takes exactly one session id")
	}
	id := p.positional[0]
	if err := checkSessionRef(id); err != nil {
		return err
	}
	untilIdle := p.on("until-idle")
	ep, err := conversationEndpoint(s, p, false, "")
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	cursor := last(p, "from")
	connected := false
	for attempt := 0; ; attempt++ {
		q := url.Values{"conversationId": {id}}
		if cursor != "" {
			q.Set("from", cursor)
		}
		if untilIdle {
			q.Set("untilIdle", "true")
		}
		stream, err := ep.OpenStream(ctx, "/ctl/v1/tail?"+q.Encode())
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			if !retryable(err, connected, attempt) {
				return conversationErr(err)
			}
			connected = connected || !isDialErr(err)
			time.Sleep(reconnectDelay)
			continue
		}
		connected = true
		status, got := relayTail(stream, s.stdout, &cursor, untilIdle)
		_ = stream.Close()
		if got {
			attempt = 0 // 收到过东西:这一次断线重新计数
		}
		switch {
		case ctx.Err() != nil:
			return nil
		case status == statusIdle || status == statusFailed:
			return nil
		case status == statusWaiting:
			return &cliError{code: exitWaiting, msg: "session " + id + " is waiting for input"}
		}
		if attempt >= maxReconnects {
			return errors.New("lost the connection to the executor")
		}
		time.Sleep(reconnectDelay)
	}
}

// relayTail 把一条 tail 流逐行写到 w 并推进 cursor。untilIdle 时读到收尾的 status 行即
// 交回它;流在此之前断开交回空串(调用方重连)。got 报告这一次是否读到过任何一行。
func relayTail(stream io.Reader, w io.Writer, cursor *string, untilIdle bool) (status string, got bool) {
	sc := bufio.NewScanner(stream)
	sc.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		got = true
		_, _ = w.Write(append(append([]byte{}, line...), '\n'))
		var ev tailEvent
		if json.Unmarshal(line, &ev) != nil {
			continue
		}
		if ev.Cursor != "" {
			*cursor = ev.Cursor
		}
		if untilIdle && ev.Type == "status" && ev.Status != statusRunning {
			return ev.Status, got
		}
	}
	return "", got
}

// runStop:`agrctl stop <session>`,停止正在运行的轮次并丢弃尚未送达的插话。
func runStop(args []string, s *sys) error {
	p, err := parseArgs(args, stopFlags)
	if err != nil {
		return err
	}
	asJSON, err := wantJSON(p)
	if err != nil {
		return err
	}
	if len(p.positional) != 1 {
		return usageErrorf("stop takes exactly one session id")
	}
	id := p.positional[0]
	if err := checkSessionRef(id); err != nil {
		return err
	}
	ep, err := conversationEndpoint(s, p, true, "stopping a session")
	if err != nil {
		return err
	}
	var out struct {
		Stopped       bool `json:"stopped"`
		Running       bool `json:"running"`
		DroppedSteers int  `json:"droppedSteers"`
	}
	if err := ep.Post("/ctl/v1/stop", map[string]any{"conversationId": id}, &out); err != nil {
		return conversationErr(err)
	}
	if asJSON {
		return printJSON(s.stdout, map[string]any{
			"session": id, "stopped": out.Stopped, "running": out.Running, "droppedSteers": out.DroppedSteers,
		})
	}
	if !out.Running {
		_, _ = fmt.Fprintln(s.stderr, "session is not running")
		return nil
	}
	_, _ = fmt.Fprintf(s.stdout, "stopped — dropped %d undelivered steer message(s)\n", out.DroppedSteers)
	return nil
}
