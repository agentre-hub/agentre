package piagent

import (
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
	"github.com/agentre-hub/agentre/internal/pkg/shellloc"
)

// stampLocation 给工具事件盖上"AI 当时所在目录"。Pi 没有结构化位置信号:bash 的
// command 文本按 shellloc 识别 cd / git -C / git worktree add,其它工具带当前位置。
// loc 为 nil(无会话上下文)时原样放行。
func stampLocation(loc *shellloc.Tracker, ev agentruntime.Event) agentruntime.Event {
	if loc == nil {
		return ev
	}
	switch e := ev.(type) {
	case agentruntime.ToolCall:
		if e.Name == "bash" {
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
