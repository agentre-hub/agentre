package acp

import (
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
	"github.com/agentre-hub/agentre/internal/pkg/shellloc"
)

// stampLocation 给工具事件盖上"AI 当时所在目录"。ACP 没有结构化位置信号:execute
// 类工具(shell=true)的 command 文本按 shellloc 识别 cd / git -C / git worktree add,
// 其它工具带当前位置。
func stampLocation(loc *shellloc.Tracker, ev agentruntime.Event, shell bool) agentruntime.Event {
	switch e := ev.(type) {
	case agentruntime.ToolCall:
		if shell {
			e.Cwd, e.Input = loc.Call(e.ID, e.Input)
		} else {
			e.Cwd = loc.Here(e.ID)
		}
		return e
	case agentruntime.ToolResult:
		e.Cwd = loc.Result(e.ToolCallID)
		return e
	}
	return ev
}
