package chat_svc_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/agent_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/project_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/project_location_entity"
	"github.com/agentre-hub/agentre/internal/service/chat_svc"
)

// ── R15 / 任务 12：组织架构页需要「每一档」的可用性，不只是最终派发到哪一档 ──

// TestListExecTargetAvailability_GivenAllUnavailable_ThenEvaluatesEveryTarget 与
// PickExecTarget 的关键差异：PickExecTarget 找到第一个可用档就提前返回（第二档的
// Find 从不会被调用）；ListExecTargetAvailability 要给组织架构页展示全部档的状态，
// 因此就算前面已经有可用档，后面的档也必须照样判定——这里用「全部不可用」这个
// 已有场景验证顺序与个数，用一个「第一档可用」的场景验证后续档不会被跳过。
func TestListExecTargetAvailability_GivenAllUnavailable_ThenEvaluatesEveryTarget(t *testing.T) {
	ctx, m, svc := setupPickExecTargetTest(t)
	m.execTarget.EXPECT().ListByAgent(ctx, int64(38)).Return([]*agent_entity.AgentExecTarget{
		{ID: 13, AgentID: 38, AgentBackendID: 95, SortOrder: 0},
		{ID: 14, AgentID: 38, AgentBackendID: 96, SortOrder: 1},
	}, nil)
	m.backend.EXPECT().Find(ctx, int64(95)).Return(&agent_backend_entity.AgentBackend{
		ID: 95, Type: string(agent_backend_entity.TypeClaudeCode), DeviceFingerprint: pickTestFingerprint(13),
	}, nil)
	m.pairedDevices()
	m.backend.EXPECT().Find(ctx, int64(96)).Return(&agent_backend_entity.AgentBackend{
		ID: 96, Type: string(agent_backend_entity.TypeBuiltin), LLMProviderKey: "missing-key",
	}, nil)
	m.provider.EXPECT().FindByKey(ctx, "missing-key").Return(nil, nil)

	statuses, err := svc.ListExecTargetAvailability(ctx, 38, 0)
	require.NoError(t, err)
	if assert.Len(t, statuses, 2) {
		assert.Equal(t, int64(95), statuses[0].AgentBackendID)
		assert.False(t, statuses[0].Available)
		assert.Equal(t, chat_svc.BlockReasonExecTargetUnpaired, statuses[0].Reason)
		assert.NotEmpty(t, statuses[0].Hint)

		assert.Equal(t, int64(96), statuses[1].AgentBackendID)
		assert.False(t, statuses[1].Available)
		assert.Equal(t, chat_svc.BlockReasonBackendRequiresProvider, statuses[1].Reason)
	}
}

// TestListExecTargetAvailability_GivenFirstAvailable_ThenStillEvaluatesTheRest 锁住
// 「不提前返回」这个与 PickExecTarget 相反的行为：第一档可用，但第二档仍然要被判定
// （这里配成同样可用），两档都要出现在结果里。
func TestListExecTargetAvailability_GivenFirstAvailable_ThenStillEvaluatesTheRest(t *testing.T) {
	ctx, m, svc := setupPickExecTargetTest(t)
	m.execTarget.EXPECT().ListByAgent(ctx, int64(31)).Return([]*agent_entity.AgentExecTarget{
		{ID: 1, AgentID: 31, AgentBackendID: 51, SortOrder: 0},
		{ID: 2, AgentID: 31, AgentBackendID: 52, SortOrder: 1},
	}, nil)
	m.backend.EXPECT().Find(ctx, int64(51)).Return(&agent_backend_entity.AgentBackend{
		ID: 51, Type: string(agent_backend_entity.TypeClaudeCode), LLMProviderKey: "",
	}, nil)
	m.backend.EXPECT().Find(ctx, int64(52)).Return(&agent_backend_entity.AgentBackend{
		ID: 52, Type: string(agent_backend_entity.TypeClaudeCode), LLMProviderKey: "",
	}, nil)

	statuses, err := svc.ListExecTargetAvailability(ctx, 31, 0)
	require.NoError(t, err)
	if assert.Len(t, statuses, 2) {
		assert.True(t, statuses[0].Available)
		assert.Equal(t, chat_svc.BlockReason(""), statuses[0].Reason)
		assert.True(t, statuses[1].Available)
	}
}

// TestListExecTargetAvailability_GivenProjectBound_ThenCarriesEachMachineProjectPath
// 锁住 R15a 改选浮层要展示的那一项：每一档都带「那台机器上这个项目的路径」——路径
// 回答的是「换过去在哪个目录干活」，比机器名更有信息量。本机档取 projects.path，
// agentred 档取 project_locations 里该指纹那一行。
func TestListExecTargetAvailability_GivenProjectBound_ThenCarriesEachMachineProjectPath(t *testing.T) {
	ctx, m, svc := setupPickExecTargetTest(t)
	m.execTarget.EXPECT().ListByAgent(ctx, int64(41)).Return([]*agent_entity.AgentExecTarget{
		{ID: 21, AgentID: 41, AgentBackendID: 81, SortOrder: 0},
		{ID: 22, AgentID: 41, AgentBackendID: 82, SortOrder: 1},
	}, nil)
	m.backend.EXPECT().Find(ctx, int64(81)).Return(&agent_backend_entity.AgentBackend{
		ID: 81, Type: string(agent_backend_entity.TypeClaudeCode), LLMProviderKey: "",
	}, nil)
	m.backend.EXPECT().Find(ctx, int64(82)).Return(&agent_backend_entity.AgentBackend{
		ID: 82, Type: string(agent_backend_entity.TypeClaudeCode), LLMProviderKey: "", DeviceFingerprint: pickTestFingerprint(13),
	}, nil)
	m.project.EXPECT().Find(ctx, int64(900)).
		Return(&project_entity.Project{ID: 900, Path: "/Users/me/code/app"}, nil).MinTimes(1)
	m.pairedDevices(pairedDevice(13, true))
	m.projectLocation.EXPECT().FindByProjectAndFingerprint(ctx, int64(900), pickTestFingerprint(13)).
		Return(&project_location_entity.ProjectLocation{Path: "/srv/app"}, nil).MinTimes(1)

	statuses, err := svc.ListExecTargetAvailability(ctx, 41, 900)
	require.NoError(t, err)
	if assert.Len(t, statuses, 2) {
		assert.True(t, statuses[0].Available)
		assert.Equal(t, "/Users/me/code/app", statuses[0].ProjectPath)
		assert.True(t, statuses[1].Available)
		assert.Equal(t, "/srv/app", statuses[1].ProjectPath)
	}
}

