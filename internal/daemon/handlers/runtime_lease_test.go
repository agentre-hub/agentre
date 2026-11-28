package handlers_test

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/cago-frame/cago/pkg/consts"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/agentre-hub/agentre/internal/daemon/enginesnapshot"
	"github.com/agentre-hub/agentre/internal/daemon/handlers"
	"github.com/agentre-hub/agentre/internal/daemon/handlers/mock_handlers"
	"github.com/agentre-hub/agentre/internal/daemon/leases"
	"github.com/agentre-hub/agentre/internal/daemon/state"
	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/llm_provider_entity"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
	"github.com/agentre-hub/agentre/pkg/syncwire"
	"github.com/agentre-hub/agentre/pkg/wire/agentrewire"
	"github.com/agentre-hub/agentre/pkg/wire/rpcerror"
)

// recordingRunLeases 记下每一轮附带的租约,以及这一轮的 release 有没有被调到。
type recordingRunLeases struct {
	mu       sync.Mutex
	attached [][]leases.Lease
	holders  []string
	released int
}

func (r *recordingRunLeases) Attach(holder string, ls []leases.Lease) func() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.attached = append(r.attached, ls)
	r.holders = append(r.holders, holder)
	var once sync.Once
	return func() {
		once.Do(func() {
			r.mu.Lock()
			defer r.mu.Unlock()
			r.released++
		})
	}
}

func (r *recordingRunLeases) releasedCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.released
}

func setupRuntimeLeaseTest(t *testing.T, rt agentruntime.Runtime, runLeases handlers.RunLeasePort) (
	context.Context, *recordingOutbound, *mock_handlers.MockGatewayPort, *mock_handlers.MockLLMProviderLookupPort, *handlers.RuntimeHandlers,
) {
	t.Helper()
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)
	notif := newRecordingOutbound()
	gw := mock_handlers.NewMockGatewayPort(ctrl)
	lookup := mock_handlers.NewMockLLMProviderLookupPort(ctrl)
	sess := newRecordingSessions()
	h := handlers.NewRuntimeHandlers(handlers.RuntimeDeps{
		NotifyFor: notif.notifierFor, Sessions: sess, SessionQuery: sess, Gateway: gw, Lookup: lookup,
		RuntimeFor: func(agent_backend_entity.BackendType) agentruntime.Runtime { return rt },
		RunLeases:  runLeases,
	})
	return context.Background(), notif, gw, lookup, h
}

// Given 桌面端经直连发来的一轮附带了供应商租约,When 执行,Then 租约在解析供应商之前就交给
// 租约缓存(只在内存),这一轮还在跑时一直留着,一轮结束才丢掉。
func TestRuntime_Run_GivenAttachedLeases_WhenTheRunEnds_ThenTheyAreHeldForTheRunAndDroppedAfter(t *testing.T) {
	events := make(chan agentruntime.Event)
	rt := &fullRT{}
	rt.runFn = func(context.Context) (<-chan agentruntime.Event, *agentruntime.RunResult, error) {
		return events, &agentruntime.RunResult{}, nil
	}
	runLeases := &recordingRunLeases{}
	ctx, notif, _, _, h := setupRuntimeLeaseTest(t, rt, runLeases)
	expires := time.Now().Add(24 * time.Hour).UnixMilli()

	_, err := h.Run(ctx, &agentrewire.RuntimeRunRequest{
		Backend:        backendProto(t, agent_backend_entity.AgentBackend{Type: string(agent_backend_entity.TypeClaudeCode)}),
		ConversationId: convID(301),
		ProviderLeases: []*agentrewire.ProviderLease{{
			ProviderKey: "pk-1", ApiKey: "desk-key", BaseUrl: "https://desk.example", ExpiresAt: expires,
			Type: "anthropic", Name: "Desk", DefaultModelKey: "mk",
			Models: []*agentrewire.LLMModel{{ModelKey: "mk", ModelId: "claude-x", Name: "X", Enabled: true}},
		}},
	})
	require.NoError(t, err)
	require.Len(t, runLeases.attached, 1)
	assert.Equal(t, []leases.Lease{{
		ProviderKey: "pk-1", APIKey: "desk-key", BaseURL: "https://desk.example", ExpiresAt: time.UnixMilli(expires),
		Provider: &state.LLMProviderMeta{
			Name: "Desk", Type: "anthropic", BaseURL: "https://desk.example", DefaultModelKey: "mk",
			Models: []state.LLMModelMeta{{ModelKey: "mk", ModelID: "claude-x", Name: "X", Enabled: true}},
		},
	}}, runLeases.attached[0])
	assert.Zero(t, runLeases.releasedCount(), "the run is still going: its leases stay")

	close(events)
	_ = notif.waitFrames(t, 1)
	require.Eventually(t, func() bool { return runLeases.releasedCount() == 1 }, 2*time.Second, 10*time.Millisecond)
}

