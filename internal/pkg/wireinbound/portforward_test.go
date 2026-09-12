package wireinbound

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/agentre-hub/agentre/pkg/wire/agentrewire"
	"github.com/agentre-hub/agentre/pkg/wire/protorpc"
	"github.com/agentre-hub/agentre/pkg/wire/rpcerror"
)

// 声明族(list / create / setEnabled / delete)的注册面与另外几族同处一室:两种执行端
// 共用这一处,所以「浏览器控制台还是另一台桌面端在问」不改变对面认不认识这四个方法。

type fakePortForward struct {
	listed   *agentrewire.PortForwardListResponse
	created  *agentrewire.PortForwardCreateRequest
	toggled  *agentrewire.PortForwardSetEnabledRequest
	deleted  *agentrewire.PortForwardDeleteRequest
	failWith error
}

func (f *fakePortForward) List(context.Context, *agentrewire.PortForwardListRequest) (*agentrewire.PortForwardListResponse, error) {
	if f.failWith != nil {
		return nil, f.failWith
	}
	return f.listed, nil
}

func (f *fakePortForward) Create(_ context.Context, request *agentrewire.PortForwardCreateRequest) (*agentrewire.PortForwardCreateResponse, error) {
	if f.failWith != nil {
		return nil, f.failWith
	}
	f.created = request
	return &agentrewire.PortForwardCreateResponse{Mapping: &agentrewire.PortForwardMapping{
		Id: 11, Port: request.GetPort(), Name: request.GetName(), Enabled: true, Createtime: 1757300000, Updatetime: 1757300000,
	}}, nil
}

func (f *fakePortForward) SetEnabled(_ context.Context, request *agentrewire.PortForwardSetEnabledRequest) (*agentrewire.PortForwardSetEnabledResponse, error) {
	if f.failWith != nil {
		return nil, f.failWith
	}
	f.toggled = request
	return &agentrewire.PortForwardSetEnabledResponse{Mapping: &agentrewire.PortForwardMapping{
		Id: request.GetId(), Port: 3000, Enabled: request.GetEnabled(),
	}}, nil
}

func (f *fakePortForward) Delete(_ context.Context, request *agentrewire.PortForwardDeleteRequest) (*agentrewire.PortForwardDeleteResponse, error) {
	if f.failWith != nil {
		return nil, f.failWith
	}
	f.deleted = request
	return &agentrewire.PortForwardDeleteResponse{Deleted: true}, nil
}

// Given 一台带着端口转发能力的机器,When 对端在一条已鉴权的连接上走完声明族四个方法,
// Then 每一格都真的过得了线 —— 方法号配错、字段号撞了,只断结构体的用例照样绿。
func TestPeripheral_GivenAPortForwardPort_WhenTheDeclarationFamilyIsCalled_ThenItRoundTrips(t *testing.T) {
	t.Parallel()

	fake := &fakePortForward{listed: &agentrewire.PortForwardListResponse{Mappings: []*agentrewire.PortForwardMapping{
		{Id: 7, Port: 3000, Name: "dev server", Enabled: true, Createtime: 1757300000, Updatetime: 1757300009},
	}}}
	ctx, client := dialPeripheral(t, PeripheralDeps{PortForward: fake})

	list, err := protorpc.CallMethod(ctx, client, uint32(agentrewire.RpcMethod_RPC_METHOD_PORT_FORWARD_LIST),
		&agentrewire.PortForwardListRequest{}, func() *agentrewire.PortForwardListResponse { return &agentrewire.PortForwardListResponse{} })
	require.NoError(t, err)
	require.Len(t, list.GetMappings(), 1)
	assert.Equal(t, uint32(3000), list.GetMappings()[0].GetPort())
	assert.Equal(t, "dev server", list.GetMappings()[0].GetName())
	assert.True(t, list.GetMappings()[0].GetEnabled())
	assert.Equal(t, int64(1757300009), list.GetMappings()[0].GetUpdatetime())

	created, err := protorpc.CallMethod(ctx, client, uint32(agentrewire.RpcMethod_RPC_METHOD_PORT_FORWARD_CREATE),
		&agentrewire.PortForwardCreateRequest{Port: 5173, Name: "vite"}, func() *agentrewire.PortForwardCreateResponse { return &agentrewire.PortForwardCreateResponse{} })
	require.NoError(t, err)
	assert.Equal(t, uint32(5173), fake.created.GetPort(), "端口必须原样送到设备侧")
	assert.Equal(t, "vite", fake.created.GetName())
	assert.Equal(t, int64(11), created.GetMapping().GetId(), "id 由设备定,调用方读回来")

	toggled, err := protorpc.CallMethod(ctx, client, uint32(agentrewire.RpcMethod_RPC_METHOD_PORT_FORWARD_SET_ENABLED),
		&agentrewire.PortForwardSetEnabledRequest{Id: 7, Enabled: false}, func() *agentrewire.PortForwardSetEnabledResponse { return &agentrewire.PortForwardSetEnabledResponse{} })
	require.NoError(t, err)
	assert.Equal(t, int64(7), fake.toggled.GetId())
	assert.False(t, fake.toggled.GetEnabled(), "停用那一位是 bool 的假值,漏传与传 false 在线上不能是同一件事")
	assert.False(t, toggled.GetMapping().GetEnabled())

	deleted, err := protorpc.CallMethod(ctx, client, uint32(agentrewire.RpcMethod_RPC_METHOD_PORT_FORWARD_DELETE),
		&agentrewire.PortForwardDeleteRequest{Id: 7}, func() *agentrewire.PortForwardDeleteResponse { return &agentrewire.PortForwardDeleteResponse{} })
	require.NoError(t, err)
	assert.Equal(t, int64(7), fake.deleted.GetId())
	assert.True(t, deleted.GetDeleted())
}

