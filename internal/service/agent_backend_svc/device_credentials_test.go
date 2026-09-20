package agent_backend_svc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/cago-frame/cago/pkg/consts"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/syncmeta_entity"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/runtimes/hermes"
	"github.com/agentre-hub/agentre/internal/pkg/backendcred"
	"github.com/agentre-hub/agentre/internal/pkg/keychain"
	"github.com/agentre-hub/agentre/internal/pkg/openclawgateway"
	"github.com/agentre-hub/agentre/pkg/wire/agentrewire"
)

// fakeRemoteCredentials 是 remoteCredentialsPort 的测试替身:记录每次调用的目标设备
// 与请求,按 setup 时塞的应答回话。
type fakeRemoteCredentials struct {
	deviceIDs []int64

	statusReq  *agentrewire.BackendCredentialStatusRequest
	statusResp *agentrewire.BackendCredentialStatusResponse

	tokenReq *agentrewire.OpenClawTokenSetRequest
	tokenErr error

	providersReq  *agentrewire.HermesAuthProvidersRequest
	providersResp *agentrewire.HermesAuthProvidersResponse

	loginReq  *agentrewire.HermesLoginRequest
	loginResp *agentrewire.HermesLoginResponse

	logoutReq  *agentrewire.HermesLogoutRequest
	logoutCall int

	testReq  *agentrewire.BackendConnectionTestRequest
	testResp *agentrewire.BackendConnectionTestResponse
}

func (f *fakeRemoteCredentials) Status(_ context.Context, deviceID int64, request *agentrewire.BackendCredentialStatusRequest) (*agentrewire.BackendCredentialStatusResponse, error) {
	f.deviceIDs = append(f.deviceIDs, deviceID)
	f.statusReq = request
	return f.statusResp, nil
}

func (f *fakeRemoteCredentials) SetOpenClawToken(_ context.Context, deviceID int64, request *agentrewire.OpenClawTokenSetRequest) (*agentrewire.OpenClawTokenSetResponse, error) {
	f.deviceIDs = append(f.deviceIDs, deviceID)
	f.tokenReq = request
	if f.tokenErr != nil {
		return nil, f.tokenErr
	}
	return &agentrewire.OpenClawTokenSetResponse{TokenSaved: !request.GetClear()}, nil
}

func (f *fakeRemoteCredentials) HermesAuthProviders(_ context.Context, deviceID int64, request *agentrewire.HermesAuthProvidersRequest) (*agentrewire.HermesAuthProvidersResponse, error) {
	f.deviceIDs = append(f.deviceIDs, deviceID)
	f.providersReq = request
	return f.providersResp, nil
}

func (f *fakeRemoteCredentials) HermesLogin(_ context.Context, deviceID int64, request *agentrewire.HermesLoginRequest) (*agentrewire.HermesLoginResponse, error) {
	f.deviceIDs = append(f.deviceIDs, deviceID)
	f.loginReq = request
	return f.loginResp, nil
}

func (f *fakeRemoteCredentials) HermesLogout(_ context.Context, deviceID int64, request *agentrewire.HermesLogoutRequest) (*agentrewire.HermesLogoutResponse, error) {
	f.deviceIDs = append(f.deviceIDs, deviceID)
	f.logoutReq = request
	f.logoutCall++
	return &agentrewire.HermesLogoutResponse{}, nil
}

func (f *fakeRemoteCredentials) TestConnection(_ context.Context, deviceID int64, request *agentrewire.BackendConnectionTestRequest) (*agentrewire.BackendConnectionTestResponse, error) {
	f.deviceIDs = append(f.deviceIDs, deviceID)
	f.testReq = request
	return f.testResp, nil
}

// remoteHermesRow 是一条绑定到 remoteTestFingerprint 的 hermes 后端。
func remoteHermesRow(id int64, rawURL string) *agent_backend_entity.AgentBackend {
	return &agent_backend_entity.AgentBackend{
		ID: id, Type: string(agent_backend_entity.TypeHermes), Name: "remote hermes",
		HermesURL: rawURL, DeviceFingerprint: remoteTestFingerprint, Status: consts.ACTIVE,
	}
}

// remoteOpenClawRow 是一条绑定到 remoteTestFingerprint 的 openclaw 后端。
func remoteOpenClawRow(id int64, syncID string) *agent_backend_entity.AgentBackend {
	return &agent_backend_entity.AgentBackend{
		ID: id, SyncMeta: syncmeta_entity.SyncMeta{SyncID: syncID},
		Type: string(agent_backend_entity.TypeOpenClaw), Name: "remote openclaw",
		OpenClawGatewayURL: "wss://10.0.0.7:18789/", OpenClawAgentID: "main",
		OpenClawDefaultModel: "anthropic/claude-sonnet-4-6",
		OpenClawSessionMode:  agent_backend_entity.OpenClawSessionPerAgentRESession,
		DeviceFingerprint:    remoteTestFingerprint, Status: consts.ACTIVE,
	}
}

