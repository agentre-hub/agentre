package agent_backend_entity

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/agentre-hub/agentre/internal/model/entity/llm_provider_entity"
)

// antigravity 是绑定 gemini 供应商的 agy ACP 后端：只匹配 gemini、必须绑供应商、
// 允许 cli_path，且 GEMINI_API_KEY / GOOGLE_GEMINI_BASE_URL 两个键只归它保留。
func TestAntigravityKind(t *testing.T) {
	ctx := context.Background()
	valid := func() *AgentBackend {
		return &AgentBackend{Type: string(TypeAntigravity), Name: "agy", LLMProviderKey: "gem"}
	}

	t.Run("kind metadata", func(t *testing.T) {
		b := valid()
		require.True(t, b.IsAntigravity())
		kind := KindFor(TypeAntigravity)
		require.NotNil(t, kind)
		assert.Empty(t, kind.KnownAliases())
		assert.True(t, kind.AllowsCLIPath())
		assert.True(t, kind.RequiresProviderModel())
		for _, pt := range []llm_provider_entity.ProviderType{
			llm_provider_entity.TypeAnthropic, llm_provider_entity.TypeOpenAIChat, llm_provider_entity.TypeOpenAIResponse,
		} {
			assert.False(t, kind.ProviderTypeMatch(pt), pt)
		}
		assert.True(t, kind.ProviderTypeMatch(llm_provider_entity.TypeGemini))
	})

	t.Run("only antigravity matches gemini", func(t *testing.T) {
		for _, bt := range []BackendType{TypeBuiltin, TypeClaudeCode, TypeCodex, TypePiAgent, TypeOpenClaw, TypeHermes, TypeACP} {
			assert.False(t, KindFor(bt).ProviderTypeMatch(llm_provider_entity.TypeGemini), bt)
		}
	})

	t.Run("builtin still matches the other provider types", func(t *testing.T) {
		for _, pt := range []llm_provider_entity.ProviderType{
			llm_provider_entity.TypeAnthropic, llm_provider_entity.TypeOpenAIChat, llm_provider_entity.TypeOpenAIResponse,
		} {
			assert.True(t, KindFor(TypeBuiltin).ProviderTypeMatch(pt), pt)
		}
	})

	t.Run("accepts a provider-bound backend with cli_path", func(t *testing.T) {
		assert.NoError(t, valid().Check(ctx))
		b := valid()
		b.CLIPath = "/opt/agy"
		assert.NoError(t, b.Check(ctx))
	})

	t.Run("rejects a missing provider", func(t *testing.T) {
		b := valid()
		b.LLMProviderKey = ""
		assert.Error(t, b.Check(ctx))
	})

	t.Run("rejects the two reserved env keys", func(t *testing.T) {
		for _, env := range []string{`{"GEMINI_API_KEY":"x"}`, `{"GOOGLE_GEMINI_BASE_URL":"http://x"}`} {
			b := valid()
			b.EnvJSON = env
			assert.Error(t, b.Check(ctx), env)
		}
		b := valid()
		b.EnvJSON = `{"AGY_LOG":"1"}`
		assert.NoError(t, b.Check(ctx))
	})

	t.Run("generic acp keeps GEMINI_API_KEY in env_json", func(t *testing.T) {
		b := &AgentBackend{Type: string(TypeACP), Name: "g", ACPCommand: "gemini", ACPArgs: []string{"--acp"},
			EnvJSON: `{"GEMINI_API_KEY":"x"}`}
		assert.NoError(t, b.Check(ctx))
		assert.False(t, IsReservedEnvKey("GEMINI_API_KEY"))
	})

	t.Run("rejects fields acp has no channel for", func(t *testing.T) {
		for _, mut := range []func(*AgentBackend){
			func(b *AgentBackend) { b.ModelRoutes = `{"OPUS":{"providerKey":"p"}}` },
			func(b *AgentBackend) { b.Sandbox = "read-only" },
			func(b *AgentBackend) { b.Approval = "never" },
			func(b *AgentBackend) { b.DefaultPermissionMode = "plan" },
			func(b *AgentBackend) { b.DefaultModel = "m" },
			func(b *AgentBackend) { b.ACPCommand = "agy" },
		} {
			b := valid()
			mut(b)
			assert.Error(t, b.Check(ctx))
		}
	})
}
