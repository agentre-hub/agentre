package sessionengine_test

import (
	"context"
	"testing"

	"github.com/cago-frame/cago/pkg/consts"
	"github.com/cago-frame/cago/pkg/utils/httputils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/agent_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/chat_entity"
	"github.com/agentre-hub/agentre/internal/pkg/code"
	"github.com/agentre-hub/agentre/internal/repository/agent_backend_repo"
	"github.com/agentre-hub/agentre/internal/repository/agent_backend_repo/mock_agent_backend_repo"
	"github.com/agentre-hub/agentre/internal/service/chat_svc/sessionengine"
	"github.com/agentre-hub/agentre/pkg/wire/devicefp"
)

type repos struct {
	profiles *mock_agent_backend_repo.MockEngineProfileRepo
	backends *mock_agent_backend_repo.MockAgentBackendRepo
}

func setup(t *testing.T) repos {
	t.Helper()
	ctrl := gomock.NewController(t)
	r := repos{
		profiles: mock_agent_backend_repo.NewMockEngineProfileRepo(ctrl),
		backends: mock_agent_backend_repo.NewMockAgentBackendRepo(ctrl),
	}
	prevProfiles, prevBackends := agent_backend_repo.EngineProfile(), agent_backend_repo.AgentBackend()
	agent_backend_repo.RegisterEngineProfile(r.profiles)
	agent_backend_repo.RegisterAgentBackend(r.backends)
	t.Cleanup(func() {
		agent_backend_repo.RegisterEngineProfile(prevProfiles)
		agent_backend_repo.RegisterAgentBackend(prevBackends)
	})
	sessionengine.RegisterEffective(nil)
	t.Cleanup(func() { sessionengine.RegisterEffective(nil) })
	return r
}

const deviceD = devicefp.Carrier("sha256:device-d")

// codexProfile 迁移时由「backend X」所在组落成的那份配置：模型、力度、权限都原样带过来。
func codexProfile(status int) *agent_backend_entity.EngineProfile {
	return &agent_backend_entity.EngineProfile{
		ID: 31, Type: string(agent_backend_entity.TypeCodex), Name: "Codex",
		LLMProviderKey: "prov-x", LLMModelKey: "model-x", ReasoningEffort: "high",
		Sandbox: "workspace-write", Approval: "on-request", Status: status,
	}
}

func requireCode(t *testing.T, err error, want int) {
	t.Helper()
	var httpErr *httputils.Error
	require.ErrorAs(t, err, &httpErr)
	assert.Equal(t, want, httpErr.Code)
}

// Given 迁移前钉在设备 D 上 backend X 的会话（迁移把 X 换成它所在组的引擎配置，设备列不动），
// When 续接，Then 仍在 D 上、用同一份模型 / 力度 / 权限设置跑。
func TestPinned_GivenMigratedSessionOnDeviceD_ThenResumesOnDWithSameSettings(t *testing.T) {
	r := setup(t)
	ctx := context.Background()
	sess := &chat_entity.Session{ID: 7, EngineProfileID: 31, ExecAgentBackendID: 52, ExecDeviceID: 4, ExecDeviceFingerprint: deviceD}
	r.profiles.EXPECT().FindAnyStatus(ctx, int64(31)).Return(codexProfile(consts.ACTIVE), nil)

	be, ok, err := sessionengine.Pinned(ctx, sess)
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, deviceD, be.DeviceFingerprint, "设备不变")
	assert.Equal(t, string(agent_backend_entity.TypeCodex), be.Type)
	assert.Equal(t, "prov-x", be.LLMProviderKey)
	assert.Equal(t, "model-x", be.LLMModelKey)
	assert.Equal(t, "high", be.ReasoningEffort)
	assert.Equal(t, "workspace-write", be.Sandbox)
	assert.Equal(t, "on-request", be.Approval)
}

