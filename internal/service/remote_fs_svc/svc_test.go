package remote_fs_svc

import (
	"context"
	"io"
	"sync"
	"testing"

	"github.com/smartystreets/goconvey/convey"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/agentre-hub/agentre/internal/daemon/client"
	"github.com/agentre-hub/agentre/internal/daemon/protorpc"
	"github.com/agentre-hub/agentre/internal/pkg/remotefs/wire"
	"github.com/agentre-hub/agentre/internal/service/remote_device_svc"
	mockRD "github.com/agentre-hub/agentre/internal/service/remote_device_svc/mock_remote_device_svc"
	"github.com/agentre-hub/agentre/pkg/wire/agentrewire"
)

func setupSvc(t *testing.T) (
	context.Context,
	*mockRD.MockRemoteDeviceSvc,
	*mockRD.MockConnPool,
	*mockRD.MockLease,
	client.ProtobufConnection,
	*protorpc.Registry,
	*remoteFsImpl,
) {
	t.Helper()
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)
	rd := mockRD.NewMockRemoteDeviceSvc(ctrl)
	pool := mockRD.NewMockConnPool(ctrl)
	lease := mockRD.NewMockLease(ctrl)
	registry := protorpc.NewRegistry()
	client := newRemoteFSTestConnection(t, registry)
	svc := &remoteFsImpl{rdSvc: rd}
	return context.Background(), rd, pool, lease, client, registry, svc
}

type remoteFSTestConnection struct{ conn *protorpc.Conn }

func (c *remoteFSTestConnection) Conn() *protorpc.Conn    { return c.conn }
func (c *remoteFSTestConnection) Closed() <-chan struct{} { return c.conn.Done() }
func (c *remoteFSTestConnection) Close() error            { return c.conn.Close() }

type remoteFSTestPipe struct {
	in, out chan []byte
	done    chan struct{}
	once    *sync.Once
}

func remoteFSTestPipePair() (*remoteFSTestPipe, *remoteFSTestPipe) {
	a, b := make(chan []byte, 4), make(chan []byte, 4)
	d := make(chan struct{})
	o := &sync.Once{}
	return &remoteFSTestPipe{a, b, d, o}, &remoteFSTestPipe{b, a, d, o}
}
func (p *remoteFSTestPipe) ReadFrame() ([]byte, error) {
	select {
	case b := <-p.in:
		return b, nil
	case <-p.done:
		return nil, io.EOF
	}
}
func (p *remoteFSTestPipe) WriteFrame(b []byte) error {
	select {
	case p.out <- append([]byte(nil), b...):
		return nil
	case <-p.done:
		return io.EOF
	}
}
func (p *remoteFSTestPipe) Close() error          { p.once.Do(func() { close(p.done) }); return nil }
func (p *remoteFSTestPipe) Done() <-chan struct{} { return p.done }
func newRemoteFSTestConnection(t *testing.T, registry *protorpc.Registry) client.ProtobufConnection {
	t.Helper()
	a, b := remoteFSTestPipePair()
	c, s := protorpc.NewConn(a, protorpc.NewRegistry()), protorpc.NewConn(b, registry)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go c.Serve(ctx)
	go s.Serve(ctx)
	return &remoteFSTestConnection{conn: c}
}

