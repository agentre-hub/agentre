package chat_svc

import (
	"context"
	"sync"
	"testing"
	"time"

	. "github.com/smartystreets/goconvey/convey"
	"go.uber.org/mock/gomock"

	"github.com/agentre-hub/agentre/internal/model/entity/chat_entity"
	"github.com/agentre-hub/agentre/internal/service/remote_device_svc"
	"github.com/agentre-hub/agentre/internal/service/remote_device_svc/mock_remote_device_svc"
	"github.com/agentre-hub/agentre/internal/service/server_svc"
	"github.com/agentre-hub/agentre/pkg/wire/agentrewire"
	"github.com/agentre-hub/agentre/pkg/wire/protorpc"
)

const replayConvB = "0194f3a2-7b1c-7d2e-8f30-aaaaaaaaaaaa"

func serverRow(conv, fp string) *chat_entity.PendingDelete {
	return &chat_entity.PendingDelete{ConversationID: conv, Target: chat_entity.PendingDeleteTargetServer, MachineFingerprint: fp}
}

func agentredRow(conv string, device int64) *chat_entity.PendingDelete {
	return &chat_entity.PendingDelete{
		ConversationID: conv, Target: chat_entity.PendingDeleteTargetAgentred,
		MachineFingerprint: propCarrierFP, DeviceID: device,
	}
}

// newReplayDaemon 起一个答 SESSION_DELETE 的假 agentred，handler 决定答什么。
func newReplayDaemon(t *testing.T, handler func(*agentrewire.SessionDeleteRequest) (*agentrewire.SessionDeleteResponse, error)) *gitStateProtoClient {
	t.Helper()
	registry := protorpc.NewRegistry()
	protorpc.RegisterMethod(registry, uint32(agentrewire.RpcMethod_RPC_METHOD_SESSION_DELETE),
		func() *agentrewire.SessionDeleteRequest { return &agentrewire.SessionDeleteRequest{} },
		func(_ context.Context, req *agentrewire.SessionDeleteRequest) (*agentrewire.SessionDeleteResponse, error) {
			return handler(req)
		})
	a, b, done, once := make(chan []byte, 4), make(chan []byte, 4), make(chan struct{}), &sync.Once{}
	clientConn := protorpc.NewConn(&gitStateProtoPipe{in: a, out: b, done: done, once: once}, protorpc.NewRegistry())
	serverConn := protorpc.NewConn(&gitStateProtoPipe{in: b, out: a, done: done, once: once}, registry)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(func() { cancel(); _ = clientConn.Close(); _ = serverConn.Close() })
	go clientConn.Serve(ctx)
	go serverConn.Serve(ctx)
	return &gitStateProtoClient{conn: clientConn, done: done}
}

func (m *deletePropMocks) lendDaemon(t *testing.T, device int64, d *gitStateProtoClient) {
	lease := mock_remote_device_svc.NewMockLease(gomock.NewController(t))
	lease.EXPECT().Client().Return(d).AnyTimes()
	lease.EXPECT().Release()
	m.pool.EXPECT().Borrow(gomock.Any(), device).Return(lease, nil)
}

