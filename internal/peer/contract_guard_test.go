package peer

import (
	"testing"

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
func TestContract_DesktopRegistersWhatCallersSend(t *testing.T) {
	t.Parallel()

	registry := NewProtobufInboundRegistry(productionProtobufInboundDeps())
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