// TestBackendCredentialStatus_RoutesByBoundDevice 是这条规格的核心:同一个查询,
// 绑到本机读本机 keychain,绑到别的设备只问那台设备,而且绝不回传凭据本身。
func TestBackendCredentialStatus_RoutesByBoundDevice(t *testing.T) {
	t.Run("Given an OpenClaw backend bound to this machine then the local keychain answers", func(t *testing.T) {
		_, _, _, _, _, svc := setupSvcTest(t)
		installRemoteDeviceFixture(t)
		memory := keychain.NewMemory()
		svc.secrets = memory
		remote := &fakeRemoteCredentials{}
		svc.remoteCredentials = remote
		require.NoError(t, memory.Set(backendcred.OpenClawTokenAccount("sync-1"), strings.Repeat("t", 40)))

		resp, err := svc.BackendCredentialStatus(context.Background(), &BackendCredentialStatusRequest{
			Type: string(agent_backend_entity.TypeOpenClaw), SyncID: "sync-1",
		})

		require.NoError(t, err)
		assert.True(t, resp.OpenClawTokenSaved)
		assert.Empty(t, remote.deviceIDs, "本机后端不该拨远端")
		raw, err := json.Marshal(resp)
		require.NoError(t, err)
		assert.NotContains(t, string(raw), strings.Repeat("t", 40))
	})

	t.Run("Given an OpenClaw backend bound to another device then only that device is asked", func(t *testing.T) {
		_, _, _, _, _, svc := setupSvcTest(t)
		installRemoteDeviceFixture(t)
		memory := keychain.NewMemory()
		svc.secrets = memory
		remote := &fakeRemoteCredentials{statusResp: &agentrewire.BackendCredentialStatusResponse{OpenclawTokenSaved: true}}
		svc.remoteCredentials = remote

		resp, err := svc.BackendCredentialStatus(context.Background(), &BackendCredentialStatusRequest{
			Type: string(agent_backend_entity.TypeOpenClaw), SyncID: "sync-1", DeviceID: remoteTestFingerprint,
		})

		require.NoError(t, err)
		assert.True(t, resp.OpenClawTokenSaved)
		require.Equal(t, []int64{42}, remote.deviceIDs)
		assert.Equal(t, "sync-1", remote.statusReq.GetSyncId())
		_, getErr := memory.Get(backendcred.OpenClawTokenAccount("sync-1"))
		assert.ErrorIs(t, getErr, keychain.ErrNotFound, "远端后端不该碰本机 keychain")
	})

	t.Run("Given a Hermes backend bound to this machine then the login state comes from the local keychain", func(t *testing.T) {
		_, backendMock, _, _, _, svc := setupSvcTest(t)
		installRemoteDeviceFixture(t)
		memory := keychain.NewMemory()
		svc.secrets = memory
		svc.hermes = backendcred.NewHermesCredentials(func() backendcred.Store { return memory })
		require.NoError(t, memory.Set(backendcred.HermesAccount("http://127.0.0.1:9119"), "refresh-1"))
		row := &agent_backend_entity.AgentBackend{
			ID: 3, Type: string(agent_backend_entity.TypeHermes), Name: "h",
			HermesURL: "http://127.0.0.1:9119", HermesAuthProvider: "basic", HermesUserID: "user-7",
			Status: consts.ACTIVE,
		}
		backendMock.EXPECT().List(gomock.Any()).Return([]*agent_backend_entity.AgentBackend{row}, nil).AnyTimes()

		resp, err := svc.BackendCredentialStatus(context.Background(), &BackendCredentialStatusRequest{
			Type: string(agent_backend_entity.TypeHermes), HermesURL: "http://127.0.0.1:9119/",
		})

		require.NoError(t, err)
		assert.True(t, resp.HermesLoggedIn)
		assert.Equal(t, "basic", resp.HermesProvider)
		assert.Equal(t, "user-7", resp.HermesUserID)
	})

	t.Run("Given a Hermes backend bound to another device then that device answers", func(t *testing.T) {
		_, _, _, _, _, svc := setupSvcTest(t)
		installRemoteDeviceFixture(t)
		remote := &fakeRemoteCredentials{statusResp: &agentrewire.BackendCredentialStatusResponse{
			HermesLoggedIn: true, HermesProvider: "basic", HermesUserId: "user-9",
		}}
		svc.remoteCredentials = remote

		resp, err := svc.BackendCredentialStatus(context.Background(), &BackendCredentialStatusRequest{
			Type: string(agent_backend_entity.TypeHermes), HermesURL: "http://10.0.0.7:9119",
			DeviceID: remoteTestFingerprint,
		})

		require.NoError(t, err)
		assert.True(t, resp.HermesLoggedIn)
		assert.Equal(t, "user-9", resp.HermesUserID)
		require.Equal(t, []int64{42}, remote.deviceIDs)
		assert.Equal(t, "http://10.0.0.7:9119", remote.statusReq.GetHermesUrl())
	})

	t.Run("Given the bound device is not paired then the caller is told so", func(t *testing.T) {
		_, _, _, _, _, svc := setupSvcTest(t)
		installRemoteDeviceFixture(t)
		svc.remoteCredentials = &fakeRemoteCredentials{}

		_, err := svc.BackendCredentialStatus(context.Background(), &BackendCredentialStatusRequest{
			Type: string(agent_backend_entity.TypeHermes), HermesURL: "http://10.0.0.9:9119",
			DeviceID: "sha256:unpaired-device",
		})

		require.Error(t, err)
	})
}

