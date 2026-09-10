package agent_backend_svc

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/cago-frame/cago/pkg/consts"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-hub/agentre/internal/pkg/keychain"
	"github.com/agentre-hub/agentre/internal/service/remote_device_svc"
	"github.com/agentre-hub/agentre/internal/service/remote_device_svc/mock_remote_device_svc"
)

type failingOpenClawKeychain struct {
	setErr error
}

func (f *failingOpenClawKeychain) Get(string) (string, error) { return "", keychain.ErrNotFound }
func (f *failingOpenClawKeychain) Set(string, string) error   { return f.setErr }
func (f *failingOpenClawKeychain) Delete(string) error        { return nil }

type restoreFailingOpenClawKeychain struct {
	token      string
	restoreErr error
	setCalls   int
}

func (f *restoreFailingOpenClawKeychain) Get(string) (string, error) { return f.token, nil }
func (f *restoreFailingOpenClawKeychain) Delete(string) error        { return nil }
func (f *restoreFailingOpenClawKeychain) Set(string, string) error {
	f.setCalls++
	return f.restoreErr
}

func openClawCreateRequest() *CreateBackendRequest {
	return &CreateBackendRequest{
		Type:                 string(agent_backend_entity.TypeOpenClaw),
		Name:                 "OpenClaw Local",
		OpenClawGatewayURL:   " ws://LOCALHOST:18789/ ",
		OpenClawAgentID:      "main",
		OpenClawDefaultModel: "anthropic/claude-sonnet-4-6",
	}
}

