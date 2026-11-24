package guard_test

import (
	"testing"

	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"

	"github.com/agentre-hub/agentre/pkg/wire/agentrewire"
)

// 「AI 当时所在目录」只作为现有 ToolCall / ToolResult 消息上的一个新字段过线
// (spec 2026-11-24-chat-right-toolwindows 决策 18):不新增事件类型、不抬协议版本。
// 字段号 7 是两条消息当前最大字段号之后的第一个空号。

// cwdField 取出消息上的 cwd 字段描述;缺失或形状不对当场判红。
func cwdField(t *testing.T, message proto.Message) protoreflect.FieldDescriptor {
	t.Helper()
	descriptor := message.ProtoReflect().Descriptor()
	field := descriptor.Fields().ByName("cwd")
	if field == nil {
		t.Fatalf("%s has no cwd field", descriptor.FullName())
	}
	if field.Number() != 7 || field.Kind() != protoreflect.StringKind || field.Cardinality() == protoreflect.Repeated {
		t.Fatalf("%s.cwd = #%d %s %s, want #7 singular string", descriptor.FullName(), field.Number(), field.Cardinality(), field.Kind())
	}
	return field
}

// Given 一条带 cwd 的工具调用 / 工具结果;When 编码再解码;Then cwd 原样到达,
// 其余字段不受影响。
func TestToolEventCwd_GivenAValue_WhenRoundTripped_ThenItArrivesVerbatim(t *testing.T) {
	t.Parallel()

	for _, message := range []proto.Message{
		&agentrewire.ToolCall{Id: "t1", Name: "Bash", ParentToolCallId: "p1"},
		&agentrewire.ToolResult{ToolCallId: "t1", Content: "ok", IsError: true},
	} {
		field := cwdField(t, message)
		message.ProtoReflect().Set(field, protoreflect.ValueOfString("/repo/.claude/worktrees/feat"))

		encoded, err := proto.Marshal(message)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		decoded := message.ProtoReflect().New().Interface()
		if err := proto.Unmarshal(encoded, decoded); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if !proto.Equal(message, decoded) {
			t.Fatalf("round trip lost data:\n sent %v\n got  %v", message, decoded)
		}
		if got := decoded.ProtoReflect().Get(field).String(); got != "/repo/.claude/worktrees/feat" {
			t.Fatalf("%T cwd = %q", decoded, got)
		}
	}
}

// Given 旧 agentred 编出的字节(它的 schema 里没有字段 7);When 用当前 schema 解码;
// Then cwd 为空(消费方据此回落到会话工作目录),旧字段全部照常读出。
//
// 字节在这里按旧 schema 逐字段手编,而不是用当前生成类型编一条「没填 cwd」的消息
// —— 后者在 cwd 被误写成非空默认值或字段号挪动时照样绿。
func TestToolEventCwd_GivenBytesFromAnOldPeer_WhenDecoded_ThenCwdIsEmpty(t *testing.T) {
	t.Parallel()

	var oldCall []byte
	oldCall = protowire.AppendTag(oldCall, 1, protowire.BytesType)
	oldCall = protowire.AppendString(oldCall, "t1")
	oldCall = protowire.AppendTag(oldCall, 2, protowire.BytesType)
	oldCall = protowire.AppendString(oldCall, "Bash")
	oldCall = protowire.AppendTag(oldCall, 6, protowire.BytesType)
	oldCall = protowire.AppendString(oldCall, "run-1")

	call := &agentrewire.ToolCall{}
	if err := proto.Unmarshal(oldCall, call); err != nil {
		t.Fatalf("unmarshal old ToolCall: %v", err)
	}
	if got := call.ProtoReflect().Get(cwdField(t, call)).String(); got != "" {
		t.Fatalf("old ToolCall decoded cwd = %q, want empty", got)
	}
	if call.GetId() != "t1" || call.GetName() != "Bash" || call.GetSubagentRunId() != "run-1" {
		t.Fatalf("old ToolCall fields lost: %v", call)
	}

	var oldResult []byte
	oldResult = protowire.AppendTag(oldResult, 1, protowire.BytesType)
	oldResult = protowire.AppendString(oldResult, "t1")
	oldResult = protowire.AppendTag(oldResult, 2, protowire.BytesType)
	oldResult = protowire.AppendString(oldResult, "ok")
	oldResult = protowire.AppendTag(oldResult, 3, protowire.VarintType)
	oldResult = protowire.AppendVarint(oldResult, 1)
	oldResult = protowire.AppendTag(oldResult, 6, protowire.BytesType)
	oldResult = protowire.AppendBytes(oldResult, []byte(`{"m":1}`))

	result := &agentrewire.ToolResult{}
	if err := proto.Unmarshal(oldResult, result); err != nil {
		t.Fatalf("unmarshal old ToolResult: %v", err)
	}
	if got := result.ProtoReflect().Get(cwdField(t, result)).String(); got != "" {
		t.Fatalf("old ToolResult decoded cwd = %q, want empty", got)
	}
	if result.GetToolCallId() != "t1" || result.GetContent() != "ok" || !result.GetIsError() || string(result.GetMeta()) != `{"m":1}` {
		t.Fatalf("old ToolResult fields lost: %v", result)
	}
}
