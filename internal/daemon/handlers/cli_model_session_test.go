package handlers_test

import (
	"context"
	"errors"
	"testing"

	"github.com/smartystreets/goconvey/convey"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/agentre-hub/agentre/internal/daemon/handlers"
	"github.com/agentre-hub/agentre/internal/daemon/handlers/mock_handlers"
	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/mock_agentruntime"
	"github.com/agentre-hub/agentre/pkg/wire/agentrewire"
	"github.com/agentre-hub/agentre/pkg/wire/devicefp"
	"github.com/agentre-hub/agentre/pkg/wire/rpcerror"
)

type cliSetterRuntime struct {
	*fullRT
	agentruntime.CLIModelSetter
}

func TestRuntimeCLIModel(t *testing.T) {
	convey.Convey("Given a remote CLI choice, when agentred runs it, then its local runtime receives the unmodified choice", t, func() {
		for _, choice := range [][2]string{{"fixed", "sonnet"}, {"cli-default", ""}, {"", ""}} {
			rt := &fullRT{}
			ctx, _, _, _, h := setupRuntimeTest(t, rt)
			_, err := h.Run(ctx, &agentrewire.RuntimeRunRequest{ConversationId: convID(8), Backend: &agentrewire.AgentBackend{Type: "claudecode", DefaultModel: "opus"}, CliModelMode: choice[0], CliModel: choice[1]})
			require.NoError(t, err)
			rt.mu.Lock()
			req := rt.runReqs[0].req
			rt.mu.Unlock()
			require.Equal(t, choice[0], req.CLIModelMode)
			require.Equal(t, choice[1], req.CLIModel)
		}
	})
	convey.Convey("Given an attached local runtime, when switching or resetting via RPC, then its session ID and raw value are forwarded and rejection text survives", t, func() {
		setter := mock_agentruntime.NewMockCLIModelSetter(gomock.NewController(t))
		rt := &cliSetterRuntime{fullRT: &fullRT{}, CLIModelSetter: setter}
		ctx, _, _, _, h := setupRuntimeTest(t, rt)
		h.Adopt(ctx, convID(8), agent_backend_entity.TypeClaudeCode)
		for _, value := range []string{"sonnet", "", "bad"} {
			var rejected error
			if value == "bad" {
				rejected = errors.New("Model 'bad' not found")
			}
			setter.EXPECT().SetCLIModel(gomock.Any(), handlers.RuntimeSessionKey(convID(8)), value).Return(rejected)
			_, err := h.SetCLIModel(ctx, &agentrewire.RuntimeSetCLIModelRequest{ConversationId: convID(8), Value: value})
			require.Equal(t, rejected, err)
		}
		_, err := h.SetCLIModel(ctx, &agentrewire.RuntimeSetCLIModelRequest{ConversationId: convID(99), Value: "sonnet"})
		require.ErrorIs(t, err, agentruntime.ErrNoActiveTurn)
	})
}

func TestSessionCLIModelHandler(t *testing.T) {
	convey.Convey("Given a daemon session, when the console selects or resets CLI model, then all three choices are persisted", t, func() {
		repo := mock_handlers.NewMockSessionCLIModelPort(gomock.NewController(t))
		h := handlers.NewSessionCLIModelHandlers(handlers.SessionCLIModelDeps{Sessions: repo})
		for _, choice := range [][2]string{{"fixed", "sonnet"}, {"cli-default", ""}, {"", ""}} {
			repo.EXPECT().SetCLIModel(gomock.Any(), devicefp.Initiator(""), convID(8), choice[0], choice[1]).Return(int64(1), nil)
			_, err := h.SetCLIModel(context.Background(), &agentrewire.SetSessionCLIModelRequest{ConversationId: convID(8), Mode: choice[0], Value: choice[1]})
			require.NoError(t, err)
		}
		repo.EXPECT().SetCLIModel(gomock.Any(), devicefp.Initiator(""), convID(99), "", "").Return(int64(0), nil)
		_, err := h.SetCLIModel(context.Background(), &agentrewire.SetSessionCLIModelRequest{ConversationId: convID(99)})
		require.ErrorIs(t, err, rpcerror.ErrSessionNotFound)
		rejected := errors.New("disk full")
		repo.EXPECT().SetCLIModel(gomock.Any(), devicefp.Initiator(""), convID(8), "fixed", "sonnet").Return(int64(0), rejected)
		_, err = h.SetCLIModel(context.Background(), &agentrewire.SetSessionCLIModelRequest{ConversationId: convID(8), Mode: "fixed", Value: "sonnet"})
		require.ErrorIs(t, err, rejected)
		for _, req := range []*agentrewire.SetSessionCLIModelRequest{{ConversationId: "bad"}, {ConversationId: convID(8), Mode: "fixed"}, {ConversationId: convID(8), Value: "sonnet"}, {ConversationId: convID(8), Mode: "unknown"}} {
			_, err := h.SetCLIModel(context.Background(), req)
			require.Error(t, err)
		}
	})
}
