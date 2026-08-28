package cliprober

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/agentre-hub/agentre/pkg/claudecode"
	"github.com/agentre-hub/agentre/pkg/codex"
	"github.com/agentre-hub/agentre/pkg/piagent"
)

func TestProbe_UnknownType(t *testing.T) {
	_, err := Probe(context.Background(), ProbeRequest{Type: "nope"})
	require.ErrorIs(t, err, ErrInvalidType)
}

func TestWrapCLIProberError_NilPassthrough(t *testing.T) {
	assert.Nil(t, wrapCLIProberError(nil))
}

func TestWrapCLIProberError_PreservesSentinel(t *testing.T) {
	// 上层依赖 errors.Is(err, context.DeadlineExceeded) 出"测试超时"文案；
	// 包装层不能吞掉这个 sentinel。
	wrapped := wrapCLIProberError(context.DeadlineExceeded)
	assert.True(t, errors.Is(wrapped, context.DeadlineExceeded))

	wrapped = wrapCLIProberError(context.Canceled)
	assert.True(t, errors.Is(wrapped, context.Canceled))
}

func TestWrapCLIProberError_WrapsExit(t *testing.T) {
	original := &claudecode.ProcessExitError{Code: 1, Stderr: "boom"}
	wrapped := wrapCLIProberError(original)
	assert.NotNil(t, wrapped)
	assert.Contains(t, wrapped.Error(), "退出码 1")
	// Unwrap 还能拿到原始 typed error。
	var cc *claudecode.ProcessExitError
	assert.True(t, errors.As(wrapped, &cc))
	assert.Equal(t, 1, cc.Code)
}

func TestFormatCLIProberError(t *testing.T) {
	t.Run("claudecode ProcessExitError → 含 exit code + stderr", func(t *testing.T) {
		err := &claudecode.ProcessExitError{Code: 127, Stderr: "command not found: claude"}
		msg, ok := formatCLIProberError(err)
		require.True(t, ok)
		assert.Contains(t, msg, "claudecode 进程")
		assert.Contains(t, msg, "退出码 127")
		assert.Contains(t, msg, "command not found: claude")
	})

	t.Run("codex ExitError → 含 codex 进程退出 + stderr", func(t *testing.T) {
		err := &codex.ExitError{Err: errors.New("kill -9 received"), Stderr: "fatal: token revoked"}
		msg, ok := formatCLIProberError(err)
		require.True(t, ok)
		assert.Contains(t, msg, "codex 进程退出")
		assert.Contains(t, msg, "fatal: token revoked")
	})

	t.Run("piagent ExitError → 含 piagent 进程退出 + stderr", func(t *testing.T) {
		err := &piagent.ExitError{Err: errors.New("killed"), Stderr: "fatal: pi auth expired"}
		msg, ok := formatCLIProberError(err)
		require.True(t, ok)
		assert.Contains(t, msg, "piagent 进程退出")
		assert.Contains(t, msg, "fatal: pi auth expired")
	})

	t.Run("普通 error → 不识别为 CLI 错误，调用方应保留原 err", func(t *testing.T) {
		_, ok := formatCLIProberError(errors.New("401 unauthorized"))
		assert.False(t, ok)
	})

	t.Run("nil err 不 panic", func(t *testing.T) {
		_, ok := formatCLIProberError(nil)
		assert.False(t, ok)
	})
}

func TestTruncateStderr(t *testing.T) {
	long := strings.Repeat("x", 500)
	out := truncateStderr(long)
	assert.LessOrEqual(t, len(out), cliStderrSnippetLimit+len("…"))
	assert.True(t, strings.HasSuffix(out, "…"))
}

func TestProbe_PiAgentUsesEphemeralNativeSession(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell-script fake binary not portable to windows")
	}
	dir := t.TempDir()
	argsFile := filepath.Join(dir, "args.txt")
	fake := writeExecutable(t, dir, "pi", `
printf '%s\n' "$@" > "$AGENTRE_TEST_PI_ARGS"
while IFS= read -r line; do
  case "$line" in
    *'"type":"get_state"'*)
      printf '%s\n' '{"id":"session-state","type":"response","command":"get_state","success":true,"data":{"sessionId":"ephemeral-probe"}}'
      ;;
    *'"type":"prompt"'*)
      printf '%s\n' '{"type":"response","command":"prompt","success":true}'
      printf '%s\n' '{"type":"message_update","assistantMessageEvent":{"type":"text_delta","delta":"pong"}}'
      printf '%s\n' '{"type":"agent_end","messages":[],"willRetry":false}'
      printf '%s\n' '{"type":"agent_settled"}'
      ;;
    *'"type":"get_session_stats"'*)
      printf '%s\n' '{"type":"response","command":"get_session_stats","success":true,"data":{}}'
      ;;
  esac
done`)

	resp, err := Probe(context.Background(), ProbeRequest{
		Type:    "piagent",
		CLIPath: fake,
		Env:     map[string]string{"AGENTRE_TEST_PI_ARGS": argsFile},
	})

	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Equal(t, "pong", resp.Text)
	argsRaw, err := os.ReadFile(argsFile) //nolint:gosec // test-owned temp path
	require.NoError(t, err)
	args := strings.Split(strings.TrimSpace(string(argsRaw)), "\n")
	assert.Contains(t, args, "--no-session")
	assert.NotContains(t, args, "--session-dir")
}

