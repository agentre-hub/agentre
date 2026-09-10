package chat_svc

import (
	"context"
	"errors"
	"io"
	"os/exec"
	"sync"
	"testing"

	. "github.com/smartystreets/goconvey/convey"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/agentre-hub/agentre/internal/daemon/protorpc"
	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/chat_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/project_location_entity"
	"github.com/agentre-hub/agentre/internal/repository/project_location_repo"
	"github.com/agentre-hub/agentre/internal/repository/project_location_repo/mock_project_location_repo"
	"github.com/agentre-hub/agentre/internal/service/remote_device_svc"
	"github.com/agentre-hub/agentre/internal/service/remote_device_svc/mock_remote_device_svc"
	"github.com/agentre-hub/agentre/pkg/wire/agentrewire"
)

type gitStateProtoPipe struct {
	in, out chan []byte
	done    chan struct{}
	once    *sync.Once
}

func (p *gitStateProtoPipe) ReadFrame() ([]byte, error) {
	select {
	case v := <-p.in:
		return v, nil
	case <-p.done:
		return nil, io.EOF
	}
}
func (p *gitStateProtoPipe) WriteFrame(v []byte) error {
	select {
	case p.out <- append([]byte(nil), v...):
		return nil
	case <-p.done:
		return io.EOF
	}
}
func (p *gitStateProtoPipe) Close() error          { p.once.Do(func() { close(p.done) }); return nil }
func (p *gitStateProtoPipe) Done() <-chan struct{} { return p.done }

type gitStateProtoClient struct {
	conn *protorpc.Conn
	done <-chan struct{}
}

func (c *gitStateProtoClient) Conn() *protorpc.Conn    { return c.conn }
func (c *gitStateProtoClient) Closed() <-chan struct{} { return c.done }
func (c *gitStateProtoClient) Close() error            { return c.conn.Close() }

