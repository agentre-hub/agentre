package protorpc

import (
	"log/slog"
	"sync/atomic"
)

// 本包是 agentre ↔ agentred 协议引擎,两个仓库的宿主都要 import 它,所以它不能
// 挑一个日志框架。出口用标准库的 *slog.Logger —— 两个宿主照样零耦合,标准库不是
// 框架。宿主各自把自己的 logger 装进来(SetLogger),没装就丢弃。
//
// 出口刻意只有三档(Debug / Warn / Error):本层的日志一共四处,全是排障用的事实
// 陈述,没有需要格式化的地方。

var currentLogger atomic.Pointer[slog.Logger]

// SetLogger 装上宿主的 logger。宿主在启动时调用一次;传 nil 表示恢复成丢弃。
//
// 它是包级的而不是每条连接一个:本层的日志有两处(handler panic、无人认领的
// 应答)发生在拿不到连接的地方,而宿主进程本来就只有一套日志配置。
func SetLogger(l *slog.Logger) {
	if l == nil {
		l = slog.New(slog.DiscardHandler)
	}
	currentLogger.Store(l)
}

func log() *slog.Logger {
	if l := currentLogger.Load(); l != nil {
		return l
	}
	return slog.New(slog.DiscardHandler)
}
