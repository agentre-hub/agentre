package agent_backend_svc

import (
	"context"
	"errors"
	"testing"

	"github.com/cago-frame/cago/pkg/consts"
	. "github.com/smartystreets/goconvey/convey"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/syncmeta_entity"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/mock_agentruntime"
	"github.com/agentre-hub/agentre/pkg/wire/devicefp"
)

func TestListCLIModels(t *testing.T) {
	Convey("Given a local backend, When querying repeatedly, Then cache is isolated by backend and invalidated on refresh or config change", t, func() {
		ctx, repo, _, _, _, svc := setupSvcTest(t)
		querier := mock_agentruntime.NewMockCLIModelQuerier(gomock.NewController(t))
		svc.cliModelQuerier = querier
		backend := &agent_backend_entity.AgentBackend{ID: 7, Type: "claudecode", Status: consts.ACTIVE, EnvJSON: "{}"}
		repo.EXPECT().Find(ctx, int64(7)).DoAndReturn(func(context.Context, int64) (*agent_backend_entity.AgentBackend, error) {
			b := *backend
			return &b, nil
		}).AnyTimes()
		querier.EXPECT().QueryCLIModels(gomock.Any(), gomock.Any()).DoAndReturn(func(ctx context.Context, req agentruntime.CLIModelQueryRequest) (agentruntime.CLIModelCatalog, error) {
			require.NotNil(t, req.Backend)
			_, hasDeadline := ctx.Deadline()
			require.True(t, hasDeadline)
			return agentruntime.CLIModelCatalog{Models: []agentruntime.CLIModel{{Value: "sonnet", ModelID: "sonnet-id"}}, EffectiveValue: "sonnet", EffectiveModelID: "sonnet-id"}, nil
		}).Times(3)
		req := &ListCLIModelsRequest{BackendID: 7}
		first, err := svc.ListCLIModels(ctx, req)
		require.NoError(t, err)
		first.Models[0].Value = "must-not-poison-cache"
		cached, err := svc.ListCLIModels(ctx, req)
		require.NoError(t, err)
		So(cached.Models[0].Value, ShouldEqual, "sonnet")
		_, err = svc.ListCLIModels(ctx, &ListCLIModelsRequest{BackendID: 7, Refresh: true})
		require.NoError(t, err)
		backend.EnvJSON = `{"ANTHROPIC_MODEL":"opus"}`
		changed, err := svc.ListCLIModels(ctx, req)
		require.NoError(t, err)
		So(changed.EffectiveModelID, ShouldEqual, "sonnet-id")
	})
	Convey("Given a failed query, When retrying, Then the original error is returned and not cached", t, func() {
		ctx, repo, _, _, _, svc := setupSvcTest(t)
		querier := mock_agentruntime.NewMockCLIModelQuerier(gomock.NewController(t))
		svc.cliModelQuerier = querier
		repo.EXPECT().Find(ctx, int64(7)).Return(&agent_backend_entity.AgentBackend{ID: 7, Type: "claudecode", Status: consts.ACTIVE}, nil).Times(2)
		querier.EXPECT().QueryCLIModels(gomock.Any(), gomock.Any()).Return(agentruntime.CLIModelCatalog{}, errors.New("unsupported initialize")).Times(2)
		for range 2 {
			got, err := svc.ListCLIModels(ctx, &ListCLIModelsRequest{BackendID: 7})
			require.NoError(t, err)
			So(got.Error, ShouldContainSubstring, "unsupported initialize")
			So(got.Models, ShouldBeEmpty)
		}
	})
	Convey("Given a backend on another machine, When listing models, Then no local CLI is queried", t, func() {
		ctx, repo, _, _, _, svc := setupSvcTest(t)
		svc.cliModelQuerier = mock_agentruntime.NewMockCLIModelQuerier(gomock.NewController(t))
		repo.EXPECT().Find(ctx, int64(7)).Return(&agent_backend_entity.AgentBackend{ID: 7, Type: "claudecode", Status: consts.ACTIVE, DeviceFingerprint: devicefp.Carrier("sha256:other")}, nil)
		got, err := svc.ListCLIModels(ctx, &ListCLIModelsRequest{BackendID: 7})
		require.NoError(t, err)
		So(got.Error, ShouldNotContainSubstring, "not supported yet")
		So(got.Error, ShouldNotBeBlank)
	})
}

