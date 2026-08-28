package agent_backend_entity

import (
	"context"
	"testing"

	. "github.com/smartystreets/goconvey/convey"
	"github.com/stretchr/testify/assert"
)

// TestAgentBackendCheck 覆盖 Check 在不同 type × 字段组合下的行为。
// claudecode / codex 现在允许创建，并按 BackendKind 校验细节。
func TestAgentBackendCheck(t *testing.T) {
	ctx := context.Background()

	cases := []struct {
		name    string
		input   *AgentBackend
		wantErr bool
	}{
		// 通用
		{
			name:    "nil receiver",
			input:   nil,
			wantErr: true,
		},
		{
			name:    "empty name",
			input:   &AgentBackend{Type: string(TypeBuiltin), LLMProviderKey: "11111111-1111-1111-1111-111111111111"},
			wantErr: true,
		},
		{
			name:    "unknown type",
			input:   &AgentBackend{Type: "foo", Name: "x", LLMProviderKey: "11111111-1111-1111-1111-111111111111"},
			wantErr: true,
		},

		// builtin
		{
			name:    "builtin valid",
			input:   &AgentBackend{Type: string(TypeBuiltin), Name: "x", LLMProviderKey: "11111111-1111-1111-1111-111111111111"},
			wantErr: false,
		},
		{
			name:    "builtin missing provider",
			input:   &AgentBackend{Type: string(TypeBuiltin), Name: "x"},
			wantErr: true,
		},
		{
			name:    "builtin rejects cli_path",
			input:   &AgentBackend{Type: string(TypeBuiltin), Name: "x", LLMProviderKey: "11111111-1111-1111-1111-111111111111", CLIPath: "/usr/local/bin/claude"},
			wantErr: true,
		},
		{
			name:    "builtin rejects non-empty model_routes",
			input:   &AgentBackend{Type: string(TypeBuiltin), Name: "x", LLMProviderKey: "11111111-1111-1111-1111-111111111111", ModelRoutes: `{"OPUS":{"providerKey":"22222222-2222-2222-2222-222222222222"}}`},
			wantErr: true,
		},
		{
			name:    "builtin rejects sandbox",
			input:   &AgentBackend{Type: string(TypeBuiltin), Name: "x", LLMProviderKey: "11111111-1111-1111-1111-111111111111", Sandbox: "read-only"},
			wantErr: true,
		},
		{
			name:    "builtin rejects approval",
			input:   &AgentBackend{Type: string(TypeBuiltin), Name: "x", LLMProviderKey: "11111111-1111-1111-1111-111111111111", Approval: "never"},
			wantErr: true,
		},
		{
			name:    "builtin rejects env_json",
			input:   &AgentBackend{Type: string(TypeBuiltin), Name: "x", LLMProviderKey: "11111111-1111-1111-1111-111111111111", EnvJSON: `{"K":"V"}`},
			wantErr: true,
		},

		// claudecode
		{
			name:    "claudecode valid minimal",
			input:   &AgentBackend{Type: string(TypeClaudeCode), Name: "cc", LLMProviderKey: "11111111-1111-1111-1111-111111111111"},
			wantErr: false,
		},
		{
			// 不关联供应商表示走 claude CLI 自身登录态，仍合法。
			name:    "claudecode without provider is valid",
			input:   &AgentBackend{Type: string(TypeClaudeCode), Name: "cc"},
			wantErr: false,
		},
		{
			name:    "claudecode allows cli_path",
			input:   &AgentBackend{Type: string(TypeClaudeCode), Name: "cc", LLMProviderKey: "11111111-1111-1111-1111-111111111111", CLIPath: "/usr/local/bin/claude"},
			wantErr: false,
		},
		{
			name:    "claudecode valid with model routes",
			input:   &AgentBackend{Type: string(TypeClaudeCode), Name: "cc", LLMProviderKey: "11111111-1111-1111-1111-111111111111", ModelRoutes: `{"OPUS":{"providerKey":"22222222-2222-2222-2222-222222222222"},"SONNET":{"providerKey":"33333333-3333-3333-3333-333333333333","modelKey":"mk-sonnet"}}`},
			wantErr: false,
		},
		{
			name:    "claudecode rejects unknown alias",
			input:   &AgentBackend{Type: string(TypeClaudeCode), Name: "cc", LLMProviderKey: "11111111-1111-1111-1111-111111111111", ModelRoutes: `{"GEMINI":{"providerKey":"22222222-2222-2222-2222-222222222222"}}`},
			wantErr: true,
		},
		{
			name:    "claudecode rejects alias with empty providerKey",
			input:   &AgentBackend{Type: string(TypeClaudeCode), Name: "cc", LLMProviderKey: "11111111-1111-1111-1111-111111111111", ModelRoutes: `{"OPUS":{"providerKey":""}}`},
			wantErr: true,
		},
		{
			name:    "claudecode rejects sandbox",
			input:   &AgentBackend{Type: string(TypeClaudeCode), Name: "cc", LLMProviderKey: "11111111-1111-1111-1111-111111111111", Sandbox: "read-only"},
			wantErr: true,
		},
		{
			name:    "claudecode rejects approval",
			input:   &AgentBackend{Type: string(TypeClaudeCode), Name: "cc", LLMProviderKey: "11111111-1111-1111-1111-111111111111", Approval: "never"},
			wantErr: true,
		},
		{
			name:    "claudecode rejects reserved env key",
			input:   &AgentBackend{Type: string(TypeClaudeCode), Name: "cc", LLMProviderKey: "11111111-1111-1111-1111-111111111111", EnvJSON: `{"ANTHROPIC_API_KEY":"x"}`},
			wantErr: true,
		},
		{
			name:    "claudecode allows non-reserved env key",
			input:   &AgentBackend{Type: string(TypeClaudeCode), Name: "cc", LLMProviderKey: "11111111-1111-1111-1111-111111111111", EnvJSON: `{"ANTHROPIC_LOG":"info"}`},
			wantErr: false,
		},
		{
			name:    "claudecode rejects malformed env_json",
			input:   &AgentBackend{Type: string(TypeClaudeCode), Name: "cc", LLMProviderKey: "11111111-1111-1111-1111-111111111111", EnvJSON: `{`},
			wantErr: true,
		},

		// codex
		{
			name:    "codex valid minimal",
			input:   &AgentBackend{Type: string(TypeCodex), Name: "cx", LLMProviderKey: "11111111-1111-1111-1111-111111111111"},
			wantErr: false,
		},
		{
			// 不关联供应商表示走 codex CLI 自身登录态，仍合法。
			name:    "codex without provider is valid",
			input:   &AgentBackend{Type: string(TypeCodex), Name: "cx"},
			wantErr: false,
		},
		{
			name:    "codex valid with sandbox & approval",
			input:   &AgentBackend{Type: string(TypeCodex), Name: "cx", LLMProviderKey: "11111111-1111-1111-1111-111111111111", Sandbox: "workspace-write", Approval: "on-request"},
			wantErr: false,
		},
		{
			name:    "codex rejects model_routes",
			input:   &AgentBackend{Type: string(TypeCodex), Name: "cx", LLMProviderKey: "11111111-1111-1111-1111-111111111111", ModelRoutes: `{"OPUS":"22222222-2222-2222-2222-222222222222"}`},
			wantErr: true,
		},
		{
			name:    "codex rejects invalid sandbox",
			input:   &AgentBackend{Type: string(TypeCodex), Name: "cx", LLMProviderKey: "11111111-1111-1111-1111-111111111111", Sandbox: "godmode"},
			wantErr: true,
		},
		{
			name:    "codex rejects invalid approval",
			input:   &AgentBackend{Type: string(TypeCodex), Name: "cx", LLMProviderKey: "11111111-1111-1111-1111-111111111111", Approval: "maybe"},
			wantErr: true,
		},
		{
			name:    "codex rejects reserved env key",
			input:   &AgentBackend{Type: string(TypeCodex), Name: "cx", LLMProviderKey: "11111111-1111-1111-1111-111111111111", EnvJSON: `{"OPENAI_API_KEY":"x"}`},
			wantErr: true,
		},
		{
			name:    "codex allows OPENAI_ORGANIZATION",
			input:   &AgentBackend{Type: string(TypeCodex), Name: "cx", LLMProviderKey: "11111111-1111-1111-1111-111111111111", EnvJSON: `{"OPENAI_ORGANIZATION":"acme"}`},
			wantErr: false,
		},

		// reasoning_effort — 6 档枚举，三种类型共用同一校验。
		{
			name:    "builtin accepts empty reasoning_effort",
			input:   &AgentBackend{Type: string(TypeBuiltin), Name: "x", LLMProviderKey: "11111111-1111-1111-1111-111111111111", ReasoningEffort: ""},
			wantErr: false,
		},
		{
			name:    "builtin accepts reasoning_effort=low",
			input:   &AgentBackend{Type: string(TypeBuiltin), Name: "x", LLMProviderKey: "11111111-1111-1111-1111-111111111111", ReasoningEffort: "low"},
			wantErr: false,
		},
		{
			name:    "claudecode accepts reasoning_effort=max",
			input:   &AgentBackend{Type: string(TypeClaudeCode), Name: "cc", LLMProviderKey: "11111111-1111-1111-1111-111111111111", ReasoningEffort: "max"},
			wantErr: false,
		},
		{
			name:    "codex accepts reasoning_effort=high",
			input:   &AgentBackend{Type: string(TypeCodex), Name: "cx", LLMProviderKey: "11111111-1111-1111-1111-111111111111", ReasoningEffort: "high"},
			wantErr: false,
		},
		{
			// codex 允许 xhigh/max 落库；xhigh 启动层直传，max 在启动层兼容折叠。
			name:    "codex accepts reasoning_effort=xhigh",
			input:   &AgentBackend{Type: string(TypeCodex), Name: "cx", LLMProviderKey: "11111111-1111-1111-1111-111111111111", ReasoningEffort: "xhigh"},
			wantErr: false,
		},
		{
			name:    "rejects unknown reasoning_effort",
			input:   &AgentBackend{Type: string(TypeBuiltin), Name: "x", LLMProviderKey: "11111111-1111-1111-1111-111111111111", ReasoningEffort: "ultra"},
			wantErr: true,
		},
		{
			name:    "rejects uppercase reasoning_effort",
			input:   &AgentBackend{Type: string(TypeClaudeCode), Name: "cc", LLMProviderKey: "11111111-1111-1111-1111-111111111111", ReasoningEffort: "LOW"},
			wantErr: true,
		},

		// default_permission_mode — claudecode 独占；其它类型设非空拒绝；非白名单值拒绝。
		{
			name:    "claudecode accepts default_permission_mode=bypassPermissions",
			input:   &AgentBackend{Type: string(TypeClaudeCode), Name: "cc", LLMProviderKey: "11111111-1111-1111-1111-111111111111", DefaultPermissionMode: "bypassPermissions"},
			wantErr: false,
		},
		{
			name:    "claudecode accepts default_permission_mode=plan",
			input:   &AgentBackend{Type: string(TypeClaudeCode), Name: "cc", LLMProviderKey: "11111111-1111-1111-1111-111111111111", DefaultPermissionMode: "plan"},
			wantErr: false,
		},
		{
			name:    "claudecode accepts default_permission_mode='' (走 acceptEdits 默认)",
			input:   &AgentBackend{Type: string(TypeClaudeCode), Name: "cc", LLMProviderKey: "11111111-1111-1111-1111-111111111111", DefaultPermissionMode: ""},
			wantErr: false,
		},
		{
			name:    "claudecode rejects default_permission_mode=nonsense",
			input:   &AgentBackend{Type: string(TypeClaudeCode), Name: "cc", LLMProviderKey: "11111111-1111-1111-1111-111111111111", DefaultPermissionMode: "garbage"},
			wantErr: true,
		},
		{
			name:    "builtin rejects non-empty default_permission_mode",
			input:   &AgentBackend{Type: string(TypeBuiltin), Name: "x", LLMProviderKey: "11111111-1111-1111-1111-111111111111", DefaultPermissionMode: "plan"},
			wantErr: true,
		},
		{
			name:    "codex rejects non-empty default_permission_mode",
			input:   &AgentBackend{Type: string(TypeCodex), Name: "cx", LLMProviderKey: "11111111-1111-1111-1111-111111111111", DefaultPermissionMode: "bypassPermissions"},
			wantErr: true,
		},

		// default_model — claudecode 独占自由文本；走 CLI 登录态时指定自定义模型。
		// 其它类型设非空一律拒绝。
		{
			name:    "claudecode accepts default_model (CLI 登录态)",
			input:   &AgentBackend{Type: string(TypeClaudeCode), Name: "cc", DefaultModel: "claude-fable-5"},
			wantErr: false,
		},
		{
			name:    "claudecode accepts default_model with provider bound",
			input:   &AgentBackend{Type: string(TypeClaudeCode), Name: "cc", LLMProviderKey: "11111111-1111-1111-1111-111111111111", DefaultModel: "claude-opus-4-8"},
			wantErr: false,
		},
		{
			name:    "claudecode accepts empty default_model",
			input:   &AgentBackend{Type: string(TypeClaudeCode), Name: "cc", DefaultModel: ""},
			wantErr: false,
		},
		{
			name:    "builtin rejects non-empty default_model",
			input:   &AgentBackend{Type: string(TypeBuiltin), Name: "x", LLMProviderKey: "11111111-1111-1111-1111-111111111111", DefaultModel: "claude-fable-5"},
			wantErr: true,
		},
		{
			name:    "codex rejects non-empty default_model",
			input:   &AgentBackend{Type: string(TypeCodex), Name: "cx", LLMProviderKey: "11111111-1111-1111-1111-111111111111", DefaultModel: "gpt-5.5"},
			wantErr: true,
		},
		{
			name:    "piagent rejects non-empty default_model",
			input:   &AgentBackend{Type: string(TypePiAgent), Name: "pi", DefaultModel: "whatever"},
			wantErr: true,
		},

		// llm_model_key（固定模型）——两个 key 都非空 = fixed-model；ProviderKey 非空且
		// ModelKey 空 = provider-default；两个都空 = native。
		{
			name:    "claudecode fixed model valid",
			input:   &AgentBackend{Type: string(TypeClaudeCode), Name: "cc", LLMProviderKey: "11111111-1111-1111-1111-111111111111", LLMModelKey: "22222222-2222-2222-2222-222222222222"},
			wantErr: false,
		},
		{
			name:    "builtin fixed model valid",
			input:   &AgentBackend{Type: string(TypeBuiltin), Name: "x", LLMProviderKey: "11111111-1111-1111-1111-111111111111", LLMModelKey: "22222222-2222-2222-2222-222222222222"},
			wantErr: false,
		},
		{
			name:    "codex fixed model valid",
			input:   &AgentBackend{Type: string(TypeCodex), Name: "cx", LLMProviderKey: "11111111-1111-1111-1111-111111111111", LLMModelKey: "22222222-2222-2222-2222-222222222222"},
			wantErr: false,
		},
		{
			name:    "piagent fixed model valid",
			input:   &AgentBackend{Type: string(TypePiAgent), Name: "pi", LLMProviderKey: "11111111-1111-1111-1111-111111111111", LLMModelKey: "22222222-2222-2222-2222-222222222222"},
			wantErr: false,
		},
		{
			name:    "modelKey without providerKey is invalid",
			input:   &AgentBackend{Type: string(TypeClaudeCode), Name: "cc", LLMModelKey: "22222222-2222-2222-2222-222222222222"},
			wantErr: true,
		},
		{
			name:    "native claudecode with empty keys is valid",
			input:   &AgentBackend{Type: string(TypeClaudeCode), Name: "cc", DefaultModel: "claude-fable-5"},
			wantErr: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.input.Check(ctx)
			if tc.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestAgentBackendTypeHelpers(t *testing.T) {
	b := &AgentBackend{Type: string(TypeBuiltin)}
	assert.True(t, b.IsBuiltin())
	assert.False(t, b.IsClaudeCode())
	assert.False(t, b.IsCodex())

	b = &AgentBackend{Type: string(TypeClaudeCode)}
	assert.True(t, b.IsClaudeCode())
	assert.False(t, b.IsBuiltin())

	b = &AgentBackend{Type: string(TypeCodex)}
	assert.True(t, b.IsCodex())
	assert.False(t, b.IsClaudeCode())

	var nilBackend *AgentBackend
	assert.False(t, nilBackend.IsBuiltin())
	assert.False(t, nilBackend.IsClaudeCode())
	assert.False(t, nilBackend.IsCodex())
	assert.Nil(t, nilBackend.Kind())
}

func TestAgentBackend_IsLocalIsRemote(t *testing.T) {
	Convey("IsLocal / IsRemote", t, func() {
		Convey("empty device → local", func() {
			b := &AgentBackend{DeviceFingerprint: ""}
			So(b.IsLocal(), ShouldBeTrue)
			So(b.IsRemote(), ShouldBeFalse)
		})
		Convey("fingerprint device → remote", func() {
			b := &AgentBackend{DeviceFingerprint: "sha256:remote"}
			So(b.IsLocal(), ShouldBeFalse)
			So(b.IsRemote(), ShouldBeTrue)
		})
		Convey("nil receiver", func() {
			var b *AgentBackend
			So(b.IsLocal(), ShouldBeFalse)
			So(b.IsRemote(), ShouldBeFalse)
		})
	})
}
