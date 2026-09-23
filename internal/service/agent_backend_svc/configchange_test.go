package agent_backend_svc

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/cago-frame/cago/pkg/consts"
	"github.com/stretchr/testify/assert"
	"go.uber.org/mock/gomock"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/syncmeta_entity"
	"github.com/agentre-hub/agentre/internal/pkg/keychain"
	"github.com/agentre-hub/agentre/internal/service/sync_svc"
	"github.com/agentre-hub/agentre/pkg/syncwire"
)

// registerConfigChangeSpy 装配 config:changed 的替身 emitter（同 llm_provider_svc 的
// 同名 helper，各服务包互不 import，按约定各自留一份）。
func registerConfigChangeSpy(t *testing.T) *[][]string {
	t.Helper()
	got := &[][]string{}
	sync_svc.SetConfigChangeEmitter(func(kinds []string) {
		*got = append(*got, kinds)
	})
	t.Cleanup(func() { sync_svc.SetConfigChangeEmitter(nil) })
	return got
}

func TestCreateBackend_EmitsConfigChanged(t *testing.T) {
	ctx, backendMock, providerMock, _, _, svc := setupSvcTest(t)
	got := registerConfigChangeSpy(t)

	backendMock.EXPECT().FindByName(gomock.Any(), "默认助手").Return(nil, nil)
	providerMock.EXPECT().FindByKey(gomock.Any(), "key-1").Return(activeProvider("key-1"), nil)
	expectDefaultModelResolution(providerMock, "key-1", 1)
	backendMock.EXPECT().Create(gomock.Any(), gomock.Any()).Return(nil)

	_, err := svc.Create(ctx, &CreateBackendRequest{
		Type: "builtin", Name: "默认助手", LLMProviderKey: "key-1",
	})

	assert.NoError(t, err)
	assert.Equal(t, [][]string{{syncwire.KindAgentBackend}}, *got)
}

func TestCreateBackend_GivenRepoFails_DoesNotEmitConfigChanged(t *testing.T) {
	ctx, backendMock, providerMock, _, _, svc := setupSvcTest(t)
	got := registerConfigChangeSpy(t)

	backendMock.EXPECT().FindByName(gomock.Any(), "默认助手").Return(nil, nil)
	providerMock.EXPECT().FindByKey(gomock.Any(), "key-1").Return(activeProvider("key-1"), nil)
	backendMock.EXPECT().Create(gomock.Any(), gomock.Any()).Return(errors.New("db down"))

	_, err := svc.Create(ctx, &CreateBackendRequest{
		Type: "builtin", Name: "默认助手", LLMProviderKey: "key-1",
	})

	assert.Error(t, err)
	assert.Empty(t, *got)
}

func TestUpdateBackend_EmitsConfigChanged(t *testing.T) {
	ctx, backendMock, _, _, _, svc := setupSvcTest(t)
	got := registerConfigChangeSpy(t)

	existing := &agent_backend_entity.AgentBackend{
		ID: 5, Type: string(agent_backend_entity.TypeClaudeCode), Name: "cc", LLMProviderKey: "key-2", Status: consts.ACTIVE,
	}
	backendMock.EXPECT().Find(gomock.Any(), int64(5)).Return(existing, nil)
	backendMock.EXPECT().Update(gomock.Any(), gomock.Any()).Return(nil)

	_, err := svc.Update(ctx, &UpdateBackendRequest{ID: 5, Name: "cc", LLMProviderKey: ""})

	assert.NoError(t, err)
	assert.Equal(t, [][]string{{syncwire.KindAgentBackend}}, *got)
}

func TestDeleteBackend_EmitsConfigChanged(t *testing.T) {
	ctx, backendMock, _, _, _, svc := setupSvcTest(t)
	got := registerConfigChangeSpy(t)

	backendMock.EXPECT().Find(gomock.Any(), int64(3)).Return(
		&agent_backend_entity.AgentBackend{ID: 3, Status: consts.ACTIVE}, nil,
	)
	backendMock.EXPECT().Delete(gomock.Any(), int64(3)).Return(nil)

	_, err := svc.Delete(ctx, &DeleteBackendRequest{ID: 3})

	assert.NoError(t, err)
	assert.Equal(t, [][]string{{syncwire.KindAgentBackend}}, *got)
}

