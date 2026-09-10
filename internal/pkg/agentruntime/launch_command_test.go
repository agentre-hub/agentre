package agentruntime

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/llm_provider_entity"
)

// agentCwdFor 直接复用 AgentCwd 在 t.TempDir() 下的产物，给 assert 提供期望路径。
// paths.AppDataDir 读 AGENTRE_DATA_DIR；t.Setenv 切到临时目录避免污染。
func agentCwdFor(t *testing.T, agentID int64) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("AGENTRE_DATA_DIR", dir)
	cwd, err := AgentCwd(agentID)
	require.NoError(t, err)
	return cwd
}

func TestBuildLaunchCommand_ClaudeCodeWithTokenAndModel(t *testing.T) {
	cwd := agentCwdFor(t, 42)

	cmd, err := BuildLaunchCommand(LaunchCommandSpec{
		Backend: &agent_backend_entity.AgentBackend{
			ID:             1,
			Type:           string(agent_backend_entity.TypeClaudeCode),
			Name:           "cc",
			LLMProviderKey: "key-9", // 绑了 provider → 命令里也该带 ANTHROPIC_*
		},
		Effective: &EffectiveLLMConfig{
			ProviderKey:  "key-9",
			ProviderType: string(llm_provider_entity.TypeAnthropic),
			ModelID:      "claude-sonnet-4-6",
		},
		AgentID:           42,
		ProviderSessionID: "sess-uuid",
		GatewayURL:        "http://127.0.0.1:60080",
		Token:             "real-secret-token",
	})
	require.NoError(t, err)

	// 单行：cd && env... cmd args
	assert.NotContains(t, cmd, "\n", "命令必须是单行")
	assert.True(t, strings.HasPrefix(cmd, "cd '"+cwd+"' && "), "前缀应为 cd '<cwd>' && ，got %q", cmd)

	// 真实 token 内联，不再是 <TOKEN>
	assert.Contains(t, cmd, "ANTHROPIC_AUTH_TOKEN='real-secret-token'")
	assert.NotContains(t, cmd, "<TOKEN>")

	// gateway URL + model + resume
	assert.Contains(t, cmd, "ANTHROPIC_BASE_URL='http://127.0.0.1:60080'")
	// hook 子进程专用 env：与 backend 是否绑 provider 无关，只要 GatewayURL+Token 有就设。
	assert.Contains(t, cmd, "AGENTRE_GATEWAY_URL='http://127.0.0.1:60080'")
	assert.Contains(t, cmd, "AGENTRE_GATEWAY_TOKEN='real-secret-token'")
	assert.Contains(t, cmd, "claude --model claude-sonnet-4-6 --resume sess-uuid")

	// 不应有 IPC flags
	assert.NotContains(t, cmd, "--output-format")
	assert.NotContains(t, cmd, "stream-json")
	assert.NotContains(t, cmd, "--settings")
	assert.NotContains(t, cmd, "--append-system-prompt")
	assert.NotContains(t, cmd, "--permission-mode")
}

func TestBuildLaunchCommand_ClaudeCodePlaceholderToken(t *testing.T) {
	// 调用方不传 Token 时仍保留 <TOKEN> 占位（让兼容路径继续工作）。
	// LLMProviderKey != "" → 命令里包含 ANTHROPIC_*；CLI 登录模式见
	// TestBuildLaunchCommand_ClaudeCodeNoProviderWithGateway。
	_ = agentCwdFor(t, 12)
	cmd, err := BuildLaunchCommand(LaunchCommandSpec{
		Backend: &agent_backend_entity.AgentBackend{
			Type:           string(agent_backend_entity.TypeClaudeCode),
			Name:           "cc",
			LLMProviderKey: "key-1",
		},
		Effective:  &EffectiveLLMConfig{ProviderKey: "key-1"},
		AgentID:    12,
		GatewayURL: "http://127.0.0.1:60080",
	})
	require.NoError(t, err)
	assert.Contains(t, cmd, "ANTHROPIC_AUTH_TOKEN='<TOKEN>'")
	assert.Contains(t, cmd, "AGENTRE_GATEWAY_TOKEN='<TOKEN>'")
}

