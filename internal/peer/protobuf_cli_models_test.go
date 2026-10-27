package peer

import (
	"context"
	"errors"
	"testing"

	. "github.com/smartystreets/goconvey/convey"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

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
	Convey("Given the desktop production registry reached via relay, When querying CLI models, Then only authenticated callers reach its local runtime and errors keep their text", t, func() {
		q := mock_agentruntime.NewMockCLIModelQuerier(gomock.NewController(t))
		bt := agent_backend_entity.BackendType("test-cli-models")
		defer agentruntime.SwapRuntimeForTest(bt, cliModelRuntime{CLIModelQuerier: q})()
		q.EXPECT().QueryCLIModels(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, req agentruntime.CLIModelQueryRequest) (agentruntime.CLIModelCatalog, error) {
			require.Equal(t, "/desktop/cli", req.Backend.CLIPath)
			require.Equal(t, `{"X":"desktop"}`, req.Backend.EnvJSON)
			return agentruntime.CLIModelCatalog{EffectiveValue: "desktop", EffectiveModelID: "desktop-id"}, nil
		})
		q.EXPECT().QueryCLIModels(gomock.Any(), gomock.Any()).Return(agentruntime.CLIModelCatalog{}, errors.New("original desktop CLI error"))
		reg := NewProtobufInboundRegistry(productionProtobufInboundDeps(nil))
		a, b := peerProtoPipePair()
		client, server := protorpc.NewConn(a, protorpc.NewRegistry()), protorpc.NewConn(b, reg)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		go client.Serve(ctx)
		go server.Serve(ctx)
		req := &agentrewire.CLIModelsListRequest{BackendType: string(bt), CliPath: "/desktop/cli", EnvJson: `{"X":"desktop"}`}
		_, err := wirecall.CLIModelsList(ctx, wirecall.On(client), req)
		var rpcErr *protorpc.Error
		require.ErrorAs(t, err, &rpcErr)
		require.Equal(t, int32(-32001), rpcErr.Code)
		server.SetAuth(protorpc.AuthState{Authenticated: true})
		got, err := wirecall.CLIModelsList(ctx, wirecall.On(client), req)
		require.NoError(t, err)
		require.Equal(t, "desktop-id", got.EffectiveModelId)
		_, err = wirecall.CLIModelsList(ctx, wirecall.On(client), req)
		require.ErrorAs(t, err, &rpcErr)
		require.Equal(t, "original desktop CLI error", rpcErr.Message)
	})
}
