package chat_svc

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	. "github.com/smartystreets/goconvey/convey"
	"go.uber.org/mock/gomock"

	"github.com/agentre-hub/agentre/internal/model/entity/chat_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/server_state_entity"
	"github.com/agentre-hub/agentre/internal/repository/chat_repo"
	"github.com/agentre-hub/agentre/internal/service/remote_device_svc"
	"github.com/agentre-hub/agentre/internal/service/remote_device_svc/mock_remote_device_svc"
	"github.com/agentre-hub/agentre/internal/service/server_svc"
)

// cleanupServer 在 fakeDeleteServer 之上补 GetState（清理要问登录态）与「并发 / 卡住」的
// 钩子，用来证明执行没有把慢传播串行化。
type cleanupServer struct {
	fakeDeleteServer
	loggedIn bool
	// gate 非 nil 时，DeleteSavedSession 先登记在飞，再等 gate 关闭才返回。
	gate     chan struct{}
	inflight int
	maxIn    int
}

func (c *cleanupServer) GetState(context.Context) (*server_state_entity.ServerState, error) {
	if !c.loggedIn {
		return &server_state_entity.ServerState{}, nil
	}
	return &server_state_entity.ServerState{ServerUserID: 1, DeviceID: 1, KeychainAccount: "k"}, nil
}

func (c *cleanupServer) DeleteSavedSession(ctx context.Context, conv, fp string) (string, error) {
	if c.gate != nil {
		c.mu.Lock()
		c.inflight++
		if c.inflight > c.maxIn {
			c.maxIn = c.inflight
		}
		c.calls = append(c.calls, [2]string{conv, fp})
		c.mu.Unlock()
		select {
		case <-c.gate:
		case <-time.After(5 * time.Second):
		}
		c.mu.Lock()
		c.inflight--
		c.mu.Unlock()
		return c.status, c.err
	}
	return c.fakeDeleteServer.DeleteSavedSession(ctx, conv, fp)
}

type cleanupMocks struct {
	*deletePropMocks
	srv *cleanupServer
	rds *mock_remote_device_svc.MockRemoteDeviceSvc
}

func setupCleanupTest(t *testing.T, loggedIn bool, devices ...*remote_device_svc.DeviceView) *cleanupMocks {
	t.Helper()
	m := setupDeletePropagationTest(t)
	srv := &cleanupServer{loggedIn: loggedIn}
	if !loggedIn {
		srv.err = server_svc.ErrNotLoggedIn
	} else {
		srv.status = server_svc.MachineStatusDeleted
	}
	server_svc.SetDefault(srv) // setupDeletePropagationTest 的 Cleanup 会还原
	rds := mock_remote_device_svc.NewMockRemoteDeviceSvc(gomock.NewController(t))
	rds.EXPECT().List(gomock.Any()).Return(devices, nil).AnyTimes()
	prev := remote_device_svc.Default()
	remote_device_svc.SetDefault(rds)
	t.Cleanup(func() { remote_device_svc.SetDefault(prev) })
	return &cleanupMocks{deletePropMocks: m, srv: srv, rds: rds}
}

func idleSess(id int64) *chat_entity.Session {
	return &chat_entity.Session{ID: id, ConversationID: "conv-" + string(rune('a'+id)), AgentStatus: "idle"}
}

func remoteIdle(id int64) *chat_entity.Session {
	s := idleSess(id)
	s.ExecDeviceID, s.ExecDeviceFingerprint = propExecDevice, propCarrierFP
	return s
}

func carrierView(online bool) *remote_device_svc.DeviceView {
	return &remote_device_svc.DeviceView{ID: propExecDevice, Name: "build-box", DaemonFingerprint: propCarrierFP, Online: online}
}