// TestBuildLaunchCommand_ClaudeCodeNoProviderWithGateway 覆盖 CLI 登录模式
// （backend 没绑 LLM provider）下的 launch-command 形态：
//   - **不**写 ANTHROPIC_BASE_URL / AUTH_TOKEN —— 设了会让 claude CLI 用 Bearer
//     覆盖 OAuth 走 gateway，CLI 登录态下我们没 provider 转发，LLM 直接挂；
//   - 但仍写 AGENTRE_GATEWAY_URL / TOKEN —— hook 子进程靠它访问 /hook/v1/inbox
//     拉排队消息（mid-turn 插入用户消息的关键路径）。
func TestBuildLaunchCommand_ClaudeCodeNoProviderWithGateway(t *testing.T) {
	_ = agentCwdFor(t, 33)
	cmd, err := BuildLaunchCommand(LaunchCommandSpec{
		Backend: &agent_backend_entity.AgentBackend{
			Type:           string(agent_backend_entity.TypeClaudeCode),
			Name:           "cc-login",
			LLMProviderKey: "", // CLI 登录模式
		},
		AgentID:    33,
		GatewayURL: "http://127.0.0.1:60080",
		Token:      "hook-tok",
	})
	require.NoError(t, err)
	assert.NotContains(t, cmd, "ANTHROPIC_BASE_URL", "CLI 登录模式不能让 claude CLI 走 gateway 转发")
	assert.NotContains(t, cmd, "ANTHROPIC_AUTH_TOKEN")
	assert.Contains(t, cmd, "AGENTRE_GATEWAY_URL='http://127.0.0.1:60080'")
	assert.Contains(t, cmd, "AGENTRE_GATEWAY_TOKEN='hook-tok'")
}

func TestBuildLaunchCommand_ClaudeCodeNoProvider(t *testing.T) {
	_ = agentCwdFor(t, 7)

	cmd, err := BuildLaunchCommand(LaunchCommandSpec{
		Backend: &agent_backend_entity.AgentBackend{Type: string(agent_backend_entity.TypeClaudeCode), Name: "cc"},
		AgentID: 7,
		// 无 GatewayURL、无 ProviderSessionID、无 Token
	})
	require.NoError(t, err)

	assert.NotContains(t, cmd, "ANTHROPIC_BASE_URL")
	assert.NotContains(t, cmd, "ANTHROPIC_AUTH_TOKEN")
	assert.NotContains(t, cmd, "--resume")
	assert.NotContains(t, cmd, "--model")
	assert.Contains(t, cmd, " && claude")
}

func TestBuildLaunchCommand_CodexWithTokenAndModel(t *testing.T) {
	cwd := agentCwdFor(t, 8)

	cmd, err := BuildLaunchCommand(LaunchCommandSpec{
		Backend: &agent_backend_entity.AgentBackend{
			Type: string(agent_backend_entity.TypeCodex),
			Name: "cx",
		},
		Effective: &EffectiveLLMConfig{
			ProviderType: string(llm_provider_entity.TypeOpenAIResponse),
			ModelID:      "gpt-5-codex",
		},
		AgentID:    8,
		GatewayURL: "http://127.0.0.1:60080",
		Token:      "sk-live-xyz",
	})
	require.NoError(t, err)

	assert.NotContains(t, cmd, "\n", "命令必须是单行")
	assert.True(t, strings.HasPrefix(cmd, "cd '"+cwd+"' && "), "前缀应为 cd '<cwd>' && ，got %q", cmd)

	// 真实 token 内联
	assert.Contains(t, cmd, "OPENAI_API_KEY='sk-live-xyz'")
	assert.NotContains(t, cmd, "<TOKEN>")

	// 5 个 -c 覆盖项 + model
	assert.Contains(t, cmd, `-c 'model_provider="agentre-gateway"'`)
	assert.Contains(t, cmd, `-c 'model_providers.agentre-gateway.base_url="http://127.0.0.1:60080/v1"'`)
	assert.Contains(t, cmd, `-c 'model_providers.agentre-gateway.env_key="OPENAI_API_KEY"'`)
	assert.Contains(t, cmd, `-c 'model_providers.agentre-gateway.wire_api="responses"'`)
	assert.Contains(t, cmd, `-c 'model="gpt-5-codex"'`)

	// 不应有 IPC 入口
	assert.NotContains(t, cmd, "app-server")
	assert.NotContains(t, cmd, "--listen")
}

func TestBuildLaunchCommand_CodexWithProviderSessionID(t *testing.T) {
	_ = agentCwdFor(t, 18)

	cmd, err := BuildLaunchCommand(LaunchCommandSpec{
		Backend: &agent_backend_entity.AgentBackend{
			Type: string(agent_backend_entity.TypeCodex),
			Name: "cx",
		},
		AgentID:           18,
		ProviderSessionID: "codex-thread-123",
	})
	require.NoError(t, err)

	assert.Contains(t, cmd, "codex resume codex-thread-123")
}