func newGitStateProtoClient(t *testing.T, handler func(*agentrewire.WorkspaceFsGitStateRequest) (*agentrewire.WorkspaceFsGitStateResponse, error)) *gitStateProtoClient {
	t.Helper()
	registry := protorpc.NewRegistry()
	protorpc.RegisterMethod(registry, uint32(agentrewire.RpcMethod_RPC_METHOD_WORKSPACE_FS_GIT_STATE), func() *agentrewire.WorkspaceFsGitStateRequest { return &agentrewire.WorkspaceFsGitStateRequest{} }, func(_ context.Context, req *agentrewire.WorkspaceFsGitStateRequest) (*agentrewire.WorkspaceFsGitStateResponse, error) {
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

// runGit 在 dir 下执行 git args。测试 helper, 失败直接 t.Fatal。
func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...) //nolint:gosec // G204: test helper, args 来自测试内常量
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func TestGetSessionGitState_LocalBackend(t *testing.T) {
	Convey("Given a local-backend session whose cwd resolves to a real git repo", t, func() {
		dir := t.TempDir()
		runGit(t, dir, "init", "-q", "-b", "main")
		runGit(t, dir, "config", "user.email", "t@t")
		runGit(t, dir, "config", "user.name", "t")
		runGit(t, dir, "commit", "--allow-empty", "-m", "init")

		sess := &chat_entity.Session{ID: 42, ProjectID: 0}
		be := &agent_backend_entity.AgentBackend{Type: string(agent_backend_entity.TypeBuiltin)}
		// 用 stub 把 resolveSessionCwd 绕到 dir
		RegisterCwdResolver(func(_ context.Context, _ *chat_entity.Session) (string, error) {
			return dir, nil
		})
		t.Cleanup(func() { RegisterCwdResolver(nil) })

		Convey("When GetSessionGitState is called", func() {
			s := &chatSvc{}
			resp, err := s.getSessionGitStateForSession(context.Background(), sess, be)
			So(err, ShouldBeNil)
			So(resp.State.Branch, ShouldEqual, "main")
			So(resp.State.NotARepo, ShouldBeFalse)
		})
	})
}

// TestGetSessionGitState_SelfBackend_ReportsRepoState R13 认领后本机 backend 的
// DeviceID 是本机指纹：git 状态必须按本机档解析 cwd 并如实报告仓库状态，而不是像远端
// 档那样直接降级成 notARepo。
func TestGetSessionGitState_SelfBackend_ReportsRepoState(t *testing.T) {
	Convey("Given a self-fingerprint backend session whose cwd resolves to a real git repo", t, func() {
		dir := t.TempDir()
		runGit(t, dir, "init", "-q", "-b", "main")
		runGit(t, dir, "config", "user.email", "t@t")
		runGit(t, dir, "config", "user.name", "t")
		runGit(t, dir, "commit", "--allow-empty", "-m", "init")

		ctrl := gomock.NewController(t)
		rds := mock_remote_device_svc.NewMockRemoteDeviceSvc(ctrl)
		rds.EXPECT().DeviceFingerprint().Return("sha256:self", nil).AnyTimes()
		prevSvc := remote_device_svc.Default()
		remote_device_svc.SetDefault(rds)
		t.Cleanup(func() {
			remote_device_svc.SetDefault(prevSvc)
			ctrl.Finish()
		})

		sess := &chat_entity.Session{ID: 42, ProjectID: 0}
		be := &agent_backend_entity.AgentBackend{Type: string(agent_backend_entity.TypeClaudeCode), DeviceFingerprint: "sha256:self"}
		RegisterCwdResolver(func(_ context.Context, _ *chat_entity.Session) (string, error) {
			return dir, nil
		})
		t.Cleanup(func() { RegisterCwdResolver(nil) })

		Convey("When GetSessionGitState is called", func() {
			s := &chatSvc{}
			resp, err := s.getSessionGitStateForSession(context.Background(), sess, be)
			So(err, ShouldBeNil)
			So(resp.State.Branch, ShouldEqual, "main")
		})
	})
}

// TestGetSessionGitState_RemoteBackend_UnpairedDegradesToNotARepo 覆盖"设备解析
// 不出"这一条降级路径：DeviceID 既非 sha256 指纹也不是可解析的数值配对行 ID，
// localPairedDeviceID 直接失败，压根不会去借租约。
func TestGetSessionGitState_RemoteBackend_UnpairedDegradesToNotARepo(t *testing.T) {
	Convey("Given a remote backend session whose DeviceID cannot be resolved to a paired device", t, func() {
		sess := &chat_entity.Session{ID: 42, ProjectID: 1}
		be := &agent_backend_entity.AgentBackend{Type: string(agent_backend_entity.TypeClaudeCode), DeviceFingerprint: "dev-1"}
		Convey("Then service returns notARepo=true without erroring", func() {
			s := &chatSvc{}
			resp, err := s.getSessionGitStateForSession(context.Background(), sess, be)
			So(err, ShouldBeNil)
			So(resp.State.NotARepo, ShouldBeTrue)
		})
	})
}

// TestGetSessionGitState_RemoteBackend_FetchesRealGitState 是本任务的核心断言：
// 远端 backend 不再恒定降级为 notARepo，而是经 workspacefs.gitState RPC 拿到
// 真实的分支 / worktree / dirty / ahead·behind 快照（硬不变量 5：本地与远端会话
// 行为一致）。
func TestGetSessionGitState_RemoteBackend_FetchesRealGitState(t *testing.T) {
	Convey("Given a remote-backend session paired to a device with a real git repo", t, func() {
		ctrl := gomock.NewController(t)
		t.Cleanup(ctrl.Finish)

		locRepo := mock_project_location_repo.NewMockProjectLocationRepo(ctrl)
		prevLoc := project_location_repo.ProjectLocation()
		project_location_repo.RegisterProjectLocation(locRepo)
		t.Cleanup(func() { project_location_repo.RegisterProjectLocation(prevLoc) })

		sess := &chat_entity.Session{ID: 42, ProjectID: 9}
		be := &agent_backend_entity.AgentBackend{Type: string(agent_backend_entity.TypeClaudeCode), DeviceFingerprint: testDeviceFingerprint(7)}
		locRepo.EXPECT().FindByProjectAndFingerprint(gomock.Any(), int64(9), testDeviceFingerprint(7)).
			Return(&project_location_entity.ProjectLocation{Path: "/remote/work"}, nil)

		pool := mock_remote_device_svc.NewMockConnPool(ctrl)
		lease := mock_remote_device_svc.NewMockLease(ctrl)
		client := newGitStateProtoClient(t, func(req *agentrewire.WorkspaceFsGitStateRequest) (*agentrewire.WorkspaceFsGitStateResponse, error) {
			require.Equal(t, "/remote/work", req.Root)
			return &agentrewire.WorkspaceFsGitStateResponse{Branch: "main", Dirty: 3, Ahead: 1, Behind: 2, HasUpstream: true}, nil
		})
		pool.EXPECT().Borrow(gomock.Any(), int64(7)).Return(lease, nil)
		lease.EXPECT().Client().Return(client)
		lease.EXPECT().Release()

		s := &chatSvc{}
		s.setConnPoolForTest(pool)
		installPairedDevice(t, ctrl, 7)
		installExecDaemonRecorder(t, ctrl)
		t.Cleanup(func() { s.setConnPoolForTest(nil) })

		Convey("When GetSessionGitState is called", func() {
			resp, err := s.getSessionGitStateForSession(context.Background(), sess, be)
			So(err, ShouldBeNil)
			So(resp.State.NotARepo, ShouldBeFalse)
			So(resp.State.Branch, ShouldEqual, "main")
			So(resp.State.Dirty, ShouldEqual, 3)
			So(resp.State.Ahead, ShouldEqual, 1)
			So(resp.State.Behind, ShouldEqual, 2)
			So(resp.State.HasUpstream, ShouldBeTrue)
		})
	})
}

// TestGetSessionGitState_RemoteBackend_CallErrorDegradesToNotARepo 覆盖 RPC 调用
// 本身失败的情形（含旧 daemon 报"方法不存在" -32601）：session git chip 是纯
// 展示区域，不应该因为设备暂时不可达 / daemon 版本过旧而把错误甩给前端，一律
// 降级为 notARepo=true，与本机分支 cwd 解析失败时的容错约定一致。
func TestGetSessionGitState_RemoteBackend_CallErrorDegradesToNotARepo(t *testing.T) {
	Convey("Given a remote-backend session whose daemon call fails", t, func() {
		ctrl := gomock.NewController(t)
		t.Cleanup(ctrl.Finish)

		locRepo := mock_project_location_repo.NewMockProjectLocationRepo(ctrl)
		prevLoc := project_location_repo.ProjectLocation()
		project_location_repo.RegisterProjectLocation(locRepo)
		t.Cleanup(func() { project_location_repo.RegisterProjectLocation(prevLoc) })

		sess := &chat_entity.Session{ID: 42, ProjectID: 9}
		be := &agent_backend_entity.AgentBackend{Type: string(agent_backend_entity.TypeClaudeCode), DeviceFingerprint: testDeviceFingerprint(7)}
		locRepo.EXPECT().FindByProjectAndFingerprint(gomock.Any(), int64(9), testDeviceFingerprint(7)).
			Return(&project_location_entity.ProjectLocation{Path: "/remote/work"}, nil)

		pool := mock_remote_device_svc.NewMockConnPool(ctrl)
		lease := mock_remote_device_svc.NewMockLease(ctrl)
		client := newGitStateProtoClient(t, func(req *agentrewire.WorkspaceFsGitStateRequest) (*agentrewire.WorkspaceFsGitStateResponse, error) {
			require.Equal(t, "/remote/work", req.Root)
			return nil, errors.New("server error")
		})
		pool.EXPECT().Borrow(gomock.Any(), int64(7)).Return(lease, nil)
		lease.EXPECT().Client().Return(client)
		lease.EXPECT().Release()

		s := &chatSvc{}
		s.setConnPoolForTest(pool)
		installPairedDevice(t, ctrl, 7)
		installExecDaemonRecorder(t, ctrl)
		t.Cleanup(func() { s.setConnPoolForTest(nil) })

		Convey("When GetSessionGitState is called", func() {
			resp, err := s.getSessionGitStateForSession(context.Background(), sess, be)
			So(err, ShouldBeNil)
			So(resp.State.NotARepo, ShouldBeTrue)
		})
	})
}

func TestGetSessionGitState_SessionNotFound(t *testing.T) {
	Convey("Given req.SessionID = 0", t, func() {
		s := &chatSvc{}
		_, err := s.GetSessionGitState(context.Background(), &GetSessionGitStateRequest{SessionID: 0})
		So(err, ShouldNotBeNil)
	})
}

// SelfFingerprint 满足 client.ProtobufConnection:这个假连接从没握过手,本端指纹为空。
func (c *gitStateProtoClient) SelfFingerprint() string { return "" }
