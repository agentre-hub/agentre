package protowire

import (
	"reflect"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"

	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/runtimes/remote/wire"
	"github.com/agentre-hub/agentre/pkg/wire/agentrewire"
)

// TestSessionSummaryPreservesEveryGoField 是这条边界的主守卫,方向是 Go → proto → Go。
//
// specimen **不是手抄的**:它由反射把 wire.SessionSummary 的每一格填成可区分的非零值。
// 手抄一张字段清单去比对,等于让守卫自己变成第 N 份手抄 —— 加字段时照样会漏,而漏掉
// 那一行不会有任何东西变红。反射填充的好处正是「加字段这件事本身」就把它纳入断言。
//
// 为什么这条边界需要守卫:proto3 里缺字段与零值不可分辨。漏映射一格,编码端发的是
// 零值、解码端读到的也是零值,双方都不报错,过线的事实就此静默消失 —— 标题变空、
// 最后活动时间变 0、思考力度回落成「跟随后端配置」,而全套测试仍是绿的。
func TestSessionSummaryPreservesEveryGoField(t *testing.T) {
	var want wire.SessionSummary
	fillEverySummaryGoField(t, reflect.ValueOf(&want).Elem())

	got := SessionSummaryFromProto(SessionSummaryToProto(want))

	require.Equal(t, want, got, "wire.SessionSummary 的每一格都必须过线")
}

// TestSessionSummaryPreservesEveryProtoField 是同一条边界的反方向 proto → Go → proto。
//
// 上一条只盯 Go 结构体:proto 里多出一格、而 Go 侧压根没有对应字段时它照样是绿的
// (往返回来两边都是零值)。线格式才是跨仓库、跨宿主的那份契约 —— 对端(agentred /
// 浏览器)发得出的每一格,解码这一侧都必须读到。
func TestSessionSummaryPreservesEveryProtoField(t *testing.T) {
	want := &agentrewire.SessionSummary{}
	fillEverySummaryProtoField(t, want)

	got := SessionSummaryToProto(SessionSummaryFromProto(want))

	require.Empty(t, unsetSummaryProtoFields(got), "这些线上字段没被解出来")
	require.True(t, proto.Equal(want, got), "want=%v got=%v", want, got)
}

// TestSessionSummaryFromProtoToleratesNil 钉住 nil 入参不 panic:解码侧拿到的是对端
// 发来的指针,清单里出现一格空指针不该把整条补齐链路打断。
func TestSessionSummaryFromProtoToleratesNil(t *testing.T) {
	require.Equal(t, wire.SessionSummary{}, SessionSummaryFromProto(nil))
}

// fillEverySummaryGoField 把结构体的每一格填成可区分的非零值。遇到不认识的字段类型
// 直接 Fatal 而不是跳过 —— 跳过等于让新加的那一格悄悄退出守卫范围,正是这条守卫要
// 防的事;真加了复杂字段,就来这里把填充规则一起补上。
func fillEverySummaryGoField(t *testing.T, value reflect.Value) {
	t.Helper()
	typ := value.Type()
	for i := 0; i < typ.NumField(); i++ {
		name := typ.Field(i).Name
		field := value.Field(i)
		switch field.Kind() {
		case reflect.String:
			field.SetString("value-" + name)
		case reflect.Int64:
			field.SetInt(int64(i) + 1)
		case reflect.Bool:
			field.SetBool(true)
		default:
			t.Fatalf("wire.SessionSummary.%s 是 %s,填充器还不认识这种字段;补上填充规则,别让它退出守卫", name, field.Kind())
		}
	}
}

// fillEverySummaryProtoField 同上,但走 protobuf descriptor:线格式的字段表才是跨仓库
// 那份契约的真相源,反射 Go 生成码会把它退化成「本仓库当下认识的字段」。
func fillEverySummaryProtoField(t *testing.T, message proto.Message) {
	t.Helper()
	reflected := message.ProtoReflect()
	fields := reflected.Descriptor().Fields()
	for i := 0; i < fields.Len(); i++ {
		field := fields.Get(i)
		if field.IsList() || field.IsMap() {
			t.Fatalf("SessionSummary.%s 是重复/映射字段,填充器还不认识;补上填充规则,别让它退出守卫", field.Name())
		}
		switch field.Kind() {
		case protoreflect.StringKind:
			reflected.Set(field, protoreflect.ValueOfString("value-"+string(field.Name())))
		case protoreflect.Int64Kind:
			reflected.Set(field, protoreflect.ValueOfInt64(int64(i)+1))
		case protoreflect.BoolKind:
			reflected.Set(field, protoreflect.ValueOfBool(true))
		default:
			t.Fatalf("SessionSummary.%s 是 %s,填充器还不认识这种字段;补上填充规则,别让它退出守卫", field.Name(), field.Kind())
		}
	}
}

// unsetSummaryProtoFields 交出仍是零值的那些线上字段名。proto.Equal 的失败信息是整条
// 消息的对比,漏了哪一格得自己数;这里直接点名,让守卫红的时候一眼看出该补哪一行。
func unsetSummaryProtoFields(message proto.Message) []string {
	reflected := message.ProtoReflect()
	fields := reflected.Descriptor().Fields()
	var missing []string
	for i := 0; i < fields.Len(); i++ {
		field := fields.Get(i)
		if !reflected.Has(field) {
			missing = append(missing, string(field.Name()))
		}
	}
	return missing
}
