package blocks

import (
	"testing"

	. "github.com/smartystreets/goconvey/convey"
)

func TestUnsupportedRequestNoticeRoundTrip(t *testing.T) {
	Convey("Encode/DecodeUnsupportedRequestNotice round-trips the purpose", t, func() {
		text := EncodeUnsupportedRequestNotice("sudo_password")

		p, ok := DecodeUnsupportedRequestNotice(text)

		So(ok, ShouldBeTrue)
		So(p.Kind, ShouldEqual, UnsupportedRequestNoticeKind)
		So(p.Purpose, ShouldEqual, "sudo_password")
	})

	Convey("Decode rejects other notice shapes", t, func() {
		_, ok := DecodeUnsupportedRequestNotice(`{"providerKey":"x"}`)
		So(ok, ShouldBeFalse)

		_, ok = DecodeUnsupportedRequestNotice("")
		So(ok, ShouldBeFalse)

		_, ok = DecodeUnsupportedRequestNotice("legacy free text notice")
		So(ok, ShouldBeFalse)
	})
}
