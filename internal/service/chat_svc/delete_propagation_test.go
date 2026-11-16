package chat_svc

import (
	"context"
	"errors"
	"sync"
	"testing"

	. "github.com/smartystreets/goconvey/convey"
	"go.uber.org/mock/gomock"

	"github.com/agentre-hub/agentre/internal/model/entity/chat_entity"
	"github.com/agentre-hub/agentre/internal/repository/chat_repo"
	"github.com/agentre-hub/agentre/internal/repository/chat_repo/mock_chat_repo"
	"github.com/agentre-hub/agentre/internal/repository/transcript_repo"
	"github.com/agentre-hub/agentre/internal/repository/transcript_repo/mock_transcript_repo"
	"github.com/agentre-hub/agentre/internal/service/remote_device_svc"
	"github.com/agentre-hub/agentre/internal/service/remote_device_svc/mock_remote_device_svc"
	"github.com/agentre-hub/agentre/internal/service/server_svc"
	"github.com/agentre-hub/agentre/pkg/wire/agentrewire"
	"github.com/agentre-hub/agentre/pkg/wire/protorpc"
)

const (
	propConvID       = "0194f3a2-7b1c-7d2e-8f30-1a2b3c4d5e6f"
	propCarrierFP    = "sha256:carrier-01"
	propExecDevice   = int64(7)
	propLocalSessID  = int64(41)
	propRemoteSessID = int64(42)
)

// fakeDeleteServer 只实现 DeleteSavedSession；其余方法靠嵌入的接口占位，被调到即 panic
// —— 删除传播除了这一个方法不该碰 server_svc 的任何别的东西。
type fakeDeleteServer struct {
	server_svc.ServerSvc
	mu     sync.Mutex
	calls  [][2]string
	status string
	err    error
}

func (f *fakeDeleteServer) DeleteSavedSession(_ context.Context, conversationID, machineFingerprint string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, [2]string{conversationID, machineFingerprint})
	return f.status, f.err
}

func (f *fakeDeleteServer) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

type deletePropMocks struct {
	ctx     context.Context
	session *mock_chat_repo.MockSessionRepo
	pending *mock_chat_repo.MockPendingDeleteRepo
	frame   *mock_transcript_repo.MockFrameSeqRepo
	message *mock_transcript_repo.MockMessageRepo
	recov   *mock_chat_repo.MockReplacementRecoveryCleanupRepo
	pool    *mock_remote_device_svc.MockConnPool
	server  *fakeDeleteServer
	svc     *chatSvc
}

func setupDeletePropagationTest(t *testing.T) *deletePropMocks {
	t.Helper()
	ctrl := gomock.NewController(t)
	m := &deletePropMocks{
		ctx:     context.Background(),
		session: mock_chat_repo.NewMockSessionRepo(ctrl),
		pending: mock_chat_repo.NewMockPendingDeleteRepo(ctrl),
		frame:   mock_transcript_repo.NewMockFrameSeqRepo(ctrl),
		message: mock_transcript_repo.NewMockMessageRepo(ctrl),
		recov:   mock_chat_repo.NewMockReplacementRecoveryCleanupRepo(ctrl),
		pool:    mock_remote_device_svc.NewMockConnPool(ctrl),
		server:  &fakeDeleteServer{},
	}
	prevSession, prevPending := chat_repo.Session(), chat_repo.PendingDelete()
	prevFrame, prevMessage := transcript_repo.FrameSeq(), transcript_repo.Message()
	prevRecov, prevServer := chat_repo.ReplacementRecoveryCleanup(), server_svc.Server()
	chat_repo.RegisterSession(m.session)
	chat_repo.RegisterPendingDelete(m.pending)
	transcript_repo.RegisterFrameSeq(m.frame)
	transcript_repo.RegisterMessage(m.message)
	chat_repo.RegisterReplacementRecoveryCleanup(m.recov)
	server_svc.SetDefault(m.server)
	t.Cleanup(func() {
		chat_repo.RegisterSession(prevSession)
		chat_repo.RegisterPendingDelete(prevPending)
		transcript_repo.RegisterFrameSeq(prevFrame)
		transcript_repo.RegisterMessage(prevMessage)
		chat_repo.RegisterReplacementRecoveryCleanup(prevRecov)
		server_svc.SetDefault(prevServer)
	})
	m.svc = NewChat(nil).(*chatSvc)
	m.svc.setConnPoolForTest(m.pool)
	return m
}

