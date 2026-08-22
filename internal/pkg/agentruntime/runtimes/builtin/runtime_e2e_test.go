package builtin

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/cago-frame/agents/agent"
	"github.com/cago-frame/agents/provider"
	"github.com/cago-frame/agents/provider/providertest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/agentre-ai/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-ai/agentre/internal/model/entity/llm_provider_entity"
	"github.com/agentre-ai/agentre/internal/pkg/agentruntime"
)

// TestRun_HappyPath_EmitsTextDelta 验证 Run() 把 cago provider 的 stream 翻成
// 新 sealed agentruntime.TextDelta;Usage / SessionID 落 *RunResult。镜像现有
// 顶层 builtin_test.go TestBuiltinRunner_HappyPath,但 emit 类型从 RuntimeEvent
// 改为 sealed Event。
func TestRun_HappyPath_EmitsTextDelta(t *testing.T) {
	t.Setenv("AGENTRE_DATA_DIR", t.TempDir())

	fp := providertest.New().
		QueueStream(
			provider.StreamChunk{ContentDelta: "hel"},
			provider.StreamChunk{ContentDelta: "lo"},
			provider.StreamChunk{FinishReason: provider.FinishStop, Usage: &provider.Usage{PromptTokens: 3, CompletionTokens: 2}},
		)
	SetBuiltinProviderBuilderForTest(func(_ *llm_provider_entity.LLMProvider) (provider.Provider, error) {
		return fp, nil
	})
	t.Cleanup(ResetBuiltinProviderBuilderForTest)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	r := New()
	events, result, err := r.Run(ctx, agentruntime.RunRequest{
		Backend:      &agent_backend_entity.AgentBackend{ID: 7, Type: "builtin", LLMProviderKey: "key-11"},
		Provider:     &llm_provider_entity.LLMProvider{ID: 11, Type: string(llm_provider_entity.TypeAnthropic)},
		Effective:    &agentruntime.EffectiveLLMConfig{ProviderKey: "key-11", ProviderType: string(llm_provider_entity.TypeAnthropic), ModelID: "claude-test"},
		AgentID:      99,
		SessionID:    42,
		SystemPrompt: "test sys",
		UserText:     "ping",
	})
	require.NoError(t, err)
	require.NotNil(t, result)

	var deltas []string
	for ev := range events {
		if td, ok := ev.(agentruntime.TextDelta); ok {
			deltas = append(deltas, td.Text)
		}
	}
	assert.Equal(t, []string{"hel", "lo"}, deltas)
	assert.Equal(t, "builtin-42", result.ProviderSessionID)
	assert.NoError(t, result.StopErr)
	if assert.NotNil(t, result.Usage) {
		assert.Equal(t, 3, result.Usage.PromptTokens)
		assert.Equal(t, 2, result.Usage.CompletionTokens)
	}
}

