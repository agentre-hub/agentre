package protorpc_test

import (
	"context"
	"strings"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"

	"github.com/agentre-hub/agentre/pkg/wire/protorpc"
)

// recordingLogger 是本包日志出口的测试替身:替身把本包取到的 logger 换成 zap observer,
// 断言的是那一行到底有没有留下来。
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

// captureLogs 返回一个挂着 observer logger 的 ctx:本包按 ctx 取 logger(protorpc.WithLogger
// 优先于宿主的 SetLogger),所以替身走 ctx 注入,不碰进程级的宿主钩子 —— 换它会与别的
// 用例遗留的 goroutine 竞争。
func captureLogs() (*recordingLogger, context.Context) {
	core, logs := observer.New(zapcore.DebugLevel)
	ctx := protorpc.WithLogger(context.Background(), zap.New(core))
	return &recordingLogger{logs: logs}, ctx
}