func TestProbe_PiAgentForwardsExtensionPaths(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell-script fake binary not portable to windows")
	}
	// 绑定供应商的 piagent：prober 把物化后的 provider 扩展透传给 pi client
	// （--extension <path>，与 chat run 同一 --extension 注入通道）。claudecode /
	// codex 不消费 Extensions 字段，此处只验证 piagent 分支透传。
	dir := t.TempDir()
	argsFile := filepath.Join(dir, "args.txt")
	fake := writeExecutable(t, dir, "pi", `
printf '%s\n' "$@" > "$AGENTRE_TEST_PI_ARGS"
while IFS= read -r line; do
  case "$line" in
    *'"type":"get_state"'*)
      printf '%s\n' '{"id":"session-state","type":"response","command":"get_state","success":true,"data":{"sessionId":"ephemeral-probe"}}'
      ;;
    *'"type":"prompt"'*)
      printf '%s\n' '{"type":"response","command":"prompt","success":true}'
      printf '%s\n' '{"type":"message_update","assistantMessageEvent":{"type":"text_delta","delta":"pong"}}'
      printf '%s\n' '{"type":"agent_end","messages":[],"willRetry":false}'
      printf '%s\n' '{"type":"agent_settled"}'
      ;;
    *'"type":"get_session_stats"'*)
      printf '%s\n' '{"type":"response","command":"get_session_stats","success":true,"data":{}}'
      ;;
  esac
done`)

	exts := []string{"/ext/agentre-provider-aaa.mjs", "/ext/agentre-provider-bbb.mjs"}
	resp, err := Probe(context.Background(), ProbeRequest{
		Type:       "piagent",
		CLIPath:    fake,
		Env:        map[string]string{"AGENTRE_TEST_PI_ARGS": argsFile},
		Extensions: exts,
	})

	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Equal(t, "pong", resp.Text)
	argsRaw, err := os.ReadFile(argsFile) //nolint:gosec // test-owned temp path
	require.NoError(t, err)
	args := strings.Split(strings.TrimSpace(string(argsRaw)), "\n")
	extCount := 0
	for _, a := range args {
		if a == "--extension" {
			extCount++
		}
	}
	assert.Equal(t, 2, extCount, "每个 Extensions 路径都应以 --extension 透传给 pi client")
	for _, ext := range exts {
		assert.Contains(t, args, ext)
	}
}

func TestProbe_ClaudeCode_FakeCLI_ExitNonZero(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell-script fake binary not portable to windows")
	}
	// 不真测端到端 claudecode SDK（依赖网络），只覆盖入口分发 + 错误整形：
	// fake CLI exit 9 应走 wrapCLIProberError 路径返回非 nil 错误。
	dir := t.TempDir()
	fake := writeExecutable(t, dir, "claude", "exit 9")
	_, err := Probe(context.Background(), ProbeRequest{
		Type:    "claudecode",
		CLIPath: fake,
		Env:     map[string]string{"PATH": filepath.Dir(fake)},
	})
	require.Error(t, err)
}

func TestProbe_ClaudeCode_CustomProviderSettingsOverrideUserEnvironment(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell-script fake binary not portable to windows")
	}
	dir := t.TempDir()
	argsFile := filepath.Join(dir, "args.txt")
	settingsPathFile := filepath.Join(dir, "settings-path.txt")
	fake := writeExecutable(t, dir, "claude", `
printf '%s\n' "$@" > "$AGENTRE_TEST_ARGS_FILE"
settings=''
model=''
while [ "$#" -gt 0 ]; do
  case "$1" in
    --settings)
      settings="$2"
      shift 2
      ;;
    --model)
      model="$2"
      shift 2
      ;;
    *)
      shift
      ;;
  esac
done
[ -n "$settings" ] || { printf '%s\n' 'missing --settings' >&2; exit 17; }
[ -f "$settings" ] || { printf '%s\n' 'settings is not a file' >&2; exit 18; }
[ "$model" = 'glm-test-model' ] || { printf '%s\n' 'missing custom model' >&2; exit 19; }
grep -q 'http://gateway.test' "$settings" || { printf '%s\n' 'missing gateway URL override' >&2; exit 20; }
grep -q 'gateway-test-token' "$settings" || { printf '%s\n' 'missing gateway token override' >&2; exit 21; }
printf '%s\n' "$settings" > "$AGENTRE_TEST_SETTINGS_PATH_FILE"
IFS= read -r _line
printf '%s\n' '{"type":"system","subtype":"init","session_id":"probe-session","model":"glm-test-model"}'
printf '%s\n' '{"type":"assistant","session_id":"probe-session","message":{"content":[{"type":"text","text":"pong"}]}}'
printf '%s\n' '{"type":"result","subtype":"success","session_id":"probe-session","usage":{"input_tokens":1,"output_tokens":1}}'
`)

	resp, err := Probe(context.Background(), ProbeRequest{
		Type:    "claudecode",
		CLIPath: fake,
		Model:   "glm-test-model",
		Env: map[string]string{
			"AGENTRE_TEST_ARGS_FILE":          argsFile,
			"AGENTRE_TEST_SETTINGS_PATH_FILE": settingsPathFile,
			"ANTHROPIC_BASE_URL":              "http://gateway.test",
			"ANTHROPIC_AUTH_TOKEN":            "gateway-test-token",
		},
	})

	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Equal(t, "pong", resp.Text)
	args, readErr := os.ReadFile(argsFile) //nolint:gosec // test-owned temp path
	require.NoError(t, readErr)
	assert.NotContains(t, string(args), "gateway-test-token", "gateway credential must not appear in argv")
	settingsPathRaw, readErr := os.ReadFile(settingsPathFile) //nolint:gosec // test-owned temp path
	require.NoError(t, readErr)
	settingsPath := strings.TrimSpace(string(settingsPathRaw))
	_, statErr := os.Stat(settingsPath) //nolint:gosec // path is emitted by the test-owned fake CLI from a temporary settings file.
	assert.ErrorIs(t, statErr, os.ErrNotExist, "temporary settings file must be removed after the probe")
}