// TestHermesAuthOperations_RouteToBoundDevice 覆盖列提供方 / 登录 / 登出三条:
// 带上绑定设备就只在那台设备上做,本机 keychain 不留痕迹。
func TestHermesAuthOperations_RouteToBoundDevice(t *testing.T) {
	t.Run("Given a bound device when listing providers then that device reads the directory", func(t *testing.T) {
		_, _, _, _, _, svc := setupSvcTest(t)
		installRemoteDeviceFixture(t)
		remote := &fakeRemoteCredentials{providersResp: &agentrewire.HermesAuthProvidersResponse{
			Providers: []*agentrewire.HermesAuthProvider{{Name: "basic", DisplayName: "Basic", SupportsPassword: true}},
		}}
		svc.remoteCredentials = remote

		resp, err := svc.ListHermesAuthProviders(context.Background(), &ListHermesAuthProvidersRequest{
			URL: "http://10.0.0.7:9119", DeviceID: remoteTestFingerprint,
		})

		require.NoError(t, err)
		require.Len(t, resp.Providers, 1)
		assert.Equal(t, "basic", resp.Providers[0].Name)
		assert.True(t, resp.Providers[0].SupportsPassword)
		require.Equal(t, []int64{42}, remote.deviceIDs)
	})

	t.Run("Given the device cannot reach the serve then the reason is readable", func(t *testing.T) {
		_, _, _, _, _, svc := setupSvcTest(t)
		installRemoteDeviceFixture(t)
		svc.remoteCredentials = &fakeRemoteCredentials{providersResp: &agentrewire.HermesAuthProvidersResponse{
			Code: HermesCodeUnreachable,
		}}

		_, err := svc.ListHermesAuthProviders(context.Background(), &ListHermesAuthProvidersRequest{
			URL: "http://10.0.0.7:9119", DeviceID: remoteTestFingerprint,
		})

		require.Error(t, err)
		assert.NotContains(t, err.Error(), HermesCodeUnreachable, "界面不贴机器原话")
	})

	t.Run("Given a bound device when logging in then the password only crosses to that device", func(t *testing.T) {
		_, _, _, _, _, svc := setupSvcTest(t)
		installRemoteDeviceFixture(t)
		memory := keychain.NewMemory()
		svc.secrets = memory
		svc.hermes = backendcred.NewHermesCredentials(func() backendcred.Store { return memory })
		remote := &fakeRemoteCredentials{loginResp: &agentrewire.HermesLoginResponse{Provider: "basic", UserId: "user-9"}}
		svc.remoteCredentials = remote

		resp, err := svc.LoginHermes(context.Background(), &LoginHermesRequest{
			URL: "http://10.0.0.7:9119", Provider: "basic", Username: "alice", Password: "correct-horse",
			DeviceID: remoteTestFingerprint,
		})

		require.NoError(t, err)
		assert.Equal(t, "user-9", resp.UserID)
		require.Equal(t, []int64{42}, remote.deviceIDs)
		assert.Equal(t, "correct-horse", remote.loginReq.GetPassword())
		_, getErr := memory.Get(backendcred.HermesAccount("http://10.0.0.7:9119"))
		assert.ErrorIs(t, getErr, keychain.ErrNotFound, "远端登录不该在本机留凭据")
		raw, err := json.Marshal(resp)
		require.NoError(t, err)
		assert.NotContains(t, string(raw), "correct-horse")
	})

	t.Run("Given the device rejects the credentials then the reason is localized", func(t *testing.T) {
		_, _, _, _, _, svc := setupSvcTest(t)
		installRemoteDeviceFixture(t)
		svc.remoteCredentials = &fakeRemoteCredentials{loginResp: &agentrewire.HermesLoginResponse{
			Code: HermesCodeInvalidCredentials,
		}}

		_, err := svc.LoginHermes(context.Background(), &LoginHermesRequest{
			URL: "http://10.0.0.7:9119", Username: "alice", Password: "nope", DeviceID: remoteTestFingerprint,
		})

		require.Error(t, err)
		assert.NotContains(t, err.Error(), HermesCodeInvalidCredentials)
	})

	t.Run("Given a saved remote backend when logging out then the device drops it and the row is cleared", func(t *testing.T) {
		_, backendMock, _, _, _, svc := setupSvcTest(t)
		installRemoteDeviceFixture(t)
		memory := keychain.NewMemory()
		svc.secrets = memory
		svc.hermes = backendcred.NewHermesCredentials(func() backendcred.Store { return memory })
		require.NoError(t, memory.Set(backendcred.HermesAccount("http://10.0.0.7:9119"), "local-refresh"))
		row := remoteHermesRow(11, "http://10.0.0.7:9119")
		row.HermesAuthProvider = "basic"
		row.HermesUserID = "user-9"
		require.NoError(t, row.MarshalConfig())
		remote := &fakeRemoteCredentials{}
		svc.remoteCredentials = remote
		backendMock.EXPECT().Find(gomock.Any(), int64(11)).Return(row, nil)
		backendMock.EXPECT().Update(gomock.Any(), gomock.Any()).Return(nil)

		_, err := svc.LogoutHermes(context.Background(), &LogoutHermesRequest{ID: 11})

		require.NoError(t, err)
		require.Equal(t, []int64{42}, remote.deviceIDs)
		assert.Equal(t, "http://10.0.0.7:9119", remote.logoutReq.GetHermesUrl())
		stored, getErr := memory.Get(backendcred.HermesAccount("http://10.0.0.7:9119"))
		require.NoError(t, getErr, "远端登出不该删本机同 URL 的凭据")
		assert.Equal(t, "local-refresh", stored)
	})
}

