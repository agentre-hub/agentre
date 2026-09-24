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

// TestBuildClaudeCodeEnv_ContextWindow Claude Code 不认识的模型名(glm-5.3 等)一律按
// 200k 窗口自动压缩;只有 CLAUDE_CODE_MAX_CONTEXT_TOKENS 能告诉它真实窗口。供应商模型上
// 配了窗口就必须随 env 带下去,否则配置只停留在 agentre 自己的展示里(sess-4039)。
func TestBuildClaudeCodeEnv_ContextWindow(t *testing.T) {
	t.Run("配置了窗口 → 注入 CLAUDE_CODE_MAX_CONTEXT_TOKENS", func(t *testing.T) {
		env, err := BuildClaudeCodeEnv(&agent_backend_entity.AgentBackend{}, CLIDeps{ContextWindow: 400000})
		require.NoError(t, err)
		assert.Equal(t, "400000", env["CLAUDE_CODE_MAX_CONTEXT_TOKENS"])
	})
	t.Run("未配置窗口 → 不注入,让 CLI 用自己的默认", func(t *testing.T) {
		env, err := BuildClaudeCodeEnv(&agent_backend_entity.AgentBackend{}, CLIDeps{})
		require.NoError(t, err)
		_, has := env["CLAUDE_CODE_MAX_CONTEXT_TOKENS"]
		assert.False(t, has)
	})
	t.Run("用户 env_json 显式写了同名变量 → 用户值优先", func(t *testing.T) {
		b := &agent_backend_entity.AgentBackend{EnvJSON: `{"CLAUDE_CODE_MAX_CONTEXT_TOKENS":"300000"}`}
		env, err := BuildClaudeCodeEnv(b, CLIDeps{ContextWindow: 400000})
		require.NoError(t, err)
		assert.Equal(t, "300000", env["CLAUDE_CODE_MAX_CONTEXT_TOKENS"])
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

// TestBuildACPEnv 钉死 acp 子进程 env 装配：ACP Agent 自带 provider/model/凭证，
// 不注入任何网关变量，只透传用户自定义 env_json（保留键已被 entity.Check 拒入）。
// prober 与 chat 路径共用这一份，避免两处漂移。
func TestBuildACPEnv(t *testing.T) {
	t.Run("仅透传 env_json，不注入网关变量", func(t *testing.T) {
		b := &agent_backend_entity.AgentBackend{EnvJSON: `{"MY_TOOL_FLAGS":"--verbose"}`}
		env, err := BuildACPEnv(b, CLIDeps{})
		require.NoError(t, err)
		assert.Equal(t, map[string]string{"MY_TOOL_FLAGS": "--verbose"}, env)
	})
	t.Run("空 env_json 产出空 map", func(t *testing.T) {
		env, err := BuildACPEnv(&agent_backend_entity.AgentBackend{}, CLIDeps{})
		require.NoError(t, err)
		assert.Empty(t, env)
	})
	t.Run("坏 env_json 报错", func(t *testing.T) {
		_, err := BuildACPEnv(&agent_backend_entity.AgentBackend{EnvJSON: `{"broken"`}, CLIDeps{})
		assert.Error(t, err)
	})
}

// TestCLIEnv_InjectsSessionCtlCredentials 钉死 spec「会话级 token」：四类 CLI 子进程 env
// 都带上 AGENTRE_CTL_ENDPOINT + 会话级 AGENTRE_CTL_TOKEN，与是否绑 provider 无关；没有
// 会话凭证（探测类调用点）时一个都不写；用户 env_json 盖不掉会话身份。
func TestCLIEnv_InjectsSessionCtlCredentials(t *testing.T) {
	ctl := CtlCredentials{Endpoint: "http://127.0.0.1:60080", Token: "sess-tok"}
	builders := map[string]func(*agent_backend_entity.AgentBackend, CLIDeps) (map[string]string, error){
		"claudecode": BuildClaudeCodeEnv,
		"codex":      BuildCodexEnv,
		"piagent":    BuildPiAgentEnv,
		"acp":        BuildACPEnv,
	}
	for name, build := range builders {
		t.Run(name+"：有会话凭证 → 注入两个变量", func(t *testing.T) {
			env, err := build(&agent_backend_entity.AgentBackend{}, CLIDeps{Ctl: ctl})
			require.NoError(t, err)
			assert.Equal(t, "http://127.0.0.1:60080", env[CtlEndpointEnv])
			assert.Equal(t, "sess-tok", env[CtlTokenEnv])
		})
		t.Run(name+"：没有会话凭证 → 不注入", func(t *testing.T) {
			env, err := build(&agent_backend_entity.AgentBackend{}, CLIDeps{})
			require.NoError(t, err)
			_, hasEndpoint := env[CtlEndpointEnv]
			_, hasToken := env[CtlTokenEnv]
			assert.False(t, hasEndpoint)
			assert.False(t, hasToken)
		})
		t.Run(name+"：凭证不完整 → 不注入", func(t *testing.T) {
			env, err := build(&agent_backend_entity.AgentBackend{}, CLIDeps{Ctl: CtlCredentials{Token: "sess-tok"}})
			require.NoError(t, err)
			_, hasToken := env[CtlTokenEnv]
			assert.False(t, hasToken)
		})
		t.Run(name+"：env_json 里同名键盖不掉会话身份", func(t *testing.T) {
			b := &agent_backend_entity.AgentBackend{EnvJSON: `{"AGENTRE_CTL_TOKEN":"forged","AGENTRE_CTL_ENDPOINT":"http://evil"}`}
			env, err := build(b, CLIDeps{Ctl: ctl})
			require.NoError(t, err)
			assert.Equal(t, "sess-tok", env[CtlTokenEnv])
			assert.Equal(t, "http://127.0.0.1:60080", env[CtlEndpointEnv])
		})
	}
}

// TestRunRequest_CtlCredentials 钉死凭证来源：runtime 按本轮 (AgentID, SessionID)
// 向进程级注册的来源要会话凭证；没注册（agentred 尚无 ctl 代理）或来源拒绝时为空。
func TestRunRequest_CtlCredentials(t *testing.T) {
	t.Cleanup(func() { RegisterCtlCredentialSource(nil) })

	req := RunRequest{AgentID: 7, SessionID: 42}
	RegisterCtlCredentialSource(nil)
	assert.Equal(t, CtlCredentials{}, req.CtlCredentials(), "未注册 → 空")

	var gotAgent, gotSession int64
	RegisterCtlCredentialSource(func(agentID, sessionID int64) CtlCredentials {
		gotAgent, gotSession = agentID, sessionID
		return CtlCredentials{Endpoint: "http://127.0.0.1:1", Token: "t"}
	})
	assert.Equal(t, CtlCredentials{Endpoint: "http://127.0.0.1:1", Token: "t"}, req.CtlCredentials())
	assert.Equal(t, int64(7), gotAgent)
	assert.Equal(t, int64(42), gotSession)

	assert.Equal(t, CtlCredentials{Endpoint: "http://127.0.0.1:1", Token: "t"}, RunRequest{SessionID: 42}.CtlCredentials(),
		"没有本地 agent id（跨主机派发只带 agent sync id）照样签：会话身份只看会话")
	assert.Equal(t, CtlCredentials{}, RunRequest{AgentID: 7}.CtlCredentials(), "没有会话 → 不签")
}