// 设备侧的领域错误码必须原样落在应答上,不被折成 -32603:调用方按码分支
// (PortTaken 让用户改端口,Internal 让用户去查那台机器的日志)。
func TestPeripheral_GivenTheDeviceRefuses_WhenCreating_ThenTheDomainCodeSurvivesTheWire(t *testing.T) {
	t.Parallel()

	ctx, client := dialPeripheral(t, PeripheralDeps{PortForward: &fakePortForward{
		failWith: &rpcerror.Error{Code: rpcerror.CodePortForwardPortTaken, Message: "port already declared"},
	}})

	_, err := protorpc.CallMethod(ctx, client, uint32(agentrewire.RpcMethod_RPC_METHOD_PORT_FORWARD_CREATE),
		&agentrewire.PortForwardCreateRequest{Port: 3000}, func() *agentrewire.PortForwardCreateResponse { return &agentrewire.PortForwardCreateResponse{} })

	var rpcErr *protorpc.Error
	require.ErrorAs(t, err, &rpcErr)
	assert.Equal(t, int32(rpcerror.CodePortForwardPortTaken), rpcErr.Code)
}

// Given 一个不带端口转发能力的宿主,When 对端调这一族的任何一个方法,Then 应答是
// method not found —— 「这台机器办不到」在协议里只有这一种说法,与另外几族同一条纪律。
func TestPeripheral_GivenNoPortForwardPort_WhenAnyMethodCalled_ThenMethodNotFound(t *testing.T) {
	t.Parallel()

	ctx, client := dialPeripheral(t, PeripheralDeps{})

	for name, call := range map[string]func() error{
		"list": func() error {
			_, err := protorpc.CallMethod(ctx, client, uint32(agentrewire.RpcMethod_RPC_METHOD_PORT_FORWARD_LIST),
				&agentrewire.PortForwardListRequest{}, func() *agentrewire.PortForwardListResponse { return &agentrewire.PortForwardListResponse{} })
			return err
		},
		"create": func() error {
			_, err := protorpc.CallMethod(ctx, client, uint32(agentrewire.RpcMethod_RPC_METHOD_PORT_FORWARD_CREATE),
				&agentrewire.PortForwardCreateRequest{Port: 3000}, func() *agentrewire.PortForwardCreateResponse { return &agentrewire.PortForwardCreateResponse{} })
			return err
		},
		"setEnabled": func() error {
			_, err := protorpc.CallMethod(ctx, client, uint32(agentrewire.RpcMethod_RPC_METHOD_PORT_FORWARD_SET_ENABLED),
				&agentrewire.PortForwardSetEnabledRequest{Id: 1}, func() *agentrewire.PortForwardSetEnabledResponse { return &agentrewire.PortForwardSetEnabledResponse{} })
			return err
		},
		"delete": func() error {
			_, err := protorpc.CallMethod(ctx, client, uint32(agentrewire.RpcMethod_RPC_METHOD_PORT_FORWARD_DELETE),
				&agentrewire.PortForwardDeleteRequest{Id: 1}, func() *agentrewire.PortForwardDeleteResponse { return &agentrewire.PortForwardDeleteResponse{} })
			return err
		},
	} {
		var rpcErr *protorpc.Error
		require.ErrorAs(t, call(), &rpcErr, name)
		assert.Equal(t, protorpc.CodeMethodNotFound, rpcErr.Code, name,
			"端口缺席只有一种说法:method not found。回 internal 会让调用方去查那台机器的日志")
	}
}