// TestUpdateOpenClaw_TokenWriteRoutesToBoundDevice 覆盖保存:token 写到绑定设备,
// 写失败则后端配置回滚。
func TestUpdateOpenClaw_TokenWriteRoutesToBoundDevice(t *testing.T) {
	t.Run("Given a remote OpenClaw backend when saving a token then only the bound device receives it", func(t *testing.T) {
		_, backendMock, _, _, _, svc := setupSvcTest(t)
		installRemoteDeviceFixture(t)
		memory := keychain.NewMemory()
		svc.secrets = memory
		remote := &fakeRemoteCredentials{}
		svc.remoteCredentials = remote
		row := remoteOpenClawRow(21, "sync-openclaw-21")
		backendMock.EXPECT().Find(gomock.Any(), int64(21)).Return(row, nil)
		backendMock.EXPECT().FindByName(gomock.Any(), gomock.Any()).Return(nil, nil).AnyTimes()
		backendMock.EXPECT().Update(gomock.Any(), gomock.Any()).Return(nil)
		backendMock.EXPECT().FindCLIOverlay(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil, nil).AnyTimes()
		backendMock.EXPECT().CreateCLIOverlay(gomock.Any(), gomock.Any()).Return(nil).AnyTimes()

		token := strings.Repeat("r", 44)
		_, err := svc.UpdateOpenClaw(context.Background(), remoteOpenClawUpdateRequest(row), token, false)

		require.NoError(t, err)
		require.Equal(t, []int64{42}, remote.deviceIDs)
		assert.Equal(t, "sync-openclaw-21", remote.tokenReq.GetSyncId())
		assert.Equal(t, token, remote.tokenReq.GetToken())
		assert.False(t, remote.tokenReq.GetClear())
		_, getErr := memory.Get(backendcred.OpenClawTokenAccount("sync-openclaw-21"))
		assert.ErrorIs(t, getErr, keychain.ErrNotFound)
	})

	t.Run("Given the bound device refuses the token then the backend configuration rolls back", func(t *testing.T) {
		_, backendMock, _, _, _, svc := setupSvcTest(t)
		installRemoteDeviceFixture(t)
		svc.secrets = keychain.NewMemory()
		svc.remoteCredentials = &fakeRemoteCredentials{tokenErr: errors.New("state.json is read-only")}
		row := remoteOpenClawRow(22, "sync-openclaw-22")
		before := *row
		backendMock.EXPECT().Find(gomock.Any(), int64(22)).Return(row, nil)
		backendMock.EXPECT().FindByName(gomock.Any(), gomock.Any()).Return(nil, nil).AnyTimes()
		backendMock.EXPECT().FindCLIOverlay(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil, nil).AnyTimes()
		backendMock.EXPECT().CreateCLIOverlay(gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
		gomock.InOrder(
			backendMock.EXPECT().Update(gomock.Any(), gomock.Any()).Return(nil),
			backendMock.EXPECT().Update(gomock.Any(), gomock.Any()).DoAndReturn(
				func(_ context.Context, restored *agent_backend_entity.AgentBackend) error {
					assert.Equal(t, before.Name, restored.Name)
					assert.Equal(t, before.OpenClawAgentID, restored.OpenClawAgentID)
					return nil
				}),
		)

		request := remoteOpenClawUpdateRequest(row)
		request.Name = "renamed"
		request.OpenClawAgentID = "other-agent"
		_, err := svc.UpdateOpenClaw(context.Background(), request, strings.Repeat("r", 44), false)

		require.Error(t, err)
	})
}

func remoteOpenClawUpdateRequest(row *agent_backend_entity.AgentBackend) *UpdateBackendRequest {
	return &UpdateBackendRequest{
		ID: row.ID, Name: row.Name,
		OpenClawGatewayURL:   row.OpenClawGatewayURL,
		OpenClawAgentID:      row.OpenClawAgentID,
		OpenClawDefaultModel: row.OpenClawDefaultModel,
		OpenClawSessionMode:  row.OpenClawSessionMode,
		DeviceID:             string(row.DeviceFingerprint),
	}
}

// TestTestBackend_RemoteCredentialBackends 覆盖测试连接:绑定设备按自己的凭据连一次,
// 结果折回桌面端既有的结构化形状。
func TestTestBackend_RemoteCredentialBackends(t *testing.T) {
	t.Run("Given a remote OpenClaw backend then the bound device tests it", func(t *testing.T) {
		_, backendMock, _, _, _, svc := setupSvcTest(t)
		installRemoteDeviceFixture(t)
		remote := &fakeRemoteCredentials{testResp: &agentrewire.BackendConnectionTestResponse{
			Ok: true, LatencyMs: 12, GatewayVersion: "1.2.3", Protocol: 3,
			OpenclawAgents: []*agentrewire.OpenClawAgentOption{{Id: "main", Name: "Main", IsDefault: true}},
			OpenclawModels: []*agentrewire.OpenClawModelOption{{Id: "m1", Name: "M1", Available: true}},
		}}
		svc.remoteCredentials = remote
		row := remoteOpenClawRow(31, "sync-openclaw-31")
		backendMock.EXPECT().Find(gomock.Any(), int64(31)).Return(row, nil)

		resp, err := svc.TestOpenClaw(context.Background(), &TestBackendRequest{ID: 31}, "draft-token")

		require.NoError(t, err)
		assert.True(t, resp.OK)
		assert.Equal(t, "1.2.3", resp.GatewayVersion)
		require.Len(t, resp.OpenClawAgents, 1)
		require.Len(t, resp.OpenClawModels, 1)
		require.Equal(t, []int64{42}, remote.deviceIDs)
		assert.Equal(t, "draft-token", remote.testReq.GetOpenclawToken())
		assert.Equal(t, "sync-openclaw-31", remote.testReq.GetSyncId())
	})

	t.Run("Given the remote device reports a structured failure then the code is preserved", func(t *testing.T) {
		_, backendMock, _, _, _, svc := setupSvcTest(t)
		installRemoteDeviceFixture(t)
		svc.remoteCredentials = &fakeRemoteCredentials{testResp: &agentrewire.BackendConnectionTestResponse{
			Code: "AUTH_FAILED", LatencyMs: 8,
		}}
		row := remoteOpenClawRow(32, "sync-openclaw-32")
		backendMock.EXPECT().Find(gomock.Any(), int64(32)).Return(row, nil)

		resp, err := svc.Test(context.Background(), &TestBackendRequest{ID: 32})

		require.NoError(t, err)
		assert.False(t, resp.OK)
		assert.Equal(t, "AUTH_FAILED", resp.Code)
	})

	t.Run("Given a remote Hermes backend then the bound device tests it with its own login", func(t *testing.T) {
		_, backendMock, _, _, _, svc := setupSvcTest(t)
		installRemoteDeviceFixture(t)
		remote := &fakeRemoteCredentials{testResp: &agentrewire.BackendConnectionTestResponse{
			Code: HermesCodeLoginRequired, LatencyMs: 5,
		}}
		svc.remoteCredentials = remote
		row := remoteHermesRow(33, "http://10.0.0.7:9119")
		row.HermesAuthProvider = "basic"
		backendMock.EXPECT().Find(gomock.Any(), int64(33)).Return(row, nil)

		resp, err := svc.Test(context.Background(), &TestBackendRequest{ID: 33})

		require.NoError(t, err)
		assert.False(t, resp.OK)
		assert.Equal(t, HermesCodeLoginRequired, resp.Code)
		assert.NotEmpty(t, resp.Message, "结构化 code 之外还要给一句人话")
		require.Equal(t, []int64{42}, remote.deviceIDs)
		assert.Equal(t, "basic", remote.testReq.GetHermesAuthProvider())
	})
}

// TestDeleteBackend_ClearsCredentialOnBoundDevice 覆盖删除:尽力在绑定设备上清除,
// 设备清不掉也不挡删除。
func TestDeleteBackend_ClearsCredentialOnBoundDevice(t *testing.T) {
	t.Run("Given a remote OpenClaw backend when deleted then its token slot is cleared on that device", func(t *testing.T) {
		_, backendMock, _, _, _, svc := setupSvcTest(t)
		installRemoteDeviceFixture(t)
		svc.secrets = keychain.NewMemory()
		remote := &fakeRemoteCredentials{}
		svc.remoteCredentials = remote
		row := remoteOpenClawRow(41, "sync-openclaw-41")
		backendMock.EXPECT().Find(gomock.Any(), int64(41)).Return(row, nil)
		backendMock.EXPECT().Delete(gomock.Any(), int64(41)).Return(nil)

		_, err := svc.Delete(context.Background(), &DeleteBackendRequest{ID: 41})

		require.NoError(t, err)
		require.Equal(t, []int64{42}, remote.deviceIDs)
		assert.Equal(t, "sync-openclaw-41", remote.tokenReq.GetSyncId())
		assert.True(t, remote.tokenReq.GetClear())
	})

	t.Run("Given the bound device is unreachable when deleted then the delete still succeeds", func(t *testing.T) {
		_, backendMock, _, _, _, svc := setupSvcTest(t)
		installRemoteDeviceFixture(t)
		svc.secrets = keychain.NewMemory()
		svc.remoteCredentials = &fakeRemoteCredentials{tokenErr: ErrRemoteDialFailed}
		row := remoteOpenClawRow(42, "sync-openclaw-42")
		backendMock.EXPECT().Find(gomock.Any(), int64(42)).Return(row, nil)
		backendMock.EXPECT().Delete(gomock.Any(), int64(42)).Return(nil)

		_, err := svc.Delete(context.Background(), &DeleteBackendRequest{ID: 42})

		require.NoError(t, err)
	})

	t.Run("Given another backend on the same device uses the same serve then the remote login is kept", func(t *testing.T) {
		_, backendMock, _, _, _, svc := setupSvcTest(t)
		installRemoteDeviceFixture(t)
		remote := &fakeRemoteCredentials{}
		svc.remoteCredentials = remote
		row := remoteHermesRow(43, "http://10.0.0.7:9119")
		backendMock.EXPECT().Find(gomock.Any(), int64(43)).Return(row, nil)
		backendMock.EXPECT().Delete(gomock.Any(), int64(43)).Return(nil)
		backendMock.EXPECT().List(gomock.Any()).Return([]*agent_backend_entity.AgentBackend{
			remoteHermesRow(44, "http://10.0.0.7:9119"),
		}, nil)

		_, err := svc.Delete(context.Background(), &DeleteBackendRequest{ID: 43})

		require.NoError(t, err)
		assert.Zero(t, remote.logoutCall, "同设备上还有后端指向同一个 serve,不能登出")
	})

	t.Run("Given the same serve is only used from another device then the bound device logs out", func(t *testing.T) {
		_, backendMock, _, _, _, svc := setupSvcTest(t)
		installRemoteDeviceFixture(t)
		remote := &fakeRemoteCredentials{}
		svc.remoteCredentials = remote
		row := remoteHermesRow(45, "http://10.0.0.7:9119")
		local := &agent_backend_entity.AgentBackend{
			ID: 46, Type: string(agent_backend_entity.TypeHermes), Name: "local same url",
			HermesURL: "http://10.0.0.7:9119", Status: consts.ACTIVE,
		}
		backendMock.EXPECT().Find(gomock.Any(), int64(45)).Return(row, nil)
		backendMock.EXPECT().Delete(gomock.Any(), int64(45)).Return(nil)
		backendMock.EXPECT().List(gomock.Any()).Return([]*agent_backend_entity.AgentBackend{local}, nil)

		_, err := svc.Delete(context.Background(), &DeleteBackendRequest{ID: 45})

		require.NoError(t, err)
		require.Equal(t, 1, remote.logoutCall)
		assert.Equal(t, "http://10.0.0.7:9119", remote.logoutReq.GetHermesUrl())
	})
}

// TestRebindingDevice_DoesNotCarryCredentials 钉住规格里的换绑语义:凭据留在旧设备,
// 新设备上按自己的存储回答。
func TestRebindingDevice_DoesNotCarryCredentials(t *testing.T) {
	_, _, _, _, _, svc := setupSvcTest(t)
	installRemoteDeviceFixture(t)
	memory := keychain.NewMemory()
	svc.secrets = memory
	require.NoError(t, memory.Set(backendcred.OpenClawTokenAccount("sync-rebind"), strings.Repeat("k", 32)))
	remote := &fakeRemoteCredentials{statusResp: &agentrewire.BackendCredentialStatusResponse{}}
	svc.remoteCredentials = remote

	resp, err := svc.BackendCredentialStatus(context.Background(), &BackendCredentialStatusRequest{
		Type: string(agent_backend_entity.TypeOpenClaw), SyncID: "sync-rebind", DeviceID: remoteTestFingerprint,
	})

	require.NoError(t, err)
	assert.False(t, resp.OpenClawTokenSaved, "换绑到别的设备后不该继承本机的 token")
}

// TestDeviceCredentialsPort_AnswersForBackendsBoundHere 是桌面端作为被调方的那一面:
// 控制台按指纹拨到这台桌面端时,同样的六个操作要用本机 keychain 答得出来。
func TestDeviceCredentialsPort_AnswersForBackendsBoundHere(t *testing.T) {
	newPort := func(t *testing.T) (*LocalBackendCredentials, keychain.Keychain) {
		t.Helper()
		memory := keychain.NewMemory()
		svc := &agentBackendSvc{
			now:     func() int64 { return 1 },
			secrets: memory,
			hermes:  backendcred.NewHermesCredentials(func() backendcred.Store { return memory }),
			probes:  map[string]context.CancelFunc{},
		}
		return &LocalBackendCredentials{svc: svc}, memory
	}

	t.Run("saving then clearing an OpenClaw token lands in this machine's keychain", func(t *testing.T) {
		port, memory := newPort(t)
		token := strings.Repeat("p", 36)

		saved, err := port.SetOpenClawToken(context.Background(), &agentrewire.OpenClawTokenSetRequest{
			SyncId: "sync-port", Token: token,
		})
		require.NoError(t, err)
		assert.True(t, saved.GetTokenSaved())
		stored, err := memory.Get(backendcred.OpenClawTokenAccount("sync-port"))
		require.NoError(t, err)
		assert.Equal(t, token, stored)

		status, err := port.Status(context.Background(), &agentrewire.BackendCredentialStatusRequest{
			BackendType: string(agent_backend_entity.TypeOpenClaw), SyncId: "sync-port",
		})
		require.NoError(t, err)
		assert.True(t, status.GetOpenclawTokenSaved())
		raw, err := json.Marshal(status)
		require.NoError(t, err)
		assert.NotContains(t, string(raw), token)

		cleared, err := port.SetOpenClawToken(context.Background(), &agentrewire.OpenClawTokenSetRequest{
			SyncId: "sync-port", Clear: true,
		})
		require.NoError(t, err)
		assert.False(t, cleared.GetTokenSaved())
		_, getErr := memory.Get(backendcred.OpenClawTokenAccount("sync-port"))
		assert.ErrorIs(t, getErr, keychain.ErrNotFound)
	})

	t.Run("a blank sync id is rejected instead of sharing one slot", func(t *testing.T) {
		port, _ := newPort(t)

		_, err := port.SetOpenClawToken(context.Background(), &agentrewire.OpenClawTokenSetRequest{Token: "x"})

		require.Error(t, err)
	})

	t.Run("hermes login, status and logout go through this machine's keychain", func(t *testing.T) {
		server := httptest.NewServer(newFakeHermesServe().handler())
		t.Cleanup(server.Close)
		port, memory := newPort(t)

		login, err := port.HermesLogin(context.Background(), &agentrewire.HermesLoginRequest{
			HermesUrl: server.URL, Provider: "basic", Username: "alice", Password: "correct-horse",
		})
		require.NoError(t, err)
		assert.Empty(t, login.GetCode())
		assert.Equal(t, "user-7", login.GetUserId())
		stored, err := memory.Get(backendcred.HermesAccount(server.URL))
		require.NoError(t, err)
		assert.Equal(t, "refresh-1", stored)
		raw, err := json.Marshal(login)
		require.NoError(t, err)
		assert.NotContains(t, string(raw), "correct-horse")
		assert.NotContains(t, string(raw), "refresh-1")

		providers, err := port.HermesAuthProviders(context.Background(), &agentrewire.HermesAuthProvidersRequest{
			HermesUrl: server.URL,
		})
		require.NoError(t, err)
		require.Len(t, providers.GetProviders(), 2)

		_, err = port.HermesLogout(context.Background(), &agentrewire.HermesLogoutRequest{HermesUrl: server.URL})
		require.NoError(t, err)
		_, getErr := memory.Get(backendcred.HermesAccount(server.URL))
		assert.ErrorIs(t, getErr, keychain.ErrNotFound)
	})

	t.Run("a wrong password answers with a structured code instead of an error", func(t *testing.T) {
		server := httptest.NewServer(newFakeHermesServe().handler())
		t.Cleanup(server.Close)
		port, _ := newPort(t)

		login, err := port.HermesLogin(context.Background(), &agentrewire.HermesLoginRequest{
			HermesUrl: server.URL, Provider: "basic", Username: "alice", Password: "wrong",
		})

		require.NoError(t, err)
		assert.Equal(t, HermesCodeInvalidCredentials, login.GetCode())
	})

	t.Run("testing an OpenClaw backend uses the token saved on this machine", func(t *testing.T) {
		port, memory := newPort(t)
		require.NoError(t, memory.Set(backendcred.OpenClawTokenAccount("sync-port-test"), "saved-token"))
		var seenToken string
		port.svc.openClawProbe = fakeOpenClawProbe(&seenToken)

		resp, err := port.TestConnection(context.Background(), &agentrewire.BackendConnectionTestRequest{
			BackendType: string(agent_backend_entity.TypeOpenClaw), SyncId: "sync-port-test",
			OpenclawGatewayUrl: "ws://127.0.0.1:18789/", OpenclawAgentId: "main",
		})

		require.NoError(t, err)
		assert.True(t, resp.GetOk())
		assert.Equal(t, "saved-token", seenToken)
		raw, err := json.Marshal(resp)
		require.NoError(t, err)
		assert.NotContains(t, string(raw), "saved-token")
	})

	t.Run("a failing probe never carries the saved token back out", func(t *testing.T) {
		port, memory := newPort(t)
		token := strings.Repeat("s", 40)
		require.NoError(t, memory.Set(backendcred.OpenClawTokenAccount("sync-port-leak"), token))
		port.svc.openClawProbe = func(_ context.Context, config openclawgateway.Config, _ openclawgateway.ProbeSelection) (*openclawgateway.ProbeResult, error) {
			return nil, errors.New("gateway refused token " + config.Token)
		}

		resp, err := port.TestConnection(context.Background(), &agentrewire.BackendConnectionTestRequest{
			BackendType: string(agent_backend_entity.TypeOpenClaw), SyncId: "sync-port-leak",
			OpenclawGatewayUrl: "ws://127.0.0.1:18789/",
		})

		require.NoError(t, err)
		assert.False(t, resp.GetOk())
		raw, err := json.Marshal(resp)
		require.NoError(t, err)
		assert.NotContains(t, string(raw), token)
	})

	// 「连不上」是规格列出的结构化结果之一(设备操作表「测试连接」),而这台桌面端被
	// 控制台当作绑定设备问到时,答的必须与 agentred 的同族应答是同一个码 —— 不能一边
	// 回 HERMES_UNREACHABLE、一边把 dial 的机器原话当正文递上去。
	t.Run("an unreachable serve answers with the unreachable code, not the dial text", func(t *testing.T) {
		port, _ := newPort(t)
		orig := hermesProbe
		hermesProbe = func(context.Context, hermes.ProbeRequest) (string, error) {
			return "", fmt.Errorf("%w: dial tcp 127.0.0.1:9119: connect: connection refused", hermes.ErrGatewayUnreachable)
		}
		t.Cleanup(func() { hermesProbe = orig })

		resp, err := port.TestConnection(context.Background(), &agentrewire.BackendConnectionTestRequest{
			BackendType: string(agent_backend_entity.TypeHermes), HermesUrl: "http://10.0.0.7:9119",
		})

		require.NoError(t, err)
		assert.False(t, resp.GetOk())
		assert.Equal(t, HermesCodeUnreachable, resp.GetCode())
		assert.NotContains(t, resp.GetMessage(), "dial tcp", "界面不贴机器原话")
	})

	t.Run("a draft token wins over the saved one and is not persisted", func(t *testing.T) {
		port, memory := newPort(t)
		var seenToken string
		port.svc.openClawProbe = fakeOpenClawProbe(&seenToken)

		_, err := port.TestConnection(context.Background(), &agentrewire.BackendConnectionTestRequest{
			BackendType: string(agent_backend_entity.TypeOpenClaw), SyncId: "sync-port-draft",
			OpenclawGatewayUrl: "ws://127.0.0.1:18789/", OpenclawToken: "draft-token",
		})

		require.NoError(t, err)
		assert.Equal(t, "draft-token", seenToken)
		_, getErr := memory.Get(backendcred.OpenClawTokenAccount("sync-port-draft"))
		assert.ErrorIs(t, getErr, keychain.ErrNotFound)
	})
}

// fakeOpenClawProbe 记下探测时用的 token,跳过真实 Gateway。
func fakeOpenClawProbe(seen *string) openClawProbeFunc {
	return func(_ context.Context, config openclawgateway.Config, _ openclawgateway.ProbeSelection) (*openclawgateway.ProbeResult, error) {
		*seen = config.Token
		return successfulOpenClawProbeResult(), nil
	}
}
