package turn

import (
	"testing"
	"time"

	. "github.com/smartystreets/goconvey/convey"
)

func TestTurnContext_NoteVisibleTokenRecordsFirstTokenOnce(t *testing.T) {
	Convey("第一次可见 token 记下首 token，后续不再改", t, func() {
		tc := &TurnContext{}
		tc.StartGenerationAt(time.UnixMilli(1000))
		tc.NoteVisibleTokenAt(time.UnixMilli(1420))
		tc.NoteVisibleTokenAt(time.UnixMilli(2000))
		So(tc.FirstTokenMs(), ShouldEqual, 420)
	})
}

// 现场 sess-3241:190.1s 的一轮报出 166.6s 的首 token —— 前 23 跳全是工具调用,
// 一个字都没吐,而首 token 只认可见正文,于是"等首 token"的计时一路走到模型终于
// 开口那一刻。前端因此把「首 token」渲染成一个不断增长的整轮耗时,tok/s 又被
// waitingFirstToken 门控成不显示。
//
// NoteOutputTokenAt 是这条口径的补齐:模型产出了输出 token(哪怕是工具入参这种
// 看不见的),首 token 就该落地。它**只记表不动表** —— 分母的开/停由工具边界与
// 可见正文负责,这条信号不参与,免得动到已经钉死的 tok/s 口径。
func TestTurnContext_OutputTokenRecordsFirstTokenWithoutVisibleText(t *testing.T) {
	Convey("一句话不吐、直接甩工具调用的那一跳,首 token 也要记下", t, func() {
		tc := &TurnContext{}
		tc.StartGenerationAt(time.UnixMilli(0))
		// 模型开始产出第一个输出块(工具入参),5s 后工具才发出去
		tc.NoteOutputTokenAt(time.UnixMilli(5000))
		tc.SuspendGenerationAt("t1", time.UnixMilli(6000))
		tc.ResumeGenerationAt("t1", time.UnixMilli(60000))
		// 模型终于开口说正文已经是 166s 之后 —— 首 token 不能被推到这里
		tc.NoteVisibleTokenAt(time.UnixMilli(166000))
		So(tc.FirstTokenMs(), ShouldEqual, 5000)
	})

	Convey("只记表不动表:分母口径不受这条信号影响", t, func() {
		tc := &TurnContext{}
		tc.StartGenerationAt(time.UnixMilli(0))
		tc.SuspendGenerationAt("t1", time.UnixMilli(2000))
		// 工具还没回来就来了一条输出信号(理论上不该发生),不能把表偷偷开起来
		tc.NoteOutputTokenAt(time.UnixMilli(5000))
		tc.ResumeGenerationAt("t1", time.UnixMilli(50000))
		tc.PauseGenerationAt(time.UnixMilli(52000))
		So(tc.TokensPerSec(400), ShouldEqual, 100) // 400 / (2s + 2s),工具空档仍不算
	})
}

// 口径选定:等首 token 的排队/prefill **计入**分母 —— tok/s 回答的是「这一轮平均
// 每秒交付几个 token」,不是「模型吐字有多快」。实测一轮 pi 9.64s 里 8.01s 在排队,
// 若把排队剔掉,同一轮会从 14 tok/s 变成 83 tok/s。这条测试就是钉住这个选择。
func TestTurnContext_QueueWaitCountsTowardDenominator(t *testing.T) {
	Convey("等首 token 的排队计入分母", t, func() {
		tc := &TurnContext{}
		tc.StartGenerationAt(time.UnixMilli(0))
		tc.NoteVisibleTokenAt(time.UnixMilli(4400)) // 排队 4.4s 才吐第一个字
		tc.PauseGenerationAt(time.UnixMilli(5400))
		So(tc.TokensPerSec(270), ShouldEqual, 50) // 270 token / 5.4s
	})
}

func TestTurnContext_TokensPerSecExcludesToolGap(t *testing.T) {
	Convey("工具执行空档不计入分母", t, func() {
		tc := &TurnContext{}
		tc.StartGenerationAt(time.UnixMilli(0))
		tc.NoteVisibleTokenAt(time.UnixMilli(1000))
		tc.SuspendGenerationAt("t1", time.UnixMilli(3000))
		tc.ResumeGenerationAt("t1", time.UnixMilli(10000)) // 工具跑了 7s，不算
		tc.NoteVisibleTokenAt(time.UnixMilli(11000))
		tc.PauseGenerationAt(time.UnixMilli(13000))
		So(tc.TokensPerSec(300), ShouldEqual, 50) // 300 / (3s + 3s)
	})
}

// 分子是整轮所有内部 API call 的 output 之和，包含「一句话不吐、直接甩工具调用」
// 那些跳。本口径下它们的耗时天然在分母里，两边对得上 —— 分母只数看得见文字的段
// 才是 sess-3226 那个 3162 token ÷ 42ms = 75331 tok/s 的来路。
func TestTurnContext_CountsHopsWithoutVisibleText(t *testing.T) {
	Convey("没有可见文字的那一跳，耗时照样计入分母", t, func() {
		tc := &TurnContext{}
		tc.StartGenerationAt(time.UnixMilli(0))
		// 第一跳：2s 只产出一个工具调用，没有任何 text/thinking
		tc.SuspendGenerationAt("t1", time.UnixMilli(2000))
		tc.ResumeGenerationAt("t1", time.UnixMilli(50000)) // 工具跑了 48s，不算
		tc.NoteVisibleTokenAt(time.UnixMilli(50500))
		tc.PauseGenerationAt(time.UnixMilli(52000))
		So(tc.TokensPerSec(400), ShouldEqual, 100) // 400 / (2s + 2s)
	})
}

func TestTurnContext_ParallelToolsResumeOnlyWhenAllDone(t *testing.T) {
	Convey("并行工具：最后一个结果回来才重新开表", t, func() {
		tc := &TurnContext{}
		tc.StartGenerationAt(time.UnixMilli(0))
		tc.SuspendGenerationAt("t1", time.UnixMilli(1000))
		tc.SuspendGenerationAt("t2", time.UnixMilli(1000))
		tc.ResumeGenerationAt("t1", time.UnixMilli(3000)) // t2 还在跑，不能开表
		tc.ResumeGenerationAt("t2", time.UnixMilli(5000))
		tc.PauseGenerationAt(time.UnixMilli(6000))
		So(tc.TokensPerSec(200), ShouldEqual, 100) // (0→1000) + (5000→6000)
	})
}

func TestTurnContext_VisibleTokenReopensClockAfterLostToolResult(t *testing.T) {
	Convey("工具结果丢了也不能把表永久按住：模型一开口就重新开表", t, func() {
		tc := &TurnContext{}
		tc.StartGenerationAt(time.UnixMilli(0))
		tc.SuspendGenerationAt("t1", time.UnixMilli(1000))
		// t1 的结果永远没回来（中断 / 帧被过滤），下一跳直接开始吐字
		tc.NoteVisibleTokenAt(time.UnixMilli(4000))
		tc.PauseGenerationAt(time.UnixMilli(5000))
		So(tc.TokensPerSec(200), ShouldEqual, 100) // (0→1000) + (4000→5000)
	})
}

func TestTurnContext_AbortDuringToolDoesNotCountToolTime(t *testing.T) {
	Convey("停在工具执行中收口，工具那段不计入", t, func() {
		tc := &TurnContext{}
		tc.StartGenerationAt(time.UnixMilli(0))
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
