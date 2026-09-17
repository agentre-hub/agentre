package protorpc_test

import (
	"context"
	"strings"

	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
)

// recordingLogger 是本包日志出口的测试替身:本包直接写 cago 的全局 logger,替身把
// 那个全局 logger 换成 zap observer,断言的是那一行到底有没有留下来。
type recordingLogger struct {
	logs *observer.ObservedLogs
}

// count 数记下过多少条消息含 snippet。
func (l *recordingLogger) count(snippet string) int {
	var n int
	for _, entry := range l.logs.All() {
		if strings.Contains(entry.Message, snippet) {
			n++
		}
	}
	return n
}

// captureLogs 返回一个挂着 observer logger 的 ctx:本包按 cago 惯例用 logger.Ctx(ctx)
// 取 logger,所以替身走 ctx 注入,不碰进程级的全局 logger —— 那是一个没有同步的包级
// 变量,换它会与别的用例遗留的 goroutine 竞争。
func captureLogs() (*recordingLogger, context.Context) {
	core, logs := observer.New(zapcore.DebugLevel)
	ctx := logger.WithContextLogger(context.Background(), zap.New(core))
	return &recordingLogger{logs: logs}, ctx
}
