package agent_backend_entity

import (
	"context"
	"testing"

	. "github.com/smartystreets/goconvey/convey"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/agentre-hub/agentre/internal/model/entity/llm_provider_entity"
)

// TestACPKind 钉死 acp 的 kind 槽位：ACP Agent 自带 provider/model/凭证，
// 因此永不参与 Agentre 的 new-session provider pill（ProviderTypeMatch 恒 false）。
// 它连一个外部 ACP Agent 子进程（stdio JSON-RPC），不是「已知 CLI」，
// 因此不接受 cli_path，只接受 ACPCommand + ACPArgs。
func TestACPKind(t *testing.T) {
	Convey("Given the acp backend type", t, func() {
		kind := KindFor(TypeACP)

		Convey("When resolving kind metadata Then it is a subprocess agent that never matches an Agentre provider", func() {
			So(kind, ShouldNotBeNil)
			So(kind, ShouldHaveSameTypeAs, acpKind{})
			So(kind.KnownAliases(), ShouldBeEmpty)
			So(kind.AllowsCLIPath(), ShouldBeFalse)
			So(kind.RequiresProviderModel(), ShouldBeFalse)
			for _, pt := range []llm_provider_entity.ProviderType{
				llm_provider_entity.TypeAnthropic,
				llm_provider_entity.TypeOpenAIChat,
				llm_provider_entity.TypeOpenAIResponse,
				llm_provider_entity.ProviderType("custom"),
			} {
				So(kind.ProviderTypeMatch(pt), ShouldBeFalse)
			}
		})

		Convey("When validating a clean acp backend Then it is accepted", func() {
			ctx := context.Background()
			So(kind.ValidateExtra(ctx, &AgentBackend{
				Type:       string(TypeACP),
				Name:       "acp",
				ACPCommand: "hermes",
				ACPArgs:    []string{"acp"},
			}), ShouldBeNil)
		})

		Convey("When the command is missing or malformed Then validation rejects it", func() {
			ctx := context.Background()
			for _, b := range []*AgentBackend{
				{Type: string(TypeACP), Name: "a"},
				{Type: string(TypeACP), Name: "a", ACPCommand: "   "},
				{Type: string(TypeACP), Name: "a", ACPCommand: "hermes\n--acp"},
				{Type: string(TypeACP), Name: "a", ACPCommand: "her\x00mes"},
				{Type: string(TypeACP), Name: "a", ACPCommand: "hermes", ACPArgs: []string{"acp\n"}},
				{Type: string(TypeACP), Name: "a", ACPCommand: "hermes", ACPArgs: []string{"a", "b\x00c"}},
			} {
				assert.Error(t, kind.ValidateExtra(ctx, b), "command %q args %v must be rejected", b.ACPCommand, b.ACPArgs)
			}
		})

		Convey("When another type's field is set Then validation rejects it", func() {
			ctx := context.Background()
			cases := []*AgentBackend{
				{Type: string(TypeACP), Name: "a", ACPCommand: "hermes", ModelRoutes: `{"OPUS":{"providerKey":"key-1"}}`},
				{Type: string(TypeACP), Name: "a", ACPCommand: "hermes", Sandbox: "read-only"},
				{Type: string(TypeACP), Name: "a", ACPCommand: "hermes", Approval: "never"},
				{Type: string(TypeACP), Name: "a", ACPCommand: "hermes", DefaultPermissionMode: "plan"},
				{Type: string(TypeACP), Name: "a", ACPCommand: "hermes", DefaultModel: "gpt-5"},
			}
			for i, b := range cases {
				assert.Error(t, kind.ValidateExtra(ctx, b), "case %d must be rejected", i)
			}
		})

		Convey("When a provider key is bound Then it is tolerated without a type whitelist", func() {
			// ACP Agent 自带登录态，但历史行可能带着 provider 绑定；kind 不加白名单，
			// 语义由 ProviderTypeMatch 恒 false 表达。
			ctx := context.Background()
			So(kind.ValidateExtra(ctx, &AgentBackend{
				Type:           string(TypeACP),
				Name:           "a",
				ACPCommand:     "hermes",
				LLMProviderKey: "key-1",
			}), ShouldBeNil)
		})
	})
}