func TestOpenClawBackendSecretLifecycle(t *testing.T) {
	t.Run("Given a transient token when an OpenClaw backend is created then only the keychain receives it", func(t *testing.T) {
		ctx, backendMock, _, _, _, svc := setupSvcTest(t)
		memory := keychain.NewMemory()
		svc.secrets = memory
		credential := strings.Repeat("t", 48)

		backendMock.EXPECT().FindByName(gomock.Any(), "OpenClaw Local").Return(nil, nil)
		backendMock.EXPECT().Create(gomock.Any(), gomock.Any()).DoAndReturn(
			func(_ context.Context, backend *agent_backend_entity.AgentBackend) error {
				require.Equal(t, "ws://localhost:18789/", backend.OpenClawGatewayURL)
				require.Equal(t, agent_backend_entity.OpenClawSessionPerAgentRESession, backend.OpenClawSessionMode)
				backend.ID = 77
				return nil
			},
		)

		response, err := svc.CreateOpenClaw(ctx, openClawCreateRequest(), credential)
		require.NoError(t, err)
		require.NotNil(t, response.Item)
		assert.True(t, response.Item.HasToken)
		stored, err := memory.Get(openClawTokenAccount(77))
		require.NoError(t, err)
		assert.Equal(t, credential, stored)

		raw, err := json.Marshal(response)
		require.NoError(t, err)
		assert.NotContains(t, string(raw), credential)
	})

	t.Run("Given keychain persistence fails when creating then the new database row is soft deleted", func(t *testing.T) {
		ctx, backendMock, _, _, _, svc := setupSvcTest(t)
		svc.secrets = &failingOpenClawKeychain{setErr: errors.New("secret store unavailable")}
		credential := strings.Repeat("f", 48)

		backendMock.EXPECT().FindByName(gomock.Any(), "OpenClaw Local").Return(nil, nil)
		backendMock.EXPECT().Create(gomock.Any(), gomock.Any()).DoAndReturn(
			func(_ context.Context, backend *agent_backend_entity.AgentBackend) error {
				backend.ID = 78
				return nil
			},
		)
		backendMock.EXPECT().Delete(gomock.Any(), int64(78)).Return(nil)

		response, err := svc.CreateOpenClaw(ctx, openClawCreateRequest(), credential)
		assert.Error(t, err)
		assert.Nil(t, response)
	})

	t.Run("Given a saved token when an edit submits an empty token then the existing token is retained", func(t *testing.T) {
		ctx, backendMock, _, _, _, svc := setupSvcTest(t)
		memory := keychain.NewMemory()
		svc.secrets = memory
		credential := strings.Repeat("k", 48)
		require.NoError(t, memory.Set(openClawTokenAccount(79), credential))
		existing := &agent_backend_entity.AgentBackend{
			ID:                  79,
			Type:                string(agent_backend_entity.TypeOpenClaw),
			Name:                "OpenClaw Local",
			ModelRoutes:         "{}",
			EnvJSON:             "{}",
			OpenClawGatewayURL:  "ws://127.0.0.1:18789",
			OpenClawSessionMode: agent_backend_entity.OpenClawSessionPerAgentRESession,
			Status:              consts.ACTIVE,
		}
		backendMock.EXPECT().Find(gomock.Any(), int64(79)).Return(existing, nil)
		backendMock.EXPECT().FindByName(gomock.Any(), "OpenClaw Renamed").Return(nil, nil)
		backendMock.EXPECT().Update(gomock.Any(), gomock.Any()).Return(nil)

		response, err := svc.UpdateOpenClaw(ctx, &UpdateBackendRequest{
			ID:                  79,
			Name:                "OpenClaw Renamed",
			OpenClawGatewayURL:  "ws://127.0.0.1:18789",
			OpenClawSessionMode: agent_backend_entity.OpenClawSessionPerAgentRESession,
		}, "", false)
		require.NoError(t, err)
		assert.True(t, response.Item.HasToken)
		stored, err := memory.Get(openClawTokenAccount(79))
		require.NoError(t, err)
		assert.Equal(t, credential, stored)
	})

	t.Run("Given clearToken when an OpenClaw backend is edited then its token is deleted", func(t *testing.T) {
		ctx, backendMock, _, _, _, svc := setupSvcTest(t)
		memory := keychain.NewMemory()
		svc.secrets = memory
		require.NoError(t, memory.Set(openClawTokenAccount(80), strings.Repeat("d", 48)))
		existing := &agent_backend_entity.AgentBackend{
			ID:                  80,
			Type:                string(agent_backend_entity.TypeOpenClaw),
			Name:                "OpenClaw Local",
			ModelRoutes:         "{}",
			EnvJSON:             "{}",
			OpenClawGatewayURL:  "ws://127.0.0.1:18789",
			OpenClawSessionMode: agent_backend_entity.OpenClawSessionPerAgentRESession,
			Status:              consts.ACTIVE,
		}
		backendMock.EXPECT().Find(gomock.Any(), int64(80)).Return(existing, nil)
		backendMock.EXPECT().Update(gomock.Any(), gomock.Any()).Return(nil)

		response, err := svc.UpdateOpenClaw(ctx, &UpdateBackendRequest{
			ID:                  80,
			Name:                "OpenClaw Local",
			OpenClawGatewayURL:  "ws://127.0.0.1:18789",
			OpenClawSessionMode: agent_backend_entity.OpenClawSessionPerAgentRESession,
		}, "", true)
		require.NoError(t, err)
		assert.False(t, response.Item.HasToken)
		_, err = memory.Get(openClawTokenAccount(80))
		assert.ErrorIs(t, err, keychain.ErrNotFound)
	})

	t.Run("Given a saved OpenClaw backend when deleted then its keychain token is removed", func(t *testing.T) {
		ctx, backendMock, _, _, _, svc := setupSvcTest(t)
		memory := keychain.NewMemory()
		svc.secrets = memory
		require.NoError(t, memory.Set(openClawTokenAccount(81), strings.Repeat("x", 48)))
		backendMock.EXPECT().Find(gomock.Any(), int64(81)).Return(&agent_backend_entity.AgentBackend{
			ID: 81, Type: string(agent_backend_entity.TypeOpenClaw), Status: consts.ACTIVE,
		}, nil)
		backendMock.EXPECT().Delete(gomock.Any(), int64(81)).Return(nil)

		_, err := svc.Delete(ctx, &DeleteBackendRequest{ID: 81})
		require.NoError(t, err)
		_, err = memory.Get(openClawTokenAccount(81))
		assert.ErrorIs(t, err, keychain.ErrNotFound)
	})

	t.Run("Given database deletion fails and token restoration also fails then both errors are returned", func(t *testing.T) {
		ctx, backendMock, _, _, _, svc := setupSvcTest(t)
		databaseErr := errors.New("database delete failed")
		restoreErr := errors.New("token restore failed")
		store := &restoreFailingOpenClawKeychain{
			token:      strings.Repeat("z", 48),
			restoreErr: restoreErr,
		}
		svc.secrets = store
		backendMock.EXPECT().Find(gomock.Any(), int64(82)).Return(&agent_backend_entity.AgentBackend{
			ID: 82, Type: string(agent_backend_entity.TypeOpenClaw), Status: consts.ACTIVE,
		}, nil)
		backendMock.EXPECT().Delete(gomock.Any(), int64(82)).Return(databaseErr)

		_, err := svc.Delete(ctx, &DeleteBackendRequest{ID: 82})
		assert.ErrorIs(t, err, databaseErr)
		assert.ErrorIs(t, err, restoreErr)
		assert.Equal(t, 1, store.setCalls)
	})
}

