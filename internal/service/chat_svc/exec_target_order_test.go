package chat_svc_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/agent_entity"
	"github.com/agentre-hub/agentre/internal/repository/agent_backend_repo"
	"github.com/agentre-hub/agentre/internal/repository/agent_backend_repo/mock_agent_backend_repo"
	"github.com/agentre-hub/agentre/internal/repository/agent_repo"
	"github.com/agentre-hub/agentre/internal/repository/agent_repo/mock_agent_repo"
	"github.com/agentre-hub/agentre/internal/repository/llm_provider_repo"
	"github.com/agentre-hub/agentre/internal/repository/llm_provider_repo/mock_llm_provider_repo"
	"github.com/agentre-hub/agentre/internal/repository/project_repo"
	"github.com/agentre-hub/agentre/internal/repository/project_repo/mock_project_repo"
	"github.com/agentre-hub/agentre/internal/service/chat_svc"
	"github.com/agentre-hub/agentre/internal/service/remote_device_svc"
	"github.com/agentre-hub/agentre/internal/service/remote_device_svc/mock_remote_device_svc"
)

// setupExecTargetOrderTest 是 R14 顺序解析专用环境。与 setupPickExecTargetTest 的
// 差别：不预置 AnyTimes 的 DeviceFingerprint / ListByDevice 宽松桩 —— 每个用例要
// 自己显式声明本机指纹与「自己」那几档（gomock 的 AnyTimes 宽松桩先注册会拦掉具体
// 期望，见 setupChatTest 注释）。多档都做 beIsSelf 判定时 DeviceFingerprint 会被调
// 多次，用例里统一用 .AnyTimes() 表达「只要命中这个指纹即可」。
type execTargetOrderMocks struct {
	execTarget         *mock_agent_repo.MockAgentExecTargetRepo
	execTargetOverride *mock_agent_repo.MockAgentExecTargetOverrideRepo
	backend            *mock_agent_backend_repo.MockAgentBackendRepo
	provider           *mock_llm_provider_repo.MockLLMProviderRepo
	project            *mock_project_repo.MockProjectRepo
	remoteDevice       *mock_remote_device_svc.MockRemoteDeviceSvc
}

func setupExecTargetOrderTest(t *testing.T) (context.Context, *execTargetOrderMocks, chat_svc.ChatSvc) {
	t.Helper()
	ctrl := gomock.NewController(t)
	m := &execTargetOrderMocks{
		execTarget:         mock_agent_repo.NewMockAgentExecTargetRepo(ctrl),
		execTargetOverride: mock_agent_repo.NewMockAgentExecTargetOverrideRepo(ctrl),
		backend:            mock_agent_backend_repo.NewMockAgentBackendRepo(ctrl),
		provider:           mock_llm_provider_repo.NewMockLLMProviderRepo(ctrl),
		project:            mock_project_repo.NewMockProjectRepo(ctrl),
		remoteDevice:       mock_remote_device_svc.NewMockRemoteDeviceSvc(ctrl),
	}

	prevExecTarget := agent_repo.AgentExecTarget()
	prevOverride := agent_repo.AgentExecTargetOverride()
	prevBackend := agent_backend_repo.AgentBackend()
	prevProvider := llm_provider_repo.LLMProvider()
	prevProject := project_repo.Project()
	prevRemoteDevice := remote_device_svc.Default()

	agent_repo.RegisterAgentExecTarget(m.execTarget)
	agent_repo.RegisterAgentExecTargetOverride(m.execTargetOverride)
	agent_backend_repo.RegisterAgentBackend(m.backend)
	llm_provider_repo.RegisterLLMProvider(m.provider)
	project_repo.RegisterProject(m.project)
	remote_device_svc.SetDefault(m.remoteDevice)

	t.Cleanup(func() {
		agent_repo.RegisterAgentExecTarget(prevExecTarget)
		agent_repo.RegisterAgentExecTargetOverride(prevOverride)
		agent_backend_repo.RegisterAgentBackend(prevBackend)
		llm_provider_repo.RegisterLLMProvider(prevProvider)
		project_repo.RegisterProject(prevProject)
		remote_device_svc.SetDefault(prevRemoteDevice)
	})

	return context.Background(), m, chat_svc.NewChat(nil)
}

