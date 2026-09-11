package protorpc_test

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/agentre-hub/agentre/pkg/wire/protorpc"
)

// recordingLogger 是本包日志出口的测试替身。
//
// 本包在共享 module 里,不能依赖任何一个日志框架(宿主两边各用各的),诊断出口因此是
// protorpc.Logger —— 替身直接实现它,断言的是那一行到底有没有留下来。
type recordingLogger struct {
	mu       sync.Mutex
	messages []string
}

func (l *recordingLogger) record(msg string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.messages = append(l.messages, msg)
}

func (l *recordingLogger) Debug(_ context.Context, msg string, _ ...protorpc.Field) { l.record(msg) }
func (l *recordingLogger) Warn(_ context.Context, msg string, _ ...protorpc.Field)  { l.record(msg) }
func (l *recordingLogger) Error(_ context.Context, msg string, _ ...protorpc.Field) { l.record(msg) }

// count 数记下过多少条消息含 snippet。
func (l *recordingLogger) count(snippet string) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	var n int
	for _, msg := range l.messages {
		if strings.Contains(msg, snippet) {
			n++
		}
	}
	return n
}

// captureLogs 把替身装进包级出口,并在用例结束时恢复。日志出口是包级的(见
// log.go 的说明),所以装了它的用例不能与别的用例并行 —— 本包没有 t.Parallel。
func captureLogs(t *testing.T) *recordingLogger {
	t.Helper()
	recorder := &recordingLogger{}
	protorpc.SetLogger(recorder)
	t.Cleanup(func() { protorpc.SetLogger(nil) })
	return recorder
}
