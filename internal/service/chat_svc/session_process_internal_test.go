package chat_svc

import (
	"context"
	"testing"

	"github.com/cago-frame/cago/pkg/consts"
	"github.com/stretchr/testify/assert"
	"go.uber.org/mock/gomock"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/chat_entity"
	"github.com/agentre-hub/agentre/internal/pkg/protorpctest"
	"github.com/agentre-hub/agentre/internal/service/remote_device_svc/mock_remote_device_svc"
)

// Given 远程会话所在设备此刻在池里没有 *remote.Runtime(典型时刻:连接刚断,watchLeaseClosed
// 已把 entry 摘掉,而在飞会话的旧 runtime 正在重连、马上要把自己装回去),When 面板的 5 秒轮询
// 列出会话进程,Then 这次查询只借一条池化连接、用完即还,**不得为该设备造出 *remote.Runtime**。
//
// 造出来的那个会在新连接上注册通知 handler;旧 runtime 重连 adoptConn 时再注册同一批,
// protorpc 当场 panic「duplicate method ID」,整个 App 退出(CI e2e remote-peer 断连用例
// 2026-11-03 实测)。列出/结束会话进程是无状态 RPC,本就不需要会话 runtime。
func TestListRemoteSessionProcesses_GivenNoCachedRuntime_ThenDoesNotCreateOne(t *testing.T) {
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)

	installPairedDevice(t, ctrl, 7)
	installExecDaemonRecorder(t, ctrl)

	lease := mock_remote_device_svc.NewMockLease(ctrl)
	lease.EXPECT().Client().Return(protorpctest.WrapConnection(newRecordingDaemonClient())).AnyTimes()
	lease.EXPECT().Closed().Return(make(chan struct{})).AnyTimes()
	lease.EXPECT().Release().MinTimes(1)
	pool := mock_remote_device_svc.NewMockConnPool(ctrl)
	pool.EXPECT().Borrow(gomock.Any(), int64(7)).Return(lease, nil).MinTimes(1)

	svc := &chatSvc{emitter: NoopEmitter{}}
	svc.setConnPoolForTest(pool)

	be := &agent_backend_entity.AgentBackend{
		ID: 12, Type: string(agent_backend_entity.TypeClaudeCode), DeviceFingerprint: testDeviceFingerprint(7), Status: consts.ACTIVE,
	}
	sess := &chat_entity.Session{ID: 42, ConversationID: convID(42)}

	svc.listRemoteSessionProcesses(context.Background(), sess, be)

	assert.Nil(t, svc.remotePool().Peek(7), "无状态查询不得为设备建 remote runtime(否则与重连中的旧 runtime 抢注 handler)")
}
