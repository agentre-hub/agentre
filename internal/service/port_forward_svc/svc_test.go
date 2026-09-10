package port_forward_svc

import (
	"context"
	"errors"
	"io"
	"sync"
	"testing"

	"github.com/cago-frame/cago/pkg/utils/httputils"
	"github.com/smartystreets/goconvey/convey"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/agentre-hub/agentre/internal/daemon/client"
	"github.com/agentre-hub/agentre/internal/pkg/code"
	"github.com/agentre-hub/agentre/internal/service/remote_device_svc"
	mockRD "github.com/agentre-hub/agentre/internal/service/remote_device_svc/mock_remote_device_svc"
	"github.com/agentre-hub/agentre/pkg/wire/agentrewire"
	"github.com/agentre-hub/agentre/pkg/wire/protorpc"
	"github.com/agentre-hub/agentre/pkg/wire/rpcerror"
)

// ── 装配 ────────────────────────────────────────────────────────────────────
//
// 与 remote_fs_svc 的单测同一副骨架:连接池全是 mockgen 出来的 mock,对端是一条
// 内存里的 protorpc 管道 —— 不连 DB,也不连真设备(AGENTS.md 硬规则 6)。

type rig struct {
	ctx      context.Context
	rd       *mockRD.MockRemoteDeviceSvc
	pool     *mockRD.MockConnPool
	lease    *mockRD.MockLease
	registry *protorpc.Registry
	svc      *portForwardImpl
}

func newRig(t *testing.T) *rig {
	t.Helper()
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)
	r := &rig{
		ctx:      context.Background(),
		rd:       mockRD.NewMockRemoteDeviceSvc(ctrl),
		pool:     mockRD.NewMockConnPool(ctrl),
		lease:    mockRD.NewMockLease(ctrl),
		registry: protorpc.NewRegistry(),
	}
	r.svc = &portForwardImpl{rdSvc: r.rd}
	return r
}

// expectRoundTrip 声明**一次**完整往返:Borrow → Client → …一次 RPC… → Release。
// gomock 默认 Times(1),多借一次或漏放一次都会当场判红。
func (r *rig) expectRoundTrip(t *testing.T, deviceID int64) {
	t.Helper()
	r.rd.EXPECT().Pool().Return(r.pool)
	r.pool.EXPECT().Borrow(r.ctx, deviceID).Return(r.lease, nil)
	r.lease.EXPECT().Client().Return(newTestConnection(t, r.registry))
	r.lease.EXPECT().Release()
}

// expectBorrowFails 声明一次借不出来的往返:此后不该有任何 RPC 发出去。
func (r *rig) expectBorrowFails(deviceID int64, err error) {
	r.rd.EXPECT().Pool().Return(r.pool)
	r.pool.EXPECT().Borrow(r.ctx, deviceID).Return(nil, err)
}

// codeOf 把 i18n 包装过的错误还原成业务码。视图层就是照这个码分态的
// (经 internal/app/coded_error.go 那条 `agentre-code:<码>` 契约过 wails 桥)。
func codeOf(t *testing.T, err error) int {
	t.Helper()
	require.Error(t, err)
	var coded *httputils.Error
	require.ErrorAs(t, err, &coded, "错误没带业务码,视图层无法分态: %v", err)
	return coded.Code
}

// ── 内存 protorpc 管道 ──────────────────────────────────────────────────────

type testConnection struct{ conn *protorpc.Conn }

func (c *testConnection) Conn() *protorpc.Conn    { return c.conn }
func (c *testConnection) Closed() <-chan struct{} { return c.conn.Done() }
func (c *testConnection) Close() error            { return c.conn.Close() }
func (c *testConnection) SelfFingerprint() string { return "sha256:test-self" }

type testPipe struct {
	in, out chan []byte
	done    chan struct{}
	once    *sync.Once
}

func testPipePair() (*testPipe, *testPipe) {
	a, b := make(chan []byte, 4), make(chan []byte, 4)
	d := make(chan struct{})
	o := &sync.Once{}
	return &testPipe{a, b, d, o}, &testPipe{b, a, d, o}
}

func (p *testPipe) ReadFrame() ([]byte, error) {
	select {
	case b := <-p.in:
		return b, nil
	case <-p.done:
		return nil, io.EOF
	}
}

func (p *testPipe) WriteFrame(b []byte) error {
	select {
	case p.out <- append([]byte(nil), b...):
		return nil
	case <-p.done:
		return io.EOF
	}
}

