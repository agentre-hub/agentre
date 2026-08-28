package handlers_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/agentre-hub/agentre/internal/daemon/handlers"
	"github.com/agentre-hub/agentre/internal/daemon/handlers/mock_handlers"
	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/llm_provider_entity"
	"github.com/agentre-hub/agentre/internal/pkg/cliprober"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
)

// setupCLITest 构造 CLIHandlers 所需的两个 mock port，返回 ctx + mocks + 被测对象。
func setupCLITest(t *testing.T) (
	context.Context,
	*mock_handlers.MockGatewayPort,
	*mock_handlers.MockLLMProviderLookupPort,
	*handlers.CLIHandlers,
) {
	t.Helper()
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)
	mgw := mock_handlers.NewMockGatewayPort(ctrl)
	mpl := mock_handlers.NewMockLLMProviderLookupPort(ctrl)
	h := handlers.NewCLIHandlers(mgw, mpl)
	return context.Background(), mgw, mpl, h
}

// --- ResolvePath ---

func TestCLIResolvePath_Found(t *testing.T) {
	ctx, _, _, h := setupCLITest(t)
	handlers.SetResolveCLIPathFunc(func(typ string) (string, bool, error) {
		assert.Equal(t, "claudecode", typ)
		return "/usr/local/bin/claude", true, nil
	})
	t.Cleanup(handlers.ResetResolveCLIPathFunc)

	res, err := h.ResolvePath(ctx, handlers.CLIResolvePathParams{Type: "claudecode"})
	require.NoError(t, err)
	assert.Equal(t, "/usr/local/bin/claude", res.Path)
	assert.True(t, res.Found)
}

func TestCLIResolvePath_NotFound(t *testing.T) {
	ctx, _, _, h := setupCLITest(t)
	handlers.SetResolveCLIPathFunc(func(string) (string, bool, error) { return "", false, nil })
	t.Cleanup(handlers.ResetResolveCLIPathFunc)

	res, err := h.ResolvePath(ctx, handlers.CLIResolvePathParams{Type: "codex"})
	require.NoError(t, err)
	assert.Equal(t, "", res.Path)
	assert.False(t, res.Found)
}

func TestCLIResolvePath_InvalidType(t *testing.T) {
	ctx, _, _, h := setupCLITest(t)
	handlers.SetResolveCLIPathFunc(func(string) (string, bool, error) {
		return "", false, cliprober.ErrInvalidType
	})
	t.Cleanup(handlers.ResetResolveCLIPathFunc)

	_, err := h.ResolvePath(ctx, handlers.CLIResolvePathParams{Type: "nonsense"})
	require.Error(t, err)
	assert.ErrorIs(t, err, cliprober.ErrInvalidType)
}

// --- Probe ---

func TestCLIProbe_NoProvider_SkipsGateway(t *testing.T) {
	ctx, _, _, h := setupCLITest(t)
	// 没绑 provider 就不应该查 provider / 调 gateway；
	// gomock 默认对未声明的 EXPECT 报 unexpected call，等价 Times(0)。

	var captured cliprober.ProbeRequest
	handlers.SetProbeFunc(func(_ context.Context, req cliprober.ProbeRequest) (*cliprober.ProbeResponse, error) {
		captured = req
		return &cliprober.ProbeResponse{Text: "pong"}, nil
	})
	t.Cleanup(handlers.ResetProbeFunc)

	res, err := h.Probe(ctx, handlers.CLIProbeParams{
		BackendType: string(agent_backend_entity.TypeClaudeCode),
		CLIPath:     "/usr/local/bin/claude",
	})
	require.NoError(t, err)
	assert.Equal(t, "pong", res.Text)
	assert.Equal(t, "claudecode", captured.Type)
	assert.Equal(t, "/usr/local/bin/claude", captured.CLIPath)
	// 没 provider → env 不该含 gateway 注入项。
	_, hasGwURL := captured.Env["AGENTRE_GATEWAY_URL"]
	_, hasGwTok := captured.Env["AGENTRE_GATEWAY_TOKEN"]
	assert.False(t, hasGwURL)
	assert.False(t, hasGwTok)
}

func TestCLIProbe_WithProvider_ClaudeCode_HappyPath(t *testing.T) {
	ctx, mgw, mpl, h := setupCLITest(t)

	gomock.InOrder(
		mpl.EXPECT().FindByKey(ctx, "key-5").Return(&llm_provider_entity.LLMProvider{
			ProviderKey: "key-5", Type: "anthropic",
		}, nil),
		mgw.EXPECT().URL().Return("http://127.0.0.1:9090"),
		mgw.EXPECT().IssueToken(ctx, gomock.Any(), gomock.Any()).
			DoAndReturn(func(_ context.Context, b *agent_backend_entity.AgentBackend, ttl time.Duration) (string, error) {
				assert.Equal(t, "key-5", b.LLMProviderKey)
				assert.Equal(t, string(agent_backend_entity.TypeClaudeCode), b.Type)
				assert.Greater(t, ttl, time.Duration(0))
				return "tok-xyz", nil
			}),
		mgw.EXPECT().RevokeToken("tok-xyz"),
	)

	var captured cliprober.ProbeRequest
	handlers.SetProbeFunc(func(_ context.Context, req cliprober.ProbeRequest) (*cliprober.ProbeResponse, error) {
		captured = req
		return &cliprober.ProbeResponse{Text: "pong"}, nil
	})
	t.Cleanup(handlers.ResetProbeFunc)

	res, err := h.Probe(ctx, handlers.CLIProbeParams{
		BackendType:    string(agent_backend_entity.TypeClaudeCode),
		LLMProviderKey: "key-5",
	})
	require.NoError(t, err)
	assert.Equal(t, "pong", res.Text)
	// token / URL 必须出现在 env 里（claudecode 路径）。
	assert.Equal(t, "http://127.0.0.1:9090", captured.Env["AGENTRE_GATEWAY_URL"])
	assert.Equal(t, "tok-xyz", captured.Env["AGENTRE_GATEWAY_TOKEN"])
	// claudecode 不走 CodexConfigs。
	assert.Empty(t, captured.CodexConfigs)
}