// TestBuildLaunchCommand_ClaudeCodeReasoningEffort 覆盖 reasoning_effort 透传到
// 用户可粘贴的 shell 命令：claudecode 走 --effort <level>，空串则不下发。
func TestBuildLaunchCommand_ClaudeCodeReasoningEffort(t *testing.T) {
	_ = agentCwdFor(t, 21)
	cmd, err := BuildLaunchCommand(LaunchCommandSpec{
		Backend: &agent_backend_entity.AgentBackend{
			Type:            string(agent_backend_entity.TypeClaudeCode),
			Name:            "cc",
			ReasoningEffort: "xhigh",
		},
		AgentID: 21,
	})
	require.NoError(t, err)
	assert.Contains(t, cmd, "--effort xhigh")
}

func TestBuildLaunchCommand_ClaudeCodeOmitsEffortWhenEmpty(t *testing.T) {
	_ = agentCwdFor(t, 22)
	cmd, err := BuildLaunchCommand(LaunchCommandSpec{
		Backend: &agent_backend_entity.AgentBackend{
			Type: string(agent_backend_entity.TypeClaudeCode),
			Name: "cc",
		},
		AgentID: 22,
	})
	require.NoError(t, err)
	assert.NotContains(t, cmd, "--effort")
}

// TestBuildLaunchCommand_CodexReasoningEffort 验证 codex shell 命令的 reasoning effort：
// low/medium/high/xhigh/max 六档原样直传（spec 2026-09-01「三后端下发档位的收敛」已
// 否决旧的 max→high 兼容折叠）；off 不下发。
func TestBuildLaunchCommand_CodexReasoningEffort(t *testing.T) {
	cases := []struct {
		effort string
		want   string // "" = 不应出现 model_reasoning_effort
	}{
		{"", ""},
		{"low", `model_reasoning_effort="low"`},
		{"medium", `model_reasoning_effort="medium"`},
		{"high", `model_reasoning_effort="high"`},
		{"xhigh", `model_reasoning_effort="xhigh"`},
		{"max", `model_reasoning_effort="max"`},
	}
	for _, tc := range cases {
		t.Run(tc.effort, func(t *testing.T) {
			_ = agentCwdFor(t, 31)
			cmd, err := BuildLaunchCommand(LaunchCommandSpec{
				Backend: &agent_backend_entity.AgentBackend{
					Type:            string(agent_backend_entity.TypeCodex),
					Name:            "cx",
					ReasoningEffort: tc.effort,
				},
				AgentID: 31,
			})
			require.NoError(t, err)
			if tc.want == "" {
				assert.NotContains(t, cmd, "model_reasoning_effort")
			} else {
				assert.Contains(t, cmd, tc.want)
			}
		})
	}
}

func TestBuildLaunchCommand_PiAgentUsesNativeSessionID(t *testing.T) {
	cwd := agentCwdFor(t, 44)

	cmd, err := BuildLaunchCommand(LaunchCommandSpec{
		Backend: &agent_backend_entity.AgentBackend{
			Type:            string(agent_backend_entity.TypePiAgent),
			Name:            "pi",
			ReasoningEffort: "high",
			EnvJSON:         `{"PI_CODING_AGENT_DIR":"/tmp/pi-agentre"}`,
		},
		AgentID:           44,
		SessionID:         59,
		ProviderSessionID: "019fac-native-session",
	})
	require.NoError(t, err)

	assert.NotContains(t, cmd, "\n", "命令必须是单行")
	assert.True(t, strings.HasPrefix(cmd, "cd '"+cwd+"' && "), "前缀应为 cd '<cwd>' && ，got %q", cmd)
	assert.Contains(t, cmd, "PI_OFFLINE='1'")
	assert.Contains(t, cmd, "PI_CODING_AGENT_DIR='/tmp/pi-agentre'")
	assert.Contains(t, cmd, "pi --session 019fac-native-session --thinking high")
	assert.NotContains(t, cmd, "--session-dir")
	assert.NotContains(t, cmd, "agentre-59.jsonl")
	assert.NotContains(t, cmd, "--mode rpc", "copied pi command should be a human terminal command, not the internal RPC transport")
	assert.NotContains(t, cmd, "--no-context-files", "copied pi command should preserve Pi's normal terminal context loading")
	assert.NotContains(t, cmd, "--model", "pi backend should use the user's ~/.pi/agent default model unless explicitly configured")
	assert.NotContains(t, cmd, "gpt-5.5:high", "thinking level should be passed only once")
	assert.NotContains(t, cmd, "AGENTRE_GATEWAY_TOKEN")
	assert.NotContains(t, cmd, "OPENAI_API_KEY")
	assert.NotContains(t, cmd, "ANTHROPIC_AUTH_TOKEN")
}

