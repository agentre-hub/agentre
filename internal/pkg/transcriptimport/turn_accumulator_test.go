package transcriptimport

import (
	"errors"
	"testing"
	"time"

	. "github.com/smartystreets/goconvey/convey"

	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
)

func text(s string) agentruntime.Event { return agentruntime.TextDelta{Text: s} }

// TestTurnAccumulator 钉住三个磁盘读取器共用的「当前这一轮」状态机:开轮取号、
// 轮外事件挂起、开轮时挂起的事件先落、收尾交给消费方、消费方喊停即中止。
func TestTurnAccumulator(t *testing.T) {
	Convey("Given 一个攒轮器", t, func() {
		var got []Turn
		acc := NewTurnAccumulator(func(turn Turn) error {
			got = append(got, turn)
			return nil
		})

		Convey("When 还没开轮, Then 当前轮不可写", func() {
			So(acc.Cur(), ShouldBeNil)
		})

		Convey("When 还没开轮就收到事件, Then 挂起到下一轮开头", func() {
			acc.Emit(text("压缩摘要注入"))
			acc.Begin(Turn{UserText: "第一句"})
			acc.Emit(text("回答"))
			So(acc.Flush(), ShouldBeNil)

			So(len(got), ShouldEqual, 1)
			So(got[0].Index, ShouldEqual, 0)
			So(got[0].UserText, ShouldEqual, "第一句")
			So(got[0].Events, ShouldResemble, []agentruntime.Event{text("压缩摘要注入"), text("回答")})

			Convey("And 挂起的事件不会再落进下一轮", func() {
				acc.Begin(Turn{UserText: "第二句"})
				So(acc.Flush(), ShouldBeNil)
				So(len(got), ShouldEqual, 2)
				So(got[1].Events, ShouldBeEmpty)
			})
		})

		Convey("When 连开两轮, Then 轮号从 0 起递增", func() {
			acc.Begin(Turn{UserText: "一"})
			So(acc.Flush(), ShouldBeNil)
			acc.Begin(Turn{UserText: "二"})
			So(acc.Flush(), ShouldBeNil)
			So(len(got), ShouldEqual, 2)
			So(got[0].Index, ShouldEqual, 0)
			So(got[1].Index, ShouldEqual, 1)
		})

		Convey("When 没开轮就收尾, Then 什么都不交出去", func() {
			So(acc.Flush(), ShouldBeNil)
			So(got, ShouldBeEmpty)
		})

		Convey("When 连续收尾两次, Then 同一轮不会交两遍", func() {
			acc.Begin(Turn{UserText: "一"})
			So(acc.Flush(), ShouldBeNil)
			So(acc.Flush(), ShouldBeNil)
			So(len(got), ShouldEqual, 1)
		})

		Convey("When 开轮后写当前轮, Then 写到交出去的那一轮上", func() {
			acc.Begin(Turn{UserText: "一"})
			cur := acc.Cur()
			So(cur, ShouldNotBeNil)
			cur.Model = "claude-opus-5"
			cur.ErrorText = "被中断"
			So(acc.Flush(), ShouldBeNil)
			So(got[0].Model, ShouldEqual, "claude-opus-5")
			So(got[0].ErrorText, ShouldEqual, "被中断")
		})

		Convey("When 空事件列表, Then 既不挂起也不落轮", func() {
			acc.Emit()
			acc.Begin(Turn{})
			acc.Emit()
			So(acc.Flush(), ShouldBeNil)
			So(got[0].Events, ShouldBeEmpty)
		})

		Convey("When 记录带时间戳, Then 推进本轮结束时间", func() {
			start := time.Date(2026, 8, 26, 1, 0, 0, 0, time.UTC)
			acc.Begin(Turn{StartedAt: start, EndedAt: start})
			acc.Touch(start.Add(time.Minute))
			acc.Touch(time.Time{}) // 没有时间戳的记录不该把结束时间抹成零值
			So(acc.Flush(), ShouldBeNil)
			So(got[0].EndedAt, ShouldEqual, start.Add(time.Minute))
		})

		Convey("When 轮外收到带时间戳的记录, Then 不影响任何一轮", func() {
			acc.Touch(time.Date(2026, 8, 26, 9, 0, 0, 0, time.UTC))
			acc.Begin(Turn{})
			So(acc.Flush(), ShouldBeNil)
			So(got[0].EndedAt.IsZero(), ShouldBeTrue)
		})
	})

	Convey("Given 消费方中途喊停(预览取前 N 轮), When 收尾, Then 原样透出该错误且轮不再开着", t, func() {
		stop := errors.New("stop here")
		var seen int
		acc := NewTurnAccumulator(func(Turn) error {
			seen++
			return stop
		})
		acc.Begin(Turn{UserText: "一"})
		err := acc.Flush()
		So(errors.Is(err, stop), ShouldBeTrue)
		So(seen, ShouldEqual, 1)
		So(acc.Cur(), ShouldBeNil)
	})
}
