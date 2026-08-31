// Package turnstats 一轮 assistant 的计时口径:耗时、首 token、tok/s。
//
// 为什么是 internal/pkg 而不是留在 chat_svc:同一条口径有两个生产者 —— 桌面端的
// chat_svc(算完落库,本机会话的转录直接读库)与 agentred 的 fanout(算完盖在
// runtime.runResultDone 终态帧上,浏览器 / 远端只拿得到事件流)。两边各写一份必然
// 漂移,而漂移的表现是同一轮在两个界面上报出不同的 tok/s。daemon 不许 import
// service 层,所以口径落在这一层,两边都往下依赖。
package turnstats

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
//
// StartedAt 是这一段 assistant 开始的时刻;FirstTokenAt 是第一帧 thinking/chunk
// (TTFT);BurstStartedAt 非零表示计时正在走,值是本次开表时刻;Generation 是已经
// 停表的各段之和;PendingTools 是正在执行、把表按住的外层工具。
type Clock struct {
	StartedAt      time.Time
	FirstTokenAt   time.Time
	BurstStartedAt time.Time
	Generation     time.Duration
	PendingTools   map[string]struct{}
}

// StartGenerationAt 开表。turn / steer 分段开始时调一次。
func (c *Clock) StartGenerationAt(now time.Time) {
	if c == nil {
		return
	}
	if c.StartedAt.IsZero() {
		c.StartedAt = now
	}
	c.resumeAt(now)
}

// NoteVisibleTokenAt 记首 token(TTFT),并在表被按住时重新开表(见上方自愈说明)。
func (c *Clock) NoteVisibleTokenAt(now time.Time) {
	if c == nil {
		return
	}
	if c.StartedAt.IsZero() {
		c.StartedAt = now
	}
	if c.FirstTokenAt.IsZero() {
		c.FirstTokenAt = now
	}
	// 模型在说话 = 工具空档必然已经结束,清掉所有挂账再开表。
	c.PendingTools = nil
	c.resumeAt(now)
}

// NoteOutputTokenAt 记首 token,**只记表不动表**。
//
// 给「模型确实在产出输出 token,但产出的东西用户看不见」的信号用 —— 典型是一跳
// 只有工具调用、一个字的正文都没有(claudecode 的 SSE content_block_start;其它
// 后端由 ToolCall 兜底)。没有它,首 token 会一路推迟到模型终于开口说正文那一刻:
// sess-3241 里 190.1s 的一轮报出了 166.6s 的首 token。
//
// 与 NoteVisibleTokenAt 的区别是刻意的:那条要清挂账 + 重新开表(可见正文 = 工具
// 空档必然已结束的自愈),而这条只补一个时间戳。分母的开/停完全由工具边界和可见
// 正文决定,不让新信号动到已经钉死的 tok/s 口径。
func (c *Clock) NoteOutputTokenAt(now time.Time) {
	if c == nil {
		return
	}
	if c.StartedAt.IsZero() {
		c.StartedAt = now
	}
	if c.FirstTokenAt.IsZero() {
		c.FirstTokenAt = now
	}
}

// SuspendGenerationAt 停表:toolCallID 这个外层工具开始执行,这段空档不算。
func (c *Clock) SuspendGenerationAt(toolCallID string, now time.Time) {
	if c == nil {
		return
	}
	if c.PendingTools == nil {
		c.PendingTools = map[string]struct{}{}
	}
	c.PendingTools[toolCallID] = struct{}{}
	c.stopAt(now)
}

// ResumeGenerationAt 开表:toolCallID 的结果已回。并行工具全部回齐才真的开表。
func (c *Clock) ResumeGenerationAt(toolCallID string, now time.Time) {
	if c == nil {
		return
	}
	delete(c.PendingTools, toolCallID)
	if len(c.PendingTools) > 0 {
		return
	}
	c.resumeAt(now)
}

// PauseGenerationAt 段末收口停表。
func (c *Clock) PauseGenerationAt(now time.Time) {
	c.stopAt(now)
}

func (c *Clock) resumeAt(now time.Time) {
	if c.BurstStartedAt.IsZero() {
		c.BurstStartedAt = now
	}
}

func (c *Clock) stopAt(now time.Time) {
	if c == nil || c.BurstStartedAt.IsZero() {
		return
	}
	if d := now.Sub(c.BurstStartedAt); d > 0 {
		c.Generation += d
	}
	c.BurstStartedAt = time.Time{}
}

// FirstTokenMs 首 token 距开表的毫秒数。没开过表 / 没记到首 token 时为 0。
func (c *Clock) FirstTokenMs() int {
	if c == nil || c.FirstTokenAt.IsZero() || c.StartedAt.IsZero() {
		return 0
	}
	ms := int(c.FirstTokenAt.Sub(c.StartedAt).Milliseconds())
	if ms < 0 {
		return 0
	}
	return ms
}

// DurationMs 这一段 assistant 到 now 为止的**墙上**耗时 —— 与 tok/s 的分母不同,
// 它**含**工具空档:耗时回答「这一轮等了多久」,一秒都不该剔。chat_svc 那侧的
// assistantMsg.DurationMs 取 time.Since(segmentStart),与这里同义。
func (c *Clock) DurationMs(now time.Time) int {
	if c == nil || c.StartedAt.IsZero() {
		return 0
	}
	ms := int(now.Sub(c.StartedAt).Milliseconds())
	if ms < 0 {
		return 0
	}
	return ms
}

// TokensPerSec 分子由调用方给(整轮累加的 completion token),分母见文件头口径。
func (c *Clock) TokensPerSec(completion int) float64 {
	if c == nil || completion <= 0 {
		return 0
	}
	gen := c.Generation
	if !c.BurstStartedAt.IsZero() {
		gen += time.Since(c.BurstStartedAt)
	}
	if gen <= 0 {
		return 0
	}
	return float64(completion) / gen.Seconds()
}
