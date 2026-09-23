package ctlcmd

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeControl 起一个假的 /ctl/v1/send 控制服务，校验 bearer 并回 canned JSON。
func fakeControl(t *testing.T, token string) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	auth := func(w http.ResponseWriter, r *http.Request) bool {
		if r.Header.Get("Authorization") != "Bearer "+token {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = io.WriteString(w, `{"error":"invalid control token"}`)
			return false
		}
		return true
	}
	mux.HandleFunc("/ctl/v1/send", func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		var body struct {
			Agent string `json:"agent"`
			Text  string `json:"text"`
			Wait  bool   `json:"wait"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body.Wait {
			_, _ = io.WriteString(w, `{"sessionId":100,"assistantMessageId":200,"text":"the answer","done":true}`)
			return
		}
		_, _ = io.WriteString(w, `{"sessionId":100,"assistantMessageId":200,"done":false}`)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

// envFor 返回一个把控制端点指向 srv 的 lookupEnv。
func envFor(srv *httptest.Server, token string) func(string) (string, bool) {
	m := map[string]string{
		"AGENTRE_CTL_ENDPOINT": srv.URL,
		"AGENTRE_CTL_TOKEN":    token,
	}
	return func(k string) (string, bool) { v, ok := m[k]; return v, ok }
}

func runCLI(args []string, env func(string) (string, bool)) (int, string, string) {
	r := runWith(args, env, term{})
	return r.code, r.stdout, r.stderr
}

func TestRun_NoArgs(t *testing.T) {
	code, _, _ := runCLI(nil, func(string) (string, bool) { return "", false })
	if code != 2 {
		t.Fatalf("no args: code = %d, want 2", code)
	}
}

func TestRun_GivenHelpWhenPrintedThenUsesAgrctlCommand(t *testing.T) {
	code, out, errs := runCLI([]string{"help"}, func(string) (string, bool) { return "", false })
	if code != 0 {
		t.Fatalf("code = %d, want 0 (stderr=%s)", code, errs)
	}
	for _, verb := range []string{"agrctl list", "agrctl get", "agrctl create", "agrctl update", "agrctl delete", "agrctl send"} {
		if !strings.Contains(out, verb) {
			t.Fatalf("stdout = %q, want top-level verb %q", out, verb)
		}
	}
	if strings.Contains(out, "agrctl ctl") || strings.Contains(out, "agentre ctl") {
		t.Fatalf("stdout = %q, must not advertise the removed ctl layer", out)
	}
}

func TestRun_UnknownSubcommand(t *testing.T) {
	code, _, errs := runCLI([]string{"bogus"}, func(string) (string, bool) { return "", false })
	if code != 2 {
		t.Fatalf("code = %d, want 2", code)
	}
	if !strings.HasPrefix(errs, "Error:") || !strings.Contains(errs, "unknown command") {
		t.Fatalf("stderr = %q, want Error: unknown command", errs)
	}
}

func TestRun_GivenRemovedCtlLayerWhenInvokedThenUsageError(t *testing.T) {
	code, _, errs := runCLI([]string{"ctl", "agents"}, func(string) (string, bool) { return "", false })
	if code != 2 {
		t.Fatalf("code = %d, want 2", code)
	}
	if !strings.Contains(errs, "unknown") {
		t.Fatalf("stderr = %q, want unknown command", errs)
	}
}

func TestRun_SendNoWait(t *testing.T) {
	srv := fakeControl(t, "tok")
	code, out, errs := runCLI([]string{"send", "--agent", "planner", "ship", "it"}, envFor(srv, "tok"))
	if code != 0 {
		t.Fatalf("code = %d, want 0 (stderr=%s)", code, errs)
	}
	// 即发即返：sessionId 落 stdout 供脚本消费。
	if !strings.Contains(out, "100") {
		t.Fatalf("stdout = %q, want sessionId 100", out)
	}
}

func TestRun_SendWaitPrintsText(t *testing.T) {
	srv := fakeControl(t, "tok")
	code, out, errs := runCLI([]string{"send", "--agent", "planner", "--wait", "do it"}, envFor(srv, "tok"))
	if code != 0 {
		t.Fatalf("code = %d, want 0 (stderr=%s)", code, errs)
	}
	if strings.TrimSpace(out) != "the answer" {
		t.Fatalf("stdout = %q, want final text", out)
	}
}

func TestRun_SendMissingText(t *testing.T) {
	srv := fakeControl(t, "tok")
	code, _, _ := runCLI([]string{"send", "--agent", "planner"}, envFor(srv, "tok"))
	if code != 2 {
		t.Fatalf("missing text: code = %d, want 2", code)
	}
}

func TestRun_SendMissingAgent(t *testing.T) {
	srv := fakeControl(t, "tok")
	code, _, _ := runCLI([]string{"send", "hello"}, envFor(srv, "tok"))
	if code != 2 {
		t.Fatalf("missing agent: code = %d, want 2", code)
	}
}

func TestRun_NoEndpointConfigured(t *testing.T) {
	// 无 env 端点、AppDataDir 指向空临时目录(无握手文件)、也不是 agentred 主机 → 提示桌面未运行。
	t.Setenv("AGENTRE_DATA_DIR", t.TempDir())
	t.Setenv("AGENTRED_DATA_DIR", t.TempDir())
	code, _, errs := runCLI([]string{"list", "agents"}, func(string) (string, bool) { return "", false })
	if code != 1 {
		t.Fatalf("code = %d, want 1", code)
	}
	if !strings.Contains(strings.ToLower(errs), "not running") && !strings.Contains(strings.ToLower(errs), "endpoint") {
		t.Fatalf("stderr = %q, want desktop-not-running hint", errs)
	}
}

func TestRun_AgentredHostWithoutSessionToken(t *testing.T) {
	// spec「Routing and approval」表最后一行：agentred 主机、没有会话 token、也没有桌面握手
	// 文件 → 退出码 1，Error: 前缀，说明只有 Agentre 派发的会话能用 agrctl，而不是桌面未运行。
	agentredDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(agentredDir, "state.json"), []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AGENTRED_DATA_DIR", agentredDir)
	t.Setenv("AGENTRE_DATA_DIR", t.TempDir())

	code, _, errs := runCLI([]string{"list", "agents"}, func(string) (string, bool) { return "", false })
	if code != 1 {
		t.Fatalf("code = %d, want 1", code)
	}
	if !strings.HasPrefix(errs, "Error: ") {
		t.Fatalf("stderr = %q, want Error: prefix", errs)
	}
	if strings.Contains(strings.ToLower(errs), "is the desktop app running") {
		t.Fatalf("stderr = %q, must not use the desktop-not-running message on an agentred host", errs)
	}
	if !strings.Contains(strings.ToLower(errs), "session") {
		t.Fatalf("stderr = %q, want it to say only an Agentre-dispatched session can use agrctl here", errs)
	}
}
