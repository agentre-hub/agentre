package turn

import (
	"testing"
	"time"

	. "github.com/smartystreets/goconvey/convey"
)

func TestTurnContext_NoteVisibleTokenRecordsFirstTokenOnce(t *testing.T) {
	Convey("第一次可见 token 记下首 token，后续不再改", t, func() {
		tc := &TurnContext{StartedAt: time.UnixMilli(1000)}
		tc.StartGenerationAt(time.UnixMilli(1000))
		tc.NoteVisibleTokenAt(time.UnixMilli(1420))
		tc.NoteVisibleTokenAt(time.UnixMilli(2000))
		So(tc.FirstTokenMs(), ShouldEqual, 420)
	})
}

// 分母是「模型真的在吐字」的那段，不是墙钟：实测一轮 pi 里 9.6s 墙钟只有 1.6s 在
// 生成，其余是等首 token 的排队/prefill。把排队算进分母，83 tok/s 的模型会被报成
// 14 tok/s —— 那已经不是输出速率了。
func TestTurnContext_QueueWaitBeforeFirstTokenIsNotGeneration(t *testing.T) {
	Convey("等首 token 的排队不计入分母", t, func() {
		tc := &TurnContext{}
		tc.StartGenerationAt(time.UnixMilli(0))
		tc.NoteVisibleTokenAt(time.UnixMilli(4400)) // 排队 4.4s 才吐第一个字
		tc.PauseGenerationAt(time.UnixMilli(5400))
		So(tc.TokensPerSec(80), ShouldEqual, 80) // 80 token / 1s
	})
}

func TestTurnContext_TokensPerSecExcludesToolGap(t *testing.T) {
	Convey("工具执行空档不计入分母", t, func() {
		tc := &TurnContext{}
		tc.StartGenerationAt(time.UnixMilli(0))
		tc.NoteVisibleTokenAt(time.UnixMilli(1000))
		tc.SuspendGenerationAt("t1", time.UnixMilli(3000)) // 这一跳生成 2s，然后跑工具
		tc.ResumeGenerationAt("t1", time.UnixMilli(10000)) // 工具跑了 7s
		tc.NoteVisibleTokenAt(time.UnixMilli(11000))       // 下一跳等 1s 才开口
		tc.PauseGenerationAt(time.UnixMilli(13000))
		So(tc.TokensPerSec(200), ShouldEqual, 50) // 200 / (2s + 2s)
	})
}

// 一句话不吐、直接甩工具调用的那一跳，output token 照样计进分子（usage 逐跳累加）。
// 它没有可见增量可以框出窗口，只能按这一跳的墙钟兜底 —— 宁可分母偏大，也不能是 0：
// 白嫖分母就是 sess-3226 那个 3162 token ÷ 42ms = 75331 tok/s 的来路。
func TestTurnContext_HopWithoutVisibleTextFallsBackToWallClock(t *testing.T) {
	Convey("没有可见增量的那一跳按墙钟兜底,不能白嫖分母", t, func() {
		tc := &TurnContext{}
		tc.StartGenerationAt(time.UnixMilli(0))
		// 第一跳:1s 内直接产出一个工具调用,全程没有 text/thinking
		tc.SuspendGenerationAt("t1", time.UnixMilli(1000))
		tc.ResumeGenerationAt("t1", time.UnixMilli(50000)) // 工具跑了 49s,不算
		tc.NoteVisibleTokenAt(time.UnixMilli(50000))
		tc.PauseGenerationAt(time.UnixMilli(52000))
		So(tc.TokensPerSec(300), ShouldEqual, 100) // 300 / (1s + 2s)
	})
}

func TestTurnContext_ParallelToolsResumeOnlyWhenAllDone(t *testing.T) {
	Convey("并行工具:最后一个结果回来才算这一跳可以重新开始", t, func() {
		tc := &TurnContext{}
		tc.StartGenerationAt(time.UnixMilli(0))
		tc.NoteVisibleTokenAt(time.UnixMilli(0))
		tc.SuspendGenerationAt("t1", time.UnixMilli(1000))
		tc.SuspendGenerationAt("t2", time.UnixMilli(1000))
		tc.ResumeGenerationAt("t1", time.UnixMilli(3000)) // t2 还在跑
		tc.ResumeGenerationAt("t2", time.UnixMilli(5000))
		tc.NoteVisibleTokenAt(time.UnixMilli(5000))
		tc.PauseGenerationAt(time.UnixMilli(6000))
		So(tc.TokensPerSec(200), ShouldEqual, 100) // (0→1000) + (5000→6000)
	})
}

func TestTurnContext_VisibleTokenReopensClockAfterLostToolResult(t *testing.T) {
	Convey("工具结果丢了也不能把表永久按住:模型一开口就重新开表", t, func() {
		tc := &TurnContext{}
		tc.StartGenerationAt(time.UnixMilli(0))
		tc.NoteVisibleTokenAt(time.UnixMilli(0))
		tc.SuspendGenerationAt("t1", time.UnixMilli(1000))
		// t1 的结果永远没回来(中断 / 帧被过滤),下一跳直接开始吐字
		tc.NoteVisibleTokenAt(time.UnixMilli(4000))
		tc.PauseGenerationAt(time.UnixMilli(5000))
		So(tc.TokensPerSec(200), ShouldEqual, 100) // (0→1000) + (4000→5000)
	})
}

// 轮在工具执行中被打断:那段工具时间不能被兜底当成生成。
func TestTurnContext_AbortDuringToolDoesNotCountToolTime(t *testing.T) {
	Convey("停在工具执行中收口,工具那段不计入", t, func() {
		tc := &TurnContext{}
		tc.StartGenerationAt(time.UnixMilli(0))
		tc.NoteVisibleTokenAt(time.UnixMilli(0))
		tc.SuspendGenerationAt("t1", time.UnixMilli(2000))
		tc.PauseGenerationAt(time.UnixMilli(60000)) // 工具跑了 58s 后整轮被中断
		So(tc.TokensPerSec(200), ShouldEqual, 100)  // 只有 0→2000
	})
}

func TestTurnContext_TokensPerSecZeroWithoutClock(t *testing.T) {
	Convey("没开过表就没有速度可言", t, func() {
		tc := &TurnContext{}
		So(tc.TokensPerSec(100), ShouldEqual, 0)
	})
}
