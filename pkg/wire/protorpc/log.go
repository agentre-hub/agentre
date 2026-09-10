package protorpc

import (
	"context"
	"sync/atomic"
)

// 本包是 agentre ↔ agentred 协议引擎,两个仓库的宿主都要 import 它,所以它不能
// 挑一个日志框架。宿主各自把自己的 logger 装进来(SetLogger),没装就丢弃 ——
// 一条诊断日志不该是 import 一整套框架的理由。
//
// 出口刻意只有三档(Debug / Warn / Error)且只有结构化字段:本层的日志一共四处,
// 全是排障用的事实陈述,没有需要格式化的地方。

// Field 是一条诊断日志上的一个结构化字段。
type Field struct {
	Key   string
	Value any
}

// String / Int / Uint64 / Any 是字段构造器。它们存在只是为了让调用点读起来仍然
// 是结构化日志的样子,不做任何转换。
func String(key, value string) Field        { return Field{Key: key, Value: value} }
func Int(key string, value int) Field       { return Field{Key: key, Value: value} }
func Uint64(key string, value uint64) Field { return Field{Key: key, Value: value} }
func Any(key string, value any) Field       { return Field{Key: key, Value: value} }

// ByteString 把字节当**文本**记,不是二进制 blob。本层唯一的用处是 panic 栈:
// 按二进制记会被大多数编码器 base64 掉,而那一串正是排障时唯一要读的东西。
func ByteString(key string, value []byte) Field { return Field{Key: key, Value: string(value)} }

// Err 的键固定是 "error",与常见结构化日志库的约定一致。
func Err(err error) Field { return Field{Key: "error", Value: err} }

// Logger 是本层唯一的诊断出口。ctx 一路带着,是因为宿主的 logger 往往从 ctx 上
// 取 trace / 请求标识;本层自己不读它。
type Logger interface {
	Debug(ctx context.Context, msg string, fields ...Field)
	Warn(ctx context.Context, msg string, fields ...Field)
	Error(ctx context.Context, msg string, fields ...Field)
}

type discardLogger struct{}

func (discardLogger) Debug(context.Context, string, ...Field) {}
func (discardLogger) Warn(context.Context, string, ...Field)  {}
func (discardLogger) Error(context.Context, string, ...Field) {}

type loggerHolder struct{ logger Logger }

var currentLogger atomic.Pointer[loggerHolder]

// SetLogger 装上宿主的 logger。宿主在启动时调用一次;传 nil 表示恢复成丢弃。
//
// 它是包级的而不是每条连接一个:本层的日志有两处(handler panic、无人认领的
// 应答)发生在拿不到连接的地方,而宿主进程本来就只有一套日志配置。
func SetLogger(l Logger) {
	if l == nil {
		l = discardLogger{}
	}
	currentLogger.Store(&loggerHolder{logger: l})
}

func log() Logger {
	holder := currentLogger.Load()
	if holder == nil {
		return discardLogger{}
	}
	return holder.logger
}
