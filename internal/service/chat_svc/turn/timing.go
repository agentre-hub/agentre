package turn

import "time"

// tok/s 的分母口径:这一段 assistant 的整轮耗时,减去工具执行占掉的空档。
//
// 也就是说,等首 token 的排队 / prefill **计入**分母 —— 这是有意选的口径:它回答
// 「这一轮平均每秒交付几个 token」,不是「模型吐字有多快」。实测一轮 pi(glm-5.3)
// 9.64s 里 8.01s 在等首 token、0.01s 跑工具、1.62s 真在吐字:按本口径是 14 tok/s,
// 按「只数吐字的那段」是 83 tok/s。两个都不算错,这里取前者。
//
// 分子是整轮所有内部 API call 的 output token 之和(usage 帧逐跳累加),包含「一句话
// 不吐、直接甩工具调用」的那些跳;本口径下它们的耗时也照样在分母里,两边对得上。
// 若分母只数看得见文字的那几段,那些跳就白嫖分母 —— sess-3226 的 3162 token ÷ 42ms
// = 75331 tok/s 就是这么来的。
//
// 表在 turn(或 steer 分段)开始时开;外层工具调用发出即停表,工具结果回来再开表;
// 段末收口。模型开口说话(NoteVisibleToken)同样重新开表 —— 工具结果因中断/过滤没
// 回来时的自愈,免得表被永久按住又把分母压回几十毫秒。

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

// NoteOutputToken / NoteOutputTokenAt 记首 token,**只记表不动表**。
//
// 给「模型确实在产出输出 token,但产出的东西用户看不见」的信号用 —— 典型是一跳
// 只有工具调用、一个字的正文都没有(claudecode 的 SSE content_block_start;其它
// 后端由 ToolCall 兜底)。没有它,首 token 会一路推迟到模型终于开口说正文那一刻:
// sess-3241 里 190.1s 的一轮报出了 166.6s 的首 token。
//
// 与 NoteVisibleTokenAt 的区别是刻意的:那条要清挂账 + 重新开表(可见正文 = 工具
// 空档必然已结束的自愈),而这条只补一个时间戳。分母的开/停完全由工具边界和可见
// 正文决定,不让新信号动到已经钉死的 tok/s 口径。
func (tc *TurnContext) NoteOutputToken() {
	tc.NoteOutputTokenAt(time.Now())
}

func (tc *TurnContext) NoteOutputTokenAt(now time.Time) {
	if tc == nil {
		return
	}
	if tc.StartedAt.IsZero() {
		tc.StartedAt = now
	}
	if tc.FirstTokenAt.IsZero() {
		tc.FirstTokenAt = now
	}
}

// SuspendGeneration 停表:toolCallID 这个外层工具开始执行,这段空档不算。
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
