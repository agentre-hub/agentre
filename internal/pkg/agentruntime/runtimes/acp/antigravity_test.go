package acp

import (
	"context"
	"testing"
	"time"

	acpsdk "github.com/coder/acp-go-sdk"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
	"github.com/agentre-hub/agentre/internal/pkg/clienv"
)

// agyAuthMethods 是 agy 1.5.16 的 initialize 实测通告的认证方式。
var agyAuthMethods = []string{"oauth-personal", "oauth-business", "gemini-api-key", "agent-platform"}

func antigravityTestBackend() *agent_backend_entity.AgentBackend {
	return &agent_backend_entity.AgentBackend{
		Type:    string(agent_backend_entity.TypeAntigravity),
		Name:    "antigravity",
		EnvJSON: "{}",
	}
}

func antigravityRequest(t *testing.T, sessionID int64) agentruntime.RunRequest {
	return agentruntime.RunRequest{
		Backend:      antigravityTestBackend(),
		SessionID:    sessionID,
		UserText:     "hi",
		Cwd:          t.TempDir(),
		GatewayURL:   "http://127.0.0.1:60080",
		GatewayToken: "sess-gw-tok",
		Effective:    &agentruntime.EffectiveLLMConfig{ProviderKey: "gem-1", ModelKey: "m-1", ModelID: "gemini-3-pro-preview"},
	}
}

func newAntigravityTestRuntime() *Runtime { return newRuntime(nil, antigravityProfile) }

// finishOneTurn 应答本轮 prompt 并收完事件流。
func finishOneTurn(t *testing.T, fa *fakeAgent, events <-chan agentruntime.Event) {
	t.Helper()
	fa.waitPrompt(2 * time.Second)
	fa.respondPrompt(acpsdk.StopReasonEndTurn, nil)
	drainEvents(t, events)
}

// 两种 ACP 后端各自登记在自己的类型下:同一个 runtime 实现,两个实例。
func TestAntigravity_RegisteredUnderItsOwnType(t *testing.T) {
	agy := agentruntime.RuntimeFor(agent_backend_entity.TypeAntigravity)
	require.NotNil(t, agy, "importing the acp runtime package must register antigravity (desktop chat_svc and agentred both import it)")
	generic := agentruntime.RuntimeFor(agent_backend_entity.TypeACP)
	require.NotNil(t, generic)
	assert.NotSame(t, generic, agy, "antigravity must not share the generic acp instance (its launch/env/auth differ)")
	assert.Equal(t, generic.Capabilities(), agy.Capabilities(), "interrupt / approval / image / context window / resume are the ACP runtime's, unchanged")
}

// 启动参数:默认按 agy 在 PATH 上找,cli_path(含按设备覆盖)优先;argv 固定 acp;
// env 带网关三件套,继承的 Google 凭证变量交给 UnsetEnv 清掉。
func TestAntigravity_LaunchSpec(t *testing.T) {
	t.Run("default executable is agy with the fixed acp argument", func(t *testing.T) {
		fa := installFakeAgent(t, fakeAgentOptions{authMethods: agyAuthMethods})
		events, _, err := newAntigravityTestRuntime().Run(context.Background(), antigravityRequest(t, 61))
		require.NoError(t, err)
		finishOneTurn(t, fa, events)

		specs := fa.launchSpecs()
		require.Len(t, specs, 1)
		spec := specs[0]
		assert.Equal(t, "agy", spec.command)
		assert.Equal(t, []string{"acp"}, spec.args)
		gemKey, _ := clienv.Lookup(spec.env, "GEMINI_API_KEY")
		baseURL, _ := clienv.Lookup(spec.env, "GOOGLE_GEMINI_BASE_URL")
		noUpdate, _ := clienv.Lookup(spec.env, "AGY_CLI_DISABLE_AUTO_UPDATE")
		assert.Equal(t, "sess-gw-tok", gemKey)
		assert.Equal(t, "http://127.0.0.1:60080", baseURL)
		assert.Equal(t, "1", noUpdate)
		assert.ElementsMatch(t, agentruntime.AntigravityClearedEnv(), spec.unsetEnv)
	})
	t.Run("cli_path overrides the executable", func(t *testing.T) {
		fa := installFakeAgent(t, fakeAgentOptions{authMethods: agyAuthMethods})
		req := antigravityRequest(t, 62)
		req.Backend.CLIPath = "/opt/agy/bin/agy"
		events, _, err := newAntigravityTestRuntime().Run(context.Background(), req)
		require.NoError(t, err)
		finishOneTurn(t, fa, events)

		specs := fa.launchSpecs()
		require.Len(t, specs, 1)
		assert.Equal(t, "/opt/agy/bin/agy", specs[0].command)
		assert.Equal(t, []string{"acp"}, specs[0].args)
	})
	t.Run("no gateway token fails before spawning", func(t *testing.T) {
		fa := installFakeAgent(t, fakeAgentOptions{authMethods: agyAuthMethods})
		req := antigravityRequest(t, 63)
		req.GatewayToken = ""
		_, _, err := newAntigravityTestRuntime().Run(context.Background(), req)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "gateway")
		assert.Empty(t, fa.launchSpecs(), "agy must not start without the gateway credential")
	})
}

