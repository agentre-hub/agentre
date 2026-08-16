package chat_svc_test

import (
	"context"
	"errors"
	"testing"

	"github.com/cago-frame/cago/pkg/utils/httputils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
	"gorm.io/gorm"

	"github.com/agentre-ai/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-ai/agentre/internal/model/entity/agent_entity"
	"github.com/agentre-ai/agentre/internal/model/entity/llm_provider_entity"
	"github.com/agentre-ai/agentre/internal/model/entity/project_entity"
	"github.com/agentre-ai/agentre/internal/model/entity/project_location_entity"
	"github.com/agentre-ai/agentre/internal/pkg/code"
	"github.com/agentre-ai/agentre/internal/repository/agent_backend_repo"
	"github.com/agentre-ai/agentre/internal/repository/agent_backend_repo/mock_agent_backend_repo"
	"github.com/agentre-ai/agentre/internal/repository/agent_repo"
	"github.com/agentre-ai/agentre/internal/repository/agent_repo/mock_agent_repo"
	"github.com/agentre-ai/agentre/internal/repository/llm_provider_repo"
	"github.com/agentre-ai/agentre/internal/repository/llm_provider_repo/mock_llm_provider_repo"
	"github.com/agentre-ai/agentre/internal/repository/project_location_repo"
	"github.com/agentre-ai/agentre/internal/repository/project_location_repo/mock_project_location_repo"
	"github.com/agentre-ai/agentre/internal/repository/project_repo"
	"github.com/agentre-ai/agentre/internal/repository/project_repo/mock_project_repo"
	"github.com/agentre-ai/agentre/internal/service/chat_svc"
	"github.com/agentre-ai/agentre/internal/service/remote_device_svc"
	"github.com/agentre-ai/agentre/internal/service/remote_device_svc/mock_remote_device_svc"
)

type pickExecTargetMocks struct {
	execTarget         *mock_agent_repo.MockAgentExecTargetRepo
	execTargetOverride *mock_agent_repo.MockAgentExecTargetOverrideRepo
	backend            *mock_agent_backend_repo.MockAgentBackendRepo
	provider           *mock_llm_provider_repo.MockLLMProviderRepo
	project            *mock_project_repo.MockProjectRepo
	projectLocation    *mock_project_location_repo.MockProjectLocationRepo
	remoteDevice       *mock_remote_device_svc.MockRemoteDeviceSvc
}

func setupPickExecTargetTest(t *testing.T) (context.Context, *pickExecTargetMocks, chat_svc.ChatSvc) {
	t.Helper()
	ctrl := gomock.NewController(t)
	m := &pickExecTargetMocks{
		execTarget:         mock_agent_repo.NewMockAgentExecTargetRepo(ctrl),
		execTargetOverride: mock_agent_repo.NewMockAgentExecTargetOverrideRepo(ctrl),
		backend:            mock_agent_backend_repo.NewMockAgentBackendRepo(ctrl),
		provider:           mock_llm_provider_repo.NewMockLLMProviderRepo(ctrl),
		project:            mock_project_repo.NewMockProjectRepo(ctrl),
		projectLocation:    mock_project_location_repo.NewMockProjectLocationRepo(ctrl),
		remoteDevice:       mock_remote_device_svc.NewMockRemoteDeviceSvc(ctrl),
	}

	previousExecTarget := agent_repo.AgentExecTarget()
	previousOverride := agent_repo.AgentExecTargetOverride()
	previousBackend := agent_backend_repo.AgentBackend()
	previousProvider := llm_provider_repo.LLMProvider()
	previousProject := project_repo.Project()
	previousProjectLocation := project_location_repo.ProjectLocation()
	previousRemoteDevice := remote_device_svc.Default()

	agent_repo.RegisterAgentExecTarget(m.execTarget)
	agent_repo.RegisterAgentExecTargetOverride(m.execTargetOverride)
	agent_backend_repo.RegisterAgentBackend(m.backend)
	llm_provider_repo.RegisterLLMProvider(m.provider)
	project_repo.RegisterProject(m.project)
	project_location_repo.RegisterProjectLocation(m.projectLocation)
	remote_device_svc.SetDefault(m.remoteDevice)

	// R14 顺序解析的默认宽松桩：这批既有测试不关心「本端覆盖 / 自己提前」，默认
	// 无覆盖、无本机指纹。AnyTimes 宽松桩注册在前，具体测试不得对同一方法再叠加
	// 精确期望（gomock 永远先匹配 AnyTimes）——R14 自身的用例在 exec_target_order_test.go
	// 用不带这些宽松桩的专用环境。
	m.execTargetOverride.EXPECT().Get(gomock.Any(), gomock.Any()).Return(nil, nil).AnyTimes()
	m.remoteDevice.EXPECT().DeviceFingerprint().Return("", nil).AnyTimes()
	m.backend.EXPECT().ListByDevice(gomock.Any(), gomock.Any()).Return(nil, nil).AnyTimes()

	t.Cleanup(func() {
		agent_repo.RegisterAgentExecTarget(previousExecTarget)
		agent_repo.RegisterAgentExecTargetOverride(previousOverride)
		agent_backend_repo.RegisterAgentBackend(previousBackend)
		llm_provider_repo.RegisterLLMProvider(previousProvider)
		project_repo.RegisterProject(previousProject)
		project_location_repo.RegisterProjectLocation(previousProjectLocation)
		remote_device_svc.SetDefault(previousRemoteDevice)
	})

	return context.Background(), m, chat_svc.NewChat(nil)
}

