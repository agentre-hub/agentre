package turnstats

import (
	"time"

	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
)

// Observe 按当前时刻观察一条事件。见 ObserveAt。
func (c *Clock) Observe(ev agentruntime.Event) { c.ObserveAt(ev, time.Now()) }

// ObserveAt 是「哪条事件动哪一下表」这条映射的唯一实现。
//
// 它刻意与算术分开:算术(Clock 的那几只方法)回答「怎么算」,这里回答「什么时候
// 算」。两个生产者 —— chat_svc 的 turn.Dispatcher 与 agentred 的 fanout —— 都只调
// 这一只,所以两边看同一条事件流必然得出同一份数。
//
// 认不出的事件一下都不动:runtime 上线新事件类型时这里不需要跟着改,计时也不会
// 因为多了一条陌生帧而漂。
func (c *Clock) ObserveAt(ev agentruntime.Event, now time.Time) {
	if c == nil || ev == nil {
		return
	}
	switch e := ev.(type) {
	case agentruntime.TextDelta, agentruntime.ThinkingDelta:
		// 看得见的增量:记首 token,并在表被按住时自愈式重新开表。
		c.NoteVisibleTokenAt(now)
	case agentruntime.OutputActivity:
		// 纯计时信号(claudecode 的 SSE content_block_start):只记表不动表。
		c.NoteOutputTokenAt(now)
	case agentruntime.ToolCall:
		// 内层工具不碰表 —— 派遣它的外层调用已经把表按住了。
		if e.ParentToolCallID != "" {
			return
		}
		// 停表前先兜底记一次首 token:工具调用摆在这里,模型显然早就在产出 token
		// 了。有 OutputActivity 的后端先到先得,没有的(codex / piagent)靠这一条。
		c.NoteOutputTokenAt(now)
		c.SuspendGenerationAt(e.ID, now)
	case agentruntime.ToolResult:
		if e.ParentToolCallID != "" {
			return
		}
		c.ResumeGenerationAt(e.ToolCallID, now)
	}
}
