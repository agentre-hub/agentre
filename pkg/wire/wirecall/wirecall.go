// Package wirecall 是 agentre wire 协议在**调用侧**的 typed 面:一个 RPC 方法一个
// 函数,method ID 与请求/响应消息类型的配对在整个工作区里只出现这一次。
//
// 从前这份配对散在两个仓库的十几个文件里 —— 桌面端的 13 个 service 包、Wails 绑定
// 层、两个 internal/pkg 包,以及 agentre-server 的 machineConn,每处各写一份
// `protorpc.CallMethod(ctx, conn, uint32(agentrewire.RpcMethod_RPC_METHOD_XXX), req,
// func() *agentrewire.XxxResponse { ... })`。同一对配了好几遍,而两边写得不一样时
// 编译器一句话都不会说。
//
// 它刻意只是**一层薄壳**:交回的仍然是 wire 消息,翻成领域类型是各自领域的事。这层
// 解决的是「配对被抄了二十遍」,不是「谁来做协议到领域的翻译」。
//
// 它住在 pkg/wire 而不是某个宿主的 internal 里,原因有二:配对是协议的一部分,不是
// 哪个宿主的;而且只有住在这里,agentre-server 与桌面端那些「刻意不依赖 daemon/client」
// 的包(internal/pkg/pty/remote)才用得上同一份。
package wirecall

import (
	"context"
	"fmt"
	"reflect"

	"google.golang.org/protobuf/proto"

	"github.com/agentre-hub/agentre/pkg/wire/agentrewire"
	"github.com/agentre-hub/agentre/pkg/wire/protorpc"
)

// Caller 是调用面需要的最小依赖:一条已经在跑的连接。
//
// 只要求「交得出一条连接」这一件事 —— 连接池的租约、客户端包装、以及测试里直接握着
// 的 *protorpc.Conn(经 On 包一层)都满足它。调用面不该因此要求调用方还得能 Close
// 或者报得出指纹。
type Caller interface{ Conn() *protorpc.Conn }

// On 把一条裸连接当作调用面的入口。生产代码手上普遍是租约或客户端,用不到它。
func On(conn *protorpc.Conn) Caller { return rawCaller{conn: conn} }

type rawCaller struct{ conn *protorpc.Conn }

func (c rawCaller) Conn() *protorpc.Conn { return c.conn }

// Pairing 是一个方法的配对,给守卫读。
type Pairing struct {
	RequestType  string
	ResponseType string
	NewResponse  func() proto.Message
}

var pairings = map[agentrewire.RpcMethod]Pairing{}

// Covered 交回已定义的全部配对。守卫据它断言「每个方法都有且只有一个 typed 调用
// 函数」—— 漏一个方法本身没什么可见后果,直到有人第二次手写它,而两处写得不一样。
func Covered() map[agentrewire.RpcMethod]Pairing {
	out := make(map[agentrewire.RpcMethod]Pairing, len(pairings))
	for method, pairing := range pairings {
		out[method] = pairing
	}
	return out
}

// Define 造出一个方法的 typed 调用函数,同时把配对登记下来。
//
// 方法号只在这一行出现一次,所以它跟请求/响应类型不可能各自漂走。重复登记直接 panic:
// 一行写错方法号会让两个函数指向同一个方法,而它们各自的用例还都是绿的 —— 两边发的
// 都是「一个合法的方法号」。这里当场炸掉,进程起不来。
func Define[Req proto.Message, Resp proto.Message](
	method agentrewire.RpcMethod, newResponse func() Resp,
) func(context.Context, Caller, Req) (Resp, error) {
	if _, exists := pairings[method]; exists {
		panic(fmt.Sprintf("wirecall: %v 已经有一个 typed 调用函数了", method))
	}
	pairings[method] = Pairing{
		RequestType:  reflect.TypeOf((*Req)(nil)).Elem().Elem().Name(),
		ResponseType: reflect.TypeOf((*Resp)(nil)).Elem().Elem().Name(),
		NewResponse:  func() proto.Message { return newResponse() },
	}
	return func(ctx context.Context, conn Caller, request Req) (Resp, error) {
		return protorpc.CallMethod(ctx, conn.Conn(), uint32(method), request, newResponse)
	}
}