func TestReplayPendingDeletes_ServerRows(t *testing.T) {
	Convey("补发 target=server 的待删除", t, func() {
		m := setupDeletePropagationTest(t)
		sc := replayScope{server: true}

		Convey("已登录：带着记下的执行端指纹重发，deleted 与 pending 都算成功并清掉这一行", func() {
			m.pending.EXPECT().List(gomock.Any()).Return([]*chat_entity.PendingDelete{
				serverRow(propConvID, propCarrierFP), serverRow(replayConvB, ""),
			}, nil)
			m.pending.EXPECT().Remove(gomock.Any(), propConvID, chat_entity.PendingDeleteTargetServer).Return(nil)
			m.pending.EXPECT().Remove(gomock.Any(), replayConvB, chat_entity.PendingDeleteTargetServer).Return(nil)
			m.server.status = server_svc.MachineStatusPending

			m.svc.replayPendingDeletes(m.ctx, sc)

			So(m.server.calls, ShouldResemble, [][2]string{{propConvID, propCarrierFP}, {replayConvB, ""}})
		})

		Convey("server 发不出去或拒绝：行留着（不调 Remove）", func() {
			m.pending.EXPECT().List(gomock.Any()).Return([]*chat_entity.PendingDelete{serverRow(propConvID, propCarrierFP)}, nil)
			m.server.err = server_svc.ErrServerUnreachable

			m.svc.replayPendingDeletes(m.ctx, sc)

			So(m.server.callCount(), ShouldEqual, 1)
		})

		Convey("没登录：行留着，且不再对每一行重复请求", func() {
			m.pending.EXPECT().List(gomock.Any()).Return([]*chat_entity.PendingDelete{
				serverRow(propConvID, propCarrierFP), serverRow(replayConvB, ""),
			}, nil)
			m.server.err = server_svc.ErrNotLoggedIn

			m.svc.replayPendingDeletes(m.ctx, sc)

			So(m.server.callCount(), ShouldEqual, 1)
		})

		Convey("server_svc 未装配：等同没登录，行留着", func() {
			m.pending.EXPECT().List(gomock.Any()).Return([]*chat_entity.PendingDelete{serverRow(propConvID, "")}, nil)
			server_svc.SetDefault(nil)

			m.svc.replayPendingDeletes(m.ctx, sc)
		})

		Convey("凭据被作废：按没登录处理，行留着", func() {
			m.pending.EXPECT().List(gomock.Any()).Return([]*chat_entity.PendingDelete{serverRow(propConvID, "")}, nil)
			m.server.err = server_svc.ErrRefreshRejected

			m.svc.replayPendingDeletes(m.ctx, sc)
		})

		Convey("范围是 server 时不碰 agentred 行", func() {
			m.pending.EXPECT().List(gomock.Any()).Return([]*chat_entity.PendingDelete{agentredRow(propConvID, propExecDevice)}, nil)

			m.svc.replayPendingDeletes(m.ctx, sc)

			So(m.server.callCount(), ShouldEqual, 0)
		})

		Convey("列表读不出来：什么都不发", func() {
			m.pending.EXPECT().List(gomock.Any()).Return(nil, context.DeadlineExceeded)

			m.svc.replayPendingDeletes(m.ctx, sc)

			So(m.server.callCount(), ShouldEqual, 0)
		})
	})
}