func (p *testPipe) Close() error          { p.once.Do(func() { close(p.done) }); return nil }
func (p *testPipe) Done() <-chan struct{} { return p.done }

func newTestConnection(t *testing.T, registry *protorpc.Registry) client.ProtobufConnection {
	t.Helper()
	a, b := testPipePair()
	c, s := protorpc.NewConn(a, protorpc.NewRegistry()), protorpc.NewConn(b, registry)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go c.Serve(ctx)
	go s.Serve(ctx)
	return &testConnection{conn: c}
}

// ── 设备侧桩 ────────────────────────────────────────────────────────────────

func onList(r *rig, fn func(*agentrewire.PortForwardListRequest) (*agentrewire.PortForwardListResponse, error)) {
	protorpc.RegisterMethod(r.registry, uint32(agentrewire.RpcMethod_RPC_METHOD_PORT_FORWARD_LIST),
		func() *agentrewire.PortForwardListRequest { return &agentrewire.PortForwardListRequest{} },
		func(_ context.Context, request *agentrewire.PortForwardListRequest) (*agentrewire.PortForwardListResponse, error) {
			return fn(request)
		})
}

func onCreate(r *rig, fn func(*agentrewire.PortForwardCreateRequest) (*agentrewire.PortForwardCreateResponse, error)) {
	protorpc.RegisterMethod(r.registry, uint32(agentrewire.RpcMethod_RPC_METHOD_PORT_FORWARD_CREATE),
		func() *agentrewire.PortForwardCreateRequest { return &agentrewire.PortForwardCreateRequest{} },
		func(_ context.Context, request *agentrewire.PortForwardCreateRequest) (*agentrewire.PortForwardCreateResponse, error) {
			return fn(request)
		})
}

func onSetEnabled(r *rig, fn func(*agentrewire.PortForwardSetEnabledRequest) (*agentrewire.PortForwardSetEnabledResponse, error)) {
	protorpc.RegisterMethod(r.registry, uint32(agentrewire.RpcMethod_RPC_METHOD_PORT_FORWARD_SET_ENABLED),
		func() *agentrewire.PortForwardSetEnabledRequest { return &agentrewire.PortForwardSetEnabledRequest{} },
		func(_ context.Context, request *agentrewire.PortForwardSetEnabledRequest) (*agentrewire.PortForwardSetEnabledResponse, error) {
			return fn(request)
		})
}

func onDelete(r *rig, fn func(*agentrewire.PortForwardDeleteRequest) (*agentrewire.PortForwardDeleteResponse, error)) {
	protorpc.RegisterMethod(r.registry, uint32(agentrewire.RpcMethod_RPC_METHOD_PORT_FORWARD_DELETE),
		func() *agentrewire.PortForwardDeleteRequest { return &agentrewire.PortForwardDeleteRequest{} },
		func(_ context.Context, request *agentrewire.PortForwardDeleteRequest) (*agentrewire.PortForwardDeleteResponse, error) {
			return fn(request)
		})
}

// rpcErr 造一个设备侧回过来的、带码的失败(设备侧就是这么发的)。
func rpcErr(codeNum int) error {
	return &protorpc.Error{Code: int32(codeNum), Message: "device says no"}
}

// ── List ────────────────────────────────────────────────────────────────────

