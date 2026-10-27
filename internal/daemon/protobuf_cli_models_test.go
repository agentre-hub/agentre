package daemon

import (
	"context"
	"errors"
	"testing"

	. "github.com/smartystreets/goconvey/convey"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/agentre-hub/agentre/internal/daemon/state"
	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/mock_agentruntime"
	"github.com/agentre-hub/agentre/pkg/wire/agentrewire"
	"github.com/agentre-hub/agentre/pkg/wire/protorpc"
	"github.com/agentre-hub/agentre/pkg/wire/wirecall"
)

type cliModelRuntime struct {
	agentruntime.Runtime
	agentruntime.CLIModelQuerier
}

func TestProtobufCLIModelsList(t *testing.T) {
	Convey("Given agentred's production registry, When querying CLI models, Then authentication is required and the local runtime answers with original errors", t, func() {
		q := mock_agentruntime.NewMockCLIModelQuerier(gomock.NewController(t))
		bt := agent_backend_entity.BackendType("test-cli-models")
		defer agentruntime.SwapRuntimeForTest(bt, cliModelRuntime{CLIModelQuerier: q})()
		q.EXPECT().QueryCLIModels(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, req agentruntime.CLIModelQueryRequest) (agentruntime.CLIModelCatalog, error) {
			require.Equal(t, "/device/cli", req.Backend.CLIPath)
			return agentruntime.CLIModelCatalog{EffectiveValue: "remote", EffectiveModelID: "remote-id"}, nil
		})
		q.EXPECT().QueryCLIModels(gomock.Any(), gomock.Any()).Return(agentruntime.CLIModelCatalog{}, errors.New("original daemon CLI error"))
		d := &Daemon{state: state.NewDefault("cli-model-test"), protobufRegistry: protorpc.NewRegistry()}
		d.registerProtobufMethods()
		a, b := protobufTestPipePair()
		client, server := protorpc.NewConn(a, protorpc.NewRegistry()), protorpc.NewConn(b, d.protobufRegistry)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		go client.Serve(ctx)
		go server.Serve(ctx)
		req := &agentrewire.CLIModelsListRequest{BackendType: string(bt), CliPath: "/device/cli"}
		_, err := wirecall.CLIModelsList(ctx, wirecall.On(client), req)
		var rpcErr *protorpc.Error
		require.ErrorAs(t, err, &rpcErr)
		require.Equal(t, int32(-32001), rpcErr.Code)
		server.SetAuth(protorpc.AuthState{Authenticated: true})
		got, err := wirecall.CLIModelsList(ctx, wirecall.On(client), req)
		require.NoError(t, err)
		require.Equal(t, "remote-id", got.EffectiveModelId)
		_, err = wirecall.CLIModelsList(ctx, wirecall.On(client), req)
		require.ErrorAs(t, err, &rpcErr)
		require.Equal(t, "original daemon CLI error", rpcErr.Message)
	})
}
