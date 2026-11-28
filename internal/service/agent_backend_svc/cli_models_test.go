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
	"github.com/agentre-hub/agentre/internal/model/entity/device_pref_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/syncmeta_entity"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/mock_agentruntime"
	"github.com/agentre-hub/agentre/internal/repository/agent_backend_repo"
	"github.com/agentre-hub/agentre/internal/repository/agent_backend_repo/mock_agent_backend_repo"
	"github.com/agentre-hub/agentre/internal/repository/device_pref_repo"
	"github.com/agentre-hub/agentre/internal/repository/device_pref_repo/mock_device_pref_repo"
	"github.com/agentre-hub/agentre/internal/service/remote_device_svc"
	"github.com/agentre-hub/agentre/internal/service/remote_device_svc/mock_remote_device_svc"
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

func TestListEngineProfileCLIModels(t *testing.T) {
	profile := &agent_backend_entity.EngineProfile{ID: 31, Type: "claudecode", Status: consts.ACTIVE, EnvJSON: "{}"}
	setup := func(t *testing.T) (context.Context, *agentBackendSvc, *mock_agent_backend_repo.MockEngineProfileRepo, *mock_device_pref_repo.MockCLIPathRepo, *mock_agentruntime.MockCLIModelQuerier) {
		ctx, _, _, _, _, svc := setupSvcTest(t)
		ctrl := gomock.NewController(t)
		profiles := mock_agent_backend_repo.NewMockEngineProfileRepo(ctrl)
		paths := mock_device_pref_repo.NewMockCLIPathRepo(ctrl)
		prevProfiles, prevPaths := agent_backend_repo.EngineProfile(), device_pref_repo.CLIPath()
		agent_backend_repo.RegisterEngineProfile(profiles)
		device_pref_repo.RegisterCLIPath(paths)
		t.Cleanup(func() {
			agent_backend_repo.RegisterEngineProfile(prevProfiles)
			device_pref_repo.RegisterCLIPath(prevPaths)
		})
		querier := mock_agentruntime.NewMockCLIModelQuerier(ctrl)
		svc.cliModelQuerier = querier
		return ctx, svc, profiles, paths, querier
	}
	Convey("Given an engine profile pinned on this machine, When listing CLI models, Then the catalog is queried with the profile's settings and this device's CLI path", t, func() {
		ctx, svc, profiles, paths, querier := setup(t)
		profiles.EXPECT().FindAnyStatus(ctx, int64(31)).Return(profile, nil)
		paths.EXPECT().Find(ctx, "claudecode", devicefp.Carrier("sha256:self")).Return(&device_pref_entity.CLIPathOverride{CLIPath: "/dev/claude"}, nil)
		querier.EXPECT().QueryCLIModels(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, req agentruntime.CLIModelQueryRequest) (agentruntime.CLIModelCatalog, error) {
			require.Equal(t, "/dev/claude", req.Backend.CLIPath)
			require.Equal(t, "claudecode", req.Backend.Type)
			return agentruntime.CLIModelCatalog{Models: []agentruntime.CLIModel{{Value: "sonnet"}}, EffectiveValue: "sonnet"}, nil
		})
		prev := remote_device_svc.Default()
		rd := mock_remote_device_svc.NewMockRemoteDeviceSvc(gomock.NewController(t))
		rd.EXPECT().DeviceFingerprint().Return(devicefp.Carrier("sha256:self"), nil).AnyTimes()
		remote_device_svc.SetDefault(rd)
		t.Cleanup(func() { remote_device_svc.SetDefault(prev) })
		got, err := svc.ListEngineProfileCLIModels(ctx, &ListEngineProfileCLIModelsRequest{EngineProfileID: 31})
		require.NoError(t, err)
		So(got.Error, ShouldBeEmpty)
		So(got.Models, ShouldHaveLength, 1)
		So(got.EffectiveValue, ShouldEqual, "sonnet")
	})
	Convey("Given a profile pinned on another machine, When listing, Then the remote querier runs with that device's path and no local CLI is touched", t, func() {
		ctx, svc, profiles, paths, _ := setup(t)
		remote := mock_agentruntime.NewMockCLIModelQuerier(gomock.NewController(t))
		svc.remoteCLIModelQuerier = remote
		fp := devicefp.Carrier("sha256:remote")
		profiles.EXPECT().FindAnyStatus(ctx, int64(31)).Return(profile, nil)
		paths.EXPECT().Find(ctx, "claudecode", fp).Return(&device_pref_entity.CLIPathOverride{CLIPath: "/remote/claude"}, nil)
		remote.EXPECT().QueryCLIModels(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, req agentruntime.CLIModelQueryRequest) (agentruntime.CLIModelCatalog, error) {
			require.Equal(t, fp, req.Backend.DeviceFingerprint)
			require.Equal(t, "/remote/claude", req.Backend.CLIPath)
			return agentruntime.CLIModelCatalog{Models: []agentruntime.CLIModel{{Value: "remote-only"}}}, nil
		})
		got, err := svc.ListEngineProfileCLIModels(ctx, &ListEngineProfileCLIModelsRequest{EngineProfileID: 31, Device: fp})
		require.NoError(t, err)
		So(got.Models[0].Value, ShouldEqual, "remote-only")
	})
	Convey("Given a deleted profile a session is still pinned to, When listing, Then it still answers; an unknown id is rejected", t, func() {
		ctx, svc, profiles, paths, querier := setup(t)
		deleted := *profile
		deleted.Status = consts.DELETE
		profiles.EXPECT().FindAnyStatus(ctx, int64(31)).Return(&deleted, nil)
		profiles.EXPECT().FindAnyStatus(ctx, int64(99)).Return(nil, nil)
		paths.EXPECT().Find(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil, nil).AnyTimes()
		querier.EXPECT().QueryCLIModels(gomock.Any(), gomock.Any()).Return(agentruntime.CLIModelCatalog{}, nil)
		_, err := svc.ListEngineProfileCLIModels(ctx, &ListEngineProfileCLIModelsRequest{EngineProfileID: 31})
		require.NoError(t, err)
		_, err = svc.ListEngineProfileCLIModels(ctx, &ListEngineProfileCLIModelsRequest{EngineProfileID: 99})
		So(err, ShouldNotBeNil)
		_, err = svc.ListEngineProfileCLIModels(ctx, &ListEngineProfileCLIModelsRequest{})
		So(err, ShouldNotBeNil)
	})
}