func TestOpenClawTokenIsNotPartOfWailsDTOs(t *testing.T) {
	for _, typ := range []reflect.Type{
		reflect.TypeOf(BackendItem{}),
		reflect.TypeOf(CreateBackendRequest{}),
		reflect.TypeOf(UpdateBackendRequest{}),
		reflect.TypeOf(TestBackendRequest{}),
		reflect.TypeOf(TestBackendResponse{}),
	} {
		for i := 0; i < typ.NumField(); i++ {
			field := typ.Field(i)
			searchable := strings.ToLower(field.Name + " " + field.Tag.Get("json"))
			if typ == reflect.TypeOf(BackendItem{}) && field.Name == "HasToken" {
				assert.Equal(t, reflect.Bool, field.Type.Kind())
				continue
			}
			assert.NotContains(t, searchable, "token", "%s must not expose a token field", typ.Name())
			assert.NotContains(t, searchable, "secret", "%s must not expose a secret field", typ.Name())
		}
	}
}

func TestResolveOpenClawRuntimeConfig(t *testing.T) {
	t.Run("Given a self-fingerprint OpenClaw backend when a turn starts then local config resolves instead of reporting remote secret unavailable", func(t *testing.T) {
		ctx, backendMock, _, _, _, svc := setupSvcTest(t)
		memory := keychain.NewMemory()
		svc.secrets = memory
		credential := strings.Repeat("s", 45)
		require.NoError(t, memory.Set(openClawTokenAccount(99), credential))

		ctrl := gomock.NewController(t)
		t.Cleanup(ctrl.Finish)
		rds := mock_remote_device_svc.NewMockRemoteDeviceSvc(ctrl)
		rds.EXPECT().DeviceFingerprint().Return("sha256:self", nil).AnyTimes()
		prevSvc := remote_device_svc.Default()
		remote_device_svc.SetDefault(rds)
		t.Cleanup(func() { remote_device_svc.SetDefault(prevSvc) })

		selfBackend := savedOpenClawBackend(99)
		selfBackend.DeviceFingerprint = "sha256:self"
		backendMock.EXPECT().Find(gomock.Any(), int64(99)).Return(selfBackend, nil)

		config, err := svc.resolveOpenClawRuntimeConfig(ctx, 99)
		require.NoError(t, err)
		assert.Equal(t, "ws://127.0.0.1:18789", config.URL)
		assert.Equal(t, credential, config.Token)
		require.NotNil(t, config.Identity)
	})

	t.Run("Given a saved local OpenClaw backend when a turn starts then runtime config receives the keychain token and stable identity", func(t *testing.T) {
		ctx, backendMock, _, _, _, svc := setupSvcTest(t)
		memory := keychain.NewMemory()
		svc.secrets = memory
		credential := strings.Repeat("r", 48)
		require.NoError(t, memory.Set(openClawTokenAccount(96), credential))
		backendMock.EXPECT().Find(gomock.Any(), int64(96)).Return(savedOpenClawBackend(96), nil)

		config, err := svc.resolveOpenClawRuntimeConfig(ctx, 96)
		require.NoError(t, err)
		assert.Equal(t, "ws://127.0.0.1:18789", config.URL)
		assert.Equal(t, credential, config.Token)
		require.NotNil(t, config.Identity)
		firstIdentity := config.Identity.ID()

		backendMock.EXPECT().Find(gomock.Any(), int64(96)).Return(savedOpenClawBackend(96), nil)
		config, err = svc.resolveOpenClawRuntimeConfig(ctx, 96)
		require.NoError(t, err)
		assert.Equal(t, firstIdentity, config.Identity.ID())
	})

	t.Run("Given a remote OpenClaw backend when a turn starts then it is rejected before a token can cross daemon wire", func(t *testing.T) {
		ctx, backendMock, _, _, _, svc := setupSvcTest(t)
		svc.secrets = keychain.NewMemory()
		remote := savedOpenClawBackend(97)
		remote.DeviceFingerprint = "7"
		backendMock.EXPECT().Find(gomock.Any(), int64(97)).Return(remote, nil)

		config, err := svc.resolveOpenClawRuntimeConfig(ctx, 97)
		assert.ErrorIs(t, err, ErrOpenClawRemoteSecretUnavailable)
		assert.Empty(t, config.Token)
	})
}
