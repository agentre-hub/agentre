package builtin

import (
	"context"
	"testing"
	"time"

	"github.com/cago-frame/agents/provider"
	"github.com/cago-frame/agents/provider/providertest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/llm_provider_entity"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
)

func bashCallStream(id, command string) []provider.StreamChunk {
	return []provider.StreamChunk{
		{ToolCallDelta: &provider.ToolCallDelta{Index: 0, ID: id, Name: "bash"}},
		{ToolCallDelta: &provider.ToolCallDelta{Index: 0, ArgsDelta: `{"command":"` + command + `"}`}},
		{FinishReason: provider.FinishToolCalls},
	}
}

func TestRun_BashCommandsSetToolCwd(t *testing.T) {
	t.Setenv("AGENTRE_DATA_DIR", t.TempDir())
	start := t.TempDir()

	fp := providertest.New().
		QueueStream(bashCallStream("b1", "cd /tmp/shellloc-a")...).
		QueueStream(bashCallStream("b2", "echo hi")...).
		QueueStream(
			provider.StreamChunk{ContentDelta: "done"},
			provider.StreamChunk{FinishReason: provider.FinishStop},
		)
	SetBuiltinProviderBuilderForTest(func(_ *llm_provider_entity.LLMProvider) (provider.Provider, error) { return fp, nil })
	t.Cleanup(ResetBuiltinProviderBuilderForTest)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	events, _, err := New().Run(ctx, agentruntime.RunRequest{
		Backend:   &agent_backend_entity.AgentBackend{ID: 7, Type: "builtin", LLMProviderKey: "key-11"},
		Provider:  &llm_provider_entity.LLMProvider{ID: 11, Type: string(llm_provider_entity.TypeAnthropic)},
		Effective: &agentruntime.EffectiveLLMConfig{ProviderKey: "key-11", ProviderType: string(llm_provider_entity.TypeAnthropic), ModelID: "claude-test"},
		AgentID:   99,
		SessionID: 43,
		UserText:  "go",
		Cwd:       start,
	})
	require.NoError(t, err)

	cwds := map[string]string{}
	for ev := range events {
		switch e := ev.(type) {
		case agentruntime.ToolCall:
			cwds["call:"+e.ID] = e.Cwd
		case agentruntime.ToolResult:
			cwds["result:"+e.ToolCallID] = e.Cwd
		}
	}
	assert.Equal(t, "/tmp/shellloc-a", cwds["call:b1"], "cd moves the location")
	assert.Equal(t, "/tmp/shellloc-a", cwds["call:b2"], "and persists into the next call")
	assert.Equal(t, "/tmp/shellloc-a", cwds["result:b1"])
	assert.Equal(t, "/tmp/shellloc-a", cwds["result:b2"])
}