// orderPlainClaude 是 DeviceID 为空的本地 claudecode（本机档，R12 之后仍是合法具名
// 目标，R15d 已删除）。LLMProviderKey 为空短路判可用，不需要 mock provider/gateway。
func orderPlainClaude(id int64) *agent_backend_entity.AgentBackend {
	return &agent_backend_entity.AgentBackend{ID: id, Type: string(agent_backend_entity.TypeClaudeCode)}
}

// orderSelfClaude 是指向本机指纹的 claudecode（R13 认领出来的「自己」那一档）。
func orderSelfClaude(id int64) *agent_backend_entity.AgentBackend {
	return &agent_backend_entity.AgentBackend{ID: id, Type: string(agent_backend_entity.TypeClaudeCode), DeviceFingerprint: "sha256:me"}
}

// ── R14 分支①：本端有覆盖 → 派发按覆盖顺序取第一个可用 ─────────────────────

func TestPickExecTarget_GivenLocalOverride_WhenResolved_ThenPicksPerOverrideOrder(t *testing.T) {
	ctx, m, svc := setupExecTargetOrderTest(t)
	// 账号默认顺序 [51, 52]（都是本机空 DeviceID 档）；本端覆盖 [52, 51]。
	m.execTarget.EXPECT().ListByAgent(ctx, int64(31)).Return([]*agent_entity.AgentExecTarget{
		{ID: 1, AgentID: 31, AgentBackendID: 51, SortOrder: 0},
		{ID: 2, AgentID: 31, AgentBackendID: 52, SortOrder: 1},
	}, nil)
	m.execTargetOverride.EXPECT().Get(ctx, int64(31)).
		Return(&agent_entity.AgentExecTargetOverride{AgentID: 31, OrderJSON: "[52,51]"}, nil)
	// 覆盖是显式顺序：52 被提到第一且可用 → 派发落 52，默认第一的 51 不会被 Find。
	m.backend.EXPECT().Find(ctx, int64(52)).Return(orderPlainClaude(52), nil)

	choice, err := svc.PickExecTarget(ctx, 31, 0)
	require.NoError(t, err)
	require.NotNil(t, choice)
	assert.Equal(t, int64(52), choice.Backend.ID, "覆盖顺序必须先于账号默认")
}

// ── R14 分支②：无覆盖 + 桌面端把自己提到第一 ───────────────────────────────

func TestPickExecTarget_GivenNoOverride_WhenSelfTargetInList_ThenSelfPromotedAndPicked(t *testing.T) {
	ctx, m, svc := setupExecTargetOrderTest(t)
	// 默认顺序 [61(远端), 62(本机)]；本机指纹 sha256:me。
	m.execTarget.EXPECT().ListByAgent(ctx, int64(32)).Return([]*agent_entity.AgentExecTarget{
		{ID: 3, AgentID: 32, AgentBackendID: 61, SortOrder: 0},
		{ID: 4, AgentID: 32, AgentBackendID: 62, SortOrder: 1},
	}, nil)
	m.execTargetOverride.EXPECT().Get(ctx, int64(32)).Return(nil, nil)
	m.remoteDevice.EXPECT().DeviceFingerprint().Return("sha256:me", nil).AnyTimes()
	m.backend.EXPECT().ListByDevice(ctx, "sha256:me").Return([]*agent_backend_entity.AgentBackend{
		{ID: 62, DeviceFingerprint: "sha256:me"},
	}, nil).AnyTimes()
	// 自己（62）被提到第一：本机 claudecode 登录态直接可用，不做远端配对判定
	//（不给 remoteDevice.Get 设期望，一旦触发就败）。
	m.backend.EXPECT().Find(ctx, int64(62)).Return(orderSelfClaude(62), nil)

	choice, err := svc.PickExecTarget(ctx, 32, 0)
	require.NoError(t, err)
	require.NotNil(t, choice)
	assert.Equal(t, int64(62), choice.Backend.ID, "无覆盖时桌面端必须把自己排到第一")
}

// ── R14 分支③：浏览器语境（没有「自己」）→ 账号默认原样 ────────────────────

