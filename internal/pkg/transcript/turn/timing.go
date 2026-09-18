package turn

import (
	"time"

	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
)

// 本轮计时的口径与算术全在 internal/pkg/turnstats —— agentred 的 fanout 也要算同
// 一份数(浏览器的转录只有事件流可读,拿不到 chat_svc 落的库),而 daemon 不许 import
// service 层。这里只留 *TurnContext 的一层薄壳:调用方拿到的 tc 可能是 nil(handler
// 单测场景),而嵌入字段的提升方法在 nil 接收者上会当场炸。
//
// 「哪条事件动哪一下表」的映射不在各个 handler 里,在 Dispatcher.Apply —— 见那里。

// StartGenerationAt 开表。turn / steer 分段开始时调一次。
func (tc *TurnContext) StartGenerationAt(now time.Time) {
	if tc == nil {
		return
	}
	tc.Clock.StartGenerationAt(now)
}

// PauseGeneration 段末收口停表。
func (tc *TurnContext) PauseGeneration() {
	if tc == nil {
		return
	}
	tc.PauseGenerationAt(time.Now())
}

func (tc *TurnContext) FirstTokenMs() int {
	if tc == nil {
		return 0
	}
	return tc.Clock.FirstTokenMs()
}

func (tc *TurnContext) TokensPerSec(completion int) float64 {
	if tc == nil {
		return 0
	}
	return tc.Clock.TokensPerSec(completion)
}

// observeAt 把一条事件交给本轮的表。映射本身归 turnstats,这里只兜 nil。
func (tc *TurnContext) observeAt(ev agentruntime.Event, now time.Time) {
	if tc == nil {
		return
	}
	tc.ObserveAt(ev, now)
}
