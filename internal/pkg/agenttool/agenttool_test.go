package agenttool

import (
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRegistry(t *testing.T) {
	d, ok := Lookup(KeyOrg)
	require.True(t, ok)
	require.Equal(t, "org", d.Key)
	require.Equal(t, "/mcp/org/", d.MCPPath)
	require.Contains(t, d.ToolNames, "org_get")
	require.Len(t, d.ToolNames, 7)

	_, ok = Lookup("nope")
	require.False(t, ok)

	require.Len(t, Keys(), 3)
	require.Equal(t, []string{"org", "subagent", "hook"}, Keys())
}

func TestRegistry_HasHook(t *testing.T) {
	d, ok := Lookup(KeyHook)
	require.True(t, ok)
	require.Equal(t, "hook", d.Key)
	require.Equal(t, "/mcp/hook/", d.MCPPath)
	require.Equal(t, []string{
		"hook_list", "hook_get", "hook_create", "hook_update", "hook_delete", "hook_run",
	}, d.ToolNames)
	require.Contains(t, Keys(), KeyHook)
}

func TestSubagentRegistered(t *testing.T) {
	def, ok := Lookup(KeySubagent)
	if !ok {
		t.Fatal("subagent tool not registered")
	}
	if def.MCPPath != "/mcp/subagent/" {
		t.Fatalf("MCPPath = %q", def.MCPPath)
	}
	want := []string{"agent_list", "agent_call"}
	if !slices.Equal(def.ToolNames, want) {
		t.Fatalf("ToolNames = %v, want %v", def.ToolNames, want)
	}
	if !slices.Contains(Keys(), KeySubagent) {
		t.Fatal("KeySubagent missing from Keys()")
	}
}

func TestRegistry_NoGroupCreate(t *testing.T) {
	_, ok := Lookup("group_create")
	assert.False(t, ok)
	assert.NotContains(t, Keys(), "group_create")
}