// 握手:initialize → authenticate(gemini-api-key) → session/new|load,新建与续话两条路都先认证。
func TestAntigravity_AuthenticatesBeforeSession(t *testing.T) {
	t.Run("new session", func(t *testing.T) {
		fa := installFakeAgent(t, fakeAgentOptions{authMethods: agyAuthMethods})
		events, _, err := newAntigravityTestRuntime().Run(context.Background(), antigravityRequest(t, 64))
		require.NoError(t, err)
		finishOneTurn(t, fa, events)

		assert.Equal(t, []string{"gemini-api-key"}, fa.authenticateIDs())
		assert.Equal(t, []string{"initialize", "authenticate", "session/new", "session/prompt"}, fa.methodCalls())
	})
	t.Run("resumed session", func(t *testing.T) {
		fa := installFakeAgent(t, fakeAgentOptions{authMethods: agyAuthMethods, loadSession: true})
		req := antigravityRequest(t, 65)
		req.ProviderSessionID = "agy-sess-9"
		events, _, err := newAntigravityTestRuntime().Run(context.Background(), req)
		require.NoError(t, err)
		finishOneTurn(t, fa, events)

		assert.Equal(t, []string{"initialize", "authenticate", "session/load", "session/prompt"}, fa.methodCalls())
		assert.Equal(t, "agy-sess-9", fa.lastLoadedSessionID())
	})
}

// 认证失败两种形态:对方没通告 gemini-api-key,或 authenticate 回错。都让这一轮失败,
// 错误带对方的报错与 stderr 尾巴,且绝不继续发 session/new。
func TestAntigravity_AuthenticationFailureFailsTheTurn(t *testing.T) {
	t.Run("method not offered", func(t *testing.T) {
		fa := installFakeAgent(t, fakeAgentOptions{
			authMethods: []string{"oauth-personal"},
			stderr:      "agy: starting acp server\n",
		})
		_, _, err := newAntigravityTestRuntime().Run(context.Background(), antigravityRequest(t, 66))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "gemini-api-key")
		assert.Contains(t, err.Error(), "oauth-personal", "name what the agent did offer")
		assert.Contains(t, err.Error(), "agy: starting acp server")
		assert.Empty(t, fa.authenticateIDs(), "never authenticate with a method the agent did not advertise")
		assert.Equal(t, 0, fa.newSessionCalls())
	})
	t.Run("authenticate returns an error", func(t *testing.T) {
		fa := installFakeAgent(t, fakeAgentOptions{
			authMethods: agyAuthMethods,
			authError:   "GEMINI_API_KEY is not set",
			stderr:      "agy: auth failed\n",
		})
		_, _, err := newAntigravityTestRuntime().Run(context.Background(), antigravityRequest(t, 67))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "GEMINI_API_KEY is not set")
		assert.Contains(t, err.Error(), "agy: auth failed")
		assert.Equal(t, 0, fa.newSessionCalls())
		assert.Equal(t, 0, fa.loadSessionCalls())
	})
}

