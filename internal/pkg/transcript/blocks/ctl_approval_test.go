package blocks

import (
	"encoding/json"
	"testing"

	. "github.com/smartystreets/goconvey/convey"
)

func TestCtlApprovalInput_ToolInput(t *testing.T) {
	Convey("ctl 审批卡的 ToolInput 是共享卡片消费的 JSON 形状", t, func() {
		before, after := "eng", "qa"
		in := CtlApprovalInput{
			Command: "agrctl update agent reviewer --department qa --api-key=…",
			Changes: []CtlApprovalChange{{
				Op: "update", Kind: "agent", ID: 12, Name: "reviewer",
				Fields: []CtlApprovalField{
					{Field: "departmentId", Before: &before, After: &after},
					{Field: "apiKey", Secret: true},
				},
			}, {
				Op: "delete", Kind: "department", ID: 7, Name: "tmp",
				Cascade: &CtlApprovalCascade{Departments: 2, Agents: 3},
			}},
		}

		Convey("when 转成 ToolInput, then 字段名是 camelCase，缺席的前后值不出现", func() {
			got := in.ToolInput()
			raw, err := json.Marshal(got)
			So(err, ShouldBeNil)
			So(string(raw), ShouldEqual, `{"changes":[`+
				`{"fields":[{"after":"qa","before":"eng","field":"departmentId"},{"field":"apiKey","secret":true}],"id":12,"kind":"agent","name":"reviewer","op":"update"},`+
				`{"cascade":{"agents":3,"departments":2},"id":7,"kind":"department","name":"tmp","op":"delete"}],`+
				`"command":"agrctl update agent reviewer --department qa --api-key=…"}`)
		})

		Convey("when 存进 tool_approval 块再解码, then 能还原成同一个 CtlApprovalInput", func() {
			back, err := ParseCtlApprovalInput(in.ToolInput())
			So(err, ShouldBeNil)
			So(back, ShouldResemble, in)
		})
	})
}
