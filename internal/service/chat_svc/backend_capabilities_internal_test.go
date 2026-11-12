package chat_svc

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/capability"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/runtimes/remote/wire"
	"github.com/agentre-hub/agentre/internal/pkg/protorpctest"
	"github.com/agentre-hub/agentre/internal/service/remote_device_svc/mock_remote_device_svc"
)

// BackendCapabilities 回答「这一档后端此刻能力集是什么、我们是否真的知道」。本机查注册表；
// 远端只看本机已经连着的那台设备上缓存的矩阵 —— 没有连接、没预取成功就是未知，
// 绝不为这次查询拨号，也不把占位矩阵当成设备上报的结果。
func TestBackendCapabilities(t *testing.T) {
	ctx := context.Background()

	t.Run("local backend reads the registered runtime", func(t *testing.T) {
		withMemory := capability.Capabilities{Set: map[capability.Capability]bool{capability.CapMemory: true}}
		t.Cleanup(agentruntime.SwapRuntimeForTest(agent_backend_entity.TypeClaudeCode, memoryRuntime{caps: withMemory}))
		svc := &chatSvc{emitter: NoopEmitter{}}
		caps, known, err := svc.BackendCapabilities(ctx, &agent_backend_entity.AgentBackend{Type: string(agent_backend_entity.TypeClaudeCode)})
		require.NoError(t, err)
		assert.True(t, known)
		assert.True(t, caps.Has(capability.CapMemory))
	})

	t.Run("local backend without a registered runtime is unknown", func(t *testing.T) {
		svc := &chatSvc{emitter: NoopEmitter{}}
		_, known, err := svc.BackendCapabilities(ctx, &agent_backend_entity.AgentBackend{Type: "no-such-type"})
		require.NoError(t, err)
		assert.False(t, known)
	})

	remoteBackend := func() *agent_backend_entity.AgentBackend {
		return &agent_backend_entity.AgentBackend{ID: 12, Type: string(agent_backend_entity.TypeClaudeCode), DeviceFingerprint: testDeviceFingerprint(7)}
	}
	// connect 让一轮在 7 号设备上借过连接；capsErr 非 nil 时那台设备的能力预取失败。
	connect := func(t *testing.T, ctrl *gomock.Controller, svc *chatSvc, caps capability.Capabilities, capsErr error) {
		t.Helper()
		installPairedDevice(t, ctrl, 7)
		installExecDaemonRecorder(t, ctrl)
		rec := newRecordingDaemonClient()
		rec.expect(wire.MethodCapabilities, func(_, result any) error {
			if capsErr != nil {
				return capsErr
			}
			*(result.(*wire.CapabilitiesResult)) = wire.CapabilitiesResult{Capabilities: caps}
			return nil
		})
		pool := mock_remote_device_svc.NewMockConnPool(ctrl)
		lease := mock_remote_device_svc.NewMockLease(ctrl)
		pool.EXPECT().Borrow(gomock.Any(), int64(7)).Return(lease, nil).Times(1)
		lease.EXPECT().Client().Return(protorpctest.WrapConnection(rec)).AnyTimes()
		lease.EXPECT().Closed().Return(make(chan struct{})).AnyTimes()
		lease.EXPECT().Release().AnyTimes()
		svc.setConnPoolForTest(pool)
		_, release, err := svc.borrowRemoteRuntimeForTurn(ctx, remoteBackend(), 42)
		require.NoError(t, err)
		release()
	}

	t.Run("remote device never connected is unknown and is not dialed", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		installPairedDevice(t, ctrl, 7)
		svc := &chatSvc{emitter: NoopEmitter{}}
		svc.setConnPoolForTest(mock_remote_device_svc.NewMockConnPool(ctrl)) // 任何 Borrow 都是 unexpected call
		_, known, err := svc.BackendCapabilities(ctx, remoteBackend())
		require.NoError(t, err)
		assert.False(t, known)
	})

	t.Run("remote device that reported memory is known and has it", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		svc := &chatSvc{emitter: NoopEmitter{}}
		connect(t, ctrl, svc, capability.Capabilities{Set: map[capability.Capability]bool{capability.CapMemory: true}}, nil)
		caps, known, err := svc.BackendCapabilities(ctx, remoteBackend())
		require.NoError(t, err)
		assert.True(t, known)
		assert.True(t, caps.Has(capability.CapMemory))
	})

	t.Run("remote device that reported without memory is known and lacks it", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		svc := &chatSvc{emitter: NoopEmitter{}}
		connect(t, ctrl, svc, capability.Capabilities{Set: map[capability.Capability]bool{capability.CapSteer: true}}, nil)
		caps, known, err := svc.BackendCapabilities(ctx, remoteBackend())
		require.NoError(t, err)
		assert.True(t, known)
		assert.False(t, caps.Has(capability.CapMemory))
	})

	t.Run("remote device whose capability prefetch failed is unknown, not placeholder", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		svc := &chatSvc{emitter: NoopEmitter{}}
		connect(t, ctrl, svc, capability.Capabilities{}, errors.New("boom"))
		_, known, err := svc.BackendCapabilities(ctx, remoteBackend())
		require.NoError(t, err)
		assert.False(t, known)
	})
}
