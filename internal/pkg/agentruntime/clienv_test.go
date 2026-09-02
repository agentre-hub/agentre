package agentruntime

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
)

// 仅做最小契约测试 — 详细分支已由 agent_backend_svc/prober_test.go 间接覆盖。
// 这里挡的是包级位置漂移：函数搬到 agentruntime 后仍按预期工作。
func TestBuildClaudeCodeEnv_Basic(t *testing.T) {
	t.Run("有 gateway + effective provider 时同时注入 ANTHROPIC_* 和 AGENTRE_GATEWAY_*", func(t *testing.T) {
		b := &agent_backend_entity.AgentBackend{LLMProviderKey: "key-7", ModelRoutes: `{"SONNET":{"providerKey":"key-1"}}`, EnvJSON: `{"X":"y"}`}
		env, err := BuildClaudeCodeEnv(b, CLIDeps{Token: "tok", GatewayURL: "http://127.0.0.1:60080", ProviderKey: "key-7"})
		require.NoError(t, err)
		assert.Equal(t, "http://127.0.0.1:60080", env["ANTHROPIC_BASE_URL"])
		assert.Equal(t, "tok", env["ANTHROPIC_AUTH_TOKEN"])
		assert.Equal(t, "http://127.0.0.1:60080", env["AGENTRE_GATEWAY_URL"])
		assert.Equal(t, "tok", env["AGENTRE_GATEWAY_TOKEN"])
		assert.Equal(t, "sonnet", env["ANTHROPIC_DEFAULT_SONNET_MODEL"])
		assert.Equal(t, "y", env["X"])
		_, hasKey := env["ANTHROPIC_API_KEY"]
		assert.False(t, hasKey, "不写 ANTHROPIC_API_KEY")
	})
	t.Run(`CLI 登录模式（deps.ProviderKey==""）：只注入 AGENTRE_GATEWAY_*，不动 ANTHROPIC_*`, func(t *testing.T) {
		// 这是修复「mid-turn chip 不消除」的核心契约：CLI 登录模式下我们
		// 仍要让 hook 子进程能访问 /hook/v1/inbox，但不能让 claude CLI 用
		// Bearer 覆盖 OAuth 走 LLM 转发（gateway 没 provider，会直接挂）。
		b := &agent_backend_entity.AgentBackend{LLMProviderKey: ""}
		env, err := BuildClaudeCodeEnv(b, CLIDeps{Token: "hook-tok", GatewayURL: "http://127.0.0.1:60080"})
		require.NoError(t, err)
		assert.Equal(t, "http://127.0.0.1:60080", env["AGENTRE_GATEWAY_URL"])
		assert.Equal(t, "hook-tok", env["AGENTRE_GATEWAY_TOKEN"])
		_, hasBase := env["ANTHROPIC_BASE_URL"]
		assert.False(t, hasBase, "CLI 登录模式不能设 ANTHROPIC_BASE_URL")
		_, hasAuth := env["ANTHROPIC_AUTH_TOKEN"]
		assert.False(t, hasAuth, "CLI 登录模式不能设 ANTHROPIC_AUTH_TOKEN")
	})
	t.Run("backend 未绑定但会话选了 agentre 供应商（deps.ProviderKey 非空）：仍注入 ANTHROPIC_*", func(t *testing.T) {
		// spec 2026-08-10 决策 6/问题 3：CLI 登录态后端上，会话级 provider_key 也要
		// 能接管网关路由 —— 门控必须看本轮 effective provider（deps.ProviderKey），
		// 不能再看 backend 绑定（b.LLMProviderKey）。
		b := &agent_backend_entity.AgentBackend{LLMProviderKey: ""}
		env, err := BuildClaudeCodeEnv(b, CLIDeps{Token: "tok", GatewayURL: "http://127.0.0.1:60080", ProviderKey: "session-picked"})
		require.NoError(t, err)
		assert.Equal(t, "http://127.0.0.1:60080", env["ANTHROPIC_BASE_URL"], "会话选了供应商就该走网关,即便 backend 未绑定")
		assert.Equal(t, "tok", env["ANTHROPIC_AUTH_TOKEN"])
	})
	t.Run("backend 绑定了供应商但调用方未传 deps.ProviderKey（探测类调用点）：回落 backend 绑定注入 ANTHROPIC_*", func(t *testing.T) {
		// daemon cli.probe / agent_backend_svc 的连通性 Test 没有会话 / session_provider
		// 覆盖概念，只按 backend 自身绑定探测，历来直接传 CLIDeps{Token,GatewayURL}、
		// 不填 ProviderKey —— 门控必须回落 b.LLMProviderKey，否则这两个既有调用点会
		// 静默停止走网关（回归成探测 CLI 自身登录态而非目标 provider）。
		b := &agent_backend_entity.AgentBackend{LLMProviderKey: "key-7"}
		env, err := BuildClaudeCodeEnv(b, CLIDeps{Token: "tok", GatewayURL: "http://127.0.0.1:60080"})
		require.NoError(t, err)
		assert.Equal(t, "http://127.0.0.1:60080", env["ANTHROPIC_BASE_URL"])
		assert.Equal(t, "tok", env["ANTHROPIC_AUTH_TOKEN"])
	})
	t.Run("无 gateway 时既不写 ANTHROPIC_* 也不写 AGENTRE_GATEWAY_*", func(t *testing.T) {
		env, err := BuildClaudeCodeEnv(&agent_backend_entity.AgentBackend{LLMProviderKey: "key-1"}, CLIDeps{ProviderKey: "key-1"})
		require.NoError(t, err)
		_, has := env["ANTHROPIC_BASE_URL"]
		assert.False(t, has)
		_, has = env["ANTHROPIC_AUTH_TOKEN"]
		assert.False(t, has)
		_, has = env["AGENTRE_GATEWAY_URL"]
		assert.False(t, has)
		_, has = env["AGENTRE_GATEWAY_TOKEN"]
		assert.False(t, has)
	})
}

