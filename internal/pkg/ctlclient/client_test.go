package ctlclient

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/agentre-hub/agentre/internal/pkg/ctlendpoint"
)

func envOf(m map[string]string) func(string) (string, bool) {
	return func(k string) (string, bool) { v, ok := m[k]; return v, ok }
}

func TestResolve_FlagWinsOverEnv(t *testing.T) {
	got, err := Resolve("http://flag", "flag-token", envOf(map[string]string{
		"AGENTRE_CTL_ENDPOINT": "http://env",
		"AGENTRE_CTL_TOKEN":    "env-token",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if got.Base != "http://flag" || got.Token != "flag-token" {
		t.Fatalf("got %+v, want flag values", got)
	}
}

func TestResolve_EnvUsedWhenNoFlag(t *testing.T) {
	got, err := Resolve("", "", envOf(map[string]string{
		"AGENTRE_CTL_ENDPOINT": "http://env/", // trailing slash trimmed
		"AGENTRE_CTL_TOKEN":    "env-token",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if got.Base != "http://env" || got.Token != "env-token" {
		t.Fatalf("got %+v, want env values", got)
	}
}

func TestResolve_FallsBackToHandshakeFile(t *testing.T) {
	dir := t.TempDir()
	if err := ctlendpoint.Write(dir, ctlendpoint.Endpoint{URL: "http://handshake", Token: "hs-token"}); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AGENTRE_DATA_DIR", dir)

	got, err := Resolve("", "", envOf(nil))
	if err != nil {
		t.Fatal(err)
	}
	if got.Base != "http://handshake" || got.Token != "hs-token" {
		t.Fatalf("got %+v, want handshake values", got)
	}
}

func TestResolve_MissingEverythingIsReadableError(t *testing.T) {
	// 既不是 agentred 主机(无 state.json)，桌面握手文件也不在 → 老提示：桌面端没跑起来。
	t.Setenv("AGENTRE_DATA_DIR", t.TempDir())
	t.Setenv("AGENTRED_DATA_DIR", t.TempDir())
	_, err := Resolve("", "", envOf(nil))
	if err == nil {
		t.Fatal("want an error when no endpoint is configured")
	}
	if !strings.Contains(err.Error(), "is the desktop app running?") {
		t.Fatalf("err = %q, want the desktop-not-running message", err.Error())
	}
	if strings.Contains(strings.ToLower(err.Error()), "agentred") {
		t.Fatalf("err = %q, must not mention agentred when there is no agentred marker", err.Error())
	}
}

// TestResolve_AgentredHostWithoutSessionToken 复现 spec「Routing and approval」表最后一行：
// agentred 主机上没有会话 token、也没有桌面握手文件时，报的是「只有 Agentre 派发的会话能用
// agrctl」，而不是桌面端未运行那句话。
func TestResolve_AgentredHostWithoutSessionToken(t *testing.T) {
	agentredDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(agentredDir, "state.json"), []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AGENTRED_DATA_DIR", agentredDir)
	t.Setenv("AGENTRE_DATA_DIR", t.TempDir()) // 无桌面握手文件

	_, err := Resolve("", "", envOf(nil))
	if err == nil {
		t.Fatal("want an error when there is no session token on an agentred host")
	}
	if strings.Contains(err.Error(), "is the desktop app running?") {
		t.Fatalf("err = %q, must not use the desktop-not-running message on an agentred host", err.Error())
	}
	if !strings.Contains(err.Error(), "agentred") {
		t.Fatalf("err = %q, want it to say this is an agentred host", err.Error())
	}
	if !strings.Contains(strings.ToLower(err.Error()), "session") {
		t.Fatalf("err = %q, want it to say only an Agentre-dispatched session can use agrctl here", err.Error())
	}
}

// TestResolve_HandshakeFileWinsEvenOnAgentredHost 覆盖三种标志同时具备的场景：即使这台机器
// 也留有 agentred 的 state.json(比如同机既跑 agentred 又跑桌面端做联调)，握手文件仍然优先。
func TestResolve_HandshakeFileWinsEvenOnAgentredHost(t *testing.T) {
	agentredDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(agentredDir, "state.json"), []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AGENTRED_DATA_DIR", agentredDir)

	desktopDir := t.TempDir()
	if err := ctlendpoint.Write(desktopDir, ctlendpoint.Endpoint{URL: "http://handshake", Token: "hs-token"}); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AGENTRE_DATA_DIR", desktopDir)

	got, err := Resolve("", "", envOf(nil))
	if err != nil {
		t.Fatal(err)
	}
	if got.Base != "http://handshake" || got.Token != "hs-token" {
		t.Fatalf("got %+v, want handshake values", got)
	}
}