// 通用 acp 后端从不调用 authenticate —— 即便对方通告了 gemini-api-key。
func TestGenericACP_NeverAuthenticates(t *testing.T) {
	fa := installFakeAgent(t, fakeAgentOptions{authMethods: agyAuthMethods})
	events, result, err := New().Run(context.Background(), agentruntime.RunRequest{
		Backend: acpTestBackend(), SessionID: 68, UserText: "hi", Cwd: t.TempDir(),
	})
	require.NoError(t, err)
	finishOneTurn(t, fa, events)

	assert.Empty(t, fa.authenticateIDs())
	assert.Equal(t, []string{"initialize", "session/new", "session/prompt"}, fa.methodCalls())
	specs := fa.launchSpecs()
	require.Len(t, specs, 1)
	assert.Empty(t, specs[0].unsetEnv, "generic acp inherits the environment untouched")
	assert.Empty(t, result.Model)
}

// 模型:用户在 Agentre 里选的供应商模型就是实际调用的模型(网关按 token 改写路径模型),
// RunResult.Model 报这一个,与 codex 绑 provider 时同一口径。
func TestAntigravity_ReportsEffectiveModel(t *testing.T) {
	fa := installFakeAgent(t, fakeAgentOptions{authMethods: agyAuthMethods})
	events, result, err := newAntigravityTestRuntime().Run(context.Background(), antigravityRequest(t, 69))
	require.NoError(t, err)
	finishOneTurn(t, fa, events)
	assert.Equal(t, "gemini-3-pro-preview", result.Model)
}

// 池键带后端类型:共享进程级池时,同号会话的 acp 与 antigravity 进程互不顶掉。
func TestAntigravity_PoolKeyDoesNotCollideWithGenericACP(t *testing.T) {
	fa := installFakeAgent(t, fakeAgentOptions{authMethods: agyAuthMethods})
	pool := agentruntime.NewCLISessionPool(agentruntime.DefaultCLISessionIdleCap)
	generic := NewWithPool(pool)
	agy := newRuntime(pool, antigravityProfile)

	events, _, err := generic.Run(context.Background(), agentruntime.RunRequest{
		Backend: acpTestBackend(), SessionID: 70, UserText: "hi", Cwd: t.TempDir(),
	})
	require.NoError(t, err)
	finishOneTurn(t, fa, events)
	events, _, err = agy.Run(context.Background(), antigravityRequest(t, 70))
	require.NoError(t, err)
	finishOneTurn(t, fa, events)

	_, ok := pool.Get(agentruntime.SessionPoolKey(agent_backend_entity.TypeACP, 70))
	assert.True(t, ok, "generic acp session must survive the antigravity turn")
	_, ok = pool.Get(agentruntime.SessionPoolKey(agent_backend_entity.TypeAntigravity, 70))
	assert.True(t, ok)
	assert.Len(t, fa.launchSpecs(), 2)
}

// cli_path 指向不存在的文件时,这一轮失败,错误点名 agy 与所用路径,不提 acpCommand;
// 通用 acp 后端的措辞不变。走真实的 spawn(不装 fake agent)。
func TestBinaryNotFound_MessageIsChosenByProfile(t *testing.T) {
	t.Run("antigravity names agy and the path", func(t *testing.T) {
		req := antigravityRequest(t, 70)
		req.Backend.CLIPath = "/nonexistent/agy"
		_, _, err := newAntigravityTestRuntime().Run(context.Background(), req)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "agy")
		assert.Contains(t, err.Error(), "/nonexistent/agy")
		assert.NotContains(t, err.Error(), "acpCommand")
	})
	t.Run("generic acp keeps its wording", func(t *testing.T) {
		req := agentruntime.RunRequest{
			Backend:   &agent_backend_entity.AgentBackend{Type: string(agent_backend_entity.TypeACP), ACPCommand: "/nonexistent/acp-agent", EnvJSON: "{}"},
			SessionID: 71, UserText: "hi", Cwd: t.TempDir(),
		}
		_, _, err := newRuntime(nil, acpProfile).Run(context.Background(), req)
		require.Error(t, err)
		assert.ErrorIs(t, err, errAgentBinaryNotFound)
	})
}
