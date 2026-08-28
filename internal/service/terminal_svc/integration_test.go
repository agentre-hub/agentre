//go:build !windows

package terminal_svc_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"sync"
	"testing"
	"time"

	"github.com/agentre-hub/agentre/internal/pkg/pty"
	"github.com/agentre-hub/agentre/internal/pkg/pty/local"
	"github.com/agentre-hub/agentre/internal/service/terminal_svc"

	"github.com/stretchr/testify/require"
)

// localBackendBridge mirrors the production app/terminal_wiring.go adapter.
type localBackendBridge struct{ be *local.Backend }

func (b localBackendBridge) Open(ctx context.Context, spec pty.Spec) (pty.Handle, error) {
	return b.be.Open(ctx, spec)
}

type collectingEmitter struct {
	mu        sync.Mutex
	buf       bytes.Buffer
	decodeErr error // first base64 decode failure; asserted on the test goroutine
}

func (c *collectingEmitter) Emit(_ context.Context, _ string, payload any) {
	c.mu.Lock()
	defer c.mu.Unlock()
	// Data events ship base64; decode each one (as the frontend's atob does)
	// before accumulating. Exit events carry a different payload type and are
	// skipped by the map type-assertion.
	if m, ok := payload.(map[string]string); ok {
		// Every data event is EncodeToString output, so a decode failure means
		// the encoding pipeline regressed. Record it rather than silently
		// dropping the chunk — Emit runs on the pump goroutine where t.Fatal is
		// unsafe, so the test goroutine asserts DecodeErr instead.
		dec, err := base64.StdEncoding.DecodeString(m["data"])
		if err != nil {
			if c.decodeErr == nil {
				c.decodeErr = err
			}
			return
		}
		c.buf.Write(dec)
	}
}

// DecodeErr returns the first base64 decode failure seen by Emit, if any.
func (c *collectingEmitter) DecodeErr() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.decodeErr
}

func (c *collectingEmitter) Bytes() []byte {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]byte(nil), c.buf.Bytes()...)
}

func TestIntegration_LocalPTY_HappyPath(t *testing.T) {
	sel := terminal_svc.NewBackendSelector(
		localBackendBridge{be: local.NewBackend()}, nil,
	)
	emit := &collectingEmitter{}
	svc := terminal_svc.NewService(sel, emit)
	t.Cleanup(svc.Shutdown)

	require.NoError(t, svc.Open(context.Background(), "integ-1", "", "/tmp", 80, 24))
	require.NoError(t, svc.Write(context.Background(), "integ-1", "echo integ-test\n"))

	deadline := time.After(5 * time.Second)
	for {
		select {
		case <-deadline:
			t.Fatalf("did not see echo output; got: %q", string(emit.Bytes()))
		case <-time.After(50 * time.Millisecond):
			require.NoError(t, emit.DecodeErr(), "every terminal data event must be valid base64")
			if bytes.Contains(emit.Bytes(), []byte("integ-test")) {
				return
			}
		}
	}
}
