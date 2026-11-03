package procscan

import (
	"testing"

	. "github.com/smartystreets/goconvey/convey"
)

func TestTrackCLI(t *testing.T) {
	Convey("TrackCLI 登记表", t, func() {
		Convey("登记后可见，release 后消失，release 幂等", func() {
			release := TrackCLI(424242)
			_, ok := trackedCLIs()[424242]
			So(ok, ShouldBeTrue)
			release()
			release()
			_, ok = trackedCLIs()[424242]
			So(ok, ShouldBeFalse)
		})
		Convey("同一 pid 登记两次，释放一次不影响另一次", func() {
			r1, r2 := TrackCLI(424243), TrackCLI(424243)
			r1()
			_, ok := trackedCLIs()[424243]
			So(ok, ShouldBeTrue)
			r2()
			_, ok = trackedCLIs()[424243]
			So(ok, ShouldBeFalse)
		})
		Convey("返回的是快照，改它不影响登记表", func() {
			defer TrackCLI(424244)()
			delete(trackedCLIs(), 424244)
			_, ok := trackedCLIs()[424244]
			So(ok, ShouldBeTrue)
		})
	})
}
