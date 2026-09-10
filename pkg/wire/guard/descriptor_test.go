// Package guard 存放 pkg/wire 这个 module 的守卫测试。
//
// 它不和生成代码同目录，是因为 buf.gen.yaml 的 clean: true 会在每次生成前清空
// agentrewire/ —— 任何放进那个目录的手写文件都会在下一次 `pnpm run proto:generate`
// 时被删掉。
package guard_test

import (
	"reflect"
	"strings"
	"testing"

	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"

	"github.com/agentre-hub/agentre/pkg/wire/agentrewire"
)

// TestDescriptorMatchesImportPath 守的是「有人手工编辑生成文件」这一类事故。
//
// wire.pb.go 的 rawDesc 是长度前缀的 protobuf 字节串，其中嵌着 go_package。对它做
// 朴素的字符串替换（例如一次仓库改名 sed）会改动字符串却不改前面的长度字节，descriptor
// 解析随即越界 panic —— 而这在编译期完全看不出来，`go build ./...` 照样是绿的。
// agentre-server 的那份手工拷贝就是这样在 6f49fa6 里被改坏的。
//
// 断言取 descriptor 自己记录的 go_package，与生成包在运行期的真实 import path 比对：
// 前者来自必须被成功解析的 rawDesc（因此覆盖了 panic 那一路），后者由 Go 工具链给出，
// 两者只有在生成文件确实是由当前 .proto 重新生成时才会相等。
func TestDescriptorMatchesImportPath(t *testing.T) {
	t.Parallel()

	if got, want := agentrewire.File_agentre_wire_wire_proto.Path(), "agentre/wire/wire.proto"; got != want {
		t.Fatalf("descriptor path = %q, want %q", got, want)
	}

	options := protodesc.ToFileDescriptorProto(agentrewire.File_agentre_wire_wire_proto).GetOptions()
	// go_package 的形式是 "<import path>;<package name>"，分号后的别名是可选的。
	declared, _, _ := strings.Cut(options.GetGoPackage(), ";")

	actual := reflect.TypeOf(agentrewire.RpcFrame{}).PkgPath()
	if declared != actual {
		t.Errorf("descriptor 记录的 go_package import path = %q，但生成包实际位于 %q；\n"+
			"生成文件与 .proto 的 go_package 不同步，或 rawDesc 被手工编辑过", declared, actual)
	}
}

// TestSessionListResponseHasNoLegacyCapabilityFlags verifies the pre-release
// protocol cleanup at the public descriptor boundary: given the current exact
// protocol version, when a client inspects SessionListResponse, then session
// metadata and model-target support are unconditional and no legacy feature
// negotiation fields remain.
func TestSessionListResponseHasNoLegacyCapabilityFlags(t *testing.T) {
	t.Parallel()

	message := agentrewire.File_agentre_wire_wire_proto.Messages().ByName("SessionListResponse")
	if message == nil {
		t.Fatal("SessionListResponse descriptor is missing")
	}
	for _, fieldName := range []string{"supports_session_metadata", "supports_session_model_target"} {
		if field := message.Fields().ByName(protoreflect.Name(fieldName)); field != nil {
			t.Errorf("legacy capability field %q is still exposed as field %d", fieldName, field.Number())
		}
	}
}

// TestRenamedFieldsCarryTheAlignedNames 守的是「跨进程的字段名」这一类:改名保号的
// 协议字段在二进制编码上看不出任何差别(号不变),编译期也抓不到——两端各自的生成
// 代码都会照自己那份 .proto 编译通过,只有 JSON 名与 TS 产物会悄悄分家。
//
// 三个字段来自 docs/specs/2026-08-27-schema-overhaul.md 决策 10/14/16:
// 机器指纹叫 device_fingerprint(它存的一直是指纹,不是数字主键)、同步来源叫
// sync_origin_fingerprint、会话「最后活动时刻」叫 last_message_at(不叫 updated_at
// ——那个名字会被 GORM 当成行更新时刻自动改写)。
func TestRenamedFieldsCarryTheAlignedNames(t *testing.T) {
	t.Parallel()

	for _, want := range []struct {
		message string
		number  int32
		name    string
	}{
		{"AgentBackend", 6, "device_fingerprint"},
		{"AgentBackend", 26, "sync_origin_fingerprint"},
		{"SessionSummary", 13, "last_message_at"},
	} {
		message := agentrewire.File_agentre_wire_wire_proto.Messages().ByName(protoreflect.Name(want.message))
		if message == nil {
			t.Fatalf("%s descriptor is missing", want.message)
		}
		field := message.Fields().ByNumber(protoreflect.FieldNumber(want.number))
		if field == nil {
			t.Errorf("%s field %d is missing", want.message, want.number)
			continue
		}
		if got := string(field.Name()); got != want.name {
			t.Errorf("%s field %d name = %q, want %q", want.message, want.number, got, want.name)
		}
	}
}
