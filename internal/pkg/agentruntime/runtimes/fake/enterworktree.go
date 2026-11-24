package fake

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
)

// EnterWorktreeDirectivePrefix 触发「AI 进入另一个 worktree」接缝的用户指令:
// e2e-enter-worktree:<绝对路径>。
//
// 路径应是测试在会话仓库下建好的同仓库 worktree。fake 发一对 Bash 调用/结果,Cwd 都是
// 该路径(与真实后端把「AI 当时所在目录」随工具事件带上来一致),随后保持轮次开着
// (AGENTRE_E2E_FAKE_WORKTREE_HOLD_MS,默认 10s)再正常收尾 —— 给 spec 留出在轮次进行中
// 观察标题栏胶囊跟随切换的窗口。
const EnterWorktreeDirectivePrefix = "e2e-enter-worktree:"

// enterWorktreeHold 是进入 worktree 之后保持轮次开着的时长。
const enterWorktreeHold = 10 * time.Second

// runEnterWorktree 执行 enter-worktree 接缝。返回 false 表示 ctx 已取消,调用方应直接收尾。
func runEnterWorktree(ctx context.Context, out chan<- agentruntime.Event, sessionID int64, path string) bool {
	id := fmt.Sprintf("e2e-enter-worktree-%d", sessionID)
	raw, _ := json.Marshal(map[string]any{"command": "cd " + path})
	if !emit(ctx, out, agentruntime.ToolCall{ID: id, Name: "Bash", Input: raw, Cwd: path}) ||
		!emit(ctx, out, agentruntime.ToolResult{ToolCallID: id, Content: "entered " + path, Cwd: path}) {
		return false
	}
	return sleepCtx(ctx, envDuration("AGENTRE_E2E_FAKE_WORKTREE_HOLD_MS", enterWorktreeHold))
}

// parseEnterWorktreeDirective 取指令里的路径;缺路径视为没有该指令。
func parseEnterWorktreeDirective(text string) (string, bool) {
	raw, ok := parseOnePartDirective(text, EnterWorktreeDirectivePrefix)
	path := strings.TrimSpace(raw)
	return path, ok && path != ""
}
