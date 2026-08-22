package turn

import "time"

// 本轮的「生成时长」口径:整段 assistant 的墙钟,减去工具执行占掉的空档。
//
// 为什么不是「第一帧可见文字 → 最后一帧可见文字」:tok/s 的分子是整轮**所有**内部
// API call 的 output token 之和(usage 帧逐跳累加),里面包含「一句话都不吐、直接发
// 工具调用」的那些跳。分母若只数看得见文字的那几段,两边口径对不上 —— sess-3226 的
// 3162 token ÷ 42ms = 75331 tok/s 就是这么算出来的。
//
// 表在 turn(或 steer 分段)开始时开;工具调用发出即停表,工具结果回来再开表;段末
// 收口。模型开口说话(NoteVisibleToken)同样重新开表 —— 工具结果因中断/过滤没回来时
// 的自愈,免得表被永久按住又把分母压回几十毫秒。

// StartGenerationAt 开表。turn / steer 分段开始时调一次。
func (tc *TurnContext) StartGenerationAt(now time.Time) {
	if tc == nil {
		return
	}
	if tc.StartedAt.IsZero() {
		tc.StartedAt = now
	}
	tc.resumeAt(now)
}

func (tc *TurnContext) NoteVisibleToken() {
	tc.NoteVisibleTokenAt(time.Now())
}

// NoteVisibleTokenAt 记首 token(TTFT),并在表被按住时重新开表(见上方自愈说明)。
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
	// 模型在说话 = 工具空档必然已经结束,清掉所有挂账再开表。
	tc.PendingTools = nil
	tc.resumeAt(now)
}

// SuspendGeneration 停表:toolCallID 这个工具开始执行,模型这一跳的生成到此为止。
func (tc *TurnContext) SuspendGeneration(toolCallID string) {
	tc.SuspendGenerationAt(toolCallID, time.Now())
}

func (tc *TurnContext) SuspendGenerationAt(toolCallID string, now time.Time) {
	if tc == nil {
		return
	}
	if tc.PendingTools == nil {
		tc.PendingTools = map[string]struct{}{}
	}
	tc.PendingTools[toolCallID] = struct{}{}
	tc.stopAt(now)
}

// ResumeGeneration 开表:toolCallID 的结果已回。并行工具全部回齐才真的开表。
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
	tc.resumeAt(now)
}

// PauseGeneration 段末收口停表。
func (tc *TurnContext) PauseGeneration() {
	tc.PauseGenerationAt(time.Now())
}

func (tc *TurnContext) PauseGenerationAt(now time.Time) {
	tc.stopAt(now)
}

func (tc *TurnContext) resumeAt(now time.Time) {
	if tc.BurstStartedAt.IsZero() {
		tc.BurstStartedAt = now
	}
}

func (tc *TurnContext) stopAt(now time.Time) {
	if tc == nil || tc.BurstStartedAt.IsZero() {
		return
	}
	if d := now.Sub(tc.BurstStartedAt); d > 0 {
		tc.Generation += d
	}
	tc.BurstStartedAt = time.Time{}
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
