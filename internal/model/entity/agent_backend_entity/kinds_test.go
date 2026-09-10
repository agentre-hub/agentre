package agent_backend_entity

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/agentre-hub/agentre/internal/model/entity/llm_provider_entity"
)

func TestKindForReturnsCorrectKind(t *testing.T) {
	cases := []struct {
		name      string
		input     BackendType
		wantType  BackendType
		wantNilOK bool
	}{
		{"builtin", TypeBuiltin, TypeBuiltin, false},
		{"claudecode", TypeClaudeCode, TypeClaudeCode, false},
		{"piagent", TypePiAgent, TypePiAgent, false},
		{"unknown", BackendType("foo"), "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			k := KindFor(tc.input)
			if tc.wantNilOK {
				assert.Nil(t, k)
				return
			}
			if assert.NotNil(t, k) {
				assert.Equal(t, tc.wantType, k.Type())
			}
		})
	}
}

func TestProviderTypeMatch(t *testing.T) {
	cases := []struct {
		name      string
		kind      BackendKind
		provType  llm_provider_entity.ProviderType
		wantMatch bool
	}{
		{"builtin matches anything", builtinKind{}, llm_provider_entity.TypeAnthropic, true},
		{"builtin matches openai-chat", builtinKind{}, llm_provider_entity.TypeOpenAIChat, true},
		{"claudecode matches anthropic", claudeCodeKind{}, llm_provider_entity.TypeAnthropic, true},
		{"claudecode rejects openai-chat", claudeCodeKind{}, llm_provider_entity.TypeOpenAIChat, false},
		{"claudecode rejects openai-response", claudeCodeKind{}, llm_provider_entity.TypeOpenAIResponse, false},
		{"codex rejects openai-chat", codexKind{}, llm_provider_entity.TypeOpenAIChat, false},
		{"codex matches openai-response", codexKind{}, llm_provider_entity.TypeOpenAIResponse, true},
		{"codex rejects anthropic", codexKind{}, llm_provider_entity.TypeAnthropic, false},
		{"piagent matches anthropic", piAgentKind{}, llm_provider_entity.TypeAnthropic, true},
		{"piagent matches openai-chat", piAgentKind{}, llm_provider_entity.TypeOpenAIChat, true},
		{"piagent matches openai-response", piAgentKind{}, llm_provider_entity.TypeOpenAIResponse, true},
		{"piagent rejects unknown type", piAgentKind{}, llm_provider_entity.ProviderType("custom"), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.wantMatch, tc.kind.ProviderTypeMatch(tc.provType))
		})
	}
}

func TestKnownAliases(t *testing.T) {
	assert.Empty(t, builtinKind{}.KnownAliases())
	assert.Equal(t, []string{"OPUS", "SONNET", "HAIKU"}, claudeCodeKind{}.KnownAliases())
	assert.Empty(t, codexKind{}.KnownAliases())
	assert.Empty(t, piAgentKind{}.KnownAliases())
}

func TestAllowsCLIPath(t *testing.T) {
	assert.False(t, builtinKind{}.AllowsCLIPath())
	assert.True(t, claudeCodeKind{}.AllowsCLIPath())
	assert.True(t, codexKind{}.AllowsCLIPath())
	assert.True(t, piAgentKind{}.AllowsCLIPath())
}

func TestRequiresProviderModel(t *testing.T) {
	assert.False(t, builtinKind{}.RequiresProviderModel())
	assert.False(t, claudeCodeKind{}.RequiresProviderModel())
	assert.False(t, codexKind{}.RequiresProviderModel())
	assert.True(t, piAgentKind{}.RequiresProviderModel())
}

func TestPiAgentValidateExtra(t *testing.T) {
	ctx := context.Background()

	// 全空 → 合法（未绑定供应商，走 pi 自带 ~/.pi/agent 配置）。
	assert.NoError(t, piAgentKind{}.ValidateExtra(ctx, &AgentBackend{}))
	// LLMProviderKey 非空 → 放行（本功能核心：piagent 可绑定自定义供应商）。
	assert.NoError(t, piAgentKind{}.ValidateExtra(ctx, &AgentBackend{LLMProviderKey: "key-1"}))

	// 其它独有字段非默认值 → 仍拒绝（沿用 InvalidParameter 风格）。
	assert.Error(t, piAgentKind{}.ValidateExtra(ctx, &AgentBackend{ModelRoutes: `{"OPUS":"x"}`}))
	assert.Error(t, piAgentKind{}.ValidateExtra(ctx, &AgentBackend{Sandbox: "read-only"}))
	assert.Error(t, piAgentKind{}.ValidateExtra(ctx, &AgentBackend{Approval: "on-request"}))
	assert.Error(t, piAgentKind{}.ValidateExtra(ctx, &AgentBackend{DefaultPermissionMode: "plan"}))
	assert.Error(t, piAgentKind{}.ValidateExtra(ctx, &AgentBackend{DefaultModel: "gpt-5"}))
}

