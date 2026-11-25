package protorpc

import (
	"context"
	"sync/atomic"

	"go.uber.org/zap"
)

// 日志出口。protorpc 是两端（桌面、agentred，以及 agentre-server 与 agrctl 这类消费方）
// 共用的协议引擎，不绑任何宿主的日志框架：宿主在启动时用 SetLogger 交出它自己的
// 「按 ctx 取 logger」（桌面与 agentred 交的是 cago 的 logger.Ctx，于是日志的去向、级别
// 与 ctx 上的关联字段与此前一致）。ctx 上带着 WithLogger 放进去的 logger 时优先用它。
// 都没有时落到 zap 的全局 logger —— 默认是 no-op，瘦客户端因此什么也不写。

type loggerKey struct{}

var hostLogger atomic.Pointer[func(context.Context) *zap.Logger]

// SetLogger 设定宿主的 logger 来源；nil 撤掉。并发安全，通常在进程启动时调一次。
func SetLogger(f func(context.Context) *zap.Logger) {
	if f == nil {
		hostLogger.Store(nil)
		return
	}
	hostLogger.Store(&f)
}

// WithLogger 让 ctx 上派生出的本包日志都写到 l，优先于 SetLogger。
func WithLogger(ctx context.Context, l *zap.Logger) context.Context {
	return context.WithValue(ctx, loggerKey{}, l)
}

func loggerFor(ctx context.Context) *zap.Logger {
	if l, ok := ctx.Value(loggerKey{}).(*zap.Logger); ok && l != nil {
		return l
	}
	if f := hostLogger.Load(); f != nil {
		if l := (*f)(ctx); l != nil {
			return l
		}
	}
	return zap.L()
}
