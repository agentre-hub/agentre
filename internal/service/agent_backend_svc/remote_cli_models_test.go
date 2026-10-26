package agent_backend_svc

import (
	"context"
	"testing"

	. "github.com/smartystreets/goconvey/convey"
	"github.com/stretchr/testify/require"

	"github.com/agentre-hub/agentre/pkg/wire/agentrewire"
	"github.com/agentre-hub/agentre/pkg/wire/protorpc"
	"github.com/agentre-hub/agentre/pkg/wire/wirecall"
)

func TestRemoteCLIModelsWire(t *testing.T) {
	Convey("Given binary device RPC frames, When querying models, Then config and every model field round trip and RPC errors surface as original text", t, func() {
		reg := protorpc.NewRegistry()
		request := &agentrewire.CLIModelsListRequest{BackendType: "piagent", CliPath: "/remote/pi", EnvJson: `{"X":"value"}`, DefaultPermissionMode: "plan", ReasoningEffort: "high"}
		failed := false
		protorpc.RegisterMethod(reg, uint32(agentrewire.RpcMethod_RPC_METHOD_CLI_MODELS_LIST), func() *agentrewire.CLIModelsListRequest { return &agentrewire.CLIModelsListRequest{} }, func(_ context.Context, got *agentrewire.CLIModelsListRequest) (*agentrewire.CLIModelsListResponse, error) {
			require.Equal(t, request.BackendType, got.BackendType)
			require.Equal(t, request.CliPath, got.CliPath)
			require.Equal(t, request.EnvJson, got.EnvJson)
			require.Equal(t, request.DefaultPermissionMode, got.DefaultPermissionMode)
			require.Equal(t, request.ReasoningEffort, got.ReasoningEffort)
			if failed {
				return nil, &protorpc.Error{Code: protorpc.CodeInternal, Message: "CLI exact error"}
			}
			return &agentrewire.CLIModelsListResponse{Models: []*agentrewire.CLIModel{{Value: "p/id", DisplayName: "Name", ModelId: "id", Group: "p", Legacy: true}}, EffectiveValue: "p/id", EffectiveModelId: "id"}, nil
		})
		a, b := serviceProtoPair()
		client, server := protorpc.NewConn(a, protorpc.NewRegistry()), protorpc.NewConn(b, reg)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		go client.Serve(ctx)
		go server.Serve(ctx)
		got, err := listRemoteCLIModels(ctx, wirecall.On(client), request)
		require.NoError(t, err)
		require.Len(t, got.Models, 1)
		require.Equal(t, "p/id", got.Models[0].Value)
		require.Equal(t, "Name", got.Models[0].DisplayName)
		require.Equal(t, "id", got.Models[0].ModelID)
		require.Equal(t, "p", got.Models[0].Group)
		require.True(t, got.Models[0].Legacy)
		require.Equal(t, "p/id", got.EffectiveValue)
		require.Equal(t, "id", got.EffectiveModelID)
		failed = true
		_, err = listRemoteCLIModels(ctx, wirecall.On(client), request)
		require.EqualError(t, err, "CLI exact error")
	})
}
