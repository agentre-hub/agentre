//go:build darwin

package procscan

import (
	"testing"

	. "github.com/smartystreets/goconvey/convey"
)

func TestLsofPortArgsScopedToCandidates(t *testing.T) {
	Convey("port query is limited to the given candidate pids", t, func() {
		args := lsofPortArgs([]int{12, 345})
		So(args, ShouldContain, "-p")
		So(args, ShouldContain, "12,345")
		So(args, ShouldContain, "-iTCP")
		So(args, ShouldContain, "-sTCP:LISTEN")
		So(args, ShouldNotContain, "-u")
	})
}