// 这一轮没开起来(解析供应商失败)时,附带的租约当场丢掉。
func TestRuntime_Run_GivenAttachedLeases_WhenTheRunFailsToStart_ThenTheyAreDroppedRightAway(t *testing.T) {
	rt := &fullRT{}
	runLeases := &recordingRunLeases{}
	ctx, _, _, lookup, h := setupRuntimeLeaseTest(t, rt, runLeases)
	lookup.EXPECT().FindByKey(gomock.Any(), "pk-1").Return(nil, fmt.Errorf("provider %q not configured", "pk-1"))

	_, err := h.Run(ctx, &agentrewire.RuntimeRunRequest{
		Backend:        backendProto(t, agent_backend_entity.AgentBackend{Type: string(agent_backend_entity.TypeClaudeCode)}),
		ConversationId: convID(302), LlmProviderKey: "pk-1", LlmModelKey: "mk-1",
		ProviderLeases: []*agentrewire.ProviderLease{{ProviderKey: "pk-1", ApiKey: "desk-key", ExpiresAt: time.Now().Add(time.Hour).UnixMilli()}},
	})
	require.Error(t, err)
	assert.Equal(t, 1, runLeases.releasedCount())
	assert.Empty(t, rt.runReqs)
}

// Given 账号供应商此刻拿不到租约,When 开一轮,Then 以「凭据租约不可用」失败(桌面端显示
// 「凭据租约过期，账号服务器连不上」),不是「供应商没配」,也不回退到别的供应商。
func TestRuntime_Run_GivenNoLease_WhenStarting_ThenFailsWithLeaseUnavailable(t *testing.T) {
	rt := &fullRT{}
	ctx, _, _, lookup, h := setupRuntimeLeaseTest(t, rt, nil)
	lookup.EXPECT().FindByKey(gomock.Any(), "pk-session").Return(nil, fmt.Errorf("provider %q: %w", "pk-session", rpcerror.ErrProviderLeaseUnavailable))
	be := agent_backend_entity.AgentBackend{Type: string(agent_backend_entity.TypeClaudeCode), LLMProviderKey: "pk-bound"}

	for i, modelKey := range []string{"mk-fixed", ""} {
		_, err := h.Run(ctx, &agentrewire.RuntimeRunRequest{
			Backend: backendProto(t, be), ConversationId: convID(int64(303 + i)), LlmProviderKey: "pk-session", LlmModelKey: modelKey,
		})
		require.ErrorIs(t, err, rpcerror.ErrProviderLeaseUnavailable, "modelKey=%q", modelKey)
		if i == 0 {
			lookup.EXPECT().FindByKey(gomock.Any(), "pk-session").Return(nil, fmt.Errorf("provider %q: %w", "pk-session", rpcerror.ErrProviderLeaseUnavailable))
		}
	}
	assert.Empty(t, rt.runReqs)
}

// Given 账号侧派发(控制台 / agrctl)只点名引擎配置的 sync_id、backend 那格只有类型,
// When 执行,Then agentred 按引擎配置从快照补全这一轮(配置、绑定的供应商与模型),
// 并把这一轮的身份记成那份引擎配置。
func TestRuntime_Run_GivenAccountDispatchNamingAnEngineProfile_WhenExecuting_ThenTheSnapshotProfileGoverns(t *testing.T) {
	rt := &fullRT{}
	notif := newRecordingOutbound()
	sess := newRecordingSessions()
	ctrl := gomock.NewController(t)
	lookup := mock_handlers.NewMockLLMProviderLookupPort(ctrl)
	gw := mock_handlers.NewMockGatewayPort(ctrl)
	gw.EXPECT().URL().Return("").AnyTimes()
	var asked []string
	h := handlers.NewRuntimeHandlers(handlers.RuntimeDeps{
		NotifyFor: notif.notifierFor, Sessions: sess, SessionQuery: sess, Gateway: gw, Lookup: lookup,
		RuntimeFor: func(agent_backend_entity.BackendType) agentruntime.Runtime { return rt },
		EngineProfileForSyncID: func(syncID string) (enginesnapshot.Profile, bool) {
			asked = append(asked, syncID)
			return enginesnapshot.Profile{
				Type: "codex", ProviderKey: "pk", ModelKey: "mk",
				AgentBackendConfig: syncwire.AgentBackendConfig{Sandbox: "read-only"},
			}, syncID == "EN-9"
		},
	})
	lookup.EXPECT().FindByKey(gomock.Any(), "pk").Return(&llm_provider_entity.LLMProvider{
		ProviderKey: "pk", Type: string(llm_provider_entity.TypeOpenAIResponse), Status: consts.ACTIVE,
	}, nil)
	lookup.EXPECT().ResolveModel(gomock.Any(), "pk", "mk").Return(handlers.EffectiveModel{ModelKey: "mk", ModelID: "gpt-5"}, nil)

	_, err := h.Run(context.Background(), &agentrewire.RuntimeRunRequest{
		Backend:             &agentrewire.AgentBackend{Type: "codex"},
		EngineProfileSyncId: "EN-9",
		ConversationId:      convID(310),
	})
	require.NoError(t, err)
	_ = notif.waitFrames(t, 1)
	assert.Contains(t, asked, "EN-9")
	rt.mu.Lock()
	defer rt.mu.Unlock()
	require.Len(t, rt.runReqs, 1)
	got := rt.runReqs[0].req.Backend
	assert.Equal(t, "read-only", got.Sandbox)
	assert.Equal(t, "pk", got.LLMProviderKey)
	assert.Equal(t, "EN-9", got.SyncID)
}
