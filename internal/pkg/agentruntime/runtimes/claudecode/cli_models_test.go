package claudecode

import (
	"context"
	"errors"
	"testing"
	"time"

	. "github.com/smartystreets/goconvey/convey"
	"github.com/stretchr/testify/require"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
	"github.com/agentre-hub/agentre/pkg/claudecode"
)

func TestQueryCLIModels(t *testing.T) {
	Convey("Given an unresolved CLI default, When aliases also have no resolved ID, Then no effective value is guessed from the model list", t, func() {
		r := New()
		r.queryModels = func(context.Context, *claudecode.Client) (claudecode.ModelCatalog, error) {
			return claudecode.ModelCatalog{Models: []claudecode.Model{{Value: "sonnet", DisplayName: "Sonnet"}}}, nil
		}
		got, err := r.QueryCLIModels(context.Background(), agentruntime.CLIModelQueryRequest{Backend: &agent_backend_entity.AgentBackend{Type: "claudecode"}})
		So(err, ShouldBeNil)
		So(got.Models, ShouldHaveLength, 1)
		So(got.EffectiveValue, ShouldBeEmpty)
		So(got.EffectiveModelID, ShouldBeEmpty)
	})
	Convey("Given a backend with a fixed model and custom environment, When querying CLI defaults, Then a fresh unbound client uses the same path/env but no model override", t, func() {
		backend := &agent_backend_entity.AgentBackend{Type: "claudecode", CLIPath: "/fake/claude", DefaultModel: "fixed-model", LLMProviderKey: "ignored-provider", EnvJSON: `{"ANTHROPIC_MODEL":"sonnet","CUSTOM":"value"}`}
		r := New()
		r.queryModels = func(ctx context.Context, c *claudecode.Client) (claudecode.ModelCatalog, error) {
			require.Empty(t, c.Model())
			require.Equal(t, "/fake/claude", c.Binary())
			require.Equal(t, map[string]string{"ANTHROPIC_MODEL": "sonnet", "CUSTOM": "value"}, c.Env())
			return claudecode.ModelCatalog{Models: []claudecode.Model{
				{Value: "default", DisplayName: "Default Opus", ResolvedModel: "claude-opus-new"},
				{Value: "sonnet", DisplayName: "Sonnet", ResolvedModel: "claude-sonnet-new"},
				{Value: "claude-sonnet-old", DisplayName: "Old Sonnet", ResolvedModel: "claude-sonnet-old"},
			}, EffectiveModel: "claude-sonnet-new"}, nil
		}
		got, err := r.QueryCLIModels(context.Background(), agentruntime.CLIModelQueryRequest{Backend: backend})
		So(err, ShouldBeNil)
		So(got.EffectiveModelID, ShouldEqual, "claude-sonnet-new")
		So(got.EffectiveValue, ShouldEqual, "sonnet")
		So(got.Models, ShouldHaveLength, 2)
		So(got.Models[0].Value, ShouldEqual, "sonnet")
		So(got.Models[1].Legacy, ShouldBeTrue)
		So(backend.DefaultModel, ShouldEqual, "fixed-model")
	})
	Convey("Given a failed CLI query, When requesting the catalog, Then the error is not replaced with guessed models", t, func() {
		r := New()
		r.queryModels = func(context.Context, *claudecode.Client) (claudecode.ModelCatalog, error) {
			return claudecode.ModelCatalog{}, errors.New("unsupported get_settings")
		}
		_, err := r.QueryCLIModels(context.Background(), agentruntime.CLIModelQueryRequest{Backend: &agent_backend_entity.AgentBackend{Type: "claudecode"}})
		So(err, ShouldNotBeNil)
		So(err.Error(), ShouldContainSubstring, "unsupported get_settings")
	})
	Convey("Given a caller without a deadline, When querying CLI models, Then the short-lived query process runs under a bounded context", t, func() {
		r := New()
		var bounded bool
		r.queryModels = func(ctx context.Context, _ *claudecode.Client) (claudecode.ModelCatalog, error) {
			deadline, ok := ctx.Deadline()
			bounded = ok && time.Until(deadline) <= agentruntime.CLIModelQueryTimeout
			return claudecode.ModelCatalog{}, nil
		}
		_, err := r.QueryCLIModels(context.Background(), agentruntime.CLIModelQueryRequest{Backend: &agent_backend_entity.AgentBackend{Type: "claudecode"}})
		So(err, ShouldBeNil)
		So(bounded, ShouldBeTrue)
	})
}
