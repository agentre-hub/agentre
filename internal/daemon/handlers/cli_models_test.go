package handlers

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
)

func TestCLIModelsList(t *testing.T) {
	Convey("Given a device-local runtime, When listing CLI models, Then launch config and all model fields cross the boundary without a model override", t, func() {
		q := mock_agentruntime.NewMockCLIModelQuerier(gomock.NewController(t))
		h := NewCLIModelsHandlers()
		h.querierFor = func(bt agent_backend_entity.BackendType) agentruntime.CLIModelQuerier {
			require.Equal(t, agent_backend_entity.TypeClaudeCode, bt)
			return q
		}
		q.EXPECT().QueryCLIModels(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, req agentruntime.CLIModelQueryRequest) (agentruntime.CLIModelCatalog, error) {
			require.Equal(t, "/device/claude", req.Backend.CLIPath)
			require.Equal(t, `{"X":"device"}`, req.Backend.EnvJSON)
			require.Equal(t, "bypassPermissions", req.Backend.DefaultPermissionMode)
			require.Equal(t, "high", req.Backend.ReasoningEffort)
			require.Empty(t, req.Backend.DefaultModel)
			require.Empty(t, req.Backend.LLMProviderKey)
			return agentruntime.CLIModelCatalog{Models: []agentruntime.CLIModel{{Value: "sonnet", DisplayName: "Sonnet", ModelID: "claude-sonnet", Group: "anthropic", Legacy: true}}, EffectiveValue: "sonnet", EffectiveModelID: "claude-sonnet"}, nil
		})
		got, err := h.List(context.Background(), &agentrewire.CLIModelsListRequest{BackendType: "claudecode", CliPath: "/device/claude", EnvJson: `{"X":"device"}`, DefaultPermissionMode: "bypassPermissions", ReasoningEffort: "high"})
		require.NoError(t, err)
		So(got.EffectiveValue, ShouldEqual, "sonnet")
		So(got.EffectiveModelId, ShouldEqual, "claude-sonnet")
		require.Len(t, got.Models, 1)
		require.Equal(t, &agentrewire.CLIModel{Value: "sonnet", DisplayName: "Sonnet", ModelId: "claude-sonnet", Group: "anthropic", Legacy: true}, got.Models[0])
	})
	Convey("Given an unsupported or failing runtime, When querying, Then the failure remains an error with the original text", t, func() {
		h := NewCLIModelsHandlers()
		h.querierFor = func(agent_backend_entity.BackendType) agentruntime.CLIModelQuerier { return nil }
		_, err := h.List(context.Background(), &agentrewire.CLIModelsListRequest{BackendType: "unknown"})
		require.ErrorContains(t, err, "not supported")
		q := mock_agentruntime.NewMockCLIModelQuerier(gomock.NewController(t))
		h.querierFor = func(agent_backend_entity.BackendType) agentruntime.CLIModelQuerier { return q }
		q.EXPECT().QueryCLIModels(gomock.Any(), gomock.Any()).Return(agentruntime.CLIModelCatalog{}, errors.New("CLI original failure"))
		_, err = h.List(context.Background(), &agentrewire.CLIModelsListRequest{BackendType: "claudecode"})
		require.EqualError(t, err, "CLI original failure")
	})
}
