package guard

import (
	"reflect"
	"testing"

	"github.com/agentre-hub/agentre/internal/service/chat_svc"
	"github.com/agentre-hub/agentre/internal/service/project_location_svc"
	"github.com/agentre-hub/agentre/pkg/wire/devicefp"
)

// 这个守卫复现的是 dev 上的 be367651「远端 Agent 加不进项目:设备指纹与配对数字
// id 被当成同一个键比」。
//
// 两个键同源看起来像:
//   - Agent 那侧的设备键是**承载者指纹**(chat_svc 把 AgentBackend.DeviceFingerprint
//     经 remote_device_svc.ExternalDeviceID 交出来);
//   - 路径那侧的 ProjectLocationView.DeviceID 是 paired_agentreds 的**数字 id**
//     字符串,而且只是个会被清空的本地缓存。
//
// 从前两边都是 string,于是 `agent.DeviceID == loc.DeviceID` 编译得过、跑起来恒
// 不相等 —— 远端 Agent 永远被判成「那台机器还没配路径」,加不进项目。
//
// 现在承载者指纹是 devicefp.Carrier,数字 id 缓存仍是裸 string。按 Go 的赋值规则
// 两个定义类型之间不可赋值,那句比较因此编译不过;要跨过去必须写一次显式转换,
// 而那正是应该被看见、被质疑的地方。reflect 的 AssignableTo 实现的就是编译器那
// 条规则,所以下面这几条断言等价于「写错了编译不过」。
func TestAgentDeviceKeyCannotBeComparedWithPairedRowID(t *testing.T) {
	t.Parallel()

	carrier := reflect.TypeOf(devicefp.Carrier(""))
	rawString := reflect.TypeOf("")

	fieldType := func(t *testing.T, v any, name string) reflect.Type {
		t.Helper()
		f, ok := reflect.TypeOf(v).FieldByName(name)
		if !ok {
			t.Fatalf("%T 没有字段 %s", v, name)
		}
		return f.Type
	}

	t.Run("Agent 与会话交出去的设备键是承载者指纹", func(t *testing.T) {
		for _, tc := range []struct {
			value any
			field string
		}{
			{chat_svc.ChatAgentItem{}, "DeviceID"},
			{chat_svc.ChatSessionDetail{}, "DeviceID"},
		} {
			if got := fieldType(t, tc.value, tc.field); got != carrier {
				t.Errorf("%T.%s 的类型是 %s,应当是 devicefp.Carrier —— "+
					"它装的是机器指纹,裸 string 会让它和任何别的 string 键比得起来",
					tc.value, tc.field, got)
			}
		}
	})

	t.Run("路径那侧的 DeviceID 是配对表数字 id 的缓存,不是指纹", func(t *testing.T) {
		if got := fieldType(t, project_location_svc.ProjectLocationView{}, "DeviceID"); got != rawString {
			t.Errorf("ProjectLocationView.DeviceID 的类型是 %s,应当仍是裸 string —— "+
				"它是 paired_agentreds.id 的字符串缓存,不属于指纹的值空间", got)
		}
		if got := fieldType(t, project_location_svc.ProjectLocationView{}, "DeviceFingerprint"); got != carrier {
			t.Errorf("ProjectLocationView.DeviceFingerprint 的类型是 %s,应当是 devicefp.Carrier", got)
		}
	})

	t.Run("两个键彼此不可赋值:那句比较编译不过", func(t *testing.T) {
		agentKey := fieldType(t, chat_svc.ChatAgentItem{}, "DeviceID")
		pairedKey := fieldType(t, project_location_svc.ProjectLocationView{}, "DeviceID")

		if agentKey.AssignableTo(pairedKey) || pairedKey.AssignableTo(agentKey) {
			t.Fatalf("ChatAgentItem.DeviceID(%s) 与 ProjectLocationView.DeviceID(%s) 之间可以直接赋值;"+
				"be367651 那句 `agent.DeviceID == loc.DeviceID` 会重新编译得过", agentKey, pairedKey)
		}
	})

	t.Run("要对上必须拿指纹对指纹", func(t *testing.T) {
		agentKey := fieldType(t, chat_svc.ChatAgentItem{}, "DeviceID")
		locFingerprint := fieldType(t, project_location_svc.ProjectLocationView{}, "DeviceFingerprint")

		if agentKey != locFingerprint {
			t.Fatalf("Agent 的设备键是 %s,路径行的指纹是 %s —— 两者必须是同一个类型,"+
				"否则「这台机器配没配过路径」这个问题在类型上就问不出来", agentKey, locFingerprint)
		}
	})
}