func TestListCLIModelsRemote(t *testing.T) {
	Convey("Given a remote backend and its device overlay, When listing or refreshing, Then only the remote querier runs with that device config and failures are not cached", t, func() {
		ctx, repo, _, _, _, svc := setupSvcTest(t)
		svc.cliModelQuerier = mock_agentruntime.NewMockCLIModelQuerier(gomock.NewController(t))
		q := mock_agentruntime.NewMockCLIModelQuerier(gomock.NewController(t))
		svc.remoteCLIModelQuerier = q
		fp := devicefp.Carrier("sha256:remote")
		backend := &agent_backend_entity.AgentBackend{ID: 7, Type: "claudecode", Status: consts.ACTIVE, DeviceFingerprint: fp, CLIPath: "/wrong/local", EnvJSON: `{"X":"remote"}`, SyncMeta: syncmeta_entity.SyncMeta{SyncID: "backend-7"}}
		repo.EXPECT().Find(ctx, int64(7)).Return(backend, nil).Times(4)
		repo.EXPECT().FindCLIOverlay(ctx, "backend-7", fp).Return(&agent_backend_entity.CLIOverlay{CLIPath: "/remote/claude"}, nil).Times(4)
		q.EXPECT().QueryCLIModels(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, req agentruntime.CLIModelQueryRequest) (agentruntime.CLIModelCatalog, error) {
			require.Equal(t, "/remote/claude", req.Backend.CLIPath)
			require.Equal(t, fp, req.Backend.DeviceFingerprint)
			require.Equal(t, backend.EnvJSON, req.Backend.EnvJSON)
			return agentruntime.CLIModelCatalog{Models: []agentruntime.CLIModel{{Value: "remote-only"}}, EffectiveValue: "remote-only", EffectiveModelID: "remote-id"}, nil
		})
		q.EXPECT().QueryCLIModels(gomock.Any(), gomock.Any()).Return(agentruntime.CLIModelCatalog{}, errors.New("remote CLI original failure")).Times(2)
		for range 2 {
			got, err := svc.ListCLIModels(ctx, &ListCLIModelsRequest{BackendID: 7})
			require.NoError(t, err)
			require.Empty(t, got.Error)
			require.Equal(t, "remote-only", got.Models[0].Value)
			require.Equal(t, "remote-id", got.EffectiveModelID)
		}
		for i := range 2 {
			got, err := svc.ListCLIModels(ctx, &ListCLIModelsRequest{BackendID: 7, Refresh: i == 0})
			require.NoError(t, err)
			require.Equal(t, "remote CLI original failure", got.Error)
			require.Empty(t, got.Models)
		}
		require.Equal(t, "/wrong/local", backend.CLIPath)
	})
}

func TestListCLIModelsOverlay(t *testing.T) {
	Convey("Given the local device CLI overlay, When the path changes, Then the next catalog uses that path rather than the legacy row path", t, func() {
		ctx, repo, _, _, rd, _, svc := setupSvcTestWithRemoteDevice(t)
		rd.EXPECT().DeviceFingerprint().Return(devicefp.Carrier("sha256:self"), nil).AnyTimes()
		backend := &agent_backend_entity.AgentBackend{ID: 7, Type: "claudecode", Status: consts.ACTIVE, CLIPath: "/legacy/claude", SyncMeta: syncmeta_entity.SyncMeta{SyncID: "backend-7"}}
		repo.EXPECT().Find(ctx, int64(7)).Return(backend, nil).Times(2)
		path := "/first/claude"
		repo.EXPECT().FindCLIOverlay(ctx, "backend-7", devicefp.Carrier("sha256:self")).DoAndReturn(func(context.Context, string, devicefp.Carrier) (*agent_backend_entity.CLIOverlay, error) {
			return &agent_backend_entity.CLIOverlay{CLIPath: path}, nil
		}).Times(2)
		querier := mock_agentruntime.NewMockCLIModelQuerier(gomock.NewController(t))
		svc.cliModelQuerier = querier
		querier.EXPECT().QueryCLIModels(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, req agentruntime.CLIModelQueryRequest) (agentruntime.CLIModelCatalog, error) {
			require.Equal(t, path, req.Backend.CLIPath)
			return agentruntime.CLIModelCatalog{}, nil
		}).Times(2)
		_, err := svc.ListCLIModels(ctx, &ListCLIModelsRequest{BackendID: 7})
		require.NoError(t, err)
		path = "/second/claude"
		_, err = svc.ListCLIModels(ctx, &ListCLIModelsRequest{BackendID: 7})
		require.NoError(t, err)
	})
}
