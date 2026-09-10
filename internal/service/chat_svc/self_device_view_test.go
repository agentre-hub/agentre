package chat_svc

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/agent_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/chat_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/llm_provider_entity"
	"github.com/agentre-hub/agentre/internal/repository/chat_repo"
	"github.com/agentre-hub/agentre/internal/repository/chat_repo/mock_chat_repo"
	"github.com/agentre-hub/agentre/internal/repository/llm_provider_repo"
	"github.com/agentre-hub/agentre/internal/repository/llm_provider_repo/mock_llm_provider_repo"
	"github.com/agentre-hub/agentre/internal/service/remote_device_svc"
	"github.com/agentre-hub/agentre/internal/service/remote_device_svc/mock_remote_device_svc"
)

// R13 的运行期认领（agent_backend_svc.ClaimRelativeBackends）把本机 backend 的
// DeviceID 从空串改写成了本机指纹，于是 be.IsRemote() 对本机档也成立。派发侧每一处
// 都补了 remote_device_svc.IsSelfDevice 判据，**展示侧没有**：LoadSession 直接把
// be.DeviceID 抄进会话视图，再拿它去本机配对表查设备名/在线态——本机指纹永远不在
// 配对表里（不会和自己配对），于是 DeviceName 空、Online 假，聊天头把本机会话渲染成
// 灰色「离线」并弹出「所在机器离线」横幅。
//
// 契约与 execDeviceID 同一条：本地档与指向本机的 self 档在展示口径上都是空 DeviceID。

// selfDeviceLoadSessionMocks 在 setupExecTargetPinTest 之上补两样 LoadSession 要用的
// 东西：消息仓储桩，以及一个把本机指纹固定成 sha256:self 的 remote_device_svc。
func registerSelfDeviceMocks(t *testing.T) {
	t.Helper()
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)

	msgMock := mock_chat_repo.NewMockMessageRepo(ctrl)
	prevMessage := chat_repo.Message()
	chat_repo.RegisterMessage(msgMock)
	msgMock.EXPECT().List(gomock.Any(), gomock.Any()).Return(nil, nil).AnyTimes()
	msgMock.EXPECT().ListMeta(gomock.Any(), gomock.Any()).Return(nil, nil).AnyTimes()
	msgMock.EXPECT().FillBlocks(gomock.Any(), gomock.Any()).Return(nil).AnyTimes()

	provMock := mock_llm_provider_repo.NewMockLLMProviderRepo(ctrl)
	prevProvider := llm_provider_repo.LLMProvider()
	llm_provider_repo.RegisterLLMProvider(provMock)
	provMock.EXPECT().BatchFindByKey(gomock.Any(), gomock.Any()).
		Return(map[string]*llm_provider_entity.LLMProvider{}, nil).AnyTimes()

	rds := mock_remote_device_svc.NewMockRemoteDeviceSvc(ctrl)
	prevRDS := remote_device_svc.Default()
	remote_device_svc.SetDefault(rds)
	rds.EXPECT().DeviceFingerprint().Return("sha256:self", nil).AnyTimes()
	// 本机指纹不在配对表里——这正是 bug 的触发条件，桩要如实反映。
	rds.EXPECT().List(gomock.Any()).Return(nil, nil).AnyTimes()

	t.Cleanup(func() {
		chat_repo.RegisterMessage(prevMessage)
		llm_provider_repo.RegisterLLMProvider(prevProvider)
		remote_device_svc.SetDefault(prevRDS)
	})
}

// Given 会话钉在一个 DeviceID == 本机指纹的档上（R13 认领后本机 backend 的常态），
// When 前端 LoadSession，Then 会话视图按本机口径给出空 DeviceID —— 而不是把本机
// 报成一台查不到、离线的远端设备。
func TestLoadSession_GivenSelfFingerprintBackend_ThenSessionViewReportsLocalDevice(t *testing.T) {
	ctx, m, svc := setupExecTargetPinTest(t)
	registerSelfDeviceMocks(t)

	m.session.EXPECT().Find(ctx, int64(2912)).Return(&chat_entity.Session{
		ID: 2912, AgentID: 8, Status: 1, ExecAgentBackendID: 234,
	}, nil)
	m.agent.EXPECT().Find(ctx, int64(8)).Return(&agent_entity.Agent{
		ID: 8, Name: "pi", AgentBackendID: 234, Status: 1,
	}, nil)
	m.execTarget.EXPECT().ListByAgent(ctx, int64(8)).Return([]*agent_entity.AgentExecTarget{
		{ID: 256, AgentID: 8, AgentBackendID: 234, SortOrder: 0},
	}, nil)
	m.backend.EXPECT().Find(ctx, int64(234)).Return(&agent_backend_entity.AgentBackend{
		ID: 234, Type: string(agent_backend_entity.TypePiAgent), Status: 1,
		DeviceFingerprint: "sha256:self",
	}, nil).AnyTimes()

	resp, err := svc.LoadSession(ctx, &LoadSessionRequest{SessionID: 2912})
	require.NoError(t, err)
	assert.Equal(t, "", resp.Session.DeviceID,
		"本机档的会话视图必须报空 DeviceID，否则聊天头按远端渲染成离线")
	assert.Equal(t, "", resp.Session.DeviceName,
		"本机档没有远端设备名——空 DeviceID 档的既有零值口径")
}

