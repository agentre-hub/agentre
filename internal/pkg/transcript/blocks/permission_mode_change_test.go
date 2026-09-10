package blocks

import (
	"testing"

	cagoblocks "github.com/cago-frame/agents/agent/blocks"
	. "github.com/smartystreets/goconvey/convey"
)

func TestPermissionModeChangeBlock(t *testing.T) {
	Convey("PermissionModeChangeBlock 类型/Audience/round-trip", t, func() {
		b := &PermissionModeChangeBlock{From: "default", To: "plan", At: 1700000000000}
		So(b.Type(), ShouldEqual, "permission_mode_change")
		So(b.Audience(), ShouldEqual, cagoblocks.ToUI)

		sb, err := cagoblocks.Encode(b)
		So(err, ShouldBeNil)
		decoded, err := cagoblocks.Decode(sb)
		So(err, ShouldBeNil)
		got, ok := decoded.(PermissionModeChangeBlock)
		So(ok, ShouldBeTrue)
		So(got.To, ShouldEqual, "plan")
	})
}
