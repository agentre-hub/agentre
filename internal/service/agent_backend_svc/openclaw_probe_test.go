package agent_backend_svc

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/cago-frame/cago/pkg/consts"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-hub/agentre/internal/pkg/keychain"
	"github.com/agentre-hub/agentre/internal/pkg/openclawgateway"
	"github.com/agentre-hub/agentre/internal/service/remote_device_svc"
	"github.com/agentre-hub/agentre/internal/service/remote_device_svc/mock_remote_device_svc"
)

func savedOpenClawBackend(id int64) *agent_backend_entity.AgentBackend {
	return &agent_backend_entity.AgentBackend{
		ID:                   id,
		Type:                 string(agent_backend_entity.TypeOpenClaw),
		Name:                 "OpenClaw Local",
		ModelRoutes:          "{}",
		EnvJSON:              "{}",
		OpenClawGatewayURL:   "ws://127.0.0.1:18789",
		OpenClawAgentID:      "main",
		OpenClawDefaultModel: "anthropic/claude-sonnet-4-6",
		OpenClawSessionMode:  agent_backend_entity.OpenClawSessionPerAgentRESession,
		Status:               consts.ACTIVE,
	}
}

func successfulOpenClawProbeResult() *openclawgateway.ProbeResult {
	return &openclawgateway.ProbeResult{
		GatewayVersion: "2026.7.1-2",
		Protocol:       openclawgateway.ProtocolVersion,
		GrantedScopes:  append([]string(nil), openclawgateway.RequiredOperatorScopes...),
		Methods:        []string{"agent", "agent.wait"},
		Events:         []string{"agent", "chat"},
		Agents: []openclawgateway.AgentSummary{
			{ID: "main", Name: "Main", PrimaryModel: "anthropic/claude-sonnet-4-6", Default: true},
		},
		Models: []openclawgateway.ModelSummary{
			{ID: "anthropic/claude-sonnet-4-6", Name: "Claude Sonnet 4.6", Provider: "anthropic", Available: true},
		},
	}
}