// Given 迁移前钉在 backend X、设备列为空（旧本机钉档只写档不写设备）的会话，When 续接，Then
// 设备取 X 那一行所在的设备 —— 与 dispatch_svc 会话作用域同一口径。
func TestPinned_GivenMigratedSessionWithoutDeviceColumn_ThenUsesLegacyBackendDevice(t *testing.T) {
	r := setup(t)
	ctx := context.Background()
	sess := &chat_entity.Session{ID: 8, EngineProfileID: 31, ExecAgentBackendID: 52}
	r.profiles.EXPECT().FindAnyStatus(ctx, int64(31)).Return(codexProfile(consts.ACTIVE), nil)
	r.backends.EXPECT().Find(ctx, int64(52)).Return(&agent_backend_entity.AgentBackend{ID: 52, DeviceFingerprint: deviceD}, nil)

	be, ok, err := sessionengine.Pinned(ctx, sess)
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, deviceD, be.DeviceFingerprint)
}

// Given 钉住的引擎配置已被删除，When 续接，Then 按它最后的设置继续（不报错、不换配置）。
func TestPinned_GivenDeletedProfile_ThenKeepsLastSettings(t *testing.T) {
	r := setup(t)
	ctx := context.Background()
	r.profiles.EXPECT().FindAnyStatus(ctx, int64(31)).Return(codexProfile(consts.DELETE), nil)

	be, ok, err := sessionengine.Pinned(ctx, &chat_entity.Session{ID: 9, EngineProfileID: 31})
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, "model-x", be.LLMModelKey)
	assert.Empty(t, be.DeviceFingerprint, "只钉了配置、没有设备 = 本机")
}

// Given 钉住的配置那一行已不存在，Then 报 EngineProfileNotFound，而不是悄悄换一份。
func TestPinned_GivenMissingProfile_ThenEngineProfileNotFound(t *testing.T) {
	r := setup(t)
	ctx := context.Background()
	r.profiles.EXPECT().FindAnyStatus(ctx, int64(31)).Return(nil, nil)

	_, _, err := sessionengine.Pinned(ctx, &chat_entity.Session{ID: 10, EngineProfileID: 31})
	requireCode(t, err, code.EngineProfileNotFound)
}

// Given 只有旧执行目标档（agrctl --backend / 旧派发写下的）且那一行还在，Then 原样用那一行；
// 那一行已不在时视为没钉。
func TestPinned_GivenLegacyBackendPin(t *testing.T) {
	r := setup(t)
	ctx := context.Background()
	legacy := &agent_backend_entity.AgentBackend{ID: 52, Type: string(agent_backend_entity.TypeClaudeCode), DeviceFingerprint: deviceD}
	r.backends.EXPECT().Find(ctx, int64(52)).Return(legacy, nil)
	be, ok, err := sessionengine.Pinned(ctx, &chat_entity.Session{ID: 11, ExecAgentBackendID: 52})
	require.NoError(t, err)
	require.True(t, ok)
	assert.Same(t, legacy, be)

	r.backends.EXPECT().Find(ctx, int64(53)).Return(nil, nil)
	_, ok, err = sessionengine.Pinned(ctx, &chat_entity.Session{ID: 12, ExecAgentBackendID: 53})
	require.NoError(t, err)
	assert.False(t, ok)
}

// Given 还没跑过第一轮的会话，When 只读读者要它的后端，Then 是成员此刻生效的配置在本机的
// 视图，且不碰任何钉住相关的仓储；没有生效配置时为 nil。
func TestForSession_GivenUnpinnedSession_ThenMemberEffectiveEngine(t *testing.T) {
	setup(t)
	ctx := context.Background()
	a := &agent_entity.Agent{ID: 5}
	be, err := sessionengine.ForSession(ctx, &chat_entity.Session{ID: 13, AgentID: 5}, a)
	require.NoError(t, err)
	assert.Nil(t, be, "未接线 / 无生效配置 = nil")

	sessionengine.RegisterEffective(func(_ context.Context, got *agent_entity.Agent) (*agent_backend_entity.EngineProfile, error) {
		assert.Same(t, a, got)
		return codexProfile(consts.ACTIVE), nil
	})
	be, err = sessionengine.ForSession(ctx, &chat_entity.Session{ID: 13, AgentID: 5}, a)
	require.NoError(t, err)
	require.NotNil(t, be)
	assert.Equal(t, "model-x", be.LLMModelKey)
	assert.Empty(t, be.DeviceFingerprint)
	assert.Zero(t, be.ID, "视图不是 agent_backends 的一行")
}
