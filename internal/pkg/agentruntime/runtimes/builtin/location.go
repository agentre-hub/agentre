package builtin

import (
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
	"github.com/agentre-hub/agentre/internal/pkg/shellloc"
)

// bashToolName 是 cago bash 工具的名字(tool/bash.Name)。
const bashToolName = "bash"

// stampLocation 给工具事件盖上"AI 当时所在目录":bash 的 command 文本按 shellloc
// 识别 cd / git -C / git worktree add,其它工具带当前位置。
func stampLocation(loc *shellloc.Tracker, ev agentruntime.Event) agentruntime.Event {
	switch e := ev.(type) {
	case agentruntime.ToolCall:
		if e.Name == bashToolName {
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