func TestList(t *testing.T) {
	convey.Convey("List", t, func() {
		convey.Convey("一次往返把设备上的声明原样带回来", func() {
			r := newRig(t)
			r.expectRoundTrip(t, 7)
			onList(r, func(*agentrewire.PortForwardListRequest) (*agentrewire.PortForwardListResponse, error) {
				return &agentrewire.PortForwardListResponse{Mappings: []*agentrewire.PortForwardMapping{
					{Id: 11, Port: 3000, Name: "dev server", Enabled: true},
					{Id: 12, Port: 8080, Name: "api", Enabled: false},
				}}, nil
			})

			views, err := r.svc.List(r.ctx, "7")
			require.NoError(t, err)
			require.Len(t, views, 2)
			// id 过 wails 那座桥是字符串:int64 落进 JS 的 number 会丢精度,
			// 而这一格前端只拿来当句柄回传。
			assert.Equal(t, "11", views[0].ID)
			assert.Equal(t, 3000, views[0].Port)
			assert.Equal(t, "dev server", views[0].Name)
			assert.True(t, views[0].Enabled)
			assert.Equal(t, "12", views[1].ID)
			assert.False(t, views[1].Enabled)
			// address 不由本层产出:桌面端那条地址要等任务 6 绑上本机监听才有。
			assert.Empty(t, views[0].Address)
		})

		convey.Convey("设备上一条声明都没有 → 空列表而不是 nil", func() {
			r := newRig(t)
			r.expectRoundTrip(t, 7)
			onList(r, func(*agentrewire.PortForwardListRequest) (*agentrewire.PortForwardListResponse, error) {
				return &agentrewire.PortForwardListResponse{}, nil
			})

			views, err := r.svc.List(r.ctx, "7")
			require.NoError(t, err)
			assert.NotNil(t, views)
			assert.Empty(t, views)
		})

		convey.Convey("设备离线 → 可分辨的离线码,视图层据此渲染离线态", func() {
			r := newRig(t)
			r.expectBorrowFails(7, errors.New("dial tcp: connection refused"))

			_, err := r.svc.List(r.ctx, "7")
			assert.Equal(t, code.PortForwardDeviceOffline, codeOf(t, err))
		})

		convey.Convey("设备已被解除配对 → 与离线分开说", func() {
			r := newRig(t)
			r.expectBorrowFails(7, remote_device_svc.ErrDeviceNotFound)

			_, err := r.svc.List(r.ctx, "7")
			assert.Equal(t, code.RemoteDeviceNotFound, codeOf(t, err))
		})

		convey.Convey("凭据已失效 → 与离线分开说", func() {
			r := newRig(t)
			r.expectBorrowFails(7, remote_device_svc.ErrDeviceUnauthorized)

			_, err := r.svc.List(r.ctx, "7")
			assert.Equal(t, code.RemoteDeviceUnauthorized, codeOf(t, err))
		})

		convey.Convey("deviceID 不是一个设备 → 不 Borrow", func() {
			r := newRig(t)
			_, err := r.svc.List(r.ctx, "abc")
			assert.Equal(t, code.RemoteDeviceNotFound, codeOf(t, err))
		})
	})
}

// ── Create ──────────────────────────────────────────────────────────────────

func TestCreate(t *testing.T) {
	convey.Convey("Create", t, func() {
		convey.Convey("一次往返,交回设备落库之后的那一行", func() {
			r := newRig(t)
			r.expectRoundTrip(t, 7)
			onCreate(r, func(request *agentrewire.PortForwardCreateRequest) (*agentrewire.PortForwardCreateResponse, error) {
				require.Equal(t, uint32(3000), request.GetPort())
				require.Equal(t, "dev server", request.GetName())
				return &agentrewire.PortForwardCreateResponse{Mapping: &agentrewire.PortForwardMapping{
					Id: 11, Port: 3000, Name: "dev server", Enabled: true,
				}}, nil
			})

			view, err := r.svc.Create(r.ctx, "7", 3000, "dev server")
			require.NoError(t, err)
			assert.Equal(t, "11", view.ID)
			assert.Equal(t, 3000, view.Port)
			assert.Equal(t, "dev server", view.Name)
			assert.True(t, view.Enabled)
		})

		convey.Convey("端口已被声明 → 端口占用码(新增表单据此指着端口那一格说)", func() {
			r := newRig(t)
			r.expectRoundTrip(t, 7)
			onCreate(r, func(*agentrewire.PortForwardCreateRequest) (*agentrewire.PortForwardCreateResponse, error) {
				return nil, rpcErr(rpcerror.CodePortForwardPortTaken)
			})

			_, err := r.svc.Create(r.ctx, "7", 3000, "dev server")
			assert.Equal(t, code.PortForwardPortTaken, codeOf(t, err))
		})

		convey.Convey("设备说端口越界 → 与端口占用分开说", func() {
			r := newRig(t)
			r.expectRoundTrip(t, 7)
			onCreate(r, func(*agentrewire.PortForwardCreateRequest) (*agentrewire.PortForwardCreateResponse, error) {
				return nil, rpcErr(rpcerror.CodePortForwardInvalidPort)
			})

			_, err := r.svc.Create(r.ctx, "7", 3000, "dev server")
			assert.Equal(t, code.PortForwardInvalidPort, codeOf(t, err))
		})

		convey.Convey("端口在本层就出了 uint32 的界 → 不 Borrow,给同一个越界码", func() {
			// 负数 / 65535 以上不能就这么转成 uint32 发出去:那会在线上变成
			// 另一个端口号,设备照着它去判定。这一格前端也填得动,所以给的是
			// 与设备侧同一个码,而不是一句通用参数错误。
			for _, port := range []int{0, -1, 65536, 1 << 20} {
				r := newRig(t)
				_, err := r.svc.Create(r.ctx, "7", port, "x")
				assert.Equal(t, code.PortForwardInvalidPort, codeOf(t, err))
			}
		})

		convey.Convey("设备离线 → 离线码", func() {
			r := newRig(t)
			r.expectBorrowFails(7, errors.New("connection refused"))

			_, err := r.svc.Create(r.ctx, "7", 3000, "dev server")
			assert.Equal(t, code.PortForwardDeviceOffline, codeOf(t, err))
		})
	})
}

