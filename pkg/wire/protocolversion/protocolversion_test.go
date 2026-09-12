package protocolversion_test

import (
	"regexp"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/descriptorpb"

	"github.com/agentre-hub/agentre/pkg/wire/agentrewire"
	"github.com/agentre-hub/agentre/pkg/wire/protocolversion"
)

// versionShape 是握手比较认得的全部形态:三段十进制,没有 pre-release、没有构建元数据
// (见宿主 wireversion 的 parseVersion)。写不出这个形状的版本号在窗口比较里一律被当成
// 无法解析,握手当场拒绝 —— 所以「schema 上那格填得对不对」必须在这里挡住。
var versionShape = regexp.MustCompile(`^\d+\.\d+\.\d+$`)

// Given 协议版本号的主人是这份 schema 自己——它写在 wire.proto 的
// (agentre.wire.protocol_version) 文件选项上,
// When  读编译进来的 descriptor,
// Then  那一格必须真的存在,且是握手认得的三段形态。
//
// 为什么要这条守卫:文件选项缺失与「填了空串」在 proto3 里是同一个零值,消费方读出来
// 的是 "",而 "" 在窗口比较里永远不匹配 —— 每一次握手都会被拒,却没有任何编译期信号。
// 删掉那行 option、把它写成 "v0.3" 或者 "0.3",都在这里判红。
func TestSchema_GivenTheProtocolVersionLivesOnTheSchema_WhenTheDescriptorIsRead_ThenItCarriesAWellFormedVersion(t *testing.T) {
	t.Parallel()

	options, ok := agentrewire.File_agentre_wire_wire_proto.Options().(*descriptorpb.FileOptions)
	require.True(t, ok, "wire.proto 的 descriptor 必须带 FileOptions")
	require.True(t, proto.HasExtension(options, agentrewire.E_ProtocolVersion),
		"wire.proto 必须声明 option (agentre.wire.protocol_version) —— 版本号的主人就是这一格")

	declared, ok := proto.GetExtension(options, agentrewire.E_ProtocolVersion).(string)
	require.True(t, ok, "protocol_version 是 string 选项")
	require.Regexp(t, versionShape, declared,
		"schema 上的协议版本号必须是握手认得的 MAJOR.MINOR.PATCH")
}

// Given schema 上那一格就是版本号本身,
// When  Go 消费方调用 Protocol(),
// Then  它交回的必须逐字是那一格 —— 本包只负责把它读出来,不另存一份。
//
// 这条钉住的是「读」而不是「复述」:Protocol() 一旦退化成一个手写常量,它就能在
// 不改 schema 的情况下漂走,而两个仓库的 Go 都是从这里拿版本号的。
func TestProtocol_GivenTheSchemaDeclaresIt_WhenProtocolIsRead_ThenItIsThatDeclaredValueVerbatim(t *testing.T) {
	t.Parallel()

	options, ok := agentrewire.File_agentre_wire_wire_proto.Options().(*descriptorpb.FileOptions)
	require.True(t, ok, "wire.proto 的 descriptor 必须带 FileOptions")
	declared, _ := proto.GetExtension(options, agentrewire.E_ProtocolVersion).(string)

	require.Equal(t, declared, protocolversion.Protocol())
}