func activeProvider(key string) *llm_provider_entity.LLMProvider {
	return &llm_provider_entity.LLMProvider{ID: 1, ProviderKey: key, Type: string(llm_provider_entity.TypeAnthropic), Status: 1}
}

// ── ① 按列表顺序取第一个可用的 ──────────────────────────────────────────────

func TestPickExecTarget_GivenOrderedTargets_WhenFirstIsAvailable_ThenPicksFirst(t *testing.T) {
	ctx, m, svc := setupPickExecTargetTest(t)
	m.execTarget.EXPECT().ListByAgent(ctx, int64(31)).Return([]*agent_entity.AgentExecTarget{
		{ID: 1, AgentID: 31, AgentBackendID: 51, SortOrder: 0},
		{ID: 2, AgentID: 31, AgentBackendID: 52, SortOrder: 1},
	}, nil)
	m.backend.EXPECT().Find(ctx, int64(51)).Return(&agent_backend_entity.AgentBackend{
		ID: 51, Type: string(agent_backend_entity.TypeClaudeCode), LLMProviderKey: "",
	}, nil)

	choice, err := svc.PickExecTarget(ctx, 31, 0)
	require.NoError(t, err)
	require.NotNil(t, choice)
	assert.Equal(t, int64(51), choice.Backend.ID)
	assert.Equal(t, int64(1), choice.Target.ID)
}

// ── ② 四类不可用各自跳过并落到下一档 ──────────────────────────────────────

func TestPickExecTarget_GivenFirstUnpaired_WhenSecondAvailable_ThenSkipsToSecond(t *testing.T) {
	ctx, m, svc := setupPickExecTargetTest(t)
	m.execTarget.EXPECT().ListByAgent(ctx, int64(32)).Return([]*agent_entity.AgentExecTarget{
		{ID: 3, AgentID: 32, AgentBackendID: 61, SortOrder: 0},
		{ID: 4, AgentID: 32, AgentBackendID: 62, SortOrder: 1},
	}, nil)
	m.backend.EXPECT().Find(ctx, int64(61)).Return(&agent_backend_entity.AgentBackend{
		ID: 61, Type: string(agent_backend_entity.TypeClaudeCode), DeviceID: "9",
	}, nil)
	// 未配对：本地配对表里没有这一行 —— remote_device_svc.Get 报 not-found。
	m.remoteDevice.EXPECT().Get(ctx, int64(9)).Return(nil, errors.New("remote device not found"))
	m.backend.EXPECT().Find(ctx, int64(62)).Return(&agent_backend_entity.AgentBackend{
		ID: 62, Type: string(agent_backend_entity.TypeClaudeCode), LLMProviderKey: "",
	}, nil)

	choice, err := svc.PickExecTarget(ctx, 32, 0)
	require.NoError(t, err)
	require.NotNil(t, choice)
	assert.Equal(t, int64(62), choice.Backend.ID)
}

func TestPickExecTarget_GivenFirstOffline_WhenSecondAvailable_ThenSkipsToSecond(t *testing.T) {
	ctx, m, svc := setupPickExecTargetTest(t)
	m.execTarget.EXPECT().ListByAgent(ctx, int64(33)).Return([]*agent_entity.AgentExecTarget{
		{ID: 5, AgentID: 33, AgentBackendID: 71, SortOrder: 0},
		{ID: 6, AgentID: 33, AgentBackendID: 72, SortOrder: 1},
	}, nil)
	m.backend.EXPECT().Find(ctx, int64(71)).Return(&agent_backend_entity.AgentBackend{
		ID: 71, Type: string(agent_backend_entity.TypeClaudeCode), DeviceID: "10",
	}, nil)
	m.remoteDevice.EXPECT().Get(ctx, int64(10)).Return(&remote_device_svc.DeviceView{ID: 10, Online: false}, nil)
	m.backend.EXPECT().Find(ctx, int64(72)).Return(&agent_backend_entity.AgentBackend{
		ID: 72, Type: string(agent_backend_entity.TypeClaudeCode), LLMProviderKey: "",
	}, nil)

	choice, err := svc.PickExecTarget(ctx, 33, 0)
	require.NoError(t, err)
	require.NotNil(t, choice)
	assert.Equal(t, int64(72), choice.Backend.ID)
}

