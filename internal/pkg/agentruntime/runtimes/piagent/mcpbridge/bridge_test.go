package mcpbridge

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
)

func TestRenderConfig_WritesServerListWithHeadersAndTools(t *testing.T) {
	dataDir := t.TempDir()
	t.Setenv("AGENTRE_DATA_DIR", dataDir)
	specs := []agentruntime.MCPServerSpec{{
		Name:    "group",
		URL:     "http://127.0.0.1:52401/mcp/group/",
		Headers: map[string]string{"Authorization": "Bearer tok"},
		Tools:   []string{"group_send"},
	}}
	path, err := RenderConfig(specs, 42)
	if err != nil {
		t.Fatalf("RenderConfig: %v", err)
	}
	assertConfigPathUnderDataDir(t, dataDir, path)
	raw, err := os.ReadFile(path) //nolint:gosec // path is returned by RenderConfig and constrained to the test temp data dir.
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	var cfg struct {
		Servers []struct {
			Name    string            `json:"name"`
			URL     string            `json:"url"`
			Headers map[string]string `json:"headers"`
			Tools   []string          `json:"tools"`
		} `json:"servers"`
	}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(cfg.Servers) != 1 || cfg.Servers[0].Name != "group" || cfg.Servers[0].Headers["Authorization"] != "Bearer tok" || cfg.Servers[0].Tools[0] != "group_send" {
		t.Fatalf("unexpected config: %s", raw)
	}
}

func TestMaterialize_IdempotentHashedPath(t *testing.T) {
	t.Setenv("AGENTRE_DATA_DIR", t.TempDir())
	p1, err := Materialize()
	if err != nil {
		t.Fatalf("Materialize: %v", err)
	}
	if _, err := os.Stat(p1); err != nil {
		t.Fatalf("bridge file missing: %v", err)
	}
	p2, err := Materialize()
	if err != nil || p2 != p1 {
		t.Fatalf("Materialize not idempotent: p1=%s p2=%s err=%v", p1, p2, err)
	}
	if !strings.HasSuffix(p1, ".mjs") || !strings.Contains(filepath.Base(p1), "agentre-mcp-bridge-") {
		t.Fatalf("unexpected bridge path: %s", p1)
	}
}

func TestRenderConfig_EmptySpecs(t *testing.T) {
	dataDir := t.TempDir()
	t.Setenv("AGENTRE_DATA_DIR", dataDir)
	path, err := RenderConfig(nil, 7)
	if err != nil {
		t.Fatalf("RenderConfig(nil): %v", err)
	}
	assertConfigPathUnderDataDir(t, dataDir, path)
	raw, err := os.ReadFile(path) //nolint:gosec // path is returned by RenderConfig and constrained to the test temp data dir.
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if got := strings.TrimSpace(string(raw)); got != `{"servers":[]}` {
		t.Fatalf("expected empty servers array, got: %s", got)
	}
}

func TestRemoveConfig_DeletesAndIdempotent(t *testing.T) {
	t.Setenv("AGENTRE_DATA_DIR", t.TempDir())
	path, err := RenderConfig([]agentruntime.MCPServerSpec{{Name: "group", URL: "http://127.0.0.1:1/"}}, 7)
	if err != nil {
		t.Fatalf("RenderConfig: %v", err)
	}
	if err := RemoveConfig(7); err != nil {
		t.Fatalf("RemoveConfig: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("config not removed, stat err=%v", err)
	}
	// 文件不存在时再删一次：幂等，不报错。
	if err := RemoveConfig(7); err != nil {
		t.Fatalf("RemoveConfig not idempotent: %v", err)
	}
}

func assertConfigPathUnderDataDir(t *testing.T, dataDir, path string) {
	t.Helper()

	wantDir := filepath.Join(dataDir, "piagent", "ext", "cfg")
	rel, err := filepath.Rel(wantDir, path)
	if err != nil || rel == "." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) || rel == ".." || filepath.IsAbs(rel) {
		t.Fatalf("config path not under %s: %s", wantDir, path)
	}
}
