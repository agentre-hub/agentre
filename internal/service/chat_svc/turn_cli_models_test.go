package chat_svc

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
	"github.com/agentre-hub/agentre/internal/service/agent_backend_svc"
	"github.com/agentre-hub/agentre/pkg/wire/devicefp"
)

// 「这一轮模型与上次不同」提示要 CLI 模型目录来解析所选别名。会话钉住引擎配置后，本轮
// 后端是配置摊成的视图（ID 为零，不是 agent_backends 的一行），目录只能按（配置, 设备）查。

type fakeCLIModels struct {
	profileReq *agent_backend_svc.ListEngineProfileCLIModelsRequest
	backendReq *agent_backend_svc.ListCLIModelsRequest
	resp       *agent_backend_svc.ListCLIModelsResponse
	err        error
}

func (f *fakeCLIModels) ListCLIModels(_ context.Context, r *agent_backend_svc.ListCLIModelsRequest) (*agent_backend_svc.ListCLIModelsResponse, error) {
	f.backendReq = r
	return f.resp, f.err
}

func (f *fakeCLIModels) ListEngineProfileCLIModels(_ context.Context, r *agent_backend_svc.ListEngineProfileCLIModelsRequest) (*agent_backend_svc.ListCLIModelsResponse, error) {
	f.profileReq = r
	return f.resp, f.err
}

func TestTurnCLIModels_GivenEngineProfileTurn_ThenCatalogIsLookedUpByProfileAndDevice(t *testing.T) {
	src := &fakeCLIModels{resp: &agent_backend_svc.ListCLIModelsResponse{Models: []agentruntime.CLIModel{{Value: "sonnet"}}}}
	run := &turnRun{
		svc: &chatSvc{cliModels: src},
		be:  &agent_backend_entity.AgentBackend{Type: "claudecode", DeviceFingerprint: devicefp.Carrier("sha256:box")},
		req: agentruntime.RunRequest{EngineProfileID: 31},
	}

	models, known := run.cliModelCatalog(context.Background())

	require.True(t, known)
	assert.Equal(t, []agentruntime.CLIModel{{Value: "sonnet"}}, models)
	require.NotNil(t, src.profileReq)
	assert.Equal(t, int64(31), src.profileReq.EngineProfileID)
	assert.Equal(t, devicefp.Carrier("sha256:box"), src.profileReq.Device)
	assert.Nil(t, src.backendReq, "profile turn must not ask by backend id (the view has none)")
}

func TestTurnCLIModels_GivenLegacyBackendTurn_ThenCatalogIsLookedUpByBackendID(t *testing.T) {
	src := &fakeCLIModels{resp: &agent_backend_svc.ListCLIModelsResponse{Models: []agentruntime.CLIModel{{Value: "opus"}}}}
	run := &turnRun{svc: &chatSvc{cliModels: src}, be: &agent_backend_entity.AgentBackend{ID: 7}}

	models, known := run.cliModelCatalog(context.Background())

	require.True(t, known)
	assert.Len(t, models, 1)
	require.NotNil(t, src.backendReq)
	assert.Equal(t, int64(7), src.backendReq.BackendID)
	assert.Nil(t, src.profileReq)
}

func TestTurnCLIModels_GivenLookupFailsOrReportsError_ThenCatalogIsUnknown(t *testing.T) {
	for name, src := range map[string]*fakeCLIModels{
		"error":         {err: errors.New("boom")},
		"catalog error": {resp: &agent_backend_svc.ListCLIModelsResponse{Error: "cli missing"}},
	} {
		t.Run(name, func(t *testing.T) {
			run := &turnRun{svc: &chatSvc{cliModels: src}, be: &agent_backend_entity.AgentBackend{}, req: agentruntime.RunRequest{EngineProfileID: 31}}
			models, known := run.cliModelCatalog(context.Background())
			assert.False(t, known)
			assert.Empty(t, models)
		})
	}
}
