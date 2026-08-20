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
	Convey("工具空档不计入 tok/s 分母", t, func() {
		tc := &TurnContext{StartedAt: time.UnixMilli(1000)}
		tc.NoteVisibleTokenAt(time.UnixMilli(1420))
		tc.PauseGenerationAt(time.UnixMilli(3420))
		// 工具跑到 10000，然后下一跳
		tc.NoteVisibleTokenAt(time.UnixMilli(10000))
		tc.PauseGenerationAt(time.UnixMilli(12000))
		// 生成时长 = 2000 + 2000 = 4000ms；200 token / 4s = 50
		So(tc.TokensPerSec(200), ShouldEqual, 50)
		So(tc.FirstTokenMs(), ShouldEqual, 420)
	})
}