// ── SetEnabled ──────────────────────────────────────────────────────────────

func TestSetEnabled(t *testing.T) {
	convey.Convey("SetEnabled", t, func() {
		convey.Convey("一次往返,交回改完之后的那一行", func() {
			r := newRig(t)
			r.expectRoundTrip(t, 7)
			onSetEnabled(r, func(request *agentrewire.PortForwardSetEnabledRequest) (*agentrewire.PortForwardSetEnabledResponse, error) {
				require.Equal(t, int64(11), request.GetId())
				require.False(t, request.GetEnabled())
				return &agentrewire.PortForwardSetEnabledResponse{Mapping: &agentrewire.PortForwardMapping{
					Id: 11, Port: 3000, Name: "dev server", Enabled: false,
				}}, nil
			})

			view, err := r.svc.SetEnabled(r.ctx, "7", "11", false)
			require.NoError(t, err)
			assert.Equal(t, "11", view.ID)
			assert.False(t, view.Enabled)
		})

		convey.Convey("那一行已经被别的客户端删掉 → 声明不存在码", func() {
			r := newRig(t)
			r.expectRoundTrip(t, 7)
			onSetEnabled(r, func(*agentrewire.PortForwardSetEnabledRequest) (*agentrewire.PortForwardSetEnabledResponse, error) {
				return nil, rpcErr(rpcerror.CodePortForwardNotDeclared)
			})

			_, err := r.svc.SetEnabled(r.ctx, "7", "11", true)
			assert.Equal(t, code.PortForwardNotDeclared, codeOf(t, err))
		})

		convey.Convey("mappingID 不是一条声明 → 不 Borrow", func() {
			r := newRig(t)
			_, err := r.svc.SetEnabled(r.ctx, "7", "nope", true)
			assert.Equal(t, code.PortForwardNotDeclared, codeOf(t, err))
		})
	})
}

// ── Delete ──────────────────────────────────────────────────────────────────

func TestDelete(t *testing.T) {
	convey.Convey("Delete", t, func() {
		convey.Convey("一次往返删掉那一行", func() {
			r := newRig(t)
			r.expectRoundTrip(t, 7)
			onDelete(r, func(request *agentrewire.PortForwardDeleteRequest) (*agentrewire.PortForwardDeleteResponse, error) {
				require.Equal(t, int64(11), request.GetId())
				return &agentrewire.PortForwardDeleteResponse{Deleted: true}, nil
			})

			require.NoError(t, r.svc.Delete(r.ctx, "7", "11"))
		})

		convey.Convey("两个客户端同时删同一条:后到的那次不是错误", func() {
			// 设备侧刻意把删除做成幂等的(deleted 如实说有没有删到一行,两种都不
			// 是错误)。本层不许把它翻回一个错误 —— 用户要的结果已经达成了。
			r := newRig(t)
			r.expectRoundTrip(t, 7)
			onDelete(r, func(*agentrewire.PortForwardDeleteRequest) (*agentrewire.PortForwardDeleteResponse, error) {
				return &agentrewire.PortForwardDeleteResponse{Deleted: false}, nil
			})

			require.NoError(t, r.svc.Delete(r.ctx, "7", "11"))
		})

		convey.Convey("设备离线 → 离线码", func() {
			r := newRig(t)
			r.expectBorrowFails(7, errors.New("connection refused"))

			err := r.svc.Delete(r.ctx, "7", "11")
			assert.Equal(t, code.PortForwardDeviceOffline, codeOf(t, err))
		})
	})
}

// ── 单例 ────────────────────────────────────────────────────────────────────

func TestDefaultIsWired(t *testing.T) {
	convey.Convey("Default() 给得出一个实现,SetDefault 换得掉(绑定层与单测都靠它)", t, func() {
		original := Default()
		t.Cleanup(func() { SetDefault(original) })
		require.NotNil(t, original)

		replacement := &portForwardImpl{}
		SetDefault(replacement)
		assert.Equal(t, PortForwardSvc(replacement), Default())
	})
}
