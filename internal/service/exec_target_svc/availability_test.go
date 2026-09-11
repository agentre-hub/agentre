package exec_target_svc_test

import (
	"context"
	"errors"
	"testing"

	"github.com/cago-frame/cago/pkg/consts"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/agent_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/llm_provider_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/project_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/project_location_entity"
	"github.com/agentre-hub/agentre/internal/service/exec_target_svc"
	"github.com/agentre-hub/agentre/internal/service/remote_device_svc"
	"github.com/agentre-hub/agentre/internal/service/server_svc"
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
	m.listedBackend(&agent_backend_entity.AgentBackend{
		ID: 95, Type: string(agent_backend_entity.TypeClaudeCode), DeviceFingerprint: pickTestFingerprint(13),
	})
	m.pairedDevices()
	m.listedBackend(&agent_backend_entity.AgentBackend{
		ID: 96, Type: string(agent_backend_entity.TypeBuiltin), LLMProviderKey: "missing-key",
	})
	m.provider.EXPECT().ListByKeysAnyStatus(ctx, []string{"missing-key"}).
		Return(map[string]*llm_provider_entity.LLMProvider{}, nil)

	statuses, err := svc.ListExecTargetAvailability(ctx, 38, 0)
	require.NoError(t, err)
	if assert.Len(t, statuses, 2) {
		assert.Equal(t, int64(95), statuses[0].AgentBackendID)
		assert.False(t, statuses[0].Available)
		assert.Equal(t, exec_target_svc.BlockReasonExecTargetUnpaired, statuses[0].Reason)
		assert.NotEmpty(t, statuses[0].Hint)

		assert.Equal(t, int64(96), statuses[1].AgentBackendID)
		assert.False(t, statuses[1].Available)
		assert.Equal(t, exec_target_svc.BlockReasonBackendRequiresProvider, statuses[1].Reason)
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
	m.listedBackend(&agent_backend_entity.AgentBackend{
		ID: 51, Type: string(agent_backend_entity.TypeClaudeCode), LLMProviderKey: "",
	})
	m.listedBackend(&agent_backend_entity.AgentBackend{
		ID: 52, Type: string(agent_backend_entity.TypeClaudeCode), LLMProviderKey: "",
	})

	statuses, err := svc.ListExecTargetAvailability(ctx, 31, 0)
	require.NoError(t, err)
	if assert.Len(t, statuses, 2) {
		assert.True(t, statuses[0].Available)
		assert.Equal(t, exec_target_svc.BlockReason(""), statuses[0].Reason)
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
	m.listedBackend(&agent_backend_entity.AgentBackend{
		ID: 81, Type: string(agent_backend_entity.TypeClaudeCode), LLMProviderKey: "",
	})
	m.listedBackend(&agent_backend_entity.AgentBackend{
		ID: 82, Type: string(agent_backend_entity.TypeClaudeCode), LLMProviderKey: "", DeviceFingerprint: pickTestFingerprint(13),
	})
	m.project.EXPECT().Find(ctx, int64(900)).
		Return(&project_entity.Project{ID: 900, Path: "/Users/me/code/app"}, nil).MinTimes(1)
	m.pairedDevices(pairedDevice(13, true))
	m.projectLocation.EXPECT().ListByProject(ctx, int64(900)).Return([]*project_location_entity.ProjectLocation{
		{DeviceFingerprint: pickTestFingerprint(13), Path: "/srv/app"},
	}, nil)

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
	m.listedBackend(&agent_backend_entity.AgentBackend{
		ID: 83, Type: string(agent_backend_entity.TypeClaudeCode), LLMProviderKey: "", DeviceFingerprint: pickTestFingerprint(14),
	})
	m.pairedDevices(pairedDevice(14, false))
	m.projectLocation.EXPECT().ListByProject(ctx, int64(901)).Return([]*project_location_entity.ProjectLocation{
		{DeviceFingerprint: pickTestFingerprint(14), Path: "/srv/offline-app"},
	}, nil)

	statuses, err := svc.ListExecTargetAvailability(ctx, 42, 901)
	require.NoError(t, err)
	if assert.Len(t, statuses, 1) {
		assert.False(t, statuses[0].Available)
		assert.Equal(t, exec_target_svc.BlockReasonExecTargetOffline, statuses[0].Reason)
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
	m.listedBackend(&agent_backend_entity.AgentBackend{
		ID: 84, Type: string(agent_backend_entity.TypeClaudeCode), LLMProviderKey: "",
	})
	m.listedBackend(&agent_backend_entity.AgentBackend{
		ID: 85, Type: string(agent_backend_entity.TypeClaudeCode), LLMProviderKey: "", DeviceFingerprint: pickTestFingerprint(15),
	})
	m.project.EXPECT().Find(ctx, int64(902)).
		Return(&project_entity.Project{ID: 902, LocalPathMissing: true}, nil).MinTimes(1)
	m.pairedDevices(pairedDevice(15, true))
	m.projectLocation.EXPECT().ListByProject(ctx, int64(902)).Return(nil, nil)

	statuses, err := svc.ListExecTargetAvailability(ctx, 43, 902)
	require.NoError(t, err)
	if assert.Len(t, statuses, 2) {
		assert.Equal(t, exec_target_svc.BlockReasonExecTargetProjectPathMissing, statuses[0].Reason)
		assert.Empty(t, statuses[0].ProjectPath)
		assert.Equal(t, exec_target_svc.BlockReasonExecTargetProjectPathMissing, statuses[1].Reason)
		assert.Empty(t, statuses[1].ProjectPath)
	}
}

// countingServerSvc 记录账号设备清单（ListDevices，一次 ServerState 读 + 一次 HTTP）被拉了几次。
type countingServerSvc struct {
	stubServerSvc
	calls *int
}

func (s countingServerSvc) ListDevices(ctx context.Context) ([]server_svc.Device, error) {
	*s.calls++
	return s.stubServerSvc.ListDevices(ctx)
}

// 要求 19：可用性列表的查询条数与执行档数无关。两档远端（共享 provider）+ 一档本机、
// 会话绑项目：backend / provider 各批量取一次，配对表、账号设备清单、本机项目行、
// 项目路径表各取一次，逐档方法一次不调；软删 provider 的档仍判 ProviderInactive。
func TestListExecTargetAvailability_GivenRemoteAndLocalTargets_ThenEachSourceReadOnce(t *testing.T) {
	ctx, m, svc := setupPickExecTargetTest(t)
	listDevices := 0
	previousServer := server_svc.Server()
	server_svc.SetDefault(countingServerSvc{calls: &listDevices})
	t.Cleanup(func() { server_svc.SetDefault(previousServer) })

	m.execTarget.EXPECT().ListByAgent(ctx, int64(44)).Return([]*agent_entity.AgentExecTarget{
		{ID: 31, AgentID: 44, AgentBackendID: 61, SortOrder: 0},
		{ID: 32, AgentID: 44, AgentBackendID: 62, SortOrder: 1},
		{ID: 33, AgentID: 44, AgentBackendID: 63, SortOrder: 2},
	}, nil)
	remoteA := &agent_backend_entity.AgentBackend{
		ID: 61, Type: string(agent_backend_entity.TypeClaudeCode), LLMProviderKey: "key-a", DeviceFingerprint: pickTestFingerprint(21),
	}
	remoteB := &agent_backend_entity.AgentBackend{
		ID: 62, Type: string(agent_backend_entity.TypeClaudeCode), LLMProviderKey: "key-a", DeviceFingerprint: pickTestFingerprint(22),
	}
	local := &agent_backend_entity.AgentBackend{
		ID: 63, Type: string(agent_backend_entity.TypeBuiltin), LLMProviderKey: "key-gone",
	}
	m.backend.EXPECT().BatchFind(ctx, gomock.InAnyOrder([]int64{61, 62, 63})).
		Return(map[int64]*agent_backend_entity.AgentBackend{61: remoteA, 62: remoteB, 63: local}, nil).Times(1)
	m.backend.EXPECT().Find(gomock.Any(), gomock.Any()).Times(0)
	deleted := activeProvider("key-gone")
	deleted.Status = consts.DELETE
	m.provider.EXPECT().ListByKeysAnyStatus(ctx, gomock.InAnyOrder([]string{"key-a", "key-gone"})).
		Return(map[string]*llm_provider_entity.LLMProvider{"key-a": activeProvider("key-a"), "key-gone": deleted}, nil).Times(1)
	m.provider.EXPECT().FindByKey(gomock.Any(), gomock.Any()).Times(0)
	m.remoteDevice.EXPECT().List(ctx).
		Return([]*remote_device_svc.DeviceView{pairedDevice(21, true), pairedDevice(22, true)}, nil).Times(1)
	m.remoteDevice.EXPECT().ListDeviceProviders(gomock.Any()).Return(nil).AnyTimes()
	m.project.EXPECT().Find(ctx, int64(910)).Return(&project_entity.Project{ID: 910, Path: "/local/app"}, nil).Times(1)
	m.projectLocation.EXPECT().ListByProject(ctx, int64(910)).Return([]*project_location_entity.ProjectLocation{
		{ProjectID: 910, DeviceFingerprint: pickTestFingerprint(21), Path: "/srv/a"},
	}, nil).Times(1)
	m.projectLocation.EXPECT().FindByProjectAndFingerprint(gomock.Any(), gomock.Any(), gomock.Any()).Times(0)

	statuses, err := svc.ListExecTargetAvailability(ctx, 44, 910)
	require.NoError(t, err)
	require.Len(t, statuses, 3)
	assert.True(t, statuses[0].Available)
	assert.Equal(t, "/srv/a", statuses[0].ProjectPath)
	assert.Equal(t, "daemon", statuses[0].Kind)
	assert.Equal(t, exec_target_svc.BlockReasonExecTargetProjectPathMissing, statuses[1].Reason)
	assert.Empty(t, statuses[1].ProjectPath)
	assert.Equal(t, exec_target_svc.BlockReasonProviderInactive, statuses[2].Reason)
	assert.Equal(t, "/local/app", statuses[2].ProjectPath)
	assert.Equal(t, "local", statuses[2].Kind)
	assert.Equal(t, 1, listDevices, "账号设备清单每次列表只拉一次")
}

// 失败路径：provider 批量取数出错时列表整体报错，快照不能把错误吞成「供应商缺失」。
func TestListExecTargetAvailability_GivenProviderBatchError_ThenReturnsError(t *testing.T) {
	ctx, m, svc := setupPickExecTargetTest(t)
	m.execTarget.EXPECT().ListByAgent(ctx, int64(45)).Return([]*agent_entity.AgentExecTarget{
		{ID: 34, AgentID: 45, AgentBackendID: 64, SortOrder: 0},
		{ID: 35, AgentID: 45, AgentBackendID: 65, SortOrder: 1},
	}, nil)
	m.listedBackend(&agent_backend_entity.AgentBackend{ID: 64, Type: string(agent_backend_entity.TypeBuiltin), LLMProviderKey: "key-x"})
	m.listedBackend(&agent_backend_entity.AgentBackend{ID: 65, Type: string(agent_backend_entity.TypeBuiltin), LLMProviderKey: "key-x"})
	m.provider.EXPECT().ListByKeysAnyStatus(ctx, []string{"key-x"}).Return(nil, errors.New("db down")).Times(1)

	statuses, err := svc.ListExecTargetAvailability(ctx, 45, 0)
	require.Error(t, err)
	assert.Nil(t, statuses)
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
