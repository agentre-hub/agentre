package agent_backend_svc

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-hub/agentre/internal/service/remote_device_svc"
)

// ClaimRelativeBackends（R13 运行期认领）把本机 backend 的 DeviceID 从空串改写成本机
// 指纹，展示侧的 toItem 却仍按「非空 DeviceID == 远端配对设备」解析：本机指纹永远不在
// paired_agentreds 里（不会和自己配对），于是 DeviceName 空、Online 假，组织架构页的
// 执行目标行渲染成「这台电脑未配对它 · 离线」。
//
// 契约与 chat_svc.execDeviceID 同一条：指向本机的 self 档在展示口径上是本机档。
func TestListBackends_GivenSelfFingerprintBackend_ThenItemReportsLocalDevice(t *testing.T) {
	ctx, backendMock, _, agentMock, rd, _, svc := setupSvcTestWithRemoteDevice(t)
	rd.EXPECT().DeviceFingerprint().Return("sha256:self", nil).AnyTimes()
	// 本机指纹不在配对表里——bug 的触发条件，桩如实反映。
	rd.EXPECT().List(ctx).Return(nil, nil).AnyTimes()

	backendMock.EXPECT().List(ctx).Return([]*agent_backend_entity.AgentBackend{{
		ID: 234, Type: string(agent_backend_entity.TypePiAgent), Name: "Pi Agent CLI",
		DeviceFingerprint: "sha256:self", Status: 1,
	}}, nil)
	agentMock.EXPECT().CountByBackends(ctx, []int64{234}).Return(map[int64]int64{}, nil)

	resp, err := svc.List(ctx, &ListBackendsRequest{})
	require.NoError(t, err)
	require.Len(t, resp.Items, 1)
	assert.Equal(t, "", resp.Items[0].DeviceID,
		"本机档必须报空 DeviceID，否则组织架构页渲染成「这台电脑未配对它」")
	assert.Equal(t, "", resp.Items[0].DeviceName)
}

// 反向守卫：真正的远端指纹（不是本机、也没在本机配对）仍然如实报告为未解析的远端档，
// 修复不能把「未配对」这一真实状态一起抹掉。
func TestListBackends_GivenUnpairedRemoteFingerprint_ThenItemKeepsRemoteDevice(t *testing.T) {
	ctx, backendMock, _, agentMock, rd, _, svc := setupSvcTestWithRemoteDevice(t)
	rd.EXPECT().DeviceFingerprint().Return("sha256:self", nil).AnyTimes()
	rd.EXPECT().List(ctx).Return(nil, nil).AnyTimes()

	backendMock.EXPECT().List(ctx).Return([]*agent_backend_entity.AgentBackend{{
		ID: 235, Type: string(agent_backend_entity.TypeClaudeCode), Name: "coding",
		DeviceFingerprint: "sha256:other-box", Status: 1,
	}}, nil)
	agentMock.EXPECT().CountByBackends(ctx, []int64{235}).Return(map[int64]int64{}, nil)

	resp, err := svc.List(ctx, &ListBackendsRequest{})
	require.NoError(t, err)
	require.Len(t, resp.Items, 1)
	assert.Equal(t, "sha256:other-box", resp.Items[0].DeviceID)
	assert.False(t, resp.Items[0].Online)
}

// IsSelfDevice 的直接守卫：本机指纹为真、别的指纹为假，且不因指纹恰好没配对而漂移。
func TestIsSelfDevice_GivenSelfFingerprint_ThenTrue(t *testing.T) {
	_, _, _, _, rd, _, _ := setupSvcTestWithRemoteDevice(t)
	rd.EXPECT().DeviceFingerprint().Return("sha256:self", nil).AnyTimes()

	assert.True(t, remote_device_svc.IsSelfDevice("sha256:self"))
	assert.False(t, remote_device_svc.IsSelfDevice("sha256:other-box"))
	assert.False(t, remote_device_svc.IsSelfDevice(""))
}
