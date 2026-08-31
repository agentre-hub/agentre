package protorpc

import (
	"fmt"
	"runtime/debug"

	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"
)

// recoverHandler 是 RPC handler 的最后一道防线:把 panic 翻成 CodeInternal 交给对端,
// 让进程活下去。**它必须挂在派发层而不是每个 handler 上** —— handler 跑在
// `go c.handle(...)` 起的独立 goroutine 上,调用方 recover 不到,少挂一处就等于整条
// 防线不存在。
//
// 这不是假想的风险:claudecode runtime 的一次 nil deref 曾经把整个 agentred 打挂,
// 那台机器上所有会话卡在「生成中」而前端一点提示也没有。防线在 daemon 侧存在过
// (daemon.recoverHandlerPanic + wrapGuarded),后来注册路径换成 Protobuf registry
// 时 wrapGuarded 没了,守卫就只剩测试在引用 —— 所以它现在落在 registry 自己这一层,
// 两端 86 个方法一次覆盖,新增方法不会漏。
//
// panic 值进 message 是有意的:它几乎总是 "runtime error: ..." 这类定位信息,而
// 对端只能看到这一句。stack trace 只进本地日志。
func recoverHandler(what string, errOut *error) {
	if r := recover(); r != nil {
		logger.Default().Error("protorpc: rpc handler panic",
			zap.String("what", what), zap.Any("panic", r), zap.ByteString("stack", debug.Stack()))
		if errOut != nil {
			*errOut = &Error{Code: CodeInternal, Message: fmt.Sprintf("rpc handler panic: %v", r)}
		}
	}
}