// TestRun_BatchesConsecutiveSteerConsumed 钉死 Part 0 §1.10 在新子包仍成立:
// 同安全点连续到达的多条 SteerConsumed 在 Run() 层合并成单个 sealed
// SteerConsumed{Steers:[batch]} emit,与顶层 builtin.go 等价。
func TestRun_BatchesConsecutiveSteerConsumed(t *testing.T) {
	t.Setenv("AGENTRE_DATA_DIR", t.TempDir())

	firstReady := make(chan struct{})
	releaseFirst := make(chan struct{})
	fp := providertest.New().
		QueueStreamFunc(func(ctx context.Context) <-chan provider.StreamChunk {
			ch := make(chan provider.StreamChunk)
			go func() {
				defer close(ch)
				close(firstReady)
				select {
				case <-releaseFirst:
				case <-ctx.Done():
					return
				}
				ch <- provider.StreamChunk{FinishReason: provider.FinishStop}
			}()
			return ch
		}).
		QueueStream(
			provider.StreamChunk{ContentDelta: "after batch"},
			provider.StreamChunk{FinishReason: provider.FinishStop, Usage: &provider.Usage{PromptTokens: 6, CompletionTokens: 2}},
		)
	SetBuiltinProviderBuilderForTest(func(_ *llm_provider_entity.LLMProvider) (provider.Provider, error) {
		return fp, nil
	})
	t.Cleanup(ResetBuiltinProviderBuilderForTest)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	r := New()
	events, _, err := r.Run(ctx, agentruntime.RunRequest{
		Backend:   &agent_backend_entity.AgentBackend{ID: 7, Type: "builtin", LLMProviderKey: "key-11"},
		Provider:  &llm_provider_entity.LLMProvider{ID: 11, Type: string(llm_provider_entity.TypeAnthropic)},
		Effective: &agentruntime.EffectiveLLMConfig{ProviderKey: "key-11", ProviderType: string(llm_provider_entity.TypeAnthropic), ModelID: "m"},
		AgentID:   99,
		SessionID: 42,
		UserText:  "first",
	})
	require.NoError(t, err)

	select {
	case <-firstReady:
	case <-ctx.Done():
		t.Fatal("timed out waiting for first stream")
	}
	require.NoError(t, r.Steer(ctx, 42, "qid-1", "f1"))
	require.NoError(t, r.Steer(ctx, 42, "qid-2", "f2"))
	require.NoError(t, r.Steer(ctx, 42, "qid-3", "f3"))
	close(releaseFirst)

	var steerBatches [][]agentruntime.ConsumedSteer
	for ev := range events {
		if sc, ok := ev.(agentruntime.SteerConsumed); ok {
			steerBatches = append(steerBatches, sc.Steers)
		}
	}
	// 三条同安全点的 steer 必须合并成单一 SteerConsumed,而不是三条独立帧。
	require.Len(t, steerBatches, 1, "expected 1 batched SteerConsumed, got %d", len(steerBatches))
	require.Len(t, steerBatches[0], 3)
	assert.Equal(t, "qid-1", steerBatches[0][0].QueuedID)
	assert.Equal(t, "qid-2", steerBatches[0][1].QueuedID)
	assert.Equal(t, "qid-3", steerBatches[0][2].QueuedID)
}

func TestRun_NilBackendOrProviderReturnsError(t *testing.T) {
	r := New()
	_, _, err := r.Run(context.Background(), agentruntime.RunRequest{})
	require.Error(t, err)

	_, _, err = r.Run(context.Background(), agentruntime.RunRequest{
		Backend: &agent_backend_entity.AgentBackend{},
	})
	require.Error(t, err)
}

func TestSteer_NoActiveReturnsErr(t *testing.T) {
	r := New()
	err := r.Steer(context.Background(), 42, "qid", "text")
	assert.True(t, errors.Is(err, agentruntime.ErrNoActiveTurn))
}

func TestSteer_MapsCagoErrSteerNoActiveTurn(t *testing.T) {
	r := New()
	fake := &fakeSteerable{steerErr: agent.ErrSteerNoActiveTurn}
	r.register(42, &builtinActive{runner: fake})
	err := r.Steer(context.Background(), 42, "qid", "text")
	assert.True(t, errors.Is(err, agentruntime.ErrNoActiveTurn))
}

func TestAbort_NoActiveReturnsErr(t *testing.T) {
	r := New()
	_, err := r.Abort(context.Background(), 42, 0)
	assert.True(t, errors.Is(err, agentruntime.ErrNoActiveTurn))
}

func TestCancelSteer_NoActiveReturnsErr(t *testing.T) {
	r := New()
	_, err := r.CancelSteer(context.Background(), 42, "qid")
	assert.True(t, errors.Is(err, agentruntime.ErrNoActiveTurn))
}

func TestCancelSteer_ByIDHitAndMiss(t *testing.T) {
	r := New()
	fake := &fakeSteerable{removeOK: map[string]bool{"qid-hit": true}}
	r.register(42, &builtinActive{runner: fake})

	ids, err := r.CancelSteer(context.Background(), 42, "qid-hit")
	require.NoError(t, err)
	assert.Equal(t, []string{"qid-hit"}, ids)

	_, err = r.CancelSteer(context.Background(), 42, "qid-miss")
	assert.True(t, errors.Is(err, agentruntime.ErrSteerNotFound))
}

func TestCancelSteer_ClearAll(t *testing.T) {
	r := New()
	fake := &fakeSteerable{clearReturns: []string{"a", "b"}}
	r.register(42, &builtinActive{runner: fake})

	ids, err := r.CancelSteer(context.Background(), 42, "")
	require.NoError(t, err)
	assert.Equal(t, []string{"a", "b"}, ids)
}