func TestReplayPendingDeletes_AgentredRows(t *testing.T) {
	Convey("补发 target=agentred 的待删除", t, func() {
		m := setupDeletePropagationTest(t)
		sc := replayScope{devices: map[int64]bool{propExecDevice: true}}

		Convey("经那台配对设备发 SESSION_DELETE，deleted:true 清掉这一行", func() {
			got := make(chan *agentrewire.SessionDeleteRequest, 1)
			m.lendDaemon(t, propExecDevice, newReplayDaemon(t, func(r *agentrewire.SessionDeleteRequest) (*agentrewire.SessionDeleteResponse, error) {
				got <- r
				return &agentrewire.SessionDeleteResponse{Deleted: true}, nil
			}))
			m.pending.EXPECT().List(gomock.Any()).Return([]*chat_entity.PendingDelete{agentredRow(propConvID, propExecDevice)}, nil)
			m.pending.EXPECT().Remove(gomock.Any(), propConvID, chat_entity.PendingDeleteTargetAgentred).Return(nil)

			m.svc.replayPendingDeletes(m.ctx, sc)

			So((<-got).GetConversationId(), ShouldEqual, propConvID)
		})

		Convey("对端答 -32002（没有这条会话）：同样清掉", func() {
			m.lendDaemon(t, propExecDevice, newReplayDaemon(t, func(*agentrewire.SessionDeleteRequest) (*agentrewire.SessionDeleteResponse, error) {
				return nil, &protorpc.Error{Code: -32002, Message: "desktop peer session not found"}
			}))
			m.pending.EXPECT().List(gomock.Any()).Return([]*chat_entity.PendingDelete{agentredRow(propConvID, propExecDevice)}, nil)
			m.pending.EXPECT().Remove(gomock.Any(), propConvID, chat_entity.PendingDeleteTargetAgentred).Return(nil)

			m.svc.replayPendingDeletes(m.ctx, sc)
		})

		Convey("对端答别的错误：行留着", func() {
			m.lendDaemon(t, propExecDevice, newReplayDaemon(t, func(*agentrewire.SessionDeleteRequest) (*agentrewire.SessionDeleteResponse, error) {
				return nil, &protorpc.Error{Code: protorpc.CodeInternal, Message: "boom"}
			}))
			m.pending.EXPECT().List(gomock.Any()).Return([]*chat_entity.PendingDelete{agentredRow(propConvID, propExecDevice)}, nil)

			m.svc.replayPendingDeletes(m.ctx, sc)
		})

		Convey("借不到连接（离线）：行留着", func() {
			m.pool.EXPECT().Borrow(gomock.Any(), propExecDevice).Return(nil, remote_device_svc.ErrDeviceUnauthorized)
			m.pending.EXPECT().List(gomock.Any()).Return([]*chat_entity.PendingDelete{agentredRow(propConvID, propExecDevice)}, nil)

			m.svc.replayPendingDeletes(m.ctx, sc)
		})

		Convey("只经这台设备：别的设备的行与 server 行都不碰", func() {
			m.pending.EXPECT().List(gomock.Any()).Return([]*chat_entity.PendingDelete{
				agentredRow(propConvID, propExecDevice+1), serverRow(replayConvB, ""),
			}, nil)

			m.svc.replayPendingDeletes(m.ctx, sc)

			So(m.server.callCount(), ShouldEqual, 0)
		})

		Convey("全量范围（启动）：server 行与 agentred 行都补发", func() {
			m.server.status = server_svc.MachineStatusDeleted
			m.lendDaemon(t, propExecDevice, newReplayDaemon(t, func(*agentrewire.SessionDeleteRequest) (*agentrewire.SessionDeleteResponse, error) {
				return &agentrewire.SessionDeleteResponse{Deleted: true}, nil
			}))
			m.pending.EXPECT().List(gomock.Any()).Return([]*chat_entity.PendingDelete{
				serverRow(replayConvB, ""), agentredRow(propConvID, propExecDevice),
			}, nil)
			m.pending.EXPECT().Remove(gomock.Any(), replayConvB, chat_entity.PendingDeleteTargetServer).Return(nil)
			m.pending.EXPECT().Remove(gomock.Any(), propConvID, chat_entity.PendingDeleteTargetAgentred).Return(nil)

			m.svc.replayPendingDeletes(m.ctx, replayScope{all: true})

			So(m.server.callCount(), ShouldEqual, 1)
		})
	})
}

func waitClosed(t *testing.T, ch <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(3 * time.Second):
		t.Fatalf("timed out waiting for %s", what)
	}
}

func TestReplayScheduler(t *testing.T) {
	Convey("补发调度：不重叠、不阻塞触发方、不丢触发", t, func() {
		var mu sync.Mutex
		var scopes []replayScope
		active, maxActive := 0, 0
		entered := make(chan struct{}, 4)
		release := make(chan struct{})
		allDone := make(chan struct{}, 4)
		sched := &replayScheduler{run: func(_ context.Context, sc replayScope) {
			mu.Lock()
			active++
			if active > maxActive {
				maxActive = active
			}
			scopes = append(scopes, sc)
			mu.Unlock()
			entered <- struct{}{}
			<-release
			mu.Lock()
			active--
			mu.Unlock()
			allDone <- struct{}{}
		}}

		returned := make(chan struct{})
		go func() {
			sched.schedule(replayScope{server: true})
			<-entered // 第一轮已在跑，且被卡住
			sched.schedule(replayScope{devices: map[int64]bool{7: true}})
			sched.schedule(replayScope{devices: map[int64]bool{8: true}})
			close(returned)
		}()
		waitClosed(t, returned, "schedule() to return while a replay is blocked")

		// 第一轮仍被卡住时，后到的触发不得另起一轮并发补发。
		time.Sleep(100 * time.Millisecond)
		mu.Lock()
		So(active, ShouldEqual, 1)
		So(len(scopes), ShouldEqual, 1)
		mu.Unlock()

		close(release)
		<-allDone // 第一轮
		<-entered // 合并后的第二轮
		<-allDone

		mu.Lock()
		defer mu.Unlock()
		So(maxActive, ShouldEqual, 1)
		So(len(scopes), ShouldEqual, 2)
		So(scopes[0].server, ShouldBeTrue)
		So(scopes[1].server, ShouldBeFalse)
		So(scopes[1].devices, ShouldResemble, map[int64]bool{7: true, 8: true})
	})
}