func TestOpenClawBackendProbe(t *testing.T) {
	t.Run("Given a self-fingerprint OpenClaw backend when tested then the local gateway probe runs instead of the remote-secret-unavailable shortcut", func(t *testing.T) {
		ctx, backendMock, _, _, _, svc := setupSvcTest(t)
		memory := keychain.NewMemory()
		svc.secrets = memory
		credential := strings.Repeat("p", 46)
		require.NoError(t, memory.Set(openClawTokenAccount(93), credential))

		ctrl := gomock.NewController(t)
		t.Cleanup(ctrl.Finish)
		rds := mock_remote_device_svc.NewMockRemoteDeviceSvc(ctrl)
		rds.EXPECT().DeviceFingerprint().Return("sha256:self", nil).AnyTimes()
		prevSvc := remote_device_svc.Default()
		remote_device_svc.SetDefault(rds)
		t.Cleanup(func() { remote_device_svc.SetDefault(prevSvc) })

		selfBackend := savedOpenClawBackend(93)
		selfBackend.DeviceFingerprint = "sha256:self"
		backendMock.EXPECT().Find(gomock.Any(), int64(93)).Return(selfBackend, nil)
		svc.openClawProbe = func(_ context.Context, config openclawgateway.Config, _ openclawgateway.ProbeSelection) (*openclawgateway.ProbeResult, error) {
			assert.Equal(t, credential, config.Token)
			return successfulOpenClawProbeResult(), nil
		}

		response, err := svc.Test(ctx, &TestBackendRequest{ID: 93})
		require.NoError(t, err)
		require.True(t, response.OK, "self-fingerprint OpenClaw backend must run the local probe, not OPENCLAW_REMOTE_SECRET_UNAVAILABLE")
		assert.Equal(t, "2026.7.1-2", response.GatewayVersion)
	})

	t.Run("Given a saved local backend when tested then the stored credential and stable device identity are used and discovery is returned", func(t *testing.T) {
		ctx, backendMock, _, _, _, svc := setupSvcTest(t)
		memory := keychain.NewMemory()
		svc.secrets = memory
		credential := strings.Repeat("c", 43)
		require.NoError(t, memory.Set(openClawTokenAccount(91), credential))
		backendMock.EXPECT().Find(gomock.Any(), int64(91)).Return(savedOpenClawBackend(91), nil)

		var identityID string
		svc.openClawProbe = func(_ context.Context, config openclawgateway.Config, selection openclawgateway.ProbeSelection) (*openclawgateway.ProbeResult, error) {
			assert.Equal(t, credential, config.Token)
			require.NotNil(t, config.Identity)
			identityID = config.Identity.ID()
			assert.Equal(t, "main", selection.AgentID)
			assert.Equal(t, "anthropic/claude-sonnet-4-6", selection.Model)
			return successfulOpenClawProbeResult(), nil
		}

		response, err := svc.Test(ctx, &TestBackendRequest{ID: 91})
		require.NoError(t, err)
		require.True(t, response.OK)
		assert.Equal(t, "2026.7.1-2", response.GatewayVersion)
		assert.Equal(t, openclawgateway.ProtocolVersion, response.Protocol)
		assert.Equal(t, openclawgateway.RequiredOperatorScopes, response.GrantedScopes)
		require.Len(t, response.OpenClawAgents, 1)
		assert.Equal(t, "main", response.OpenClawAgents[0].ID)
		require.Len(t, response.OpenClawModels, 1)
		assert.Equal(t, "anthropic/claude-sonnet-4-6", response.OpenClawModels[0].ID)
		assert.NotEmpty(t, identityID)
		storedSeed, err := memory.Get(openClawIdentityAccount)
		require.NoError(t, err)
		assert.NotEmpty(t, storedSeed)

		raw, err := json.Marshal(response)
		require.NoError(t, err)
		assert.NotContains(t, string(raw), credential)
		assert.NotContains(t, string(raw), storedSeed)
	})

	t.Run("Given a transient draft credential when tested then it is not persisted", func(t *testing.T) {
		ctx, _, _, _, _, svc := setupSvcTest(t)
		memory := keychain.NewMemory()
		svc.secrets = memory
		credential := strings.Repeat("d", 47)
		svc.openClawProbe = func(_ context.Context, config openclawgateway.Config, _ openclawgateway.ProbeSelection) (*openclawgateway.ProbeResult, error) {
			assert.Equal(t, credential, config.Token)
			return successfulOpenClawProbeResult(), nil
		}

		response, err := svc.TestOpenClaw(ctx, &TestBackendRequest{
			Type:                string(agent_backend_entity.TypeOpenClaw),
			Name:                "draft",
			OpenClawGatewayURL:  "ws://127.0.0.1:18789",
			OpenClawSessionMode: agent_backend_entity.OpenClawSessionPerAgentRESession,
		}, credential)
		require.NoError(t, err)
		assert.True(t, response.OK)
		_, err = memory.Get(openClawTokenAccount(0))
		assert.ErrorIs(t, err, keychain.ErrNotFound)
	})

	t.Run("Given probe selection validation fails when tested then a structured soft failure is returned", func(t *testing.T) {
		ctx, backendMock, _, _, _, svc := setupSvcTest(t)
		svc.secrets = keychain.NewMemory()
		backendMock.EXPECT().Find(gomock.Any(), int64(92)).Return(savedOpenClawBackend(92), nil)
		svc.openClawProbe = func(context.Context, openclawgateway.Config, openclawgateway.ProbeSelection) (*openclawgateway.ProbeResult, error) {
			return nil, errors.Join(openclawgateway.ErrSelectedModelNotFound, errors.New("missing model"))
		}

		response, err := svc.Test(ctx, &TestBackendRequest{ID: 92})
		require.NoError(t, err)
		assert.False(t, response.OK)
		assert.Equal(t, "OPENCLAW_MODEL_NOT_FOUND", response.Code)
		assert.NotEmpty(t, response.Message)
	})

	t.Run("Given an OpenClaw backend targets agentred when tested then remote execution is explicitly unavailable without sending a credential", func(t *testing.T) {
		ctx, backendMock, _, _, _, svc := setupSvcTest(t)
		svc.secrets = keychain.NewMemory()
		remote := savedOpenClawBackend(93)
		remote.DeviceFingerprint = "7"
		backendMock.EXPECT().Find(gomock.Any(), int64(93)).Return(remote, nil)

		response, err := svc.Test(ctx, &TestBackendRequest{ID: 93})
		require.NoError(t, err)
		assert.False(t, response.OK)
		assert.Equal(t, "OPENCLAW_REMOTE_SECRET_UNAVAILABLE", response.Code)
	})
}
