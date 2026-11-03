package procscan

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	. "github.com/smartystreets/goconvey/convey"
)

// fakeOS 是可注入的假 OS；dieOnTerm 为 true 时 TERM 就让进程消失。
type fakeOS struct {
	mu        sync.Mutex
	procs     map[int]Proc
	dieOnTerm bool
	signals   []string
	snapErr   error
}

func (f *fakeOS) Snapshot(context.Context) ([]Proc, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.snapErr != nil {
		return nil, f.snapErr
	}
	out := []Proc{}
	for _, x := range f.procs {
		out = append(out, x)
	}
	return out, nil
}

func (f *fakeOS) Inspect(_ context.Context, pid int) (Proc, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	x, ok := f.procs[pid]
	return x, ok, nil
}

func (f *fakeOS) Terminate(pid int, force bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if force {
		f.signals = append(f.signals, "KILL")
		delete(f.procs, pid)
		return nil
	}
	f.signals = append(f.signals, "TERM")
	if f.dieOnTerm {
		delete(f.procs, pid)
	}
	return nil
}

func newFake(dieOnTerm bool) *fakeOS {
	return &fakeOS{procs: map[int]Proc{100: {PID: 100, PPID: 1, StartMs: 5000, Marker: testConv}}, dieOnTerm: dieOnTerm}
}

func TestScannerKill(t *testing.T) {
	Convey("Scanner.Kill 安全核对", t, func() {
		ctx := context.Background()
		Convey("pid 不存在：不发信号", func() {
			f := newFake(true)
			err := NewWith(f, 20*time.Millisecond).Kill(ctx, testConv, 999, 5000)
			So(errors.Is(err, ErrNotSessionProcess), ShouldBeTrue)
			So(f.signals, ShouldBeEmpty)
		})
		Convey("标记不符：不发信号", func() {
			f := newFake(true)
			err := NewWith(f, 20*time.Millisecond).Kill(ctx, "other", 100, 5000)
			So(errors.Is(err, ErrNotSessionProcess), ShouldBeTrue)
			So(f.signals, ShouldBeEmpty)
		})
		Convey("启动时间不符（pid 被复用）：不发信号", func() {
			f := newFake(true)
			err := NewWith(f, 20*time.Millisecond).Kill(ctx, testConv, 100, 4999)
			So(errors.Is(err, ErrNotSessionProcess), ShouldBeTrue)
			So(f.signals, ShouldBeEmpty)
		})
		Convey("TERM 后退出：只发 TERM", func() {
			f := newFake(true)
			So(NewWith(f, time.Second).Kill(ctx, testConv, 100, 5000), ShouldBeNil)
			So(f.signals, ShouldResemble, []string{"TERM"})
		})
		Convey("TERM 后宽限内不退出：再发 KILL", func() {
			f := newFake(false)
			So(NewWith(f, 30*time.Millisecond).Kill(ctx, testConv, 100, 5000), ShouldBeNil)
			So(f.signals, ShouldResemble, []string{"TERM", "KILL"})
		})
		Convey("宽限期内进程被换成别的进程：不补 KILL", func() {
			f := newFake(false)
			go func() {
				time.Sleep(10 * time.Millisecond)
				f.mu.Lock()
				f.procs[100] = Proc{PID: 100, StartMs: 9999, Marker: testConv}
				f.mu.Unlock()
			}()
			err := NewWith(f, 80*time.Millisecond).Kill(ctx, testConv, 100, 5000)
			So(errors.Is(err, ErrNotSessionProcess), ShouldBeTrue)
			So(f.signals, ShouldResemble, []string{"TERM"})
		})
		Convey("ctx 取消：返回 ctx 错误且不补 KILL", func() {
			f := newFake(false)
			c, cancel := context.WithCancel(ctx)
			cancel()
			err := NewWith(f, time.Minute).Kill(c, testConv, 100, 5000)
			So(errors.Is(err, context.Canceled), ShouldBeTrue)
			So(f.signals, ShouldResemble, []string{"TERM"})
		})
	})
}

func TestScannerListAndUnsupported(t *testing.T) {
	Convey("Scanner.List", t, func() {
		ctx := context.Background()
		Convey("用当前登记的 CLI pid 做 Claim", func() {
			f := &fakeOS{procs: map[int]Proc{
				7: {PID: 7, PPID: 1, Marker: testConv, StartMs: 1},
				8: {PID: 8, PPID: 7, Marker: testConv, StartMs: 2, Ports: []uint16{80}},
			}}
			defer TrackCLI(7)()
			got, err := NewWith(f, time.Second).List(ctx, testConv)
			So(err, ShouldBeNil)
			So(pids(got), ShouldResemble, []int{8})
		})
		Convey("快照失败：返回错误", func() {
			boom := errors.New("boom")
			_, err := NewWith(&fakeOS{snapErr: boom}, time.Second).List(ctx, testConv)
			So(errors.Is(err, boom), ShouldBeTrue)
		})
		Convey("平台未实现时 New() 的各方法返回 ErrUnsupported", func() {
			s := NewWith(unsupportedOS{}, time.Millisecond)
			_, err := s.List(ctx, testConv)
			So(errors.Is(err, ErrUnsupported), ShouldBeTrue)
			So(errors.Is(s.Kill(ctx, testConv, 1, 1), ErrUnsupported), ShouldBeTrue)
		})
		Convey("platformOS 为 nil 时 New() 退化为 ErrUnsupported", func() {
			saved := platformOS
			platformOS = nil
			defer func() { platformOS = saved }()
			_, err := New().List(ctx, testConv)
			So(errors.Is(err, ErrUnsupported), ShouldBeTrue)
		})
	})
}