func (m *deletePropMocks) expectLocalDelete(id int64) {
	m.session.EXPECT().SoftDelete(gomock.Any(), id).Return(nil)
	m.frame.EXPECT().DeleteBySession(gomock.Any(), id).Return(int64(0), nil)
	m.message.EXPECT().DeleteFromSeq(gomock.Any(), id, 0).Return(int64(0), nil)
	m.recov.EXPECT().DeleteReplacementRecovery(gomock.Any(), id).Return(int64(0), nil)
}

func remoteSession() *chat_entity.Session {
	return &chat_entity.Session{
		ID: propRemoteSessID, ConversationID: propConvID,
		ExecDeviceID: propExecDevice, ExecDeviceFingerprint: propCarrierFP,
	}
}

func localSession() *chat_entity.Session {
	return &chat_entity.Session{ID: propLocalSessID, ConversationID: propConvID}
}

// newSessionDeleteDaemon 起一个只答 SESSION_DELETE 的假 agentred，收到的请求逐条交给 got。
func newSessionDeleteDaemon(t *testing.T, got chan<- *agentrewire.SessionDeleteRequest) *gitStateProtoClient {
	t.Helper()
	registry := protorpc.NewRegistry()
	protorpc.RegisterMethod(registry, uint32(agentrewire.RpcMethod_RPC_METHOD_SESSION_DELETE),
		func() *agentrewire.SessionDeleteRequest { return &agentrewire.SessionDeleteRequest{} },
		func(_ context.Context, req *agentrewire.SessionDeleteRequest) (*agentrewire.SessionDeleteResponse, error) {
			got <- req
			return &agentrewire.SessionDeleteResponse{Deleted: true}, nil
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

func TestDeleteByUser_PropagatesToOtherCopies(t *testing.T) {
	Convey("DeleteByUser：本机删掉之后，其它副本按登录态只走一条路", t, func() {
		m := setupDeletePropagationTest(t)

		Convey("已登录、远端对话：本机删完请求 server 删除并告诉它执行端指纹，不直连 agentred", func() {
			m.server.status = server_svc.MachineStatusDeleted
			m.session.EXPECT().Find(gomock.Any(), propRemoteSessID).Return(remoteSession(), nil)
			m.expectLocalDelete(propRemoteSessID)

			_, err := m.svc.DeleteByUser(m.ctx, &DeleteRequest{SessionID: propRemoteSessID})

			So(err, ShouldBeNil)
			So(m.server.calls, ShouldResemble, [][2]string{{propConvID, propCarrierFP}})
		})

		Convey("本机那份删除失败：照旧报错，不发起任何其它删除", func() {
			m.session.EXPECT().Find(gomock.Any(), propRemoteSessID).Return(remoteSession(), nil)
			m.session.EXPECT().SoftDelete(gomock.Any(), propRemoteSessID).Return(errors.New("disk full"))

			_, err := m.svc.DeleteByUser(m.ctx, &DeleteRequest{SessionID: propRemoteSessID})

			So(err, ShouldNotBeNil)
			So(m.server.callCount(), ShouldEqual, 0)
		})

		Convey("会话行已经不在：照旧删本机（幂等），没有身份可传播", func() {
			m.session.EXPECT().Find(gomock.Any(), propRemoteSessID).Return(nil, nil)
			m.expectLocalDelete(propRemoteSessID)

			_, err := m.svc.DeleteByUser(m.ctx, &DeleteRequest{SessionID: propRemoteSessID})

			So(err, ShouldBeNil)
			So(m.server.callCount(), ShouldEqual, 0)
		})
	})
}

// 入站删除（server 转来的、或别的设备发来的 SESSION_DELETE）走 chatSvc.Delete：只删本机
// 这份，不再往外传播（决策 10）——否则 server 转来的删除会被再发回 server。
func TestDelete_InboundPathDoesNotPropagate(t *testing.T) {
	Convey("Given 已登录且对话在配对的 agentred 上 When 入站路径调 Delete Then 不调 server、不借 agentred 连接、不记待删除", t, func() {
		m := setupDeletePropagationTest(t)
		m.server.status = server_svc.MachineStatusDeleted
		m.session.EXPECT().Find(gomock.Any(), gomock.Any()).Return(remoteSession(), nil).AnyTimes()
		m.expectLocalDelete(propRemoteSessID)
		// pool / pending 没有任何期望：被调到 gomock 即判失败。

		_, err := m.svc.Delete(m.ctx, &DeleteRequest{SessionID: propRemoteSessID})

		So(err, ShouldBeNil)
		So(m.server.callCount(), ShouldEqual, 0)
	})
}

func TestPropagateDelete(t *testing.T) {
	Convey("propagateDelete", t, func() {
		m := setupDeletePropagationTest(t)

		Convey("已登录：server 答执行端 pending → carrierOffline，不在本地记待删除（server 自己记了待办）", func() {
			m.server.status = server_svc.MachineStatusPending

			out := m.svc.propagateDelete(m.ctx, remoteSession())

			So(m.server.calls, ShouldResemble, [][2]string{{propConvID, propCarrierFP}})
			So(out, ShouldResemble, deletePropagation{carrierFingerprint: propCarrierFP, carrierOffline: true})
		})

		Convey("已登录、本机执行的对话：也请求 server（可能存过），执行端指纹留空", func() {
			m.server.status = server_svc.MachineStatusDeleted

			out := m.svc.propagateDelete(m.ctx, localSession())

			So(m.server.calls, ShouldResemble, [][2]string{{propConvID, ""}})
			So(out, ShouldResemble, deletePropagation{})
		})

		Convey("server 发不出去（够不着 / 4xx 拒绝）：本地记一笔 target=server 的待删除，带上执行端指纹", func() {
			for _, cause := range []error{server_svc.ErrServerUnreachable, errors.New("server: delete saved session rejected with code 10001")} {
				m.server.err = cause
				m.pending.EXPECT().Create(gomock.Any(), gomock.Any()).DoAndReturn(
					func(_ context.Context, row *chat_entity.PendingDelete) error {
						So(row.ConversationID, ShouldEqual, propConvID)
						So(row.Target, ShouldEqual, chat_entity.PendingDeleteTargetServer)
						So(row.MachineFingerprint, ShouldEqual, propCarrierFP)
						return nil
					})

				out := m.svc.propagateDelete(m.ctx, remoteSession())

				So(out, ShouldResemble, deletePropagation{serverPending: true, carrierFingerprint: propCarrierFP})
			}
		})

		Convey("没登录、远端对话：直连那台 agentred 发 SESSION_DELETE，发起端指纹留空，不记待删除", func() {
			m.server.err = server_svc.ErrNotLoggedIn
			got := make(chan *agentrewire.SessionDeleteRequest, 1)
			daemon := newSessionDeleteDaemon(t, got)
			lease := mock_remote_device_svc.NewMockLease(gomock.NewController(t))
			lease.EXPECT().Client().Return(daemon).AnyTimes()
			lease.EXPECT().Release()
			m.pool.EXPECT().Borrow(gomock.Any(), propExecDevice).Return(lease, nil)

			out := m.svc.propagateDelete(m.ctx, remoteSession())

			So(out, ShouldResemble, deletePropagation{carrierFingerprint: propCarrierFP})
			req := <-got
			So(req.GetConversationId(), ShouldEqual, propConvID)
			So(req.GetPeerFingerprint(), ShouldEqual, "")
		})

		Convey("没登录、agentred 离线（借不到连接）：本地记一笔 target=agentred 的待删除 → carrierOffline", func() {
			m.server.err = server_svc.ErrNotLoggedIn
			m.pool.EXPECT().Borrow(gomock.Any(), propExecDevice).Return(nil, remote_device_svc.ErrDeviceUnauthorized)
			m.pending.EXPECT().Create(gomock.Any(), gomock.Any()).DoAndReturn(
				func(_ context.Context, row *chat_entity.PendingDelete) error {
					So(row.ConversationID, ShouldEqual, propConvID)
					So(row.Target, ShouldEqual, chat_entity.PendingDeleteTargetAgentred)
					So(row.DeviceID, ShouldEqual, propExecDevice)
					So(row.MachineFingerprint, ShouldEqual, propCarrierFP)
					return nil
				})

			out := m.svc.propagateDelete(m.ctx, remoteSession())

			So(out, ShouldResemble, deletePropagation{carrierFingerprint: propCarrierFP, carrierOffline: true})
		})

		Convey("没登录、本机执行的对话：只删本机，什么都不发、不记", func() {
			m.server.err = server_svc.ErrNotLoggedIn

			out := m.svc.propagateDelete(m.ctx, localSession())

			So(out, ShouldResemble, deletePropagation{})
		})

		Convey("登录在这次请求里被 server 作废（凭据被拒）：按没登录处理，不给 server 记待删除", func() {
			m.server.err = server_svc.ErrRefreshRejected

			out := m.svc.propagateDelete(m.ctx, localSession())

			So(out, ShouldResemble, deletePropagation{})
		})

		Convey("对话没有全局身份：无从指称，什么都不发", func() {
			sess := remoteSession()
			sess.ConversationID = ""

			out := m.svc.propagateDelete(m.ctx, sess)

			So(m.server.callCount(), ShouldEqual, 0)
			So(out, ShouldResemble, deletePropagation{carrierFingerprint: propCarrierFP})
		})
	})
}
