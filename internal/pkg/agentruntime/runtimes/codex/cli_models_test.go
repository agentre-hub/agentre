package codex

import (
	"context"
	"errors"
	"testing"
	"time"

	. "github.com/smartystreets/goconvey/convey"
	"github.com/stretchr/testify/require"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
	"github.com/agentre-hub/agentre/pkg/codex"
)

func TestQueryCLIModels(t *testing.T) {
	Convey("Given a backend with a fixed model, a supplier binding and custom environment", t, func() {
		backend := &agent_backend_entity.AgentBackend{
			Type:           string(agent_backend_entity.TypeCodex),
			CLIPath:        "/fake/codex",
			DefaultModel:   "fixed-model",
			LLMProviderKey: "ignored-provider",
			EnvJSON:        `{"CODEX_HOME":"/tmp/codex-home"}`,
		}
		var seen *codex.Client
		r := NewWithPool(nil)
		r.queryModels = func(_ context.Context, c *codex.Client) (codex.ModelCatalog, error) {
			seen = c
			return codex.ModelCatalog{
				Models: []codex.Model{
					{ID: "gpt-6-luna", Model: "gpt-6-luna", DisplayName: "GPT-6 Luna", IsDefault: true},
					{ID: "gpt-5.5", Model: "gpt-5.5", DisplayName: "GPT-5.5"},
				},
				EffectiveModel: "gpt-6-luna",
			}, nil
		}

		got, err := r.QueryCLIModels(context.Background(), agentruntime.CLIModelQueryRequest{Backend: backend})

		Convey("Then a fresh login-state client answers: same path/env, never a model override", func() {
			So(err, ShouldBeNil)
			require.NotNil(t, seen)
			So(seen.Binary(), ShouldEqual, "/fake/codex")
			So(seen.Model(), ShouldBeEmpty)
			So(seen.Env(), ShouldResemble, map[string]string{"CODEX_HOME": "/tmp/codex-home"})
			So(seen.Config(), ShouldBeEmpty)
		})
		Convey("Then the CLI values and the effective model from config/read are kept apart", func() {
			So(got.Models, ShouldHaveLength, 2)
			So(got.Models[0].Value, ShouldEqual, "gpt-6-luna")
			So(got.Models[0].DisplayName, ShouldEqual, "GPT-6 Luna")
			So(got.Models[0].ModelID, ShouldEqual, "gpt-6-luna")
			So(got.EffectiveValue, ShouldEqual, "gpt-6-luna")
			So(got.EffectiveModelID, ShouldEqual, "gpt-6-luna")
		})
		Convey("Then the backend itself is not mutated by the query", func() {
			So(backend.DefaultModel, ShouldEqual, "fixed-model")
			So(backend.LLMProviderKey, ShouldEqual, "ignored-provider")
		})
	})

	Convey("Given model/list also carries a resolved model id", t, func() {
		r := NewWithPool(nil)
		r.queryModels = func(context.Context, *codex.Client) (codex.ModelCatalog, error) {
			return codex.ModelCatalog{Models: []codex.Model{{ID: "gpt-6-luna", Model: "gpt-6-luna-2026"}}}, nil
		}

		got, err := r.QueryCLIModels(context.Background(), agentruntime.CLIModelQueryRequest{
			Backend: &agent_backend_entity.AgentBackend{Type: string(agent_backend_entity.TypeCodex)},
		})

		Convey("Then the value stays the CLI id while the model id is reported separately", func() {
			So(err, ShouldBeNil)
			So(got.Models[0].Value, ShouldEqual, "gpt-6-luna")
			So(got.Models[0].ModelID, ShouldEqual, "gpt-6-luna-2026")
			So(got.EffectiveValue, ShouldBeEmpty)
			So(got.EffectiveModelID, ShouldBeEmpty)
		})
	})

	Convey("Given a failed CLI query, When requesting the catalog, Then the original error stands", t, func() {
		r := NewWithPool(nil)
		r.queryModels = func(context.Context, *codex.Client) (codex.ModelCatalog, error) {
			return codex.ModelCatalog{}, errors.New("method not found: model/list")
		}

		_, err := r.QueryCLIModels(context.Background(), agentruntime.CLIModelQueryRequest{
			Backend: &agent_backend_entity.AgentBackend{Type: string(agent_backend_entity.TypeCodex)},
		})

		So(err, ShouldNotBeNil)
		So(err.Error(), ShouldContainSubstring, "method not found: model/list")
	})

	Convey("Given no backend, When requesting the catalog, Then it is rejected", t, func() {
		_, err := NewWithPool(nil).QueryCLIModels(context.Background(), agentruntime.CLIModelQueryRequest{})
		So(err, ShouldNotBeNil)
	})

	Convey("Given a caller without a deadline (a turn resolving the CLI default before turn/start)", t, func() {
		r := NewWithPool(nil)
		var bounded bool
		r.queryModels = func(ctx context.Context, _ *codex.Client) (codex.ModelCatalog, error) {
			deadline, ok := ctx.Deadline()
			bounded = ok && time.Until(deadline) <= agentruntime.CLIModelQueryTimeout
			return codex.ModelCatalog{}, nil
		}

		_, err := r.QueryCLIModels(context.Background(), agentruntime.CLIModelQueryRequest{
			Backend: &agent_backend_entity.AgentBackend{Type: string(agent_backend_entity.TypeCodex)},
		})

		Convey("Then the query app-server runs under a bounded context", func() {
			So(err, ShouldBeNil)
			So(bounded, ShouldBeTrue)
		})
	})
}
