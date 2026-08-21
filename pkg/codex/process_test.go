package codex

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExecAppServerRunner_EnvShebangCanFindInterpreterNextToBinary(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("env shebang lookup is a Unix behavior")
	}

	binDir := t.TempDir()
	interpreter := filepath.Join(binDir, "agentre-test-node")
	codexShim := filepath.Join(binDir, "codex")
	require.NoError(t, os.WriteFile(interpreter, []byte("#!/bin/sh\nprintf 'booted\\n'\n"), 0o755))
	require.NoError(t, os.WriteFile(codexShim, []byte("#!/usr/bin/env agentre-test-node\n"), 0o755))

	t.Setenv("PATH", "/usr/bin:/bin")
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	h, err := execAppServerRunner{}.Start(ctx, procOptions{Binary: codexShim})
	require.NoError(t, err)

	out, readErr := io.ReadAll(h.Stdout())
	require.NoError(t, readErr)
	assert.Equal(t, "booted\n", string(out))
	assert.NoError(t, h.Wait())
}