func TestPreviewCleanup(t *testing.T) {
	Convey("PreviewCleanup", t, func() {
		Convey("条件原样交给仓储；进行中/等待只计保留；子 agent 单列且不计数；各份副本分行", func() {
			m := setupCleanupTest(t, true, carrierView(true))
			filter := chat_repo.SessionCleanupFilter{BeforeMs: 1700, AgentID: 3, ProjectID: 5}
			sub := filter
			sub.Subagent = true
			running, waiting := idleSess(3), idleSess(4)
			running.AgentStatus, waiting.AgentStatus = "running", "waiting"
			m.session.EXPECT().ListForCleanup(gomock.Any(), filter).Return(
				[]*chat_entity.Session{idleSess(1), remoteIdle(2), running, waiting}, nil)
			subRunning := idleSess(11)
			subRunning.AgentStatus = "running"
			m.session.EXPECT().ListForCleanup(gomock.Any(), sub).Return(
				[]*chat_entity.Session{idleSess(10), subRunning}, nil)

			resp, err := m.svc.PreviewCleanup(m.ctx, &PreviewCleanupRequest{BeforeMs: 1700, AgentID: 3, ProjectID: 5})

			So(err, ShouldBeNil)
			So(resp.Count, ShouldEqual, 2)
			So(resp.Retained, ShouldEqual, 2)
			So(resp.SessionIDs, ShouldResemble, []int64{1, 2})
			So(resp.SubagentSessionIDs, ShouldResemble, []int64{10})
			So(resp.Copies, ShouldResemble, []CleanupCopy{
				{Kind: CleanupCopyLocal, Count: 2},
				{Kind: CleanupCopyAccount, Count: 2},
				{Kind: CleanupCopyMachine, Name: "build-box", Online: true, Count: 1},
			})
		})

		Convey("没登录：没有账号行，机器行标局域网直连，离线照实标", func() {
			m := setupCleanupTest(t, false, carrierView(false))
			m.session.EXPECT().ListForCleanup(gomock.Any(), chat_repo.SessionCleanupFilter{}).Return(
				[]*chat_entity.Session{remoteIdle(2)}, nil)
			m.session.EXPECT().ListForCleanup(gomock.Any(), chat_repo.SessionCleanupFilter{Subagent: true}).Return(nil, nil)

			resp, err := m.svc.PreviewCleanup(m.ctx, &PreviewCleanupRequest{})

			So(err, ShouldBeNil)
			So(resp.Copies, ShouldResemble, []CleanupCopy{
				{Kind: CleanupCopyLocal, Count: 1},
				{Kind: CleanupCopyMachine, Name: "build-box", Online: false, LanDirect: true, Count: 1},
			})
			So(resp.SubagentSessionIDs, ShouldNotBeNil)
		})

		Convey("执行端已从配对里移除：不单独列出", func() {
			m := setupCleanupTest(t, true)
			m.session.EXPECT().ListForCleanup(gomock.Any(), gomock.Any()).Return(
				[]*chat_entity.Session{remoteIdle(2)}, nil).Times(2)

			resp, err := m.svc.PreviewCleanup(m.ctx, &PreviewCleanupRequest{})

			So(err, ShouldBeNil)
			So(resp.Copies, ShouldResemble, []CleanupCopy{
				{Kind: CleanupCopyLocal, Count: 1}, {Kind: CleanupCopyAccount, Count: 1}})
		})

		Convey("读库失败：整个预览报错，不给半截数字", func() {
			m := setupCleanupTest(t, true)
			m.session.EXPECT().ListForCleanup(gomock.Any(), gomock.Any()).Return(nil, errors.New("db down")).AnyTimes()

			_, err := m.svc.PreviewCleanup(m.ctx, &PreviewCleanupRequest{})

			So(err, ShouldNotBeNil)
		})
	})
}