// TestACPBackendCheck 证明 acp 行能通过整条 entity.Check，独占字段出现在别的
// type 上时被拒（与 hermes 的 hasHermesConfig 守卫同一形状）。
func TestACPBackendCheck(t *testing.T) {
	ctx := context.Background()

	t.Run("clean acp backend passes Check", func(t *testing.T) {
		b := &AgentBackend{
			Type:       string(TypeACP),
			Name:       "acp",
			EnvJSON:    "{}",
			ACPCommand: "hermes",
			ACPArgs:    []string{"acp"},
		}
		require.NoError(t, b.Check(ctx))
	})

	t.Run("acp backend without a command is rejected", func(t *testing.T) {
		b := &AgentBackend{Type: string(TypeACP), Name: "acp", EnvJSON: "{}"}
		require.Error(t, b.Check(ctx))
	})

	t.Run("command with a newline is rejected", func(t *testing.T) {
		b := &AgentBackend{Type: string(TypeACP), Name: "acp", EnvJSON: "{}", ACPCommand: "her\nmes"}
		require.Error(t, b.Check(ctx))
	})

	t.Run("arg element with a newline is rejected", func(t *testing.T) {
		b := &AgentBackend{Type: string(TypeACP), Name: "acp", EnvJSON: "{}", ACPCommand: "npx", ACPArgs: []string{"-y", "bad\narg"}}
		require.Error(t, b.Check(ctx))
	})

	t.Run("non-empty model_routes is rejected", func(t *testing.T) {
		b := &AgentBackend{
			Type: string(TypeACP), Name: "acp", EnvJSON: "{}", ACPCommand: "hermes",
			ModelRoutes: `{"OPUS":{"providerKey":"key-1"}}`,
		}
		require.Error(t, b.Check(ctx))
	})

	t.Run("cli path on acp is rejected", func(t *testing.T) {
		b := &AgentBackend{Type: string(TypeACP), Name: "acp", EnvJSON: "{}", ACPCommand: "hermes", CLIPath: "/usr/bin/hermes"}
		require.Error(t, b.Check(ctx))
	})

	t.Run("unknown type still fails Check", func(t *testing.T) {
		b := &AgentBackend{Type: "nosuch", Name: "x", EnvJSON: "{}"}
		require.Error(t, b.Check(ctx))
	})

	t.Run("empty name is rejected", func(t *testing.T) {
		b := &AgentBackend{Type: string(TypeACP), Name: " ", EnvJSON: "{}", ACPCommand: "hermes"}
		require.Error(t, b.Check(ctx))
	})

	t.Run("acp config on another type is rejected", func(t *testing.T) {
		b := &AgentBackend{Type: string(TypePiAgent), Name: "pi", EnvJSON: "{}", ACPCommand: "hermes"}
		require.Error(t, b.Check(ctx))
	})
}

// TestACPConfigRoundTrip 钉死 ACPCommand / ACPArgs 成对进出 config_json：
// 漏掉 Marshal 或 Unmarshal 的任何一侧都会变成「库里明明有、界面是空」。
// 空 slice 与 nil 的往返都算「没配」。
func TestACPConfigRoundTrip(t *testing.T) {
	t.Run("command and args round-trip", func(t *testing.T) {
		original := &AgentBackend{
			ACPCommand: "npx",
			ACPArgs:    []string{"-y", "@agentclientprotocol/codex-acp"},
		}
		require.NoError(t, original.MarshalConfig())

		restored := &AgentBackend{ConfigJSON: original.ConfigJSON}
		require.NoError(t, restored.UnmarshalConfig())
		assert.Equal(t, original.ACPCommand, restored.ACPCommand)
		assert.Equal(t, original.ACPArgs, restored.ACPArgs)
	})

	t.Run("nil args round-trip to nil-or-empty", func(t *testing.T) {
		original := &AgentBackend{ACPCommand: "gemini"}
		require.NoError(t, original.MarshalConfig())

		restored := &AgentBackend{ConfigJSON: original.ConfigJSON}
		require.NoError(t, restored.UnmarshalConfig())
		assert.Equal(t, "gemini", restored.ACPCommand)
		assert.LessOrEqual(t, len(restored.ACPArgs), 0)
	})

	t.Run("empty args do not leave a key behind", func(t *testing.T) {
		b := &AgentBackend{ACPCommand: "gemini", ACPArgs: []string{}}
		require.NoError(t, b.MarshalConfig())
		assert.NotContains(t, b.ConfigJSON, "acpArgs")
	})
}
