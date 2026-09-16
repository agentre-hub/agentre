package protorpc_test

import (
	"context"
	"log/slog"
	"strings"
	"sync"
	"testing"

	"github.com/agentre-hub/agentre/pkg/wire/protorpc"
)

// recordingHandler 是本包日志出口的测试替身。
//
// 本包在共享 module 里,诊断出口是标准库的 slog,替身实现 slog.Handler,断言的是
// 那一行到底有没有留下来。
type recordingHandler struct {
	mu       sync.Mutex
	messages []string
}

func (h *recordingHandler) Enabled(context.Context, slog.Level) bool { return true }

func (h *recordingHandler) Handle(_ context.Context, r slog.Record) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.messages = append(h.messages, r.Message)
	return nil
}

func (h *recordingHandler) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h *recordingHandler) WithGroup(string) slog.Handler      { return h }

// count 数记下过多少条消息含 snippet。
func (h *recordingHandler) count(snippet string) int {
	h.mu.Lock()
	defer h.mu.Unlock()
	var n int
	for _, msg := range h.messages {
		if strings.Contains(msg, snippet) {
			n++
		}
	}
	return n
}

// captureLogs 把替身装进包级出口,并在用例结束时恢复。日志出口是包级的(见
// log.go 的说明),所以装了它的用例不能与别的用例并行 —— 本包没有 t.Parallel。
func captureLogs(t *testing.T) *recordingHandler {
	t.Helper()
	recorder := &recordingHandler{}
	protorpc.SetLogger(slog.New(recorder))
	t.Cleanup(func() { protorpc.SetLogger(nil) })
	return recorder
}
