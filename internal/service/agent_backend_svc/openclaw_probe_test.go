package agent_backend_svc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/cago-frame/cago/pkg/consts"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/syncmeta_entity"
	"github.com/agentre-hub/agentre/internal/pkg/backendcred"
	"github.com/agentre-hub/agentre/internal/pkg/keychain"
	"github.com/agentre-hub/agentre/internal/pkg/openclawgateway"
	"github.com/agentre-hub/agentre/internal/service/remote_device_svc"
	"github.com/agentre-hub/agentre/internal/service/remote_device_svc/mock_remote_device_svc"
	"github.com/agentre-hub/agentre/pkg/wire/devicefp"
)

func savedOpenClawBackend(id int64) *agent_backend_entity.AgentBackend {
	return &agent_backend_entity.AgentBackend{
		ID:                   id,
		SyncMeta:             syncmeta_entity.SyncMeta{SyncID: fmt.Sprintf("sync-openclaw-%d", id)},
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

// recordingSecretStore records every account written so a test can prove a
// transient secret never reached storage.
type recordingSecretStore struct {
	keychain.Keychain
	written []string
}

func (r *recordingSecretStore) Set(account, secret string) error {
	r.written = append(r.written, account)
	return r.Keychain.Set(account, secret)
}

// countingSecretStore counts reads so a test can prove a credential on another
// device never causes a local keychain lookup.
type countingSecretStore struct {
	keychain.Keychain
	reads int
}

func (c *countingSecretStore) Get(account string) (string, error) {
	c.reads++
	return c.Keychain.Get(account)
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
		require.NoError(t, memory.Set(backendcred.OpenClawTokenAccount(savedOpenClawBackend(93).SyncID), credential))

		ctrl := gomock.NewController(t)
		t.Cleanup(ctrl.Finish)
		rds := mock_remote_device_svc.NewMockRemoteDeviceSvc(ctrl)
		rds.EXPECT().DeviceFingerprint().Return(devicefp.Carrier("sha256:self"), nil).AnyTimes()
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
		require.NoError(t, memory.Set(backendcred.OpenClawTokenAccount(savedOpenClawBackend(91).SyncID), credential))
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
		storedSeed, err := memory.Get(backendcred.OpenClawIdentityAccount)
		require.NoError(t, err)
		assert.NotEmpty(t, storedSeed)

		raw, err := json.Marshal(response)
		require.NoError(t, err)
		assert.NotContains(t, string(raw), credential)
		assert.NotContains(t, string(raw), storedSeed)
	})

	t.Run("Given a transient draft credential when tested then it is not persisted", func(t *testing.T) {
		ctx, _, _, _, _, svc := setupSvcTest(t)
		memory := &recordingSecretStore{Keychain: keychain.NewMemory()}
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
		assert.Equal(t, []string{backendcred.OpenClawIdentityAccount}, memory.written,
			"a draft test may persist only the device identity, never the transient token")
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

	// 绑到别的设备的 OpenClaw 后端由那台设备自己连(规格「设备操作」)。这台机器上
	// 既没有它的 token 也没有它的身份种子,所以连不上那台设备时只能如实说「设备不在」,
	// 而且一次本机凭据读取都不该发生。
	t.Run("Given an OpenClaw backend bound to a device this installation never paired when tested then the reason is readable and no local credential is read", func(t *testing.T) {
		ctx, backendMock, _, _, _, svc := setupSvcTest(t)
		store := &countingSecretStore{Keychain: keychain.NewMemory()}
		svc.secrets = store
		remote := savedOpenClawBackend(93)
		remote.DeviceFingerprint = "sha256:never-paired-device"
		backendMock.EXPECT().Find(gomock.Any(), int64(93)).Return(remote, nil)

		response, err := svc.Test(ctx, &TestBackendRequest{ID: 93})
		require.NoError(t, err)
		assert.False(t, response.OK)
		assert.Empty(t, response.Code)
		assert.NotEmpty(t, response.Message)
		assert.Zero(t, store.reads, "凭据在那台设备上,本机 keychain 不该被读")
	})
}
