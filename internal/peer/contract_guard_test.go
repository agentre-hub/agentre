package peer

import (
	"testing"

	"github.com/agentre-hub/agentre/internal/daemon/relaytransport"
	"github.com/agentre-hub/agentre/internal/pkg/wireinbound"
)

// 桌面端这一侧的注册面必须覆盖它作为**被调方**的义务。
//
// 这条断言此前不存在,代价是一串编译绿、测试绿、界面坏的缺陷:控制台对桌面端会话的
// 「停止」按下去毫无反应(RUNTIME_ABORT 没注册,-32601 被前端 catch{} 吞掉),引擎探测
// 四个方法对 kind=desktop 的机器全线打不通,导入历史会话同理。它们的共同形态是
// **调用方能打到这一侧,而这一侧不认识那个方法** —— 只有把「谁必须答得出什么」写成
// 数据(wireinbound.Contract),这件事才有地方红。
//
// 用生产装配而不是测试专用的 deps:测试里自己拼一份端口集会把「桌面端到底挂了什么」
// 这个问题偷换成「我这条用例挂了什么」,而后者永远是对的。
//
// 注册面因此从 **NewInbound** 上取,而不是在这里自己调一次
// NewProtobufInboundRegistry(productionProtobufInboundDeps(newDevicePortForward())):
// 后者虽然用的也是生产的那两个构造函数,却把「NewInbound 到底递了什么进去」这一步
// 替这条用例做掉了。把 inbound.go 里那次递参换成 nil,声明族四个方法会一个不剩地
// 掉出注册面(registerProtobufPortForward 把 nil 端口当作「本宿主没有这个能力」,
// 静默什么都不挂),而这条守卫照样绿 —— 那正是它存在的理由被绕开的样子。
//
// HubLink 只是构造出来当参数,Run 从没被调过:NewInbound 不做任何 I/O。
func TestContract_DesktopRegistersWhatCallersSend(t *testing.T) {
	t.Parallel()

	link := relaytransport.NewHubLink(relaytransport.HubLinkOptions{
		ServerURL: "http://127.0.0.1:1", AccessToken: "contract-guard",
	})
	registry := NewInbound(link).protobufRegistry
	result := wireinbound.CheckContract(wireinbound.HostDesktop, registry.RegisteredMethods())

	for _, missing := range result.Missing {
		t.Errorf("桌面端没注册 %s,而 %v 会发它(判据:%s)。\n"+
			"要么在桌面端实现它,要么收窄调用侧的目标筛选;若确认暂时不修,"+
			"去 wireinbound.KnownGaps() 里记一行并写清欠着什么。",
			missing.Method, missing.Callers, missing.Evidence)
	}
	for _, stale := range result.Stale {
		t.Errorf("KnownGaps 里 %s / %s 这一行该删了:这一侧已经实现了它,或者它本就不是这一侧的义务。\n"+
			"留着一条不成立的豁免,下一次它会掩护一个真缺口。原记录理由:%s",
			stale.Host, stale.Method, stale.Reason)
	}
}
