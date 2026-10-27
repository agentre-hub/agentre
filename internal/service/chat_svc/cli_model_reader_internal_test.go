package chat_svc

import (
	"context"
	"testing"
	"time"

	"github.com/cago-frame/cago/pkg/consts"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-hub/agentre/internal/pkg/protorpctest"
	"github.com/agentre-hub/agentre/internal/service/remote_device_svc/mock_remote_device_svc"
)

// Given 一条远端会话在本机已开着的那条连接上跑完了一轮（轮末 release 把 lease 还给了
// 池，但连接与它上面的 *remote.Runtime 都还在），When 打开这条会话读 pill 的 CLI 模型账，
// Then 读侧必须交出那个 runtime 记下的账 —— 不拨号、不占池引用、不落库。
//
// 轮与轮之间正是用户看 pill 的时候：读侧只认「此刻握着 lease」的话，一轮结束 pill 就
// 退回「跟随 Agent 绑定」不带模型名，而本机会话同一时刻显示的是 CLI 上报的模型。
// 连接被池回收（Lease.Closed）之后 entry 摘掉，读侧回 nil，pill 不猜。
func TestCLIModelStateReader_GivenRemoteTurnReleased_ThenStillReadsCachedRuntime(t *testing.T) {
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)

	backend := &agent_backend_entity.AgentBackend{
		ID: 12, Type: string(agent_backend_entity.TypeClaudeCode), DeviceFingerprint: testDeviceFingerprint(7), Status: consts.ACTIVE,
	}
	installPairedDevice(t, ctrl, 7)
	installExecDaemonRecorder(t, ctrl)

	client := newRecordingDaemonClient()
	closed := make(chan struct{})
	pool := mock_remote_device_svc.NewMockConnPool(ctrl)
	lease := mock_remote_device_svc.NewMockLease(ctrl)
	lease.EXPECT().Client().Return(protorpctest.WrapConnection(client)).AnyTimes()
	lease.EXPECT().Closed().Return(closed).AnyTimes()
	lease.EXPECT().Release().Times(1)
	// 只许那一轮自己借这一次：读侧再借一条就是 unexpected call。
	pool.EXPECT().Borrow(gomock.Any(), int64(7)).Return(lease, nil).Times(1)

	svc := &chatSvc{emitter: NoopEmitter{}}
	svc.setConnPoolForTest(pool)
	ctx := context.Background()

	require.Nil(t, svc.cliModelStateReader(ctx, backend, 42, 7), "从没开过连接：不拨号，交 nil")

	rt, release, err := svc.borrowRemoteRuntimeForTurn(ctx, backend, 42)
	require.NoError(t, err)
	release()

	reader := svc.cliModelStateReader(ctx, backend, 42, 7)
	require.NotNil(t, reader, "轮间连接还在：pill 要读得到那条连接上记下的 CLI 模型账")
	// 账记在跑那一轮的 runtime 上（轮末执行端上报的模型），读侧必须就是它，不能另造一个。
	assert.Same(t, rt, reader)
	assert.Zero(t, svc.remoteRuntimeCount(7), "只读查询不得给会话记引用")

	close(closed)
	require.Eventually(t, func() bool { return svc.cliModelStateReader(ctx, backend, 42, 7) == nil },
		time.Second, 5*time.Millisecond, "连接被池回收后 entry 摘掉，读侧回 nil")
}
