package transcriptimport

import (
	"time"

	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
)

// TurnAccumulator 是三个磁盘读取器共用的「当前这一轮」状态:轮号、开着没开着、
// 攒到一半的那一轮、以及首轮开始之前到达的事件。
//
// 只攒当前这一轮,不累计整份转录 —— 42 轮 / 402 次工具调用的会话是常态,而同一份
// 实现还要在 daemon 里按 RPC 逐轮发出去。
//
// 各读取器把它嵌进自己的 turnReader,自己那份"什么算一轮的起点"(claude 是
// isUserPrompt、codex 是 task_started、pi 是 role=user)留在各自包里。
type TurnAccumulator struct {
	yield func(Turn) error

	index   int
	started bool
	cur     Turn
	pending []agentruntime.Event
}

// NewTurnAccumulator 造一个攒轮器。yield 返回非 nil 时立刻停止回放并原样返回该
// 错误(预览取前 N 轮就靠它提前收工)。
func NewTurnAccumulator(yield func(Turn) error) TurnAccumulator {
	return TurnAccumulator{yield: yield}
}

// Begin 开一轮:取轮号、把此前挂起的事件落到这一轮开头。turn 里的 Index 与 Events
// 由攒轮器填,调用方给的是那一轮自己的东西(用户正文、时间、fork 锚点、模型)。
func (a *TurnAccumulator) Begin(turn Turn) {
	turn.Index = a.index
	turn.Events = a.pending
	a.pending = nil
	a.cur = turn
	a.started = true
}

// Cur 返回当前这一轮的可写指针;还没开轮时返回 nil —— 「轮外读到的东西不该落进
// 任何一轮」这条判据因此只写一遍,而不是在每个写点重复一次 started 检查。
func (a *TurnAccumulator) Cur() *Turn {
	if !a.started {
		return nil
	}
	return &a.cur
}

// Emit 把事件落到当前轮;还没开轮就挂起,等下一轮开头再落(压缩摘要注入之类的
// 轮外事件靠它不丢)。
func (a *TurnAccumulator) Emit(events ...agentruntime.Event) {
	if len(events) == 0 {
		return
	}
	if !a.started {
		a.pending = append(a.pending, events...)
		return
	}
	a.cur.Events = append(a.cur.Events, events...)
}

// Touch 用一条记录的时间戳推进本轮的结束时间。没有时间戳(零值)或还没开轮都是
// 空操作 —— 不拿零值把已经算对的结束时间抹掉。
func (a *TurnAccumulator) Touch(ts time.Time) {
	if !a.started || ts.IsZero() {
		return
	}
	a.cur.EndedAt = ts
}

// Flush 把攒好的一轮交给消费方。消费方返回错误即中止回放(预览取前 N 轮靠它收工)。
func (a *TurnAccumulator) Flush() error {
	if !a.started {
		return nil
	}
	turn := a.cur
	a.started = false
	a.cur = Turn{}
	a.index++
	return a.yield(turn)
}
