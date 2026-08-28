package terminal_svc_test

import (
	"context"
	"testing"

	"github.com/agentre-hub/agentre/internal/service/terminal_svc"

	"github.com/stretchr/testify/assert"
)

func TestStreamName_DataAndExit(t *testing.T) {
	assert.Equal(t, "terminal:t1:data", terminal_svc.DataEventName("t1"))
	assert.Equal(t, "terminal:t1:exit", terminal_svc.ExitEventName("t1"))
}

func TestNoopEmitter_DoesNotPanic(t *testing.T) {
	terminal_svc.NoopEmitter{}.Emit(context.Background(), "x", nil)
}

func TestEmitterFunc_DispatchesAndNilSafe(t *testing.T) {
	called := 0
	var f terminal_svc.EmitterFunc = func(_ context.Context, _ string, _ any) { called++ }
	f.Emit(context.Background(), "x", nil)
	assert.Equal(t, 1, called)
	var nilF terminal_svc.EmitterFunc
	nilF.Emit(context.Background(), "x", nil) // must not panic
}
