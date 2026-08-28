package project_location_entity

import (
	"context"
	"testing"

	"github.com/cago-frame/cago/pkg/consts"
	. "github.com/smartystreets/goconvey/convey"
)

func TestProjectLocation_Check(t *testing.T) {
	ctx := context.Background()

	Convey("Check", t, func() {
		Convey("nil receiver → error", func() {
			var p *ProjectLocation
			So(p.Check(ctx), ShouldNotBeNil)
		})
		Convey("invalid project_id → error", func() {
			p := &ProjectLocation{ProjectID: 0, Path: "/a", DeviceFingerprint: "fp-1"}
			So(p.Check(ctx), ShouldNotBeNil)
		})
		Convey("empty path → error", func() {
			p := &ProjectLocation{ProjectID: 1, Path: "", DeviceFingerprint: "fp-1"}
			So(p.Check(ctx), ShouldNotBeNil)
		})
		Convey("relative path on remote device → error", func() {
			p := &ProjectLocation{ProjectID: 1, DeviceID: "7", DeviceFingerprint: "fp-1", Path: "foo/bar"}
			So(p.Check(ctx), ShouldNotBeNil)
		})
		Convey("empty device_fingerprint → error（本表不存空指纹的行，决策 26）", func() {
			p := &ProjectLocation{ProjectID: 1, DeviceID: "7", DeviceFingerprint: "", Path: "/home/me/foo"}
			So(p.Check(ctx), ShouldNotBeNil)
		})
		Convey("valid remote absolute with fingerprint → ok", func() {
			p := &ProjectLocation{ProjectID: 1, DeviceID: "7", DeviceFingerprint: "fp-1", Path: "/home/me/foo"}
			So(p.Check(ctx), ShouldBeNil)
		})
		Convey("valid but unresolved（device_id 缓存为空、指纹仍在）→ ok（R2b：未配对不是校验失败）", func() {
			p := &ProjectLocation{ProjectID: 1, DeviceID: "", DeviceFingerprint: "fp-1", Path: "/home/me/foo"}
			So(p.Check(ctx), ShouldBeNil)
		})
	})

	Convey("IsActive / IsUnresolved", t, func() {
		p := &ProjectLocation{Status: consts.ACTIVE, DeviceID: "", DeviceFingerprint: "fp-1"}
		So(p.IsActive(), ShouldBeTrue)
		So(p.IsUnresolved(), ShouldBeTrue)

		q := &ProjectLocation{Status: consts.DELETE, DeviceID: "7", DeviceFingerprint: "fp-2"}
		So(q.IsActive(), ShouldBeFalse)
		So(q.IsUnresolved(), ShouldBeFalse)
	})
}