func TestPickExecTarget_GivenFirstBlockedByExistingBlockReason_WhenSecondAvailable_ThenSkipsToSecond(t *testing.T) {
	ctx, m, svc := setupPickExecTargetTest(t)
	m.execTarget.EXPECT().ListByAgent(ctx, int64(34)).Return([]*agent_entity.AgentExecTarget{
		{ID: 7, AgentID: 34, AgentBackendID: 81, SortOrder: 0},
		{ID: 8, AgentID: 34, AgentBackendID: 82, SortOrder: 1},
	}, nil)
	m.backend.EXPECT().Find(ctx, int64(81)).Return(&agent_backend_entity.AgentBackend{
		ID: 81, Type: string(agent_backend_entity.TypeBuiltin), LLMProviderKey: "missing-key",
	}, nil)
	m.provider.EXPECT().FindByKey(ctx, "missing-key").Return(nil, nil)
	m.backend.EXPECT().Find(ctx, int64(82)).Return(&agent_backend_entity.AgentBackend{
		ID: 82, Type: string(agent_backend_entity.TypeBuiltin), LLMProviderKey: "key-2",
	}, nil)
	m.provider.EXPECT().FindByKey(ctx, "key-2").Return(activeProvider("key-2"), nil)

	choice, err := svc.PickExecTarget(ctx, 34, 0)
	require.NoError(t, err)
	require.NotNil(t, choice)
	assert.Equal(t, int64(82), choice.Backend.ID)
}

// backend 的删除是软删（status=DELETE），而引用它的执行目标行**留在原地**：本端发起的
// 删除要等墓碑绕服务端一圈回来才由下行清掉，服务端发起的删除（设备离开账号时把指向它
// 的 backend 落墓碑）则根本不会有对应的执行目标墓碑到达。这条用例钉住那个悬空档的既有
// 兜底：Find 按 status 过滤后返回 nil，该档判不可用并跳到下一档 —— 派发因此不需要在下行
// 侧再补一遍 R6 的级联。
func TestPickExecTarget_GivenFirstBackendRowGone_WhenSecondAvailable_ThenSkipsToSecond(t *testing.T) {
	ctx, m, svc := setupPickExecTargetTest(t)
	m.execTarget.EXPECT().ListByAgent(ctx, int64(341)).Return([]*agent_entity.AgentExecTarget{
		{ID: 71, AgentID: 341, AgentBackendID: 811, SortOrder: 0},
		{ID: 72, AgentID: 341, AgentBackendID: 812, SortOrder: 1},
	}, nil)
	// 软删过的 backend：agent_backend_repo.Find 带 status = ACTIVE 条件，取不到行。
	m.backend.EXPECT().Find(ctx, int64(811)).Return(nil, nil)
	m.backend.EXPECT().Find(ctx, int64(812)).Return(&agent_backend_entity.AgentBackend{
		ID: 812, Type: string(agent_backend_entity.TypeClaudeCode), LLMProviderKey: "",
	}, nil)

	choice, err := svc.PickExecTarget(ctx, 341, 0)
	require.NoError(t, err)
	require.NotNil(t, choice)
	assert.Equal(t, int64(812), choice.Backend.ID)
}

// 悬空档是**唯一**一档时不静默失败，而是按 R15 逐档报原因——这一档的原因是
// 「引用的后端已不存在」，与「这个 Agent 一档都没配」区分得开。
func TestPickExecTarget_GivenOnlyTargetBackendRowGone_ThenReportsBackendGone(t *testing.T) {
	ctx, m, svc := setupPickExecTargetTest(t)
	m.execTarget.EXPECT().ListByAgent(ctx, int64(342)).Return([]*agent_entity.AgentExecTarget{
		{ID: 73, AgentID: 342, AgentBackendID: 813, SortOrder: 0},
	}, nil)
	m.backend.EXPECT().Find(ctx, int64(813)).Return(nil, nil)

	choice, err := svc.PickExecTarget(ctx, 342, 0)
	require.Error(t, err)
	assert.Nil(t, choice)
	var noneAvailable *chat_svc.ExecTargetNoneAvailableError
	require.ErrorAs(t, err, &noneAvailable)
	require.Len(t, noneAvailable.Reasons, 1)
	assert.Equal(t, chat_svc.BlockReasonNoBackend, noneAvailable.Reasons[0].Reason)
}