// CreateOpenClaw / UpdateOpenClaw are separate Wails-bound entry points but both
// delegate straight into the same private create/update helpers that Create/Update
// above already prove emit — this pins that delegation so the OpenClaw entry
// points can't silently drift onto a path that skips the notification.
func TestCreateOpenClawBackend_EmitsConfigChanged(t *testing.T) {
	ctx, backendMock, _, _, _, svc := setupSvcTest(t)
	svc.secrets = keychain.NewMemory()
	got := registerConfigChangeSpy(t)

	backendMock.EXPECT().FindByName(gomock.Any(), "OpenClaw Local").Return(nil, nil)
	backendMock.EXPECT().Create(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, backend *agent_backend_entity.AgentBackend) error {
			backend.ID = 90
			backend.SyncID = "sync-openclaw-90"
			return nil
		},
	)

	_, err := svc.CreateOpenClaw(ctx, openClawCreateRequest(), strings.Repeat("t", 48))

	assert.NoError(t, err)
	assert.Equal(t, [][]string{{syncwire.KindAgentBackend}}, *got)
}

func TestCreateOpenClawBackend_GivenRepoFails_DoesNotEmitConfigChanged(t *testing.T) {
	ctx, backendMock, _, _, _, svc := setupSvcTest(t)
	got := registerConfigChangeSpy(t)

	backendMock.EXPECT().FindByName(gomock.Any(), "OpenClaw Local").Return(nil, nil)
	backendMock.EXPECT().Create(gomock.Any(), gomock.Any()).Return(errors.New("db down"))

	_, err := svc.CreateOpenClaw(ctx, openClawCreateRequest(), strings.Repeat("t", 48))

	assert.Error(t, err)
	assert.Empty(t, *got)
}

func TestUpdateOpenClawBackend_EmitsConfigChanged(t *testing.T) {
	ctx, backendMock, _, _, _, svc := setupSvcTest(t)
	svc.secrets = keychain.NewMemory()
	got := registerConfigChangeSpy(t)

	existing := &agent_backend_entity.AgentBackend{
		ID:                  91,
		SyncMeta:            syncmeta_entity.SyncMeta{SyncID: "sync-openclaw-91"},
		Type:                string(agent_backend_entity.TypeOpenClaw),
		Name:                "OpenClaw Local",
		ModelRoutes:         "{}",
		EnvJSON:             "{}",
		OpenClawGatewayURL:  "ws://127.0.0.1:18789",
		OpenClawSessionMode: agent_backend_entity.OpenClawSessionPerAgentRESession,
		Status:              consts.ACTIVE,
	}
	backendMock.EXPECT().Find(gomock.Any(), int64(91)).Return(existing, nil)
	backendMock.EXPECT().Update(gomock.Any(), gomock.Any()).Return(nil)

	_, err := svc.UpdateOpenClaw(ctx, &UpdateBackendRequest{
		ID:                  91,
		Name:                "OpenClaw Local",
		OpenClawGatewayURL:  "ws://127.0.0.1:18789",
		OpenClawSessionMode: agent_backend_entity.OpenClawSessionPerAgentRESession,
	}, "", true)

	assert.NoError(t, err)
	assert.Equal(t, [][]string{{syncwire.KindAgentBackend}}, *got)
}

func TestUpdateOpenClawBackend_GivenRepoFails_DoesNotEmitConfigChanged(t *testing.T) {
	ctx, backendMock, _, _, _, svc := setupSvcTest(t)
	got := registerConfigChangeSpy(t)

	existing := &agent_backend_entity.AgentBackend{
		ID:                  92,
		Type:                string(agent_backend_entity.TypeOpenClaw),
		Name:                "OpenClaw Local",
		ModelRoutes:         "{}",
		EnvJSON:             "{}",
		OpenClawGatewayURL:  "ws://127.0.0.1:18789",
		OpenClawSessionMode: agent_backend_entity.OpenClawSessionPerAgentRESession,
		Status:              consts.ACTIVE,
	}
	backendMock.EXPECT().Find(gomock.Any(), int64(92)).Return(existing, nil)
	backendMock.EXPECT().Update(gomock.Any(), gomock.Any()).Return(errors.New("db down"))

	_, err := svc.UpdateOpenClaw(ctx, &UpdateBackendRequest{
		ID:                  92,
		Name:                "OpenClaw Local",
		OpenClawGatewayURL:  "ws://127.0.0.1:18789",
		OpenClawSessionMode: agent_backend_entity.OpenClawSessionPerAgentRESession,
	}, "", true)

	assert.Error(t, err)
	assert.Empty(t, *got)
}
