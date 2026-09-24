package ccoauth_test

import (
	"testing"
	"time"

	. "github.com/smartystreets/goconvey/convey"

	"github.com/agentre-hub/agentre/internal/pkg/ccoauth"
)

func TestParseUsageResponse(t *testing.T) {
	Convey("ParseUsageResponse 解析 5h / 7d 两个主窗口", t, func() {
		body := []byte(`{
			"five_hour": {"utilization": 42.5, "resets_at": "2026-05-28T13:00:00Z"},
			"seven_day": {"utilization": 18.2, "resets_at": "2026-06-04T00:00:00Z"}
		}`)

		got, err := ccoauth.ParseUsageResponse(body)
		So(err, ShouldBeNil)
		So(got, ShouldNotBeNil)
		So(got.FiveHourPercent, ShouldAlmostEqual, 42.5, 0.001)
		So(got.WeeklyPercent, ShouldAlmostEqual, 18.2, 0.001)
		So(got.FiveHourResetsAt, ShouldNotBeNil)
		So(got.FiveHourResetsAt.Equal(time.Date(2026, 5, 28, 13, 0, 0, 0, time.UTC)), ShouldBeTrue)
		So(got.ModelWeekly, ShouldBeEmpty)
	})

	Convey("ParseUsageResponse 从 limits[] 的 weekly_scoped 条目取按模型的周配额（真实响应形状）", t, func() {
		// 2026-09 实测：Fable 没有 seven_day_fable 之类的具名字段，只以
		// weekly_scoped + scope.model.display_name 出现在 limits[] 里；
		// session / weekly_all 与 5h / 7d 重复，不应再进模型列表。
		body := []byte(`{
			"five_hour": {"utilization": 28.0, "resets_at": "2026-09-24T06:39:59.869317+00:00"},
			"seven_day": {"utilization": 74.0, "resets_at": "2026-09-24T11:59:59.869338+00:00"},
			"seven_day_opus": null,
			"seven_day_sonnet": null,
			"limits": [
				{"kind": "session", "group": "session", "percent": 28, "resets_at": "2026-09-24T06:39:59.869317+00:00", "scope": null},
				{"kind": "weekly_all", "group": "weekly", "percent": 74, "resets_at": "2026-09-24T11:59:59.869338+00:00", "scope": null},
				{"kind": "weekly_scoped", "group": "weekly", "percent": 3, "resets_at": "2026-09-24T12:00:00+00:00",
				 "scope": {"model": {"id": null, "display_name": "Fable"}, "surface": null}},
				{"kind": "weekly_scoped", "group": "weekly", "percent": 9, "resets_at": null,
				 "scope": {"model": null, "surface": "cowork"}}
			]
		}`)

		got, err := ccoauth.ParseUsageResponse(body)
		So(err, ShouldBeNil)
		So(got.FiveHourResetsAt, ShouldNotBeNil)
		So(got.ModelWeekly, ShouldHaveLength, 1)
		So(got.ModelWeekly[0].Model, ShouldEqual, "Fable")
		So(got.ModelWeekly[0].Percent, ShouldAlmostEqual, 3, 0.001)
		So(got.ModelWeekly[0].ResetsAt, ShouldNotBeNil)
		So(got.ModelWeekly[0].ResetsAt.Equal(time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)), ShouldBeTrue)
	})

	Convey("ParseUsageResponse 把旧的 seven_day_sonnet / seven_day_opus 并进模型列表，limits[] 已有同名则不重复", t, func() {
		body := []byte(`{
			"seven_day": {"utilization": 18.2, "resets_at": "2026-06-04T00:00:00Z"},
			"seven_day_sonnet": {"utilization": 12.1, "resets_at": "2026-06-04T00:00:00Z"},
			"seven_day_opus":   {"utilization": 6.0,  "resets_at": "2026-06-04T00:00:00Z"},
			"limits": [
				{"kind": "weekly_scoped", "percent": 7, "resets_at": "2026-06-04T00:00:00Z",
				 "scope": {"model": {"display_name": "Opus"}}}
			]
		}`)

		got, err := ccoauth.ParseUsageResponse(body)
		So(err, ShouldBeNil)
		So(got.ModelWeekly, ShouldHaveLength, 2)
		So(got.ModelWeekly[0].Model, ShouldEqual, "Opus")
		So(got.ModelWeekly[0].Percent, ShouldAlmostEqual, 7, 0.001)
		So(got.ModelWeekly[1].Model, ShouldEqual, "Sonnet")
		So(got.ModelWeekly[1].Percent, ShouldAlmostEqual, 12.1, 0.001)
	})

	Convey("ParseUsageResponse 在仅有 five_hour 时也成功（缺字段不算错）", t, func() {
		body := []byte(`{"five_hour": {"utilization": 10, "resets_at": "2026-05-28T13:00:00Z"}}`)

		got, err := ccoauth.ParseUsageResponse(body)
		So(err, ShouldBeNil)
		So(got.FiveHourPercent, ShouldAlmostEqual, 10, 0.001)
		So(got.WeeklyPercent, ShouldEqual, 0)
		So(got.WeeklyResetsAt, ShouldBeNil)
		So(got.ModelWeekly, ShouldBeEmpty)
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
