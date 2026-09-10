package agent_backend_entity

import (
	"context"
	"testing"

	. "github.com/smartystreets/goconvey/convey"

	"github.com/agentre-hub/agentre/internal/model/entity/llm_provider_entity"
)

func TestPiAgentKind(t *testing.T) {
	Convey("Given pi-agent backend type", t, func() {
		kind := KindFor(TypePiAgent)

		Convey("When resolving kind metadata Then it is a CLI backend that can couple to any of the three provider types", func() {
			So(kind, ShouldNotBeNil)
			So(kind.Type(), ShouldEqual, TypePiAgent)
			So(kind.KnownAliases(), ShouldBeEmpty)
			So(kind.AllowsCLIPath(), ShouldBeTrue)
			So(kind.RequiresProviderModel(), ShouldBeTrue)
			So(kind.ProviderTypeMatch(llm_provider_entity.TypeAnthropic), ShouldBeTrue)
			So(kind.ProviderTypeMatch(llm_provider_entity.TypeOpenAIChat), ShouldBeTrue)
			So(kind.ProviderTypeMatch(llm_provider_entity.TypeOpenAIResponse), ShouldBeTrue)
			So(kind.ProviderTypeMatch(llm_provider_entity.ProviderType("custom")), ShouldBeFalse)
		})

		Convey("When validating extra fields Then LLMProviderKey is allowed and codex-only / claudecode-only fields are rejected", func() {
			ctx := context.Background()
			So(kind.ValidateExtra(ctx, &AgentBackend{Type: string(TypePiAgent), Name: "pi", ModelRoutes: "{}", EnvJSON: "{}"}), ShouldBeNil)
			So(kind.ValidateExtra(ctx, &AgentBackend{Type: string(TypePiAgent), Name: "pi", LLMProviderKey: "key-1", ModelRoutes: "{}", EnvJSON: "{}"}), ShouldBeNil)
			So(kind.ValidateExtra(ctx, &AgentBackend{Type: string(TypePiAgent), Name: "pi", Sandbox: "read-only", ModelRoutes: "{}", EnvJSON: "{}"}), ShouldNotBeNil)
			So(kind.ValidateExtra(ctx, &AgentBackend{Type: string(TypePiAgent), Name: "pi", Approval: "never", ModelRoutes: "{}", EnvJSON: "{}"}), ShouldNotBeNil)
			So(kind.ValidateExtra(ctx, &AgentBackend{Type: string(TypePiAgent), Name: "pi", DefaultPermissionMode: "plan", ModelRoutes: "{}", EnvJSON: "{}"}), ShouldNotBeNil)
			So(kind.ValidateExtra(ctx, &AgentBackend{Type: string(TypePiAgent), Name: "pi", DefaultModel: "gpt-5", ModelRoutes: "{}", EnvJSON: "{}"}), ShouldNotBeNil)
		})
	})
}
