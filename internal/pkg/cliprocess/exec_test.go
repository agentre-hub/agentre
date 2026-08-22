package cliprocess

import (
	"bufio"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStart_GivenEnvShebangInterpreterNextToBinary_WhenStartingProcess_ThenItBoots(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("env shebang lookup is a Unix behavior")
	}

	binDir := t.TempDir()
	interpreter := filepath.Join(binDir, "agentre-test-node")
	shim := filepath.Join(binDir, "agentre-test-cli")
	require.NoError(t, os.WriteFile(interpreter, []byte("#!/bin/sh\nprintf 'booted:%s\\n' \"$AGENTRE_CLIPROCESS_TEST\"\n"), 0o755))
	require.NoError(t, os.WriteFile(shim, []byte("#!/usr/bin/env agentre-test-node\n"), 0o755))

	t.Setenv("PATH", "/usr/bin:/bin")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	h, err := Start(ctx, Options{
		Binary: shim,
		Env:    []string{"AGENTRE_CLIPROCESS_TEST=ok"},
	}, errors.New("missing"))
	require.NoError(t, err)

	out, readErr := io.ReadAll(h.Stdout())
	require.NoError(t, readErr)
	assert.Equal(t, "booted:ok\n", string(out))
	assert.NoError(t, h.Wait())
}

func TestStart_GivenMissingBinary_WhenStartingProcess_ThenReturnsCallerSentinel(t *testing.T) {
	errMissing := errors.New("custom missing sentinel")
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	h, err := Start(ctx, Options{Binary: "agentre-definitely-missing-binary"}, errMissing)

	require.Nil(t, h)
	require.ErrorIs(t, err, errMissing)
}

// 契约:Start 收下的 ctx 只守 spawn 阶段 —— 进程一旦起来,它的寿命由调用方显式结束
// (Kill / 关 stdin),不随这个 ctx 取消而终止。
//
// 这是跨轮常驻子进程的前提:codex 的 app-server 在**首轮**的 turnCtx 里 OpenSession,
// 之后被 CLISessionPool 留给后续轮复用;而 chat_svc 每轮结束都 cancel turnCtx。把进程
// 寿命绑在那个 ctx 上,池里留下的就是一个已被 SIGKILL 的死进程,下一轮必然开不起来。
// pkg/claudecode(context.Background() 开 session)与 pkg/piagent(自带 exec.Command
// runner)都各自绕开过这件事,底座这里是最后一处。
func TestStart_GivenStartContextCanceledAfterSpawn_WhenCallerWritesToProcess_ThenProcessStaysAlive(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell echo fixture is a Unix behavior")
	}

	binDir := t.TempDir()
	echoer := filepath.Join(binDir, "agentre-test-echoer")
	require.NoError(t, os.WriteFile(echoer, []byte("#!/bin/sh\nwhile IFS= read -r line; do printf '%s\\n' \"$line\"; done\n"), 0o755))

	ctx, cancel := context.WithCancel(context.Background())
	h, err := Start(ctx, Options{Binary: echoer}, errors.New("missing"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = h.Kill() })

	cancel()

	_, writeErr := io.WriteString(h.Stdin(), "ping\n")
	require.NoError(t, writeErr, "cancel 之后进程应当还能收到 stdin")

	line := make(chan string, 1)
	go func() {
		reader := bufio.NewReader(h.Stdout())
		text, readErr := reader.ReadString('\n')
		if readErr != nil {
			close(line)
			return
		}
		line <- strings.TrimSpace(text)
	}()
	select {
	case got, ok := <-line:
		require.True(t, ok, "cancel 之后 stdout 读到 EOF,说明进程已被 ctx 杀掉")
		assert.Equal(t, "ping", got)
	case <-time.After(5 * time.Second):
		t.Fatal("cancel 之后进程没有回显,疑似已被 ctx 杀掉")
	}

	require.NoError(t, h.Kill())
	assert.Error(t, h.Wait(), "显式 Kill 之后 Wait 应当报告被信号终止")
}

// 边界:ctx 在 Start 之前就已经取消 —— spawn 阶段仍归它管,一个进程都不该起来。
func TestStart_GivenContextCanceledBeforeSpawn_WhenStarting_ThenReturnsContextError(t *testing.T) {
	binDir := t.TempDir()
	echoer := filepath.Join(binDir, "agentre-test-echoer")
	require.NoError(t, os.WriteFile(echoer, []byte("#!/bin/sh\ncat\n"), 0o755))

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	h, err := Start(ctx, Options{Binary: echoer}, errors.New("missing"))

	require.Nil(t, h)
	assert.ErrorIs(t, err, context.Canceled)
}