func TestIsReservedEnvKey(t *testing.T) {
	cases := []struct {
		key  string
		want bool
	}{
		{"AGENTRE_GATEWAY_URL", true},
		{"AGENTRE_GATEWAY_TOKEN", true},
		{"ANTHROPIC_BASE_URL", true},
		{"ANTHROPIC_API_KEY", true},
		{"ANTHROPIC_AUTH_TOKEN", true},
		{"ANTHROPIC_MODEL", true},
		{"ANTHROPIC_DEFAULT_OPUS_MODEL", true},
		{"ANTHROPIC_DEFAULT_SONNET_MODEL", true},
		{"ANTHROPIC_DEFAULT_HAIKU_MODEL", true},
		{"OPENAI_API_KEY", true},
		{"OPENAI_BASE_URL", true},
		{"OPENAI_API_BASE", true},
		{"PI_OFFLINE", true},
		{"PI_CODING_AGENT_DIR", true},
		{"PI_CODING_AGENT_SESSION_DIR", true},
		{"ANTHROPIC_LOG", false},
		{"OPENAI_ORGANIZATION", false},
		{"FOO", false},
	}
	for _, tc := range cases {
		t.Run(tc.key, func(t *testing.T) {
			assert.Equal(t, tc.want, IsReservedEnvKey(tc.key))
		})
	}
}

func TestValidateSandboxEnum(t *testing.T) {
	ctx := context.Background()
	for _, v := range []string{"", "read-only", "workspace-write", "danger-full-access"} {
		assert.NoError(t, validateSandbox(ctx, v), v)
	}
	for _, v := range []string{"  ", "weird", "Full Access"} {
		// 含空白的字符串去空白后变空 → 合法；其它必须报错
		if v == "  " {
			assert.NoError(t, validateSandbox(ctx, v))
			continue
		}
		assert.Error(t, validateSandbox(ctx, v))
	}
}

func TestValidateApprovalEnum(t *testing.T) {
	ctx := context.Background()
	for _, v := range []string{"", "untrusted", "on-request", "never"} {
		assert.NoError(t, validateApproval(ctx, v), v)
	}
	for _, v := range []string{"on-failure", "maybe", "Yes"} {
		assert.Error(t, validateApproval(ctx, v))
	}
}

func TestParseModelRoutes_TypedTargets(t *testing.T) {
	t.Run("结构化 target：provider-default（modelKey 空）", func(t *testing.T) {
		got, err := ParseModelRoutes(`{"OPUS":{"providerKey":"4f8c-1234"},"SONNET":{"providerKey":"a2bc","modelKey":"mk-sonnet"}}`)
		require.NoError(t, err)
		assert.Equal(t, map[string]ModelRouteTarget{
			"OPUS":   {ProviderKey: "4f8c-1234"},
			"SONNET": {ProviderKey: "a2bc", ModelKey: "mk-sonnet"},
		}, got)
	})
	t.Run("空对象", func(t *testing.T) {
		got, err := ParseModelRoutes(`{}`)
		require.NoError(t, err)
		assert.Empty(t, got)
	})
	t.Run("小写别名自动 ToUpper", func(t *testing.T) {
		got, err := ParseModelRoutes(`{"opus":{"providerKey":"4f8c"},"sonnet":{"providerKey":"a2bc"}}`)
		require.NoError(t, err)
		assert.Equal(t, map[string]ModelRouteTarget{
			"OPUS":   {ProviderKey: "4f8c"},
			"SONNET": {ProviderKey: "a2bc"},
		}, got)
	})
	t.Run("空 providerKey 被拒绝", func(t *testing.T) {
		_, err := ParseModelRoutes(`{"OPUS":{"providerKey":""}}`)
		assert.Error(t, err, "空 providerKey 应该被拒绝")
	})
	t.Run("旧字符串格式被拒绝（生产 parser 只接受新对象形状）", func(t *testing.T) {
		_, err := ParseModelRoutes(`{"OPUS":"4f8c-1234"}`)
		assert.Error(t, err, "旧 string value 已由迁移转换，运行时不再保留旧 parser")
	})
	t.Run("非对象值被拒绝", func(t *testing.T) {
		_, err := ParseModelRoutes(`{"OPUS":42}`)
		assert.Error(t, err)
	})
}

func TestMarshalModelRoutes_RoundTrip(t *testing.T) {
	t.Run("空 map 序列化为空对象", func(t *testing.T) {
		s, err := MarshalModelRoutes(nil)
		require.NoError(t, err)
		assert.Equal(t, "{}", s)
	})
	t.Run("结构化 target 序列化后能解析回来", func(t *testing.T) {
		s, err := MarshalModelRoutes(map[string]ModelRouteTarget{
			"OPUS":  {ProviderKey: "p1"},
			"HAIKU": {ProviderKey: "p2", ModelKey: "mk-h"},
		})
		require.NoError(t, err)
		got, err := ParseModelRoutes(s)
		require.NoError(t, err)
		assert.Equal(t, map[string]ModelRouteTarget{
			"OPUS":  {ProviderKey: "p1"},
			"HAIKU": {ProviderKey: "p2", ModelKey: "mk-h"},
		}, got)
	})
	t.Run("小写 alias 序列化时统一 ToUpper", func(t *testing.T) {
		s, err := MarshalModelRoutes(map[string]ModelRouteTarget{"opus": {ProviderKey: "p1"}})
		require.NoError(t, err)
		got, err := ParseModelRoutes(s)
		require.NoError(t, err)
		_, ok := got["OPUS"]
		assert.True(t, ok)
	})
}

func TestParseEnvJSON(t *testing.T) {
	cases := []struct {
		name    string
		input   string
		want    map[string]string
		wantErr bool
	}{
		{"empty", "", map[string]string{}, false},
		{"empty object", "{}", map[string]string{}, false},
		{"single", `{"K":"V"}`, map[string]string{"K": "V"}, false},
		{"malformed", `{not json`, nil, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParseEnvJSON(tc.input)
			if tc.wantErr {
				assert.Error(t, err)
				return
			}
			if assert.NoError(t, err) {
				assert.Equal(t, tc.want, got)
			}
		})
	}
}
