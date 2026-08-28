package piagentskill

import (
	"context"
	"testing"

	. "github.com/smartystreets/goconvey/convey"

	"github.com/agentre-hub/agentre/internal/pkg/agentskill"
	"github.com/agentre-hub/agentre/pkg/piagent"
)

func TestDiscoverCommands(t *testing.T) {
	Convey("Given Pi RPC commands from multiple sources", t, func() {
		var gotBinary, gotCwd string
		d := Discoverer{listCommands: func(_ context.Context, binary, cwd string) ([]piagent.Command, error) {
			gotBinary, gotCwd = binary, cwd
			return []piagent.Command{
				{Name: "skill:review", Description: "Review changes", Source: "skill"},
				{Name: "session-name", Source: "extension"},
				{Name: "skill:review", Source: "skill"},
				{Name: " ", Source: "skill"},
			}, nil
		}}

		commands, err := d.DiscoverCommands(context.Background(), agentskill.CommandDiscoverQuery{
			CLIPath: " /opt/pi ",
			Cwd:     " /work/project ",
		})

		Convey("Then only unique /skill:name candidates are exposed with launch context", func() {
			So(err, ShouldBeNil)
			So(gotBinary, ShouldEqual, "/opt/pi")
			So(gotCwd, ShouldEqual, "/work/project")
			So(commands, ShouldResemble, []agentskill.SkillCommand{{Name: "skill:review", Description: "Review changes"}})
		})
	})
}
