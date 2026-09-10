package turn

import (
	"context"
	"strings"
	"sync"
)

// WaitTracker owns unresolved control requests for one turn. The session
// enters waiting on the first unique request and resumes only after the last.
type WaitTracker struct {
	mu      sync.Mutex
	pending map[string]struct{}
}

func NewWaitTracker() *WaitTracker {
	return &WaitTracker{pending: map[string]struct{}{}}
}

func (w *WaitTracker) Begin(kind, id string) bool {
	if w == nil || strings.TrimSpace(id) == "" {
		return false
	}
	key := kind + ":" + id
	w.mu.Lock()
	defer w.mu.Unlock()
	if _, exists := w.pending[key]; exists {
		return false
	}
	first := len(w.pending) == 0
	w.pending[key] = struct{}{}
	return first
}

func (w *WaitTracker) Resolve(kind, id string) bool {
	if w == nil || strings.TrimSpace(id) == "" {
		return false
	}
	key := kind + ":" + id
	w.mu.Lock()
	defer w.mu.Unlock()
	if _, exists := w.pending[key]; !exists {
		return false
	}
	delete(w.pending, key)
	return len(w.pending) == 0
}

func (w *WaitTracker) Clear() {
	if w == nil {
		return
	}
	w.mu.Lock()
	clear(w.pending)
	w.mu.Unlock()
}

func (tc *TurnContext) BeginWait(ctx context.Context, kind, id string) {
	if tc == nil || tc.SessionTransitioner == nil || tc.Session == nil {
		return
	}
	if tc.Waits == nil || tc.Waits.Begin(kind, id) {
		tc.SessionTransitioner.MarkWaiting(ctx, tc.Session, tc.Stream)
	}
}

func (tc *TurnContext) ResolveWait(ctx context.Context, kind, id string) {
	if tc == nil || tc.SessionTransitioner == nil || tc.Session == nil {
		return
	}
	if tc.Waits == nil || tc.Waits.Resolve(kind, id) {
		tc.SessionTransitioner.MarkRunning(ctx, tc.Session, tc.Stream)
	}
}

func (tc *TurnContext) ClearWaits() {
	if tc != nil && tc.Waits != nil {
		tc.Waits.Clear()
	}
}
