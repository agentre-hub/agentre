// Package eventkind 交出 RuntimeEventNotification 每条 oneof 分支的转录判别值。
//
// 判别值写在 .proto 的 (agentre.wire.event_kind) 字段选项上,本包只负责从
// descriptor 把它读出来。存在的理由是那张对照表从前是**手抄的**:分支名与判别值
// 没有可推导的规则(tool_call → tool_use_start,user_ask_request →
// ask_user_question,usage_update → usage),于是每个消费方各抄一份,而抄错编译器
// 发现不了 —— 消费方的归约落进 default 分支,那一类卡片整块不渲染。
//
// 它和生成代码放在同一个 module 里、却不在 agentrewire/ 目录下:buf.gen.yaml 的
// clean: true 每次生成前会清空那个目录(guard 包同理)。
package eventkind

import (
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/descriptorpb"

	"github.com/agentre-hub/agentre/pkg/wire/agentrewire"
)

// Of 交出这一帧当前置上的分支的判别值与其载荷。
//
// ok 为 false 只有三种情况:帧是空的、oneof 一个分支也没置上、置上的分支在 schema
// 上没有判别值(只可能是新加分支忘了标注,由 eventkind_test.go 的守卫在 CI 挡住)。
// 消费方应当把它当作「这一帧投影不出来」的错误,而不是静默丢帧 —— 丢一帧在页面上
// 就是一段无声消失的转录。
func Of(frame *agentrewire.RuntimeEventNotification) (string, proto.Message, bool) {
	if frame == nil {
		return "", nil, false
	}
	message := frame.ProtoReflect()
	oneof := message.Descriptor().Oneofs().ByName("event")
	if oneof == nil {
		return "", nil, false
	}
	field := message.WhichOneof(oneof)
	if field == nil {
		return "", nil, false
	}
	kind := ForField(field)
	if kind == "" {
		return "", nil, false
	}
	return kind, message.Get(field).Message().Interface(), true
}

// ForField 读一个字段描述符上标注的判别值,没有标注时是空串。
func ForField(field protoreflect.FieldDescriptor) string {
	if field == nil {
		return ""
	}
	options, ok := field.Options().(*descriptorpb.FieldOptions)
	if !ok || options == nil {
		return ""
	}
	if !proto.HasExtension(options, agentrewire.E_EventKind) {
		return ""
	}
	kind, _ := proto.GetExtension(options, agentrewire.E_EventKind).(string)
	return kind
}

// All 按 schema 顺序交出 oneof 分支名 → 判别值。守卫与排障用;投影走 Of。
func All() map[string]string {
	fields := (&agentrewire.RuntimeEventNotification{}).ProtoReflect().
		Descriptor().Oneofs().ByName("event").Fields()
	out := make(map[string]string, fields.Len())
	for i := 0; i < fields.Len(); i++ {
		field := fields.Get(i)
		out[string(field.Name())] = ForField(field)
	}
	return out
}
