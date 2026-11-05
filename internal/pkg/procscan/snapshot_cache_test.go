package procscan

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	. "github.com/smartystreets/goconvey/convey"
)

// countingOS 包一层 fakeOS，统计 Snapshot 调用次数。
type countingOS struct {
	*fakeOS
	snaps atomic.Int32
}

func (c *countingOS) Snapshot(ctx context.Context) ([]Proc, error) {
	c.snaps.Add(1)
	return c.fakeOS.Snapshot(ctx)
}

func TestScannerSharesSnapshot(t *testing.T) {
	Convey("given a scanner with a shared snapshot TTL", t, func() {
		base := &fakeOS{procs: map[int]Proc{
			10: {PID: 10, PPID: 1, StartMs: 5, Marker: "c1", Argv: []string{"srv"}},
			20: {PID: 20, PPID: 1, StartMs: 6, Marker: "c2", Argv: []string{"srv2"}},
		}, dieOnTerm: true}
		cos := &countingOS{fakeOS: base}
		s := NewWith(cos, time.Second)
		clock := time.Unix(1000, 0)
		s.now = func() time.Time { return clock }
		ctx := context.Background()

		Convey("when two conversations list within the TTL, then one snapshot serves both", func() {
			a, err := s.List(ctx, "c1")
			So(err, ShouldBeNil)
			b, err := s.List(ctx, "c2")
			So(err, ShouldBeNil)
			So(cos.snaps.Load(), ShouldEqual, 1)
			So(a, ShouldHaveLength, 1)
			So(a[0].PID, ShouldEqual, 10)
			So(b, ShouldHaveLength, 1)
			So(b[0].PID, ShouldEqual, 20)
		})

		Convey("when the TTL has elapsed, then a new snapshot is taken", func() {
			_, _ = s.List(ctx, "c1")
			clock = clock.Add(snapshotTTL + time.Millisecond)
			_, _ = s.List(ctx, "c1")
			So(cos.snaps.Load(), ShouldEqual, 2)
		})

		Convey("when a kill succeeds, then the next list re-snapshots and the process is gone", func() {
			l, _ := s.List(ctx, "c1")
			So(l, ShouldHaveLength, 1)
			So(s.Kill(ctx, "c1", 10, 5), ShouldBeNil)
			l, err := s.List(ctx, "c1")
			So(err, ShouldBeNil)
			So(l, ShouldBeEmpty)
			So(cos.snaps.Load(), ShouldEqual, 2)
		})

		Convey("when a kill is refused, then the snapshot stays shared", func() {
			_, _ = s.List(ctx, "c1")
			So(s.Kill(ctx, "c1", 10, 999), ShouldEqual, ErrNotSessionProcess)
			_, _ = s.List(ctx, "c1")
			So(cos.snaps.Load(), ShouldEqual, 1)
		})

		// 第一个调用方的 ctx 被取消时,OS 层的辅助查询(darwin 的 lsof)会被一起杀掉、只交回
		// 一份缺端口 / cwd 的残缺快照却不报错。这份快照不能拿去在 TTL 内喂给别的会话。
		Convey("when the caller's ctx ended during the snapshot, then that snapshot is not shared", func() {
			canceled, cancel := context.WithCancel(ctx)
			cancel()
			_, _ = s.List(canceled, "c1")
			l, err := s.List(ctx, "c1")
			So(err, ShouldBeNil)
			So(l, ShouldHaveLength, 1)
			So(cos.snaps.Load(), ShouldEqual, 2)
		})

		Convey("when a snapshot fails, then the error is not cached", func() {
			base.snapErr = context.Canceled
			_, err := s.List(ctx, "c1")
			So(err, ShouldNotBeNil)
			base.snapErr = nil
			l, err := s.List(ctx, "c1")
			So(err, ShouldBeNil)
			So(l, ShouldHaveLength, 1)
			So(cos.snaps.Load(), ShouldEqual, 2)
		})
	})
}
