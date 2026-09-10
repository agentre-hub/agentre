package ccoauth_test

import (
	"testing"
	"time"

	. "github.com/smartystreets/goconvey/convey"

	"github.com/agentre-hub/agentre/internal/pkg/ccoauth"
)

func TestParseUsageResponse(t *testing.T) {
	Convey("ParseUsageResponse 解析完整响应（含 5h / 7d / sonnet / opus）", t, func() {
		body := []byte(`{
			"five_hour": {"utilization": 42.5, "resets_at": "2026-05-28T13:00:00Z"},
			"seven_day": {"utilization": 18.2, "resets_at": "2026-06-04T00:00:00Z"},
			"seven_day_sonnet": {"utilization": 12.1, "resets_at": "2026-06-04T00:00:00Z"},
			"seven_day_opus":   {"utilization": 6.0,  "resets_at": "2026-06-04T00:00:00Z"}
		}`)

		got, err := ccoauth.ParseUsageResponse(body)
		So(err, ShouldBeNil)
		So(got, ShouldNotBeNil)
		So(got.FiveHourPercent, ShouldAlmostEqual, 42.5, 0.001)
		So(got.WeeklyPercent, ShouldAlmostEqual, 18.2, 0.001)
		So(got.FiveHourResetsAt, ShouldNotBeNil)
		So(got.FiveHourResetsAt.Equal(time.Date(2026, 5, 28, 13, 0, 0, 0, time.UTC)), ShouldBeTrue)
		So(got.SonnetWeeklyPercent, ShouldNotBeNil)
		So(*got.SonnetWeeklyPercent, ShouldAlmostEqual, 12.1, 0.001)
		So(got.OpusWeeklyPercent, ShouldNotBeNil)
		So(*got.OpusWeeklyPercent, ShouldAlmostEqual, 6.0, 0.001)
	})

	Convey("ParseUsageResponse 在仅有 five_hour 时也成功（缺字段不算错）", t, func() {
		body := []byte(`{"five_hour": {"utilization": 10, "resets_at": "2026-05-28T13:00:00Z"}}`)

		got, err := ccoauth.ParseUsageResponse(body)
		So(err, ShouldBeNil)
		So(got.FiveHourPercent, ShouldAlmostEqual, 10, 0.001)
		So(got.WeeklyPercent, ShouldEqual, 0)
		So(got.WeeklyResetsAt, ShouldBeNil)
		So(got.SonnetWeeklyPercent, ShouldBeNil)
	})

	Convey("ParseUsageResponse 把 utilization 钳制到 [0, 100]", t, func() {
		body := []byte(`{
			"five_hour": {"utilization": -3, "resets_at": "2026-05-28T13:00:00Z"},
			"seven_day": {"utilization": 150, "resets_at": "2026-06-04T00:00:00Z"}
		}`)

		got, err := ccoauth.ParseUsageResponse(body)
		So(err, ShouldBeNil)
		So(got.FiveHourPercent, ShouldEqual, 0)
		So(got.WeeklyPercent, ShouldEqual, 100)
	})

	Convey("ParseUsageResponse 在两个窗口都没有 utilization 时返回 nil + 错误", t, func() {
		body := []byte(`{}`)

		got, err := ccoauth.ParseUsageResponse(body)
		So(err, ShouldNotBeNil)
		So(got, ShouldBeNil)
	})

	Convey("ParseUsageResponse 在 JSON 非法时返回错误", t, func() {
		got, err := ccoauth.ParseUsageResponse([]byte(`not-json`))
		So(err, ShouldNotBeNil)
		So(got, ShouldBeNil)
	})
}
