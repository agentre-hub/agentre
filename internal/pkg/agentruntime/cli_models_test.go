package agentruntime

import (
	"testing"

	. "github.com/smartystreets/goconvey/convey"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
)

func TestCLIModelChoiceValue(t *testing.T) {
	Convey("Given the three session choices, When resolving the value handed to the CLI, Then fixed uses its value, cli-default is empty and follow-agent uses the trimmed backend default", t, func() {
		backend := &agent_backend_entity.AgentBackend{DefaultModel: " opus "}
		for _, tc := range []struct {
			mode, value, want string
			backend           *agent_backend_entity.AgentBackend
		}{
			{"fixed", " sonnet ", "sonnet", backend},
			{"cli-default", "", "", backend},
			{"", "", "opus", backend},
			{"", "", "", nil},
		} {
			So(RunRequest{CLIModelMode: tc.mode, CLIModel: tc.value, Backend: tc.backend}.CLIModelChoiceValue(), ShouldEqual, tc.want)
		}
	})
}