func TestCLIProbe_WithProvider_Codex_PopulatesCodexConfigs(t *testing.T) {
	ctx, mgw, mpl, h := setupCLITest(t)

	mpl.EXPECT().FindByKey(ctx, "key-7").Return(&llm_provider_entity.LLMProvider{
		ProviderKey: "key-7", Type: "openai",
	}, nil)
	mgw.EXPECT().URL().Return("http://127.0.0.1:9090")
	mgw.EXPECT().IssueToken(ctx, gomock.Any(), gomock.Any()).Return("tok-abc", nil)
	mgw.EXPECT().RevokeToken("tok-abc")

	var captured cliprober.ProbeRequest
	handlers.SetProbeFunc(func(_ context.Context, req cliprober.ProbeRequest) (*cliprober.ProbeResponse, error) {
		captured = req
		return &cliprober.ProbeResponse{Text: "pong"}, nil
	})
	t.Cleanup(handlers.ResetProbeFunc)

	res, err := h.Probe(ctx, handlers.CLIProbeParams{
		BackendType:    string(agent_backend_entity.TypeCodex),
		LLMProviderKey: "key-7",
		Sandbox:        "workspace-write",
		Approval:       "on-request",
		Model:          "gpt-5-codex",
	})
	require.NoError(t, err)
	assert.Equal(t, "pong", res.Text)
	assert.Equal(t, "codex", captured.Type)
	assert.Equal(t, "workspace-write", captured.Sandbox)
	assert.Equal(t, "on-request", captured.Approval)
	assert.Equal(t, "gpt-5-codex", captured.Model)
	// codex 通过 OPENAI_API_KEY env 透传 token。
	assert.Equal(t, "tok-abc", captured.Env["OPENAI_API_KEY"])
	// codex 必须填了 -c 覆盖项。
	assert.NotEmpty(t, captured.CodexConfigs)
}

func TestCLIProbe_ProviderNotFound(t *testing.T) {
	ctx, _, mpl, h := setupCLITest(t)
	mpl.EXPECT().FindByKey(ctx, "key-404").Return(nil, errors.New("provider key-404 not configured"))

	_, err := h.Probe(ctx, handlers.CLIProbeParams{
		BackendType:    string(agent_backend_entity.TypeClaudeCode),
		LLMProviderKey: "key-404",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "provider")
}

func TestCLIProbe_GatewayURLEmpty(t *testing.T) {
	ctx, mgw, mpl, h := setupCLITest(t)
	mpl.EXPECT().FindByKey(ctx, "key-5").Return(&llm_provider_entity.LLMProvider{ProviderKey: "key-5"}, nil)
	mgw.EXPECT().URL().Return("")
	// 不应再调 IssueToken。

	_, err := h.Probe(ctx, handlers.CLIProbeParams{
		BackendType:    string(agent_backend_entity.TypeClaudeCode),
		LLMProviderKey: "key-5",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "gateway")
}

func TestCLIProbe_GatewayIssueTokenFails(t *testing.T) {
	ctx, mgw, mpl, h := setupCLITest(t)
	mpl.EXPECT().FindByKey(ctx, "key-5").Return(&llm_provider_entity.LLMProvider{ProviderKey: "key-5"}, nil)
	mgw.EXPECT().URL().Return("http://127.0.0.1:9090")
	mgw.EXPECT().IssueToken(ctx, gomock.Any(), gomock.Any()).Return("", errors.New("boom"))

	_, err := h.Probe(ctx, handlers.CLIProbeParams{
		BackendType:    string(agent_backend_entity.TypeClaudeCode),
		LLMProviderKey: "key-5",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "boom")
}

func TestCLIProbe_InvalidBackendType(t *testing.T) {
	ctx, _, _, h := setupCLITest(t)
	// backend 类型校验在 provider 查询之前；nonsense 直接报错，
	// 不调 providerLookup / gateway。
	_, err := h.Probe(ctx, handlers.CLIProbeParams{BackendType: "nonsense"})
	require.Error(t, err)
}

func TestCLIProbe_ProbeFuncError_Propagates(t *testing.T) {
	ctx, _, _, h := setupCLITest(t)
	handlers.SetProbeFunc(func(context.Context, cliprober.ProbeRequest) (*cliprober.ProbeResponse, error) {
		return nil, errors.New("cli exit 1")
	})
	t.Cleanup(handlers.ResetProbeFunc)

	_, err := h.Probe(ctx, handlers.CLIProbeParams{
		BackendType: string(agent_backend_entity.TypeClaudeCode),
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cli exit 1")
}