func TestExecuteCleanup(t *testing.T) {
	Convey("ExecuteCleanup", t, func() {
		Convey("逐条重新判断：已不存在跳过、变成进行中保留；一条本机删除失败不拦住其余", func() {
			m := setupCleanupTest(t, true)
			running := idleSess(2)
			running.AgentStatus = "running"
			m.session.EXPECT().Find(gomock.Any(), int64(1)).Return(nil, nil)
			m.session.EXPECT().Find(gomock.Any(), int64(2)).Return(running, nil)
			m.session.EXPECT().Find(gomock.Any(), int64(3)).Return(idleSess(3), nil)
			m.session.EXPECT().Find(gomock.Any(), int64(4)).Return(idleSess(4), nil)
			m.session.EXPECT().Find(gomock.Any(), int64(5)).Return(idleSess(5), nil)
			m.session.EXPECT().Find(gomock.Any(), int64(6)).Return(nil, errors.New("read boom"))
			m.expectLocalDelete(3)
			m.session.EXPECT().SoftDelete(gomock.Any(), int64(4)).Return(errors.New("disk full"))
			m.expectLocalDelete(5)

			resp, err := m.svc.ExecuteCleanup(m.ctx, &ExecuteCleanupRequest{SessionIDs: []int64{1, 2, 3, 4, 5, 6}})

			So(err, ShouldBeNil)
			So(resp.Deleted, ShouldResemble, []int64{3, 5})
			So(resp.Failed, ShouldResemble, []CleanupFailure{
				{ID: 4, Reason: CleanupFailedDelete}, {ID: 6, Reason: CleanupFailedRead}})
			So(resp.Retained, ShouldEqual, 1)
			// 本机没删掉的不传播：只有 3 和 5 去问了 server。
			So(m.srv.callCount(), ShouldEqual, 2)
		})

		Convey("子 agent 会话一起删，但不进 deleted / failed / pending", func() {
			m := setupCleanupTest(t, true)
			m.session.EXPECT().Find(gomock.Any(), int64(1)).Return(idleSess(1), nil)
			m.session.EXPECT().Find(gomock.Any(), int64(10)).Return(idleSess(10), nil)
			m.session.EXPECT().Find(gomock.Any(), int64(11)).Return(idleSess(11), nil)
			m.expectLocalDelete(1)
			m.expectLocalDelete(10)
			m.session.EXPECT().SoftDelete(gomock.Any(), int64(11)).Return(errors.New("disk full"))

			resp, err := m.svc.ExecuteCleanup(m.ctx, &ExecuteCleanupRequest{
				SessionIDs: []int64{1}, SubagentSessionIDs: []int64{10, 11}})

			So(err, ShouldBeNil)
			So(resp.Deleted, ShouldResemble, []int64{1})
			So(resp.Failed, ShouldBeEmpty)
			So(m.srv.callCount(), ShouldEqual, 2)
		})

		Convey("慢传播不串行化：server 卡住时多条同时在飞", func() {
			m := setupCleanupTest(t, true)
			m.srv.gate = make(chan struct{})
			ids := []int64{1, 2, 3, 4}
			for _, id := range ids {
				m.session.EXPECT().Find(gomock.Any(), id).Return(idleSess(id), nil)
				m.expectLocalDelete(id)
			}
			go func() {
				deadline := time.Now().Add(3 * time.Second)
				for time.Now().Before(deadline) {
					m.srv.mu.Lock()
					n := m.srv.inflight
					m.srv.mu.Unlock()
					if n >= 2 {
						break
					}
					time.Sleep(5 * time.Millisecond)
				}
				close(m.srv.gate)
			}()

			resp, err := m.svc.ExecuteCleanup(m.ctx, &ExecuteCleanupRequest{SessionIDs: ids})

			So(err, ShouldBeNil)
			So(resp.Deleted, ShouldResemble, ids)
			So(m.srv.maxIn, ShouldBeGreaterThanOrEqualTo, 2)
		})

		Convey("server 够不着：只有头一批并发的会真去试，其余直接记待删除，一条不丢", func() {
			m := setupCleanupTest(t, true)
			m.srv.err = server_svc.ErrServerUnreachable
			m.srv.gate = make(chan struct{})
			var ids []int64
			for id := int64(1); id <= 12; id++ {
				ids = append(ids, id)
				m.session.EXPECT().Find(gomock.Any(), id).Return(idleSess(id), nil)
				m.expectLocalDelete(id)
			}
			var mu sync.Mutex
			pendingRows := 0
			m.pending.EXPECT().Create(gomock.Any(), gomock.Any()).DoAndReturn(
				func(_ context.Context, row *chat_entity.PendingDelete) error {
					mu.Lock()
					if row.Target == chat_entity.PendingDeleteTargetServer {
						pendingRows++
					}
					mu.Unlock()
					return nil
				}).AnyTimes()
			go func() {
				deadline := time.Now().Add(3 * time.Second)
				for time.Now().Before(deadline) {
					if m.srv.callCount() >= cleanupConcurrency {
						break
					}
					time.Sleep(5 * time.Millisecond)
				}
				close(m.srv.gate)
			}()

			resp, err := m.svc.ExecuteCleanup(m.ctx, &ExecuteCleanupRequest{SessionIDs: ids})

			So(err, ShouldBeNil)
			So(resp.Deleted, ShouldResemble, ids)
			So(m.srv.callCount(), ShouldEqual, cleanupConcurrency)
			So(pendingRows, ShouldEqual, 12)
			// 完成提示只报离线机器（spec「执行」）；server 那份留在本地待删除里补发，不进结果。
			So(resp.Pending, ShouldResemble, []CleanupPending{})
		})

		Convey("没登录且已知离线的机器：不拨号，直接记 agentred 待删除并按机器名汇总", func() {
			m := setupCleanupTest(t, false, carrierView(false))
			for _, id := range []int64{1, 2} {
				m.session.EXPECT().Find(gomock.Any(), id).Return(remoteIdle(id), nil)
				m.expectLocalDelete(id)
			}
			var mu sync.Mutex
			var rows []chat_entity.PendingDelete
			m.pending.EXPECT().Create(gomock.Any(), gomock.Any()).DoAndReturn(
				func(_ context.Context, row *chat_entity.PendingDelete) error {
					mu.Lock()
					rows = append(rows, *row)
					mu.Unlock()
					return nil
				}).Times(2)
			// m.pool 没有 Borrow 期望：被调到即失败。

			resp, err := m.svc.ExecuteCleanup(m.ctx, &ExecuteCleanupRequest{SessionIDs: []int64{1, 2}})

			So(err, ShouldBeNil)
			So(resp.Deleted, ShouldResemble, []int64{1, 2})
			So(resp.Pending, ShouldResemble, []CleanupPending{{Kind: CleanupCopyMachine, Machine: "build-box", Count: 2}})
			So(rows, ShouldHaveLength, 2)
			for _, row := range rows {
				So(row.Target, ShouldEqual, chat_entity.PendingDeleteTargetAgentred)
				So(row.DeviceID, ShouldEqual, propExecDevice)
				So(row.MachineFingerprint, ShouldEqual, propCarrierFP)
			}
		})

		Convey("已登录、server 答执行端离线：按机器名汇总，server 自己记了待办，本地不记", func() {
			m := setupCleanupTest(t, true, carrierView(false))
			m.srv.status = server_svc.MachineStatusPending
			m.session.EXPECT().Find(gomock.Any(), int64(2)).Return(remoteIdle(2), nil)
			m.expectLocalDelete(2)

			resp, err := m.svc.ExecuteCleanup(m.ctx, &ExecuteCleanupRequest{SessionIDs: []int64{2}})

			So(err, ShouldBeNil)
			So(resp.Pending, ShouldResemble, []CleanupPending{{Kind: CleanupCopyMachine, Machine: "build-box", Count: 1}})
		})
	})
}