func TestPickExecTarget_GivenProjectBoundSession_WhenLocalPathMissing_ThenSkipsToRemoteWithPath(t *testing.T) {
	ctx, m, svc := setupPickExecTargetTest(t)
	m.execTarget.EXPECT().ListByAgent(ctx, int64(35)).Return([]*agent_entity.AgentExecTarget{
		{ID: 9, AgentID: 35, AgentBackendID: 91, SortOrder: 0},
		{ID: 10, AgentID: 35, AgentBackendID: 92, SortOrder: 1},
	}, nil)
	m.backend.EXPECT().Find(ctx, int64(91)).Return(&agent_backend_entity.AgentBackend{
		ID: 91, Type: string(agent_backend_entity.TypeClaudeCode), LLMProviderKey: "",
	}, nil)
	m.project.EXPECT().Find(ctx, int64(401)).Return(&project_entity.Project{ID: 401, LocalPathMissing: true}, nil)
	m.backend.EXPECT().Find(ctx, int64(92)).Return(&agent_backend_entity.AgentBackend{
		ID: 92, Type: string(agent_backend_entity.TypeClaudeCode), LLMProviderKey: "", DeviceID: "11",
	}, nil)
	m.remoteDevice.EXPECT().Get(ctx, int64(11)).Return(&remote_device_svc.DeviceView{ID: 11, Online: true}, nil)
	m.projectLocation.EXPECT().FindByProjectAndDevice(ctx, int64(401), "11").
		Return(&project_location_entity.ProjectLocation{Path: "/remote/401"}, nil)

	choice, err := svc.PickExecTarget(ctx, 35, 401)
	require.NoError(t, err)
	require.NotNil(t, choice)
	assert.Equal(t, int64(92), choice.Backend.ID)
}

func TestPickExecTarget_GivenProjectBoundSession_WhenRemoteLocationMissing_ThenUnavailable(t *testing.T) {
	ctx, m, svc := setupPickExecTargetTest(t)
	m.execTarget.EXPECT().ListByAgent(ctx, int64(36)).Return([]*agent_entity.AgentExecTarget{
		{ID: 11, AgentID: 36, AgentBackendID: 93, SortOrder: 0},
	}, nil)
	m.backend.EXPECT().Find(ctx, int64(93)).Return(&agent_backend_entity.AgentBackend{
		ID: 93, Type: string(agent_backend_entity.TypeClaudeCode), LLMProviderKey: "", DeviceID: "12",
	}, nil)
	m.remoteDevice.EXPECT().Get(ctx, int64(12)).Return(&remote_device_svc.DeviceView{ID: 12, Online: true}, nil)
	m.projectLocation.EXPECT().FindByProjectAndDevice(ctx, int64(402), "12").
		Return(nil, gorm.ErrRecordNotFound)

	choice, err := svc.PickExecTarget(ctx, 36, 402)
	require.Error(t, err)
	assert.Nil(t, choice)
	var noneErr *chat_svc.ExecTargetNoneAvailableError
	require.True(t, errors.As(err, &noneErr))
	if assert.Len(t, noneErr.Reasons, 1) {
		assert.Equal(t, int64(93), noneErr.Reasons[0].AgentBackendID)
		assert.Equal(t, chat_svc.BlockReasonExecTargetProjectPathMissing, noneErr.Reasons[0].Reason)
	}
	var httpErr *httputils.Error
	require.True(t, errors.As(err, &httpErr))
	assert.Equal(t, code.ChatAgentNoAvailableExecTarget, httpErr.Code)
}

// ── 会话不绑项目时不受路径这一项约束 ────────────────────────────────────────

func TestPickExecTarget_GivenFreeSession_WhenProjectIDZero_ThenProjectPathNotChecked(t *testing.T) {
	ctx, m, svc := setupPickExecTargetTest(t)
	m.execTarget.EXPECT().ListByAgent(ctx, int64(37)).Return([]*agent_entity.AgentExecTarget{
		{ID: 12, AgentID: 37, AgentBackendID: 94, SortOrder: 0},
	}, nil)
	m.backend.EXPECT().Find(ctx, int64(94)).Return(&agent_backend_entity.AgentBackend{
		ID: 94, Type: string(agent_backend_entity.TypeClaudeCode), LLMProviderKey: "",
	}, nil)
	// 不注册 m.project / m.projectLocation 的任何 EXPECT —— projectID<=0 时一次都不该被调用。

	choice, err := svc.PickExecTarget(ctx, 37, 0)
	require.NoError(t, err)
	require.NotNil(t, choice)
	assert.Equal(t, int64(94), choice.Backend.ID)
}