func TestPickExecTarget_GivenNoOverride_WhenBrowserHasNoSelf_ThenDefaultOrderUnchanged(t *testing.T) {
	ctx, m, svc := setupExecTargetOrderTest(t)
	// 指纹取不到（空）→ 没有「自己」可提前，按账号默认 [71, 72] 取第一个可用。
	m.execTarget.EXPECT().ListByAgent(ctx, int64(33)).Return([]*agent_entity.AgentExecTarget{
		{ID: 5, AgentID: 33, AgentBackendID: 71, SortOrder: 0},
		{ID: 6, AgentID: 33, AgentBackendID: 72, SortOrder: 1},
	}, nil)
	m.execTargetOverride.EXPECT().Get(ctx, int64(33)).Return(nil, nil)
	m.remoteDevice.EXPECT().DeviceFingerprint().Return("", nil).AnyTimes()
	// 不注册 ListByDevice —— 空指纹不该触发本机档查询。
	m.backend.EXPECT().Find(ctx, int64(71)).Return(orderPlainClaude(71), nil)

	choice, err := svc.PickExecTarget(ctx, 33, 0)
	require.NoError(t, err)
	require.NotNil(t, choice)
	assert.Equal(t, int64(71), choice.Backend.ID, "浏览器语境必须原样用账号默认顺序")
}

// ── R15 守卫：不存在需要跳过的相对槽位（R15d 随相对取值删除）─────────────────

func TestPickExecTarget_GivenEmptyDeviceBackend_ThenNotSkippedAsRelativeSlot(t *testing.T) {
	ctx, m, svc := setupExecTargetOrderTest(t)
	// DeviceID 为空的档（本机）是合法具名目标，R15d 已删除——绝不能因为它
	//「指代相对本机」就被跳过。
	m.execTarget.EXPECT().ListByAgent(ctx, int64(34)).Return([]*agent_entity.AgentExecTarget{
		{ID: 7, AgentID: 34, AgentBackendID: 81, SortOrder: 0},
	}, nil)
	m.execTargetOverride.EXPECT().Get(ctx, int64(34)).Return(nil, nil)
	m.remoteDevice.EXPECT().DeviceFingerprint().Return("", nil).AnyTimes()
	m.backend.EXPECT().Find(ctx, int64(81)).Return(orderPlainClaude(81), nil)

	choice, err := svc.PickExecTarget(ctx, 34, 0)
	require.NoError(t, err)
	require.NotNil(t, choice)
	assert.Equal(t, int64(81), choice.Backend.ID, "空 DeviceID 的本机档必须可被派发，不得按相对槽位跳过")
}

// ── R14 / R16：组织架构页的可用性视图按解析后顺序给出，并如实标注覆盖态 ────

func TestListExecTargetAvailability_GivenOverride_ThenResolvedOrderAndHasOverride(t *testing.T) {
	ctx, m, svc := setupExecTargetOrderTest(t)
	m.execTarget.EXPECT().ListByAgent(ctx, int64(35)).Return([]*agent_entity.AgentExecTarget{
		{ID: 8, AgentID: 35, AgentBackendID: 91, SortOrder: 0},
		{ID: 9, AgentID: 35, AgentBackendID: 92, SortOrder: 1},
	}, nil)
	// Get 会被调用两次：一次在顺序解析，一次在覆盖态标注（hasExecTargetOverride）。
	m.execTargetOverride.EXPECT().Get(ctx, int64(35)).
		Return(&agent_entity.AgentExecTargetOverride{AgentID: 35, OrderJSON: "[92,91]"}, nil).AnyTimes()
	m.remoteDevice.EXPECT().DeviceFingerprint().Return("", nil).AnyTimes()
	m.backend.EXPECT().Find(ctx, int64(92)).Return(orderPlainClaude(92), nil)
	m.backend.EXPECT().Find(ctx, int64(91)).Return(orderPlainClaude(91), nil)

	statuses, err := svc.ListExecTargetAvailability(ctx, 35, 0)
	require.NoError(t, err)
	require.Len(t, statuses, 2)
	assert.Equal(t, int64(92), statuses[0].AgentBackendID, "可用性视图必须按覆盖顺序给出")
	assert.Equal(t, int64(91), statuses[1].AgentBackendID)
	assert.True(t, statuses[0].HasOverride, "有本端覆盖时必须如实标注")
}
