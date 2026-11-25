package piagent

import (
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
	"github.com/agentre-hub/agentre/internal/pkg/shellloc"
)

// locationStamper 给工具事件盖上"AI 当时所在目录"。Pi 没有结构化位置信号:bash 的
// command 文本按 shellloc 识别 cd / git -C / git worktree add,其它工具带当前位置。
//
// 子代理(事件带 ParentToolCallID / SubagentRunID)的位置变化只参与认领,不移动主会话
// 的位置(spec 2026-11-24-chat-right-toolwindows 决策 16):每个子代理 run 有自己的
// tracker,从它第一次出现时主 agent 所在的目录起算;主 tracker 只由主 agent 的调用推进。
// 一个 stamper 只在一次 drainStream 里用,不并发。
type locationStamper struct {
	main *shellloc.Tracker
	runs map[string]*shellloc.Tracker
}

// newLocationStamper 包住会话的主 tracker;loc 为 nil(无会话上下文)时原样放行。
func newLocationStamper(loc *shellloc.Tracker) *locationStamper {
	return &locationStamper{main: loc, runs: map[string]*shellloc.Tracker{}}
}

func (s *locationStamper) trackerFor(parent, run string) *shellloc.Tracker {
	if parent == "" && run == "" {
		return s.main
	}
	key := parent + "\x00" + run
	t, ok := s.runs[key]
	if !ok {
		t = shellloc.NewFreshTracker(s.main.Dir())
		s.runs[key] = t
	}
	return t
}

func (s *locationStamper) stamp(ev agentruntime.Event) agentruntime.Event {
	if s == nil || s.main == nil {
		return ev
	}
	switch e := ev.(type) {
	case agentruntime.ToolCall:
		loc := s.trackerFor(e.ParentToolCallID, e.SubagentRunID)
		if e.Name == "bash" {
			e.Cwd, e.Input = loc.Call(e.ID, e.Input)
		} else {
			e.Cwd = loc.Here(e.ID)
		}
		return e
	case agentruntime.ToolResult:
		e.Cwd = s.trackerFor(e.ParentToolCallID, e.SubagentRunID).Result(e.ToolCallID)
		return e
	}
	return ev
}
