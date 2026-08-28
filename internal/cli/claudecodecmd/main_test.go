package claudecodecmd

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func envStub(m map[string]string) func(string) (string, bool) {
	return func(k string) (string, bool) {
		v, ok := m[k]
		return v, ok
	}
}

func runPostToolHook(t *testing.T, sessionID string, messages []string, env map[string]string) (string, string) {
	t.Helper()
	gotAuth := ""
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		_ = json.NewEncoder(w).Encode(map[string][]string{"messages": messages})
	}))
	defer srv.Close()

	if _, ok := env["AGENTRE_GATEWAY_URL"]; ok {
		env["AGENTRE_GATEWAY_URL"] = srv.URL
	}
	if _, ok := env["ANTHROPIC_BASE_URL"]; ok {
		env["ANTHROPIC_BASE_URL"] = srv.URL
	}

	var out, errBuf bytes.Buffer
	code := run(
		[]string{"hook", "post-tool"},
		strings.NewReader(`{"session_id":"`+sessionID+`"}`),
		&out, &errBuf,
		envStub(env),
	)
	if code != 0 {
		t.Fatalf("exit=%d stderr=%s", code, errBuf.String())
	}
	return gotAuth, out.String()
}

func TestRun_NoArgs(t *testing.T) {
	var out, errBuf bytes.Buffer
	code := run(nil, strings.NewReader(""), &out, &errBuf, envStub(nil))
	if code != 2 {
		t.Fatalf("exit=%d", code)
	}
	if !strings.Contains(errBuf.String(), "missing subcommand") {
		t.Fatalf("stderr: %s", errBuf.String())
	}
}

func TestRun_UnknownSubcommand(t *testing.T) {
	var out, errBuf bytes.Buffer
	code := run([]string{"unknown"}, strings.NewReader(""), &out, &errBuf, envStub(nil))
	if code != 2 {
		t.Fatalf("exit=%d", code)
	}
}

func TestRun_HookWithoutEvent(t *testing.T) {
	var out, errBuf bytes.Buffer
	code := run([]string{"hook"}, strings.NewReader(""), &out, &errBuf, envStub(nil))
	if code != 2 {
		t.Fatalf("exit=%d", code)
	}
	if !strings.Contains(errBuf.String(), "missing event") {
		t.Fatalf("stderr: %s", errBuf.String())
	}
}

func TestRun_HookPostToolNoEnv_EmitsNoop(t *testing.T) {
	var out, errBuf bytes.Buffer
	code := run([]string{"hook", "post-tool"}, strings.NewReader(`{}`), &out, &errBuf, envStub(nil))
	if code != 0 {
		t.Fatalf("exit=%d stderr=%s", code, errBuf.String())
	}
	if !strings.Contains(out.String(), `"hookEventName":"PostToolUse"`) {
		t.Fatalf("no-op post-tool output missing: %s", out.String())
	}
}

func TestRun_HookUnknownEvent(t *testing.T) {
	var out, errBuf bytes.Buffer
	code := run([]string{"hook", "bogus"}, strings.NewReader(""), &out, &errBuf, envStub(map[string]string{
		"AGENTRE_GATEWAY_URL":   "http://x",
		"AGENTRE_GATEWAY_TOKEN": "tok",
	}))
	if code != 2 {
		t.Fatalf("exit=%d", code)
	}
}

// TestRun_HookPostTool_PrefersAgentreGatewayEnv 锁住 CLI 登录模式核心契约：
// agentre runtime 总是设 AGENTRE_GATEWAY_*；hook 必须用它而不是 ANTHROPIC_*，
// 否则 CLI 登录模式（ANTHROPIC_* 故意不设）下 hook 会 noop，mid-turn 排队
// 消息再也插不进 turn。
func TestRun_HookPostTool_PrefersAgentreGatewayEnv(t *testing.T) {
	gotAuth, out := runPostToolHook(t, "sid-1", []string{"hello from inbox"}, map[string]string{
		"AGENTRE_GATEWAY_URL":   "",
		"AGENTRE_GATEWAY_TOKEN": "agentre-tok",
		// 故意 *不* 设 ANTHROPIC_*；模拟 CLI 登录模式
	})
	if gotAuth != "Bearer agentre-tok" {
		t.Fatalf("hook should use AGENTRE_GATEWAY_TOKEN, got auth=%q", gotAuth)
	}
	if !strings.Contains(out, "hello from inbox") {
		t.Fatalf("hook should inject inbox messages, got: %s", out)
	}
}

func TestRun_HookPostTool_AnthropicEnvDoesNotActivateHook(t *testing.T) {
	gotAuth, out := runPostToolHook(t, "sid-legacy", []string{"legacy-msg"}, map[string]string{
		"ANTHROPIC_BASE_URL":   "http://legacy",
		"ANTHROPIC_AUTH_TOKEN": "legacy-tok",
	})
	if gotAuth != "" {
		t.Fatalf("hook should not use legacy ANTHROPIC auth, got %q", gotAuth)
	}
	if strings.Contains(out, "legacy-msg") {
		t.Fatalf("hook should noop with legacy env only, got: %s", out)
	}
}
