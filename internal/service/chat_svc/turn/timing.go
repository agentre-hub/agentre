package turn

import "time"

// tok/s 的分母口径:各次内部 API call 的**生成窗口**之和 —— 模型真的在吐 token 的
// 那段时间。等首 token 的排队/prefill 不算,工具执行不算。
//
// 为什么不是「整段墙钟减工具空档」:实测一轮 pi(glm-5.3)9.6s 墙钟里只有 1.6s 在
// 生成,其余全是等首 token;按墙钟算会把 83 tok/s 的模型报成 14 —— 那是含排队的
// 吞吐量,不是输出速率。
//
// 一次 call 的窗口:
//   - 有可见增量(text/thinking)→ [首个增量, 本次 call 收尾]。工具入参 JSON 的产出
//     时间天然落在窗口里(它在 call 收尾之前),不必单独盯。
//   - 一句话不吐、直接甩工具调用 → 没有增量可以框窗口,退回按这一跳的墙钟算
//     [本 call 起点, 收尾]。它的 output token 照样进分子(usage 逐跳累加),分母
//     若给 0 就是白嫖 —— sess-3226 的 3162 token ÷ 42ms = 75331 tok/s 就是这么来的。
//     兜底宁可偏大,不可为 0。
//
// call 起点 = turn/分段开始、上一次 call 收尾、最后一个工具结果回来,三者中最晚的那个。
// call 收尾 = 外层 tool_use 事件(模型这一跳说完了),或段末收口。

// StartGenerationAt 起一段 assistant 的计时。turn / steer 分段开始时调一次。
// 注意它不开表 —— 表要等模型真的开口(NoteVisibleToken)才走。
func (tc *TurnContext) StartGenerationAt(now time.Time) {
	if tc == nil {
		return
	}
	if tc.StartedAt.IsZero() {
		tc.StartedAt = now
	}
	tc.CallStartedAt = now
	tc.SawVisibleToken = false
}

func (tc *TurnContext) NoteVisibleToken() {
	tc.NoteVisibleTokenAt(time.Now())
}

// NoteVisibleTokenAt 记首 token(TTFT)并开表。表没在走就从这一刻开始算生成。
func (tc *TurnContext) NoteVisibleTokenAt(now time.Time) {
	if tc == nil {
		return
	}
	if tc.StartedAt.IsZero() {
		tc.StartedAt = now
	}
	if tc.FirstTokenAt.IsZero() {
		tc.FirstTokenAt = now
	}
	// 模型在说话 = 工具空档必然已经结束。工具结果因中断/过滤没回来时,这是唯一的
	// 自愈路径,否则表被永久按住,分母又会塌回几十毫秒。
	if len(tc.PendingTools) > 0 {
		tc.PendingTools = nil
		tc.CallStartedAt = now
	}
	tc.SawVisibleToken = true
	if tc.BurstStartedAt.IsZero() {
		tc.BurstStartedAt = now
	}
}

// SuspendGeneration 外层工具调用发出:模型这一跳到此为止,接下来是工具执行。
func (tc *TurnContext) SuspendGeneration(toolCallID string) {
	tc.SuspendGenerationAt(toolCallID, time.Now())
}

func (tc *TurnContext) SuspendGenerationAt(toolCallID string, now time.Time) {
	if tc == nil {
		return
	}
	tc.endCallAt(now)
	if tc.PendingTools == nil {
		tc.PendingTools = map[string]struct{}{}
	}
	tc.PendingTools[toolCallID] = struct{}{}
}

// ResumeGeneration 工具结果回来:下一跳从这一刻起算(并行工具全部回齐才算)。
func (tc *TurnContext) ResumeGeneration(toolCallID string) {
	tc.ResumeGenerationAt(toolCallID, time.Now())
}

func (tc *TurnContext) ResumeGenerationAt(toolCallID string, now time.Time) {
	if tc == nil {
		return
	}
	delete(tc.PendingTools, toolCallID)
	if len(tc.PendingTools) > 0 {
		return
	}
	tc.CallStartedAt = now
	tc.SawVisibleToken = false
}

// PauseGeneration 段末收口。
func (tc *TurnContext) PauseGeneration() {
	tc.PauseGenerationAt(time.Now())
}

func (tc *TurnContext) PauseGenerationAt(now time.Time) {
	if tc == nil {
		return
	}
	tc.endCallAt(now)
}

// endCallAt 收口当前这次 API call 的窗口并累加进 Generation。
func (tc *TurnContext) endCallAt(now time.Time) {
	switch {
	case !tc.BurstStartedAt.IsZero():
		if d := now.Sub(tc.BurstStartedAt); d > 0 {
			tc.Generation += d
		}
	case tc.SawVisibleToken || len(tc.PendingTools) > 0 || tc.CallStartedAt.IsZero():
		// 已经收过口(重复调用)、或此刻正卡在工具执行里(那段不是生成),都不再累加。
	default:
		// 这一跳一个字没吐就甩了工具调用 —— 按墙钟兜底。
		if d := now.Sub(tc.CallStartedAt); d > 0 {
			tc.Generation += d
		}
	}
	tc.BurstStartedAt = time.Time{}
	tc.SawVisibleToken = false
	tc.CallStartedAt = now
}

func (tc *TurnContext) FirstTokenMs() int {
	if tc == nil || tc.FirstTokenAt.IsZero() || tc.StartedAt.IsZero() {
		return 0
	}
	ms := int(tc.FirstTokenAt.Sub(tc.StartedAt).Milliseconds())
	if ms < 0 {
		return 0
	}
	return ms
}

func (tc *TurnContext) TokensPerSec(completion int) float64 {
	if tc == nil || completion <= 0 {
		return 0
	}
	gen := tc.Generation
	if !tc.BurstStartedAt.IsZero() {
		gen += time.Since(tc.BurstStartedAt)
	}
	if gen <= 0 {
		return 0
	}
	return float64(completion) / gen.Seconds()
}
