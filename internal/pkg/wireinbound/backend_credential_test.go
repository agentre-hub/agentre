package wireinbound

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/agentre-hub/agentre/pkg/wire/agentrewire"
	"github.com/agentre-hub/agentre/pkg/wire/protorpc"
	"github.com/agentre-hub/agentre/pkg/wire/wirecall"
)

// 设备本地后端凭据一族:凭据只存在后端绑定的那台设备上,调用方是已经能在它上面发起
// 对话的对端。这一族的注册与另外几族同处一室,「端口缺席 → method not found」「没鉴权
// → 拒绝」两条纪律同样成立。

type fakeBackendCredentials struct {
	calls []string
}

func (f *fakeBackendCredentials) Status(context.Context, *agentrewire.BackendCredentialStatusRequest) (*agentrewire.BackendCredentialStatusResponse, error) {
	f.calls = append(f.calls, "status")
	return &agentrewire.BackendCredentialStatusResponse{OpenclawTokenSaved: true}, nil
}

func (f *fakeBackendCredentials) SetOpenClawToken(_ context.Context, request *agentrewire.OpenClawTokenSetRequest) (*agentrewire.OpenClawTokenSetResponse, error) {
	f.calls = append(f.calls, "setOpenClawToken")
	return &agentrewire.OpenClawTokenSetResponse{TokenSaved: request.GetToken() != ""}, nil
}

func (f *fakeBackendCredentials) HermesAuthProviders(context.Context, *agentrewire.HermesAuthProvidersRequest) (*agentrewire.HermesAuthProvidersResponse, error) {
	f.calls = append(f.calls, "hermesAuthProviders")
	return &agentrewire.HermesAuthProvidersResponse{Providers: []*agentrewire.HermesAuthProvider{{Name: "basic", SupportsPassword: true}}}, nil
}

func (f *fakeBackendCredentials) HermesLogin(context.Context, *agentrewire.HermesLoginRequest) (*agentrewire.HermesLoginResponse, error) {
	f.calls = append(f.calls, "hermesLogin")
	return &agentrewire.HermesLoginResponse{Provider: "basic", UserId: "7"}, nil
}

func (f *fakeBackendCredentials) HermesLogout(context.Context, *agentrewire.HermesLogoutRequest) (*agentrewire.HermesLogoutResponse, error) {
	f.calls = append(f.calls, "hermesLogout")
	return &agentrewire.HermesLogoutResponse{}, nil
}

func (f *fakeBackendCredentials) TestConnection(context.Context, *agentrewire.BackendConnectionTestRequest) (*agentrewire.BackendConnectionTestResponse, error) {
	f.calls = append(f.calls, "testConnection")
	return &agentrewire.BackendConnectionTestResponse{Code: "HERMES_LOGIN_REQUIRED"}, nil
}

// callEveryBackendCredentialMethod 走一遍这一族六个方法,返回每一条的错误。
func callEveryBackendCredentialMethod(ctx context.Context, client *protorpc.Conn) map[string]error {
	errs := map[string]error{}
	_, errs["status"] = wirecall.BackendCredentialStatus(ctx, wirecall.On(client), &agentrewire.BackendCredentialStatusRequest{BackendType: "openclaw", SyncId: "s"})
	_, errs["setOpenClawToken"] = wirecall.OpenClawTokenSet(ctx, wirecall.On(client), &agentrewire.OpenClawTokenSetRequest{SyncId: "s", Token: "t"})
	_, errs["hermesAuthProviders"] = wirecall.HermesAuthProviders(ctx, wirecall.On(client), &agentrewire.HermesAuthProvidersRequest{HermesUrl: "http://127.0.0.1:9119"})
	_, errs["hermesLogin"] = wirecall.HermesLogin(ctx, wirecall.On(client), &agentrewire.HermesLoginRequest{HermesUrl: "http://127.0.0.1:9119"})
	_, errs["hermesLogout"] = wirecall.HermesLogout(ctx, wirecall.On(client), &agentrewire.HermesLogoutRequest{HermesUrl: "http://127.0.0.1:9119"})
	_, errs["testConnection"] = wirecall.BackendConnectionTest(ctx, wirecall.On(client), &agentrewire.BackendConnectionTestRequest{BackendType: "hermes"})
	return errs
}

// Given 一台带凭据能力的设备,When 已鉴权的对端调这一族,Then 每个方法都过得了线并交到端口。
func TestBackendCredentials_GivenAPort_WhenAnAuthenticatedPeerCalls_ThenEachMethodReachesIt(t *testing.T) {
	t.Parallel()

	fake := &fakeBackendCredentials{}
	ctx, client := dialPeripheral(t, PeripheralDeps{BackendCredentials: fake})

	for name, err := range callEveryBackendCredentialMethod(ctx, client) {
		require.NoError(t, err, name)
	}
	assert.ElementsMatch(t, []string{"status", "setOpenClawToken", "hermesAuthProviders", "hermesLogin", "hermesLogout", "testConnection"}, fake.calls)

	login, err := wirecall.HermesLogin(ctx, wirecall.On(client), &agentrewire.HermesLoginRequest{HermesUrl: "http://127.0.0.1:9119"})
	require.NoError(t, err)
	assert.Equal(t, "7", login.GetUserId())
}

// Given 一条没握过手的连接(既不是同账号认证的对端,也不是配对过的桌面端),When 它调这一族
// 的任何方法,Then 一律被拒且端口一次都没被调到 —— 凭据写入与带凭据的连接不对陌生人开放。
func TestBackendCredentials_GivenAnUnauthenticatedCaller_WhenAnyMethodIsCalled_ThenRejectedBeforeThePort(t *testing.T) {
	t.Parallel()

	fake := &fakeBackendCredentials{}
	registry := protorpc.NewRegistry()
	RegisterPeripheralMethods(registry, PeripheralDeps{BackendCredentials: fake})
	clientTransport, serverTransport := peripheralPipePair()
	client := protorpc.NewConn(clientTransport, protorpc.NewRegistry())
	server := protorpc.NewConn(serverTransport, registry)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go client.Serve(ctx)
	go server.Serve(ctx)

	for name, err := range callEveryBackendCredentialMethod(ctx, client) {
		var rpcErr *protorpc.Error
		require.ErrorAs(t, err, &rpcErr, name)
		assert.Equal(t, int32(-32001), rpcErr.Code, name)
	}
	assert.Empty(t, fake.calls)
}

// Given 一个不带凭据能力的宿主,When 对端调这一族,Then method not found。
func TestBackendCredentials_GivenNoPort_WhenAnyMethodIsCalled_ThenMethodNotFound(t *testing.T) {
	t.Parallel()

	ctx, client := dialPeripheral(t, PeripheralDeps{})

	for name, err := range callEveryBackendCredentialMethod(ctx, client) {
		var rpcErr *protorpc.Error
		require.ErrorAs(t, err, &rpcErr, name)
		assert.Equal(t, protorpc.CodeMethodNotFound, rpcErr.Code, name)
	}
}
