package turn

import (
	"testing"
	"time"

	. "github.com/smartystreets/goconvey/convey"
)

func TestTurnContext_NoteVisibleTokenRecordsFirstTokenOnce(t *testing.T) {
	Convey("第一次可见 token 记下首 token，后续不再改", t, func() {
		tc := &TurnContext{StartedAt: time.UnixMilli(1000)}
		tc.NoteVisibleTokenAt(time.UnixMilli(1420))
		tc.NoteVisibleTokenAt(time.UnixMilli(2000))
		So(tc.FirstTokenMs(), ShouldEqual, 420)
	})
}

func TestTurnContext_TokensPerSecExcludesToolGap(t *testing.T) {
	Convey("工具执行空档不计入 tok/s 分母", t, func() {
		tc := &TurnContext{StartedAt: time.UnixMilli(1000)}
		tc.StartGenerationAt(time.UnixMilli(1000))
		tc.NoteVisibleTokenAt(time.UnixMilli(1420))
		// 第一跳生成到 3420 发出工具调用，工具跑到 10000 才回结果
		tc.SuspendGenerationAt("t1", time.UnixMilli(3420))
		tc.ResumeGenerationAt("t1", time.UnixMilli(10000))
		tc.PauseGenerationAt(time.UnixMilli(12000))
		// 生成时长 = (1000→3420) + (10000→12000) = 4420ms；221 token / 4.42s = 50
		So(tc.TokensPerSec(221), ShouldEqual, 50)
		So(tc.FirstTokenMs(), ShouldEqual, 420)
	})
}

// 分子是整轮所有内部 API call 的 output 之和，其中包括「一句话都不吐、直接发工具
// 调用」那些跳。分母必须同样把那些跳的生成时间算进去，否则 3162 token ÷ 42ms 这种
// 75331 tok/s 的鬼数字会再回来（sess-3226）。
func TestTurnContext_CountsHopsWithoutVisibleText(t *testing.T) {
	Convey("没有可见文字的那一跳，生成时间照样计入分母", t, func() {
		tc := &TurnContext{StartedAt: time.UnixMilli(0)}
		tc.StartGenerationAt(time.UnixMilli(0))
		// 第一跳：整整 2s 只产出一个工具调用，没有任何 text/thinking
		tc.SuspendGenerationAt("t1", time.UnixMilli(2000))
		tc.ResumeGenerationAt("t1", time.UnixMilli(50000)) // 工具跑了 48s
		// 第二跳：2s 吐完正文
		tc.NoteVisibleTokenAt(time.UnixMilli(50500))
		tc.PauseGenerationAt(time.UnixMilli(52000))
		So(tc.TokensPerSec(400), ShouldEqual, 100) // 400 / 4s
	})
}

func TestTurnContext_ParallelToolsResumeOnlyWhenAllDone(t *testing.T) {
	Convey("并行工具：最后一个结果回来才重新开表", t, func() {
		tc := &TurnContext{StartedAt: time.UnixMilli(0)}
		tc.StartGenerationAt(time.UnixMilli(0))
		tc.SuspendGenerationAt("t1", time.UnixMilli(1000))
		tc.SuspendGenerationAt("t2", time.UnixMilli(1000))
		tc.ResumeGenerationAt("t1", time.UnixMilli(3000)) // t2 还在跑，不能开表
		tc.ResumeGenerationAt("t2", time.UnixMilli(5000))
		tc.PauseGenerationAt(time.UnixMilli(6000))
		So(tc.TokensPerSec(200), ShouldEqual, 100) // (0→1000)+(5000→6000) = 2s
	})
}

func TestTurnContext_VisibleTokenReopensClockAfterLostToolResult(t *testing.T) {
	Convey("工具结果丢了也不能把表永久按住：模型一开口就重新开表", t, func() {
		tc := &TurnContext{StartedAt: time.UnixMilli(0)}
		tc.StartGenerationAt(time.UnixMilli(0))
		tc.SuspendGenerationAt("t1", time.UnixMilli(1000))
		// t1 的结果永远没回来（中断 / 帧被过滤），下一跳直接开始吐字
		tc.NoteVisibleTokenAt(time.UnixMilli(4000))
		tc.PauseGenerationAt(time.UnixMilli(5000))
		So(tc.TokensPerSec(200), ShouldEqual, 100) // (0→1000)+(4000→5000) = 2s
	})
}

func TestTurnContext_TokensPerSecZeroWithoutClock(t *testing.T) {
	Convey("没开过表就没有速度可言", t, func() {
		tc := &TurnContext{StartedAt: time.UnixMilli(0)}
		So(tc.TokensPerSec(100), ShouldEqual, 0)
	})
}
