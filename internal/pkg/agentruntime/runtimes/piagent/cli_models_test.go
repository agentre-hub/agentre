package piagent

import (
	"context"
	"errors"
	"testing"

	. "github.com/smartystreets/goconvey/convey"
	"github.com/stretchr/testify/require"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
	pkgpiagent "github.com/agentre-hub/agentre/pkg/piagent"
)

func TestQueryCLIModels(t *testing.T) {
	Convey("Given a pi software backend with a supplier binding and custom environment", t, func() {
		backend := &agent_backend_entity.AgentBackend{
			Type:           string(agent_backend_entity.TypePiAgent),
			CLIPath:        "/fake/pi",
			DefaultModel:   "zhipu/glm-5.3",
			LLMProviderKey: "ignored-provider",
			EnvJSON:        `{"PI_CODING_AGENT_DIR":"/tmp/pi-home"}`,
		}
		var seen *pkgpiagent.Client
		r := NewWithPool(nil)
		r.queryModels = func(_ context.Context, c *pkgpiagent.Client) (pkgpiagent.ModelCatalog, error) {
			seen = c
			return pkgpiagent.ModelCatalog{
				Models: []pkgpiagent.Model{
					{Provider: "anthropic", ID: "claude-sonnet-5-5", Name: "Claude Sonnet 5.5"},
					{Provider: "zhipu", ID: "glm-5.3", Name: "GLM 5.3"},
				},
				Effective: pkgpiagent.Model{Provider: "zhipu", ID: "glm-5.3", Name: "GLM 5.3"},
			}, nil
		}

		got, err := r.QueryCLIModels(context.Background(), agentruntime.CLIModelQueryRequest{Backend: backend})

		Convey("Then a fresh login-state client answers: same CLI path and env, never a model or session", func() {
			So(err, ShouldBeNil)
			require.NotNil(t, seen)
			So(seen.Binary(), ShouldEqual, "/fake/pi")
			So(seen.Model(), ShouldBeEmpty)
			So(seen.Session(), ShouldBeEmpty)
			So(seen.Env(), ShouldResemble, map[string]string{"PI_OFFLINE": "1", "PI_CODING_AGENT_DIR": "/tmp/pi-home"})
		})
		Convey("Then every model's value is provider/id and grouped by the pi provider", func() {
			So(got.Models, ShouldHaveLength, 2)
			So(got.Models[0].Value, ShouldEqual, "anthropic/claude-sonnet-5-5")
			So(got.Models[0].DisplayName, ShouldEqual, "Claude Sonnet 5.5")
			So(got.Models[0].ModelID, ShouldEqual, "claude-sonnet-5-5")
			So(got.Models[0].Group, ShouldEqual, "anthropic")
			So(got.Models[1].Value, ShouldEqual, "zhipu/glm-5.3")
			So(got.Models[1].Group, ShouldEqual, "zhipu")
		})
		Convey("Then the effective model is get_state's, as provider/id", func() {
			So(got.EffectiveValue, ShouldEqual, "zhipu/glm-5.3")
			So(got.EffectiveModelID, ShouldEqual, "glm-5.3")
		})
		Convey("Then the backend itself is not mutated by the query", func() {
			So(backend.DefaultModel, ShouldEqual, "zhipu/glm-5.3")
			So(backend.LLMProviderKey, ShouldEqual, "ignored-provider")
		})
	})

	Convey("Given get_state reports no model", t, func() {
		r := NewWithPool(nil)
		r.queryModels = func(context.Context, *pkgpiagent.Client) (pkgpiagent.ModelCatalog, error) {
			return pkgpiagent.ModelCatalog{Models: []pkgpiagent.Model{{Provider: "zhipu", ID: "glm-5.3"}}}, nil
		}

		got, err := r.QueryCLIModels(context.Background(), agentruntime.CLIModelQueryRequest{
			Backend: &agent_backend_entity.AgentBackend{Type: string(agent_backend_entity.TypePiAgent)},
		})

		Convey("Then no effective model is guessed from the list", func() {
			So(err, ShouldBeNil)
			So(got.EffectiveValue, ShouldBeEmpty)
			So(got.EffectiveModelID, ShouldBeEmpty)
			So(got.Models[0].DisplayName, ShouldEqual, "glm-5.3")
		})
	})

	Convey("Given a failed CLI query, When requesting the catalog, Then the original error stands", t, func() {
		r := NewWithPool(nil)
		r.queryModels = func(context.Context, *pkgpiagent.Client) (pkgpiagent.ModelCatalog, error) {
			return pkgpiagent.ModelCatalog{}, errors.New("piagent rpc get_available_models failed")
		}

		_, err := r.QueryCLIModels(context.Background(), agentruntime.CLIModelQueryRequest{
			Backend: &agent_backend_entity.AgentBackend{Type: string(agent_backend_entity.TypePiAgent)},
		})

		So(err, ShouldNotBeNil)
		So(err.Error(), ShouldContainSubstring, "get_available_models")
	})

	Convey("Given no backend, When requesting the catalog, Then it is rejected", t, func() {
		_, err := NewWithPool(nil).QueryCLIModels(context.Background(), agentruntime.CLIModelQueryRequest{})
		So(err, ShouldNotBeNil)
	})
}
