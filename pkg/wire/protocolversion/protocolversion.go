// Package protocolversion 交出 agentre ↔ agentred 协议自己声明的版本号。
//
// 版本号写在 .proto 的 (agentre.wire.protocol_version) 文件选项上,本包只负责从
// descriptor 把它读出来 —— 和 eventkind 读字段选项是同一套做法,理由也一样:这个值
// 从前住在一个消费方(前端 npm 包的 package.json)里,两个仓库的 Go 各自复述一份,
// 各自靠一条解析文件的守卫钉住自己看得见的那个来源。复述能漂,守卫只能事后判红。
//
// 现在它跟着 schema 住进协议 module:谁 import 这个 module 谁就直接拿到版本号。
// agentre-server 钉的那个不可变 revision 因此同时含着 schema 与它的版本号。
//
// **窗口的另一端不在这里。**「这个 build 还接受多老的对端」(MinSupported)是宿主的
// 策略,不是协议的属性:同一份协议,桌面端可以只认自己这一档,另一个消费方可以把地板
// 放低。它留在各宿主的 wireversion 包里,和 Match/Reject 一起。
//
// 它和生成代码放在同一个 module 里、却不在 agentrewire/ 目录下:buf.gen.yaml 的
// clean: true 每次生成前会清空那个目录(eventkind、guard 同理)。
package protocolversion

import (
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/descriptorpb"

	"github.com/agentre-hub/agentre/pkg/wire/agentrewire"
)

// protocol 在包初始化时读一次:schema 是编译进来的,读到的值在进程生命周期内不会变。
var protocol = fromSchema()

// Protocol 交出这份协议声明的版本号,形如 MAJOR.MINOR.PATCH。
//
// 宿主拿它填握手消息的 protocol_version,并用它作为自己版本窗口的上沿。
// schema 上那一格缺失或写错形状时它会交回一个握手认不出的值 —— 由
// protocolversion_test.go 的守卫在 CI 挡住,而不是留到运行期每次握手都被拒。
func Protocol() string { return protocol }

func fromSchema() string {
	options, ok := agentrewire.File_agentre_wire_wire_proto.Options().(*descriptorpb.FileOptions)
	if !ok || options == nil {
		return ""
	}
	if !proto.HasExtension(options, agentrewire.E_ProtocolVersion) {
		return ""
	}
	version, _ := proto.GetExtension(options, agentrewire.E_ProtocolVersion).(string)
	return version
}