func TestListDir(t *testing.T) {
	convey.Convey("ListDir", t, func() {
		convey.Convey("deviceID 非法", func() {
			_, _, _, _, _, _, svc := setupSvc(t)
			_, err := svc.ListDir(context.Background(), "abc", "/home")
			assert.Error(t, err)
		})

		convey.Convey("path 落黑名单 → 不 Borrow", func() {
			ctx, _, _, _, _, _, svc := setupSvc(t)
			_, err := svc.ListDir(ctx, "7", "/proc")
			assert.Error(t, err)
		})

		convey.Convey("Borrow 报 device not found", func() {
			ctx, rd, pool, _, _, _, svc := setupSvc(t)
			rd.EXPECT().Pool().Return(pool)
			pool.EXPECT().Borrow(ctx, int64(7)).Return(nil, remote_device_svc.ErrDeviceNotFound)
			_, err := svc.ListDir(ctx, "7", "/home/me")
			assert.Error(t, err)
		})

		convey.Convey("Borrow ok + Call wire.ErrPermDenied → RemoteFsPermDenied", func() {
			ctx, rd, pool, lease, client, registry, svc := setupSvc(t)
			rd.EXPECT().Pool().Return(pool)
			pool.EXPECT().Borrow(ctx, int64(7)).Return(lease, nil)
			lease.EXPECT().Client().Return(client)
			lease.EXPECT().Release()
			protorpc.RegisterMethod(registry, uint32(agentrewire.RpcMethod_RPC_METHOD_REMOTE_FS_LIST_DIR), func() *agentrewire.RemoteFsListDirRequest { return &agentrewire.RemoteFsListDirRequest{} }, func(context.Context, *agentrewire.RemoteFsListDirRequest) (*agentrewire.RemoteFsListDirResponse, error) {
				return nil, &protorpc.Error{Code: wire.ErrCodePermDenied, Message: "x"}
			})
			_, err := svc.ListDir(ctx, "7", "/home/me")
			require.Error(t, err)
		})

		convey.Convey("Borrow ok + Call ok → 透传 view", func() {
			ctx, rd, pool, lease, client, registry, svc := setupSvc(t)
			rd.EXPECT().Pool().Return(pool)
			pool.EXPECT().Borrow(ctx, int64(7)).Return(lease, nil)
			lease.EXPECT().Client().Return(client)
			lease.EXPECT().Release()
			protorpc.RegisterMethod(registry, uint32(agentrewire.RpcMethod_RPC_METHOD_REMOTE_FS_LIST_DIR), func() *agentrewire.RemoteFsListDirRequest { return &agentrewire.RemoteFsListDirRequest{} }, func(_ context.Context, request *agentrewire.RemoteFsListDirRequest) (*agentrewire.RemoteFsListDirResponse, error) {
				require.Equal(t, "/home/me", request.Path)
				return &agentrewire.RemoteFsListDirResponse{Path: "/home/me", Truncated: true, Entries: []*agentrewire.RemoteFsEntry{{Name: "Work", IsDir: true, ModTime: 1700000000}, {Name: "f.txt", Size: 12, ModTime: 1700000001}}}, nil
			})
			view, err := svc.ListDir(ctx, "7", "/home/me")
			require.NoError(t, err)
			assert.Equal(t, "/home/me", view.Path)
			assert.True(t, view.Truncated)
			require.Len(t, view.Entries, 2)
			assert.Equal(t, "Work", view.Entries[0].Name)
			assert.True(t, view.Entries[0].IsDir)
			assert.Equal(t, int64(12), view.Entries[1].Size)
		})
	})
}

func TestMkdir(t *testing.T) {
	convey.Convey("Mkdir", t, func() {
		convey.Convey("名字非法 → 不 Borrow", func() {
			_, _, _, _, _, _, svc := setupSvc(t)
			_, err := svc.Mkdir(context.Background(), "7", "/home", "a/b")
			assert.Error(t, err)
		})

		convey.Convey("parent 落黑名单 → 不 Borrow", func() {
			_, _, _, _, _, _, svc := setupSvc(t)
			_, err := svc.Mkdir(context.Background(), "7", "/proc", "x")
			assert.Error(t, err)
		})

		convey.Convey("Borrow ok + Call ErrMkdirExists", func() {
			ctx, rd, pool, lease, client, registry, svc := setupSvc(t)
			rd.EXPECT().Pool().Return(pool)
			pool.EXPECT().Borrow(ctx, int64(7)).Return(lease, nil)
			lease.EXPECT().Client().Return(client)
			lease.EXPECT().Release()
			protorpc.RegisterMethod(registry, uint32(agentrewire.RpcMethod_RPC_METHOD_REMOTE_FS_MKDIR), func() *agentrewire.RemoteFsMkdirRequest { return &agentrewire.RemoteFsMkdirRequest{} }, func(context.Context, *agentrewire.RemoteFsMkdirRequest) (*agentrewire.RemoteFsMkdirResponse, error) {
				return nil, &protorpc.Error{Code: wire.ErrCodeMkdirExists, Message: "x"}
			})
			_, err := svc.Mkdir(ctx, "7", "/home/me", "dup")
			assert.Error(t, err)
		})

		convey.Convey("happy", func() {
			ctx, rd, pool, lease, client, registry, svc := setupSvc(t)
			rd.EXPECT().Pool().Return(pool)
			pool.EXPECT().Borrow(ctx, int64(7)).Return(lease, nil)
			lease.EXPECT().Client().Return(client)
			lease.EXPECT().Release()
			protorpc.RegisterMethod(registry, uint32(agentrewire.RpcMethod_RPC_METHOD_REMOTE_FS_MKDIR), func() *agentrewire.RemoteFsMkdirRequest { return &agentrewire.RemoteFsMkdirRequest{} }, func(_ context.Context, request *agentrewire.RemoteFsMkdirRequest) (*agentrewire.RemoteFsMkdirResponse, error) {
				require.Equal(t, "/home/me", request.Parent)
				require.Equal(t, "new", request.Name)
				return &agentrewire.RemoteFsMkdirResponse{Path: "/home/me/new"}, nil
			})
			view, err := svc.Mkdir(ctx, "7", "/home/me", "new")
			require.NoError(t, err)
			assert.Equal(t, "/home/me/new", view.Path)
		})
	})
}