// Given Agent 的主档 DeviceID == 本机指纹，When 前端拉 Agent 列表，
// Then 该 Agent 的设备归属按本机口径给出——同一根因的第二个展示出口。
func TestListAgents_GivenSelfFingerprintBackend_ThenAgentItemReportsLocalDevice(t *testing.T) {
	ctx, m, svc := setupExecTargetPinTest(t)
	registerSelfDeviceMocks(t)

	be := &agent_backend_entity.AgentBackend{
		ID: 234, Type: string(agent_backend_entity.TypePiAgent), Status: 1,
		DeviceFingerprint: "sha256:self",
	}
	m.agent.EXPECT().List(ctx).Return([]*agent_entity.Agent{
		{ID: 8, Name: "pi", AgentBackendID: 234, Status: 1},
	}, nil)
	m.backend.EXPECT().BatchFind(ctx, []int64{234}).Return(
		map[int64]*agent_backend_entity.AgentBackend{234: be}, nil)
	m.session.EXPECT().CountRunningByAgents(ctx, []int64{8}).Return(map[int64]int{}, nil)
	m.session.EXPECT().CountByAgents(ctx, []int64{8}).Return(map[int64]int64{}, nil)
	m.session.EXPECT().ListIDsByAgents(ctx, []int64{8}).Return(map[int64][]int64{}, nil)
	m.session.EXPECT().ListByAgent(ctx, int64(8), gomock.Any()).Return(nil, nil)
	m.session.EXPECT().ListAttentionByAgent(ctx, int64(8), gomock.Any()).Return(nil, nil)

	resp, err := svc.ListAgents(ctx, &ListAgentsRequest{})
	require.NoError(t, err)
	require.Len(t, resp.Agents, 1)
	assert.Equal(t, "", resp.Agents[0].DeviceID,
		"本机档的 Agent 列表项必须报空 DeviceID，否则设备 chip 按远端渲染")
	assert.Equal(t, "", resp.Agents[0].DeviceName)
}

// beTargetsRemote 的 nil 语义必须与它取代的 be.IsRemote() 一致：nil backend 不是
// 「别的机器」。26 处 call site 里有几处原本靠 IsRemote()/IsLocal() 的 nil 安全性
// 兜底，翻错了会让本机档走远端派发（或反过来）。
func TestBeTargetsRemote_NilSemantics(t *testing.T) {
	registerSelfDeviceMocks(t)

	assert.False(t, beTargetsRemote(nil), "nil backend 与空 DeviceID 一样是本机")
	assert.False(t, beTargetsRemote(&agent_backend_entity.AgentBackend{DeviceFingerprint: ""}))
	assert.False(t, beTargetsRemote(&agent_backend_entity.AgentBackend{DeviceFingerprint: "sha256:self"}),
		"R13 认领后本机 backend 带的就是本机指纹")
	assert.True(t, beTargetsRemote(&agent_backend_entity.AgentBackend{DeviceFingerprint: "sha256:other"}))
	assert.True(t, beTargetsRemote(&agent_backend_entity.AgentBackend{DeviceFingerprint: "7"}))
}

// effectiveLLMForNonRemoteTurn 的名字就是契约：**非远端**的轮要在本机解析出完整
// provider 配置（BaseURL/APIKey/ModelID/ContextWindow）。它却拿裸 be.IsRemote() 提问，
// R13 认领后本机 backend 的 DeviceID 是本机指纹 → 本机轮被当远端轮，退化成 keys-only
// 配置（daemon 自己解析的口径），本地 runtime 因此拿不到 BaseURL/APIKey，配了自定义
// 供应商的本机后端会静默回落到 CLI 自身登录。
func TestEffectiveLLMForNonRemoteTurn_GivenSelfFingerprintBackend_ThenResolvesProviderLocally(t *testing.T) {
	_, _, svc := setupExecTargetPinTest(t)
	registerSelfDeviceMocks(t)
	registerProviderMockForTurn(t, "pk-local", "", "")

	be := &agent_backend_entity.AgentBackend{
		ID: 234, Type: string(agent_backend_entity.TypeClaudeCode),
		DeviceFingerprint: "sha256:self", LLMProviderKey: "pk-local",
	}
	prov := &llm_provider_entity.LLMProvider{
		ProviderKey: "pk-local", Type: string(llm_provider_entity.TypeAnthropic),
		Enabled: llm_provider_entity.EnabledOn, DefaultModelKey: "mk-pk-local", Status: 1,
	}

	cfg, err := svc.effectiveLLMForNonRemoteTurn(context.Background(),
		&chat_entity.Session{ID: 2912, AgentID: 8}, be, prov)
	require.NoError(t, err)
	require.NotNil(t, cfg)
	assert.Equal(t, "model-pk-local", cfg.ModelID,
		"本机档必须走本地解析拿到真实 ModelID，而不是远端 keys-only 的空值")
}
