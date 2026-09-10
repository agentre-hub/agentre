package blocks

import (
	"testing"

	cagoblocks "github.com/cago-frame/agents/agent/blocks"
	. "github.com/smartystreets/goconvey/convey"
)

func TestCompactBoundaryBlock(t *testing.T) {
	Convey("CompactBoundaryBlock 类型/Audience/round-trip", t, func() {
		b := &CompactBoundaryBlock{PreTokens: 12345, Trigger: "auto", At: 1700000000000}
		So(b.Type(), ShouldEqual, "compact_boundary")
		So(b.Audience(), ShouldEqual, cagoblocks.ToUI)

		sb, err := cagoblocks.Encode(b)
		So(err, ShouldBeNil)
		decoded, err := cagoblocks.Decode(sb)
		So(err, ShouldBeNil)
		got, ok := decoded.(CompactBoundaryBlock)
		So(ok, ShouldBeTrue)
		So(got.PreTokens, ShouldEqual, 12345)
		So(got.Trigger, ShouldEqual, "auto")
		So(got.At, ShouldEqual, 1700000000000)
	})
}
