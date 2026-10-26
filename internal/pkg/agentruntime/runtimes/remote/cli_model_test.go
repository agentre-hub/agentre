package remote

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/smartystreets/goconvey/convey"
	"github.com/stretchr/testify/require"

	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/runtimes/remote/wire"
	"github.com/agentre-hub/agentre/pkg/wire/agentrewire"
	"github.com/agentre-hub/agentre/pkg/wire/protorpc"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/runtimes/remote/protowire"
)

func TestCLIModelRunTransport(t *testing.T) {
	convey.Convey("Given a session CLI choice, when building a remote run, then the choice survives the wire", t, func() {
		for _, choice := range [][2]string{{"fixed", "sonnet"}, {"cli-default", ""}, {"", ""}} {
			params, err := New(newFakeConn(), WithConversationIDResolver(convOf)).buildRunParams(agentruntime.RunRequest{
				Backend: &agent_backend_entity.AgentBackend{Type: "claudecode", DefaultModel: "opus"}, SessionID: 6,
				CLIModelMode: choice[0], CLIModel: choice[1],
			})
			require.NoError(t, err)
			raw, err := json.Marshal(params)
			require.NoError(t, err)
			var fields map[string]any
			require.NoError(t, json.Unmarshal(raw, &fields))
			if choice[0] != "" {
				require.Equal(t, choice[0], fields["cliModelMode"])
			}
			if choice[1] != "" {
				require.Equal(t, choice[1], fields["cliModel"])
			}
			pb, err := protowire.RunRequestToProto(params)
			require.NoError(t, err)
			decoded, err := protowire.RunRequestFromProto(pb)
			require.NoError(t, err)
			require.Equal(t, params, decoded)
		}
	})
}

func TestCLIModelSetterTransport(t *testing.T) {
	convey.Convey("Given an attached remote session, when switching or resetting, then the RPC preserves value and original rejection", t, func() {
		a, b := budgetPipePair()
		registry := protorpc.NewRegistry()
		protorpc.RegisterMethod(registry, uint32(agentrewire.RpcMethod_RPC_METHOD_RUNTIME_RUN), func() *agentrewire.RuntimeRunRequest { return &agentrewire.RuntimeRunRequest{} }, func(_ context.Context, req *agentrewire.RuntimeRunRequest) (*agentrewire.RuntimeRunResponse, error) {
			return &agentrewire.RuntimeRunResponse{ConversationId: req.ConversationId}, nil
		})
		requests := make(chan *agentrewire.RuntimeSetCLIModelRequest, 3)
		rejected := errors.New("Model 'bad' not found")
		protorpc.RegisterMethod(registry, uint32(agentrewire.RpcMethod_RPC_METHOD_RUNTIME_SET_CLI_MODEL), func() *agentrewire.RuntimeSetCLIModelRequest { return &agentrewire.RuntimeSetCLIModelRequest{} }, func(_ context.Context, req *agentrewire.RuntimeSetCLIModelRequest) (*agentrewire.Empty, error) {
			if req.ConversationId == convOf(99) {
				return nil, &protorpc.Error{Code: wire.ErrCodeNoActiveTurn, Message: "no active turn"}
			}
			requests <- req
			if req.Value == "bad" {
				return nil, &protorpc.Error{Code: -32000, Message: rejected.Error()}
			}
			return &agentrewire.Empty{}, nil
		})
		client, server := protorpc.NewConn(a, protorpc.NewRegistry()), protorpc.NewConn(b, registry)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		defer func() { _ = client.Close() }()
		defer func() { _ = server.Close() }()
		go client.Serve(ctx)
		go server.Serve(ctx)
		rt := New(&budgetConnection{conn: client}, WithConversationIDResolver(convOf))
		setter, ok := any(rt).(agentruntime.CLIModelSetter)
		require.True(t, ok, "remote runtime must implement CLIModelSetter")
		_, _, err := rt.Run(ctx, agentruntime.RunRequest{Backend: &agent_backend_entity.AgentBackend{Type: "claudecode"}, SessionID: 6})
		require.NoError(t, err)
		defer func() { _ = rt.Close() }()
		_, err = rt.handleRunResultDone(ctx, &wire.RunResultDoneFrame{ConversationID: convOf(6)})
		require.NoError(t, err)
		for _, value := range []string{"sonnet", "", "bad"} {
			err := setter.SetCLIModel(ctx, 6, value)
			if value == "bad" {
				require.EqualError(t, err, rejected.Error())
			} else {
				require.NoError(t, err)
			}
			req := <-requests
			require.Equal(t, convOf(6), req.ConversationId)
			require.Equal(t, value, req.Value)
		}
		require.ErrorIs(t, setter.SetCLIModel(ctx, 99, "sonnet"), agentruntime.ErrNoActiveTurn)
	})
}