// ── ③ 全不可用时报错并逐档列出原因 ──────────────────────────────────────────

func TestPickExecTarget_GivenAllUnavailable_ThenReturnsErrorListingEachReason(t *testing.T) {
	ctx, m, svc := setupPickExecTargetTest(t)
	m.execTarget.EXPECT().ListByAgent(ctx, int64(38)).Return([]*agent_entity.AgentExecTarget{
		{ID: 13, AgentID: 38, AgentBackendID: 95, SortOrder: 0},
		{ID: 14, AgentID: 38, AgentBackendID: 96, SortOrder: 1},
	}, nil)
	m.backend.EXPECT().Find(ctx, int64(95)).Return(&agent_backend_entity.AgentBackend{
		ID: 95, Type: string(agent_backend_entity.TypeClaudeCode), DeviceID: "13",
	}, nil)
	m.remoteDevice.EXPECT().Get(ctx, int64(13)).Return(nil, errors.New("not found"))
	m.backend.EXPECT().Find(ctx, int64(96)).Return(&agent_backend_entity.AgentBackend{
		ID: 96, Type: string(agent_backend_entity.TypeBuiltin), LLMProviderKey: "missing-key",
	}, nil)
	m.provider.EXPECT().FindByKey(ctx, "missing-key").Return(nil, nil)

	choice, err := svc.PickExecTarget(ctx, 38, 0)
	require.Error(t, err)
	assert.Nil(t, choice)
	var noneErr *chat_svc.ExecTargetNoneAvailableError
	require.True(t, errors.As(err, &noneErr))
	if assert.Len(t, noneErr.Reasons, 2) {
		assert.Equal(t, int64(95), noneErr.Reasons[0].AgentBackendID)
		assert.Equal(t, chat_svc.BlockReasonExecTargetUnpaired, noneErr.Reasons[0].Reason)
		assert.Equal(t, int64(96), noneErr.Reasons[1].AgentBackendID)
		assert.Equal(t, chat_svc.BlockReasonBackendRequiresProvider, noneErr.Reasons[1].Reason)
	}
	var httpErr *httputils.Error
	require.True(t, errors.As(err, &httpErr))
	assert.Equal(t, code.ChatAgentNoAvailableExecTarget, httpErr.Code)
	// Wails 只透 Error() 字符串；逐档原因必须在这条字符串里才能到前端。
	assert.Contains(t, err.Error(), "95")
	assert.Contains(t, err.Error(), "96")
}

// ── 空列表的 Agent 不能起会话 ───────────────────────────────────────────────

func TestPickExecTarget_GivenEmptyTargetList_ThenReturnsChatAgentNoBackend(t *testing.T) {
	ctx, m, svc := setupPickExecTargetTest(t)
	m.execTarget.EXPECT().ListByAgent(ctx, int64(39)).Return(nil, nil)

	choice, err := svc.PickExecTarget(ctx, 39, 0)
	require.Error(t, err)
	assert.Nil(t, choice)
	var httpErr *httputils.Error
	require.True(t, errors.As(err, &httpErr))
	assert.Equal(t, code.ChatAgentNoBackend, httpErr.Code)
}

// ── 守卫：空 LLMProviderKey（CLI 登录态）一律判可用，不做任何预探测 ─────────

func TestPickExecTarget_GivenEmptyProviderKey_ThenAvailableWithoutProviderProbe(t *testing.T) {
	ctx, m, svc := setupPickExecTargetTest(t)
	m.execTarget.EXPECT().ListByAgent(ctx, int64(40)).Return([]*agent_entity.AgentExecTarget{
		{ID: 15, AgentID: 40, AgentBackendID: 97, SortOrder: 0},
	}, nil)
	m.backend.EXPECT().Find(ctx, int64(97)).Return(&agent_backend_entity.AgentBackend{
		ID: 97, Type: string(agent_backend_entity.TypeClaudeCode), LLMProviderKey: "",
	}, nil)
	// 不注册 m.provider.FindByKey 的任何 EXPECT —— 空 key 触碰它就是"做了预探测"，测试要因
	// unexpected call 而失败。

	choice, err := svc.PickExecTarget(ctx, 40, 0)
	require.NoError(t, err)
	require.NotNil(t, choice)
	assert.Equal(t, int64(97), choice.Backend.ID)
}
