package chat_entity

import (
	"testing"

	. "github.com/smartystreets/goconvey/convey"
)

func TestCLIModelChoice(t *testing.T) {
	Convey("Given session CLI choices, When validating the three modes, Then only well formed choices are accepted", t, func() {
		for _, tc := range []struct {
			mode, value string
			valid       bool
		}{
			{"", "", true}, {"cli-default", "", true}, {"fixed", "sonnet", true},
			{"fixed", "", false}, {"cli-default", "sonnet", false}, {"", "sonnet", false}, {"unknown", "", false},
		} {
			So(ValidCLIModelChoice(tc.mode, tc.value), ShouldEqual, tc.valid)
		}
	})
}
