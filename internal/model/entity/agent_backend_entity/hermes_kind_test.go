package agent_backend_entity

import (
	"context"
	"testing"

	. "github.com/smartystreets/goconvey/convey"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/agentre-hub/agentre/internal/model/entity/llm_provider_entity"
)

// TestHermesKind 钉死 hermes 的 kind 槽位：Hermes 自带 provider/model 配置，
// 因此永不参与 Agentre 的 new-session provider pill（ProviderTypeMatch 恒 false）。
// hermes 连一个已在运行的 `hermes serve`，所以它既不接受 cli_path，也不接受
// HERMES_HOME / 解释器参数，只接受 Server URL。
func TestHermesKind(t *testing.T) {
	Convey("Given the hermes backend type", t, func() {
		kind := KindFor(TypeHermes)

		Convey("When resolving kind metadata Then it is a URL backend that never matches an Agentre provider", func() {
			So(kind, ShouldNotBeNil)
			// kind 的身份由 backendKinds 的 key 承载（KindFor(t) 就是查那张表），
			// 没有单独的 Type() 可断言——上面那行非 nil 就是「这个类型已登记」。
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

		Convey("When validating a clean hermes backend Then its Server URL is required and accepted", func() {
			ctx := context.Background()
			So(kind.ValidateExtra(ctx, &AgentBackend{
				Type:      string(TypeHermes),
				Name:      "hermes",
				HermesURL: "http://127.0.0.1:9119",
			}), ShouldBeNil)
		})

		Convey("When the Server URL is missing or malformed Then validation rejects it", func() {
			ctx := context.Background()
			for _, raw := range []string{"", "127.0.0.1:9119", "http://127.0.0.1", "ftp://127.0.0.1:9119"} {
				err := kind.ValidateExtra(ctx, &AgentBackend{
					Type:      string(TypeHermes),
					Name:      "hermes",
					HermesURL: raw,
				})
				So(err, ShouldNotBeNil)
			}
		})

		Convey("When a CLI path or another type's field is set Then validation rejects it", func() {
			ctx := context.Background()
			cases := []*AgentBackend{
				{Type: string(TypeHermes), Name: "h", HermesURL: "http://127.0.0.1:9119", CLIPath: "/opt/hermes/venv/bin/python"},
				{Type: string(TypeHermes), Name: "h", HermesURL: "http://127.0.0.1:9119", LLMProviderKey: "key-1"},
				{Type: string(TypeHermes), Name: "h", HermesURL: "http://127.0.0.1:9119", LLMModelKey: "mk-1"},
				{Type: string(TypeHermes), Name: "h", HermesURL: "http://127.0.0.1:9119", ModelRoutes: `{"OPUS":{"providerKey":"key-1"}}`},
				{Type: string(TypeHermes), Name: "h", HermesURL: "http://127.0.0.1:9119", Sandbox: "read-only"},
				{Type: string(TypeHermes), Name: "h", HermesURL: "http://127.0.0.1:9119", Approval: "never"},
				{Type: string(TypeHermes), Name: "h", HermesURL: "http://127.0.0.1:9119", DefaultPermissionMode: "plan"},
				{Type: string(TypeHermes), Name: "h", HermesURL: "http://127.0.0.1:9119", DefaultModel: "gpt-5"},
				{Type: string(TypeHermes), Name: "h", HermesURL: "http://127.0.0.1:9119", ReasoningEffort: "high"},
			}
			for i, b := range cases {
				assert.Error(t, kind.ValidateExtra(ctx, b), "case %d must be rejected", i)
			}
		})
	})
}

// TestHermesBackendCheck 证明 hermes 行能通过整条 entity.Check，且 hermes 独占字段
// 出现在别的 type 上时被拒（与 openclaw 的 hasOpenClawConfig 守卫同一形状）。
func TestHermesBackendCheck(t *testing.T) {
	ctx := context.Background()

	t.Run("clean hermes backend passes Check", func(t *testing.T) {
		b := &AgentBackend{
			Type:      string(TypeHermes),
			Name:      "hermes",
			EnvJSON:   "{}",
			HermesURL: "http://127.0.0.1:9119",
		}
		require.NoError(t, b.Check(ctx))
	})

	t.Run("hermes backend without a Server URL is rejected", func(t *testing.T) {
		b := &AgentBackend{Type: string(TypeHermes), Name: "hermes", EnvJSON: "{}"}
		require.Error(t, b.Check(ctx))
	})

	t.Run("cli path on hermes is rejected", func(t *testing.T) {
		b := &AgentBackend{
			Type:      string(TypeHermes),
			Name:      "hermes",
			EnvJSON:   "{}",
			HermesURL: "http://127.0.0.1:9119",
			CLIPath:   "/opt/hermes/venv/bin/python",
		}
		require.Error(t, b.Check(ctx))
	})

	t.Run("hermes url on another type is rejected", func(t *testing.T) {
		b := &AgentBackend{
			Type:      string(TypePiAgent),
			Name:      "pi",
			EnvJSON:   "{}",
			HermesURL: "http://127.0.0.1:9119",
		}
		require.Error(t, b.Check(ctx))
	})

	t.Run("hermes auth display fields on another type are rejected", func(t *testing.T) {
		for _, b := range []*AgentBackend{
			{Type: string(TypePiAgent), Name: "pi", EnvJSON: "{}", HermesAuthProvider: "basic"},
			{Type: string(TypePiAgent), Name: "pi", EnvJSON: "{}", HermesUserID: "user-7"},
		} {
			require.Error(t, b.Check(ctx))
		}
	})

	t.Run("hermes backend carrying auth display fields passes Check", func(t *testing.T) {
		b := &AgentBackend{
			Type: string(TypeHermes), Name: "hermes", EnvJSON: "{}",
			HermesURL: "http://10.0.0.8:9119", HermesAuthProvider: "basic", HermesUserID: "user-7",
		}
		require.NoError(t, b.Check(ctx))
	})

	t.Run("hermes config round-trips through config_json", func(t *testing.T) {
		original := &AgentBackend{
			Type:      string(TypeHermes),
			HermesURL: "https://hermes.example.com:443",
		}
		require.NoError(t, original.MarshalConfig())

		restored := &AgentBackend{ConfigJSON: original.ConfigJSON}
		require.NoError(t, restored.UnmarshalConfig())
		assert.Equal(t, original.HermesURL, restored.HermesURL)
	})
}

// TestNormalizeHermesURL 锁死 hermes Server URL 的规范形式：只接受 http(s)://host:port
// 与 ws(s)://host:port，统一归一成 http(s)://host:port（接 WS 时运行时再换 scheme）。
func TestNormalizeHermesURL(t *testing.T) {
	cases := []struct {
		name    string
		in      string
		want    string
		wantErr error
	}{
		{name: "http is canonical", in: "http://127.0.0.1:9119", want: "http://127.0.0.1:9119"},
		{name: "ws folds to http", in: "ws://127.0.0.1:9119", want: "http://127.0.0.1:9119"},
		{name: "https is canonical", in: "https://hermes.example.com:443", want: "https://hermes.example.com:443"},
		{name: "wss folds to https", in: "wss://hermes.example.com:443", want: "https://hermes.example.com:443"},
		{name: "scheme case is folded", in: "HTTP://127.0.0.1:9119", want: "http://127.0.0.1:9119"},
		{name: "host case is folded", in: "http://LOCALHOST:9119", want: "http://localhost:9119"},
		{name: "root slash is dropped", in: "http://127.0.0.1:9119/", want: "http://127.0.0.1:9119"},
		{name: "surrounding space is trimmed", in: "  http://127.0.0.1:9119  ", want: "http://127.0.0.1:9119"},
		{name: "ipv6 host", in: "http://[::1]:9119", want: "http://[::1]:9119"},
		{name: "empty is required", in: "", wantErr: ErrHermesURLInvalid},
		{name: "scheme is required", in: "127.0.0.1:9119", wantErr: ErrHermesURLInvalid},
		{name: "unsupported scheme", in: "ftp://127.0.0.1:9119", wantErr: ErrHermesURLInvalid},
		{name: "port is required", in: "http://127.0.0.1", wantErr: ErrHermesURLInvalid},
		{name: "host is required", in: "http://:9119", wantErr: ErrHermesURLInvalid},
		{name: "credentials are rejected", in: "http://user:pass@127.0.0.1:9119", wantErr: ErrHermesURLInvalid}, //nolint:gosec // test fixture: a URL that must be rejected, not a real credential
		{name: "query is rejected", in: "http://127.0.0.1:9119/?a=1", wantErr: ErrHermesURLInvalid},
		{name: "fragment is rejected", in: "http://127.0.0.1:9119/#f", wantErr: ErrHermesURLInvalid},
		{name: "path is rejected", in: "http://127.0.0.1:9119/api", wantErr: ErrHermesURLInvalid},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := NormalizeHermesURL(tc.in)
			if tc.wantErr != nil {
				require.Error(t, err)
				assert.ErrorIs(t, err, tc.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}