// TestRun_UsesProviderModel 锁住 builtin 的模型解析:#26 会话级模型覆盖已移除,
// effectiveModel 直接回落 provider.Model,经 coding.WithModel 下发给 provider
// (进程内每轮重建,无子进程/缓存问题),RunResult.Model 同步为 effectiveModel。
func TestRun_UsesProviderModel(t *testing.T) {
	t.Setenv("AGENTRE_DATA_DIR", t.TempDir())

	fp := providertest.New().
		QueueStream(provider.StreamChunk{FinishReason: provider.FinishStop, Usage: &provider.Usage{PromptTokens: 1, CompletionTokens: 1}})
	SetBuiltinProviderBuilderForTest(func(_ *llm_provider_entity.LLMProvider) (provider.Provider, error) {
		return fp, nil
	})
	t.Cleanup(ResetBuiltinProviderBuilderForTest)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	r := New()
	events, result, err := r.Run(ctx, agentruntime.RunRequest{
		Backend:   &agent_backend_entity.AgentBackend{ID: 7, Type: "builtin", LLMProviderKey: "key-11"},
		Provider:  &llm_provider_entity.LLMProvider{ID: 11, Type: string(llm_provider_entity.TypeAnthropic)},
		Effective: &agentruntime.EffectiveLLMConfig{ProviderKey: "key-11", ProviderType: string(llm_provider_entity.TypeAnthropic), ModelID: "claude-test"},
		AgentID:   99,
		SessionID: 42,
		UserText:  "ping",
	})
	require.NoError(t, err)
	require.NotNil(t, result)
	for range events {
	}

	assert.Equal(t, "claude-test", result.Model)
	reqs := fp.Received()
	require.Len(t, reqs, 1)
	assert.Equal(t, "claude-test", reqs[0].Model)
}

type fakeSteerable struct {
	steerErr     error
	removeOK     map[string]bool
	clearReturns []string
}

func (f *fakeSteerable) Steer(_ context.Context, _ string, _ ...agent.SteerOption) error {
	return f.steerErr
}
func (f *fakeSteerable) RemovePendingSteer(id string) bool {
	return f.removeOK[id]
}
func (f *fakeSteerable) ClearPendingSteers() []string {
	return f.clearReturns
}

// TestRun_WebInitiatedFreeSessionResolvesCwdFromSyncID 与 claudecode / codex / piagent
// 那三条同源(2026-08-22 的 AgentCwd(0) 报错):web 发起的对话在这里同样是 AgentID=0 +
// Cwd 空,兜底目录改由账号级 AgentSyncID 定。本包没有观察 cwd 的接缝,因此看落地结果 ——
// ResolveAgentCwd 会把目录建出来。
func TestRun_WebInitiatedFreeSessionResolvesCwdFromSyncID(t *testing.T) {
	dataDir := t.TempDir()
	t.Setenv("AGENTRE_DATA_DIR", dataDir)

	fp := providertest.New().
		QueueStream(provider.StreamChunk{ContentDelta: "ok"},
			provider.StreamChunk{FinishReason: provider.FinishStop})
	SetBuiltinProviderBuilderForTest(func(_ *llm_provider_entity.LLMProvider) (provider.Provider, error) {
		return fp, nil
	})
	t.Cleanup(ResetBuiltinProviderBuilderForTest)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	events, _, err := New().Run(ctx, agentruntime.RunRequest{
		Backend:     &agent_backend_entity.AgentBackend{ID: 7, Type: "builtin", LLMProviderKey: "key-11"},
		Provider:    &llm_provider_entity.LLMProvider{ID: 11, Type: string(llm_provider_entity.TypeAnthropic)},
		Effective:   &agentruntime.EffectiveLLMConfig{ProviderKey: "key-11", ProviderType: string(llm_provider_entity.TypeAnthropic), ModelID: "claude-test"},
		AgentID:     0,
		AgentSyncID: "01KZNE7YKJQ6A79YVDCMW1A63R",
		SessionID:   42,
		UserText:    "ping",
	})
	require.NoError(t, err)
	for range events { //nolint:revive // drain
	}

	info, err := os.Stat(filepath.Join(dataDir, "agents", "sync-01KZNE7YKJQ6A79YVDCMW1A63R"))
	require.NoError(t, err)
	assert.True(t, info.IsDir())
}