// TestBuildLaunchCommand_PiAgentMaxThinkingPassesThrough 锁住 spec 2026-09-01「三
// 后端下发档位的收敛」:max 原样下发 --thinking max,不再被本地兼容折叠成 xhigh。
func TestBuildLaunchCommand_PiAgentMaxThinkingPassesThrough(t *testing.T) {
	_ = agentCwdFor(t, 45)
	cmd, err := BuildLaunchCommand(LaunchCommandSpec{
		Backend: &agent_backend_entity.AgentBackend{
			Type:            string(agent_backend_entity.TypePiAgent),
			Name:            "pi",
			ReasoningEffort: "max",
		},
		AgentID: 45,
	})
	require.NoError(t, err)
	assert.Contains(t, cmd, "--thinking max")
	assert.NotContains(t, cmd, "--session", "a Pi chat without a native ID must start a fresh native session")
}

func TestBuildLaunchCommand_BuiltinRejected(t *testing.T) {
	_ = agentCwdFor(t, 5)
	_, err := BuildLaunchCommand(LaunchCommandSpec{
		Backend: &agent_backend_entity.AgentBackend{Type: string(agent_backend_entity.TypeBuiltin), Name: "x"},
		AgentID: 5,
	})
	assert.Error(t, err)
}

func TestBuildLaunchCommand_NilBackend(t *testing.T) {
	_, err := BuildLaunchCommand(LaunchCommandSpec{AgentID: 1})
	assert.Error(t, err)
}

func TestBuildLaunchCommand_BadAgentID(t *testing.T) {
	_, err := BuildLaunchCommand(LaunchCommandSpec{
		Backend: &agent_backend_entity.AgentBackend{Type: string(agent_backend_entity.TypeClaudeCode), Name: "x"},
		AgentID: 0,
	})
	assert.Error(t, err)
}

func TestBuildLaunchCommand_ShellEscapes(t *testing.T) {
	// 自定义 env 含特殊字符 → 单引号包裹 + ' 转义
	_ = agentCwdFor(t, 11)
	cmd, err := BuildLaunchCommand(LaunchCommandSpec{
		Backend: &agent_backend_entity.AgentBackend{
			Type:           string(agent_backend_entity.TypeClaudeCode),
			Name:           "cc",
			LLMProviderKey: "key-1", // 让 ANTHROPIC_* 入 env，覆盖单引号转义
			EnvJSON:        `{"WEIRD":"a'b c"}`,
		},
		Effective:  &EffectiveLLMConfig{ProviderKey: "key-1"},
		AgentID:    11,
		GatewayURL: "http://127.0.0.1:60080",
		Token:      "tok",
	})
	require.NoError(t, err)
	assert.Contains(t, cmd, `WEIRD='a'\''b c'`)
	// 真实 token 也走单引号包裹
	assert.Contains(t, cmd, "ANTHROPIC_AUTH_TOKEN='tok'")
}

func TestBuildLaunchCommand_BinaryOverride(t *testing.T) {
	_ = agentCwdFor(t, 3)
	cmd, err := BuildLaunchCommand(LaunchCommandSpec{
		Backend: &agent_backend_entity.AgentBackend{
			Type:    string(agent_backend_entity.TypeClaudeCode),
			Name:    "cc",
			CLIPath: "/opt/anthropic/bin/claude",
		},
		AgentID: 3,
	})
	require.NoError(t, err)
	assert.Contains(t, cmd, " && /opt/anthropic/bin/claude")
}

// sanity check that AgentCwd 用法正确：不要测 filepath.Join，只保证拼好的路径在 cmd 中能出现
func TestBuildLaunchCommand_CwdSanity(t *testing.T) {
	cwd := agentCwdFor(t, 99)
	require.True(t, filepath.IsAbs(cwd))
	cmd, err := BuildLaunchCommand(LaunchCommandSpec{
		Backend: &agent_backend_entity.AgentBackend{Type: string(agent_backend_entity.TypeClaudeCode), Name: "cc"},
		AgentID: 99,
	})
	require.NoError(t, err)
	assert.Contains(t, cmd, cwd)
}