// TestListExecTargetAvailability_GivenUnavailableTarget_ThenStillCarriesProjectPath
// 边界：一档因为别的原因（这里是离线）不可用时，路径照样配着——改选浮层仍要把它
// 显示出来（用户据此判断「等它上线值不值」）。可用性判定在离线那一步就提前返回，
// 因此路径必须独立取，不能顺带。
func TestListExecTargetAvailability_GivenUnavailableTarget_ThenStillCarriesProjectPath(t *testing.T) {
	ctx, m, svc := setupPickExecTargetTest(t)
	m.execTarget.EXPECT().ListByAgent(ctx, int64(42)).Return([]*agent_entity.AgentExecTarget{
		{ID: 23, AgentID: 42, AgentBackendID: 83, SortOrder: 0},
	}, nil)
	m.backend.EXPECT().Find(ctx, int64(83)).Return(&agent_backend_entity.AgentBackend{
		ID: 83, Type: string(agent_backend_entity.TypeClaudeCode), LLMProviderKey: "", DeviceFingerprint: pickTestFingerprint(14),
	}, nil)
	m.pairedDevices(pairedDevice(14, false))
	m.projectLocation.EXPECT().FindByProjectAndFingerprint(ctx, int64(901), pickTestFingerprint(14)).
		Return(&project_location_entity.ProjectLocation{Path: "/srv/offline-app"}, nil).MinTimes(1)

	statuses, err := svc.ListExecTargetAvailability(ctx, 42, 901)
	require.NoError(t, err)
	if assert.Len(t, statuses, 1) {
		assert.False(t, statuses[0].Available)
		assert.Equal(t, chat_svc.BlockReasonExecTargetOffline, statuses[0].Reason)
		assert.Equal(t, "/srv/offline-app", statuses[0].ProjectPath)
	}
}

// TestListExecTargetAvailability_GivenNoPathOnThatMachine_ThenProjectPathIsEmpty
// 那台机器上没配这个项目的路径时给空串（浮层据此不渲染这一行，而不是渲染一行空的）；
// 不绑项目的会话（projectID<=0）同理不做这项查询。
func TestListExecTargetAvailability_GivenNoPathOnThatMachine_ThenProjectPathIsEmpty(t *testing.T) {
	ctx, m, svc := setupPickExecTargetTest(t)
	m.execTarget.EXPECT().ListByAgent(ctx, int64(43)).Return([]*agent_entity.AgentExecTarget{
		{ID: 24, AgentID: 43, AgentBackendID: 84, SortOrder: 0},
		{ID: 25, AgentID: 43, AgentBackendID: 85, SortOrder: 1},
	}, nil)
	m.backend.EXPECT().Find(ctx, int64(84)).Return(&agent_backend_entity.AgentBackend{
		ID: 84, Type: string(agent_backend_entity.TypeClaudeCode), LLMProviderKey: "",
	}, nil)
	m.backend.EXPECT().Find(ctx, int64(85)).Return(&agent_backend_entity.AgentBackend{
		ID: 85, Type: string(agent_backend_entity.TypeClaudeCode), LLMProviderKey: "", DeviceFingerprint: pickTestFingerprint(15),
	}, nil)
	m.project.EXPECT().Find(ctx, int64(902)).
		Return(&project_entity.Project{ID: 902, LocalPathMissing: true}, nil).MinTimes(1)
	m.pairedDevices(pairedDevice(15, true))
	m.projectLocation.EXPECT().FindByProjectAndFingerprint(ctx, int64(902), pickTestFingerprint(15)).
		Return(nil, gorm.ErrRecordNotFound).MinTimes(1)

	statuses, err := svc.ListExecTargetAvailability(ctx, 43, 902)
	require.NoError(t, err)
	if assert.Len(t, statuses, 2) {
		assert.Equal(t, chat_svc.BlockReasonExecTargetProjectPathMissing, statuses[0].Reason)
		assert.Empty(t, statuses[0].ProjectPath)
		assert.Equal(t, chat_svc.BlockReasonExecTargetProjectPathMissing, statuses[1].Reason)
		assert.Empty(t, statuses[1].ProjectPath)
	}
}

// TestListExecTargetAvailability_GivenEmptyTargetList_ThenReturnsEmptySlice 空列表
// 不是错误——R15 的「保存被拒」发生在写路径，读路径只需要如实报告「没有档」。
func TestListExecTargetAvailability_GivenEmptyTargetList_ThenReturnsEmptySlice(t *testing.T) {
	ctx, m, svc := setupPickExecTargetTest(t)
	m.execTarget.EXPECT().ListByAgent(ctx, int64(39)).Return(nil, nil)

	statuses, err := svc.ListExecTargetAvailability(ctx, 39, 0)
	require.NoError(t, err)
	assert.Empty(t, statuses)
}
