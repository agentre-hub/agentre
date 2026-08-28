package piagent

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
)

func minimalReq(sessionID int64, specs []agentruntime.MCPServerSpec) agentruntime.RunRequest {
	return agentruntime.RunRequest{
		SessionID:  sessionID,
		Backend:    &agent_backend_entity.AgentBackend{},
		MCPServers: specs,
	}
}

func TestSessionFactory_InjectsBridgeWhenMCPServersPresent(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("AGENTRE_DATA_DIR", dir)
	specs := []agentruntime.MCPServerSpec{{Name: "group", URL: "http://127.0.0.1:1/mcp/group/", Headers: map[string]string{"Authorization": "Bearer t"}, Tools: []string{"group_send"}}}

	if _, err := sessionFactory(minimalReq(7, specs), map[string]string{}, t.TempDir()); err != nil {
		t.Fatalf("sessionFactory: %v", err)
	}

	cfg := filepath.Join(dir, "piagent", "ext", "cfg", "7.json")
	if _, err := os.Stat(cfg); err != nil {
		t.Fatalf("expected config at %s: %v", cfg, err)
	}
	matches, _ := filepath.Glob(filepath.Join(dir, "piagent", "ext", "agentre-mcp-bridge-*.mjs"))
	if len(matches) == 0 {
		t.Fatalf("expected materialized bridge .mjs under %s", filepath.Join(dir, "piagent", "ext"))
	}
}

func TestSessionFactory_NoBridgeWhenNoMCPServers(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("AGENTRE_DATA_DIR", dir)

	if _, err := sessionFactory(minimalReq(7, nil), map[string]string{}, t.TempDir()); err != nil {
		t.Fatalf("sessionFactory: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "piagent", "ext")); !os.IsNotExist(err) {
		t.Fatalf("did not expect piagent/ext dir when no MCPServers, stat err=%v", err)
	}
}
