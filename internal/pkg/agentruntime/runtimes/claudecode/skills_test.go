package claudecode

import (
	"encoding/json"
	"testing"

	. "github.com/smartystreets/goconvey/convey"
)

func TestBuildSkillsSettings(t *testing.T) {
	Convey("把 enabledPlugins 合进 settings", t, func() {
		Convey("空 base", func() {
			got := buildSkillsSettings(map[string]bool{"a@m": true, "b@m": false}, "")
			var m map[string]any
			So(json.Unmarshal([]byte(got), &m), ShouldBeNil)
			ep := m["enabledPlugins"].(map[string]any)
			So(ep["a@m"], ShouldEqual, true)
			So(ep["b@m"], ShouldEqual, false)
		})
		Convey("空 map → 原样返回 base", func() {
			So(buildSkillsSettings(map[string]bool{}, `{"x":1}`), ShouldEqual, `{"x":1}`)
			So(buildSkillsSettings(nil, ""), ShouldEqual, "")
		})
		Convey("base 已是 JSON 对象 → 合并保留既有键", func() {
			got := buildSkillsSettings(map[string]bool{"a@m": true}, `{"effortLevel":"high"}`)
			var m map[string]any
			So(json.Unmarshal([]byte(got), &m), ShouldBeNil)
			So(m["effortLevel"], ShouldEqual, "high")
			So(m["enabledPlugins"].(map[string]any)["a@m"], ShouldEqual, true)
		})
		Convey("非对象/坏 JSON base → 空起,只剩 enabledPlugins", func() {
			got := buildSkillsSettings(map[string]bool{"a@m": true}, "{bad json")
			var m map[string]any
			So(json.Unmarshal([]byte(got), &m), ShouldBeNil)
			So(m["enabledPlugins"].(map[string]any)["a@m"], ShouldEqual, true)
			So(len(m), ShouldEqual, 1) // 坏 base 内容被丢弃,不阻断
		})
	})
}

func TestBuildAutoMemorySettings(t *testing.T) {
	Convey("按开关把 autoMemoryEnabled:false 合进 settings", t, func() {
		Convey("未置位 → 原样返回 base,不写键", func() {
			So(buildAutoMemorySettings(false, `{"x":1}`), ShouldEqual, `{"x":1}`)
			So(buildAutoMemorySettings(false, ""), ShouldEqual, "")
		})
		Convey("置位 → 合并并保留既有键", func() {
			got := buildAutoMemorySettings(true, `{"effortLevel":"high"}`)
			var m map[string]any
			So(json.Unmarshal([]byte(got), &m), ShouldBeNil)
			So(m["autoMemoryEnabled"], ShouldEqual, false)
			So(m["effortLevel"], ShouldEqual, "high")
		})
		Convey("置位 + 空 base → 只有该键", func() {
			var m map[string]any
			So(json.Unmarshal([]byte(buildAutoMemorySettings(true, "")), &m), ShouldBeNil)
			So(m, ShouldResemble, map[string]any{"autoMemoryEnabled": false})
		})
	})
}
