package fake

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
)

// CtlSendDirectivePrefix 触发「会话 token 的 agrctl send --wait」接缝的用户指令:
// e2e-agrctl-send:<fg|bg>:<agentID>:<label>。
//
// 本轮回显后,fake 像 Claude Code 那样发出一条外层 Bash ToolCall(命令文本含
// `agrctl send --agent-id <id> --wait`;bg 变体入参带 run_in_background:true),在这条
// 调用还开着时用 req.CtlCredentials() 进程内 POST /ctl/v1/send {wait:true},等 send 返回后
// 才给前台调用结果并收尾。bg 变体的 Bash 立刻拿到结果(后台任务语义),send 仍在途。
//
// 子会话的任务文本是 ChildStepsDirectivePrefix+<label>:目标 agent 的子会话由本 fake 执行,
// 因而会产出嵌套 Bash 步骤。
const CtlSendDirectivePrefix = "e2e-agrctl-send:"

// ChildStepsDirectivePrefix 触发「慢速产出 n 条成对 Bash 步骤」:e2e-child-steps:<n>。
// 步骤之间停 AGENTRE_E2E_FAKE_STEP_DELAY_MS(默认 1500ms),给 spec 留出观察实时嵌套步骤的窗口。
const ChildStepsDirectivePrefix = "e2e-child-steps:"

// childStepsPerSend 是 ctl-send 接缝派给子会话的步骤数。
const childStepsPerSend = 2

type ctlSendDirective struct {
	background bool
	agentID    int64
	label      string
}

func parseCtlSendDirective(text string) (ctlSendDirective, bool) {
	raw, ok := parseOnePartDirective(text, CtlSendDirectivePrefix)
	if !ok {
		return ctlSendDirective{}, false
	}
	parts := strings.SplitN(raw, ":", 3)
	if len(parts) != 3 || (parts[0] != "fg" && parts[0] != "bg") {
		return ctlSendDirective{}, false
	}
	id, err := strconv.ParseInt(strings.TrimSpace(parts[1]), 10, 64)
	label := strings.TrimSpace(parts[2])
	if err != nil || id <= 0 || label == "" {
		return ctlSendDirective{}, false
	}
	return ctlSendDirective{background: parts[0] == "bg", agentID: id, label: label}, true
}

// runCtlSend 执行 ctl-send 接缝。返回 false 表示 ctx 已取消,调用方应直接收尾。
func runCtlSend(ctx context.Context, out chan<- agentruntime.Event, req agentruntime.RunRequest, d ctlSendDirective) bool {
	task := fmt.Sprintf("%s%d %s", ChildStepsDirectivePrefix, childStepsPerSend, d.label)
	callID := fmt.Sprintf("e2e-ctl-send-%d", req.SessionID)
	input := map[string]any{
		"command":     fmt.Sprintf("agrctl send --agent-id %d --wait %q", d.agentID, task),
		"description": "e2e agrctl send --wait",
	}
	if d.background {
		input["run_in_background"] = true
	}
	raw, _ := json.Marshal(input)
	if !emit(ctx, out, agentruntime.ToolCall{ID: callID, Name: "Bash", Input: raw}) {
		return false
	}
	// 给消费方一个把 ToolCall 登记进父轮的窗口:真实 CLI 的 send 总晚于 tool_use 帧数百毫秒。
	if !sleepCtx(ctx, envDuration("AGENTRE_E2E_FAKE_CTLSEND_DELAY_MS", 300*time.Millisecond)) {
		return false
	}
	if d.background {
		if !emit(ctx, out, agentruntime.ToolResult{ToolCallID: callID, Content: "Command running in background"}) {
			return false
		}
	}
	text, err := postCtlSend(ctx, req.CtlCredentials(), d.agentID, task)
	if ctx.Err() != nil {
		return false
	}
	if d.background {
		return true
	}
	return emit(ctx, out, agentruntime.ToolResult{ToolCallID: callID, Content: text, IsError: err != nil})
}

// postCtlSend 与 `agrctl send --wait` 同一请求;失败返回错误描述文本供前台结果展示。
func postCtlSend(ctx context.Context, c agentruntime.CtlCredentials, agentID int64, task string) (string, error) {
	if c.Endpoint == "" || c.Token == "" {
		err := fmt.Errorf("no agrctl session credentials")
		return err.Error(), err
	}
	body, _ := json.Marshal(map[string]any{"agentId": agentID, "text": task, "wait": true})
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.Endpoint+"/ctl/v1/send", bytes.NewReader(body))
	if err != nil {
		return err.Error(), err
	}
	httpReq.Header.Set("Authorization", "Bearer "+c.Token)
	httpReq.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		return err.Error(), err
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		err := fmt.Errorf("send: unexpected status %d: %s", resp.StatusCode, raw)
		return err.Error(), err
	}
	var out struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return err.Error(), err
	}
	return out.Text, nil
}

// runChildSteps 产出 n 对 Bash 步骤(调用+结果),每对之后停一个步骤间隔。
func runChildSteps(ctx context.Context, out chan<- agentruntime.Event, sessionID int64, n int) bool {
	delay := envDuration("AGENTRE_E2E_FAKE_STEP_DELAY_MS", 1500*time.Millisecond)
	for i := 1; i <= n; i++ {
		id := fmt.Sprintf("e2e-step-%d-%d", sessionID, i)
		raw, _ := json.Marshal(map[string]any{"command": fmt.Sprintf("echo e2e-step-%d", i)})
		if !emit(ctx, out, agentruntime.ToolCall{ID: id, Name: "Bash", Input: raw}) ||
			!emit(ctx, out, agentruntime.ToolResult{ToolCallID: id, Content: fmt.Sprintf("e2e-step-%d", i)}) ||
			!sleepCtx(ctx, delay) {
			return false
		}
	}
	return true
}

func emit(ctx context.Context, out chan<- agentruntime.Event, ev agentruntime.Event) bool {
	select {
	case <-ctx.Done():
		return false
	case out <- ev:
		return true
	}
}

func sleepCtx(ctx context.Context, d time.Duration) bool {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