func TestBuildCodexEnv_Basic(t *testing.T) {
	t.Run("有 gateway 时只注入 OPENAI_API_KEY", func(t *testing.T) {
		b := &agent_backend_entity.AgentBackend{EnvJSON: `{"Z":"q"}`}
		env, err := BuildCodexEnv(b, CLIDeps{Token: "tok", GatewayURL: "http://127.0.0.1:60080"})
		require.NoError(t, err)
		assert.Equal(t, "tok", env["OPENAI_API_KEY"])
		assert.Equal(t, "q", env["Z"])
		_, hasBase := env["OPENAI_BASE_URL"]
		assert.False(t, hasBase)
	})
	t.Run("无 gateway 时不写 BASE_URL / API_KEY", func(t *testing.T) {
		env, err := BuildCodexEnv(&agent_backend_entity.AgentBackend{}, CLIDeps{})
		require.NoError(t, err)
		_, has := env["OPENAI_BASE_URL"]
		assert.False(t, has)
		_, has = env["OPENAI_API_KEY"]
		assert.False(t, has)
	})
}

// TestCodexReasoningEffortConfigValue 锁住 codex 启动层的 reasoning effort 转译：
// 六档原样透传（含 max —— codex-cli 本地不做枚举校验，spec 2026-09-01「三后端下发
// 档位的收敛」已否决旧的 max→high 兼容折叠）；非法值（含大小写错、含空格）→ 空串
// （不下发，走 CLI 自身默认）。
func TestCodexReasoningEffortConfigValue(t *testing.T) {
	cases := map[string]string{
		"":       "",
		"low":    "low",
		"medium": "medium",
		"high":   "high",
		"xhigh":  "xhigh",
		"max":    "max",
		"ultra":  "",
		"LOW":    "",
		" high":  "",
	}
	for in, want := range cases {
		assert.Equal(t, want, CodexReasoningEffortConfigValue(in), "CodexReasoningEffortConfigValue(%q)", in)
	}
}

func TestBuildCodexConfig_Basic(t *testing.T) {
	t.Run("有 gateway 时生成 Codex model_provider 覆盖项", func(t *testing.T) {
		configs := BuildCodexConfig(CLIDeps{Token: "tok", GatewayURL: "http://127.0.0.1:60080/"})
		assert.Equal(t, []string{
			`model_provider="agentre-gateway"`,
			`model_providers.agentre-gateway.name="Agentre Gateway"`,
			`model_providers.agentre-gateway.base_url="http://127.0.0.1:60080/v1"`,
			`model_providers.agentre-gateway.env_key="OPENAI_API_KEY"`,
			`model_providers.agentre-gateway.wire_api="responses"`,
		}, configs)
	})
	t.Run("缺 gateway 或 token 时不生成覆盖项", func(t *testing.T) {
		assert.Empty(t, BuildCodexConfig(CLIDeps{Token: "tok"}))
		assert.Empty(t, BuildCodexConfig(CLIDeps{GatewayURL: "http://127.0.0.1:60080"}))
	})
}
