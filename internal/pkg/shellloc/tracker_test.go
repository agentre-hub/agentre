package shellloc

import (
	"testing"

	. "github.com/smartystreets/goconvey/convey"
)

func TestTracker(t *testing.T) {
	Convey("a cd persists into later calls", t, func() {
		tr := NewTracker("/w")
		cwd, _ := tr.Call("a", []byte(`{"command":"cd sub"}`))
		So(cwd, ShouldEqual, "/w/sub")
		cwd, _ = tr.Call("b", []byte(`{"command":"ls"}`))
		So(cwd, ShouldEqual, "/w/sub")
		So(tr.Dir(), ShouldEqual, "/w/sub")
	})

	Convey("an undeterminable command keeps the previous location", t, func() {
		tr := NewTracker("/w")
		tr.Call("a", []byte(`{"command":"cd /x"}`))
		cwd, _ := tr.Call("b", []byte(`{"command":"cd $HOME"}`))
		So(cwd, ShouldEqual, "/x")
		So(tr.Dir(), ShouldEqual, "/x")
	})

	Convey("input without a command leaves the location alone", t, func() {
		tr := NewTracker("/w")
		for _, in := range []string{``, `{}`, `[1]`, `not json`, `{"command":5}`, `{"path":"/x"}`} {
			cwd, out := tr.Call("a", []byte(in))
			So(cwd, ShouldEqual, "/w")
			So(string(out), ShouldEqual, in)
		}
	})

	Convey("a result carries the location of its call, once", t, func() {
		tr := NewTracker("/w")
		tr.Call("a", []byte(`{"command":"cd /x"}`))
		tr.Call("b", []byte(`{"command":"cd /y"}`))
		So(tr.Result("a"), ShouldEqual, "/x")
		So(tr.Result("b"), ShouldEqual, "/y")
		So(tr.Result("a"), ShouldEqual, "/y") // unknown id falls back to the current dir
	})

	Convey("a worktree add path is exposed on the call input and the location is unchanged", t, func() {
		tr := NewTracker("/w/r")
		cwd, out := tr.Call("a", []byte(`{"command":"git worktree add ../wt -b b"}`))
		So(cwd, ShouldEqual, "/w/r")
		So(string(out), ShouldEqual, `{"command":"git worktree add ../wt -b b","agentre_worktree_add":"/w/wt"}`)
		_, out = tr.Call("b", []byte(`{ }`))
		So(string(out), ShouldEqual, `{ }`)
	})

	Convey("worktree add into an empty input object keeps it valid JSON", t, func() {
		So(withWorktreeAdd([]byte(`{}`), "/p"), ShouldResemble, []byte(`{"agentre_worktree_add":"/p"}`))
	})

	Convey("the registry hands out one tracker per key, started at the first cwd", t, func() {
		r := NewRegistry()
		a := r.For(1, "/w")
		a.Call("x", []byte(`{"command":"cd /moved"}`))
		So(r.For(1, "/other").Dir(), ShouldEqual, "/moved")
		So(r.For(2, "/w").Dir(), ShouldEqual, "/w")
	})
}

func TestAbsDir(t *testing.T) {
	Convey("only absolute directories are accepted as a location", t, func() {
		So(AbsDir("/repo/wt"), ShouldEqual, "/repo/wt")
		So(AbsDir("/repo/../wt/"), ShouldEqual, "/wt")
		So(AbsDir(`C:\repo`), ShouldEqual, `C:\repo`)
		So(AbsDir("rel/dir"), ShouldEqual, "")
		So(AbsDir("~/x"), ShouldEqual, "")
		So(AbsDir(""), ShouldEqual, "")
	})
}

func TestRegistryZeroValue(t *testing.T) {
	Convey("a zero Registry is usable", t, func() {
		var r Registry
		So(r.For(1, "/w").Dir(), ShouldEqual, "/w")
	})
}

func TestTrackerRepeatedCallIsIdempotent(t *testing.T) {
	Convey("re-emitting the same call id does not apply its cd twice", t, func() {
		tr := NewTracker("/w")
		in := []byte(`{"command":"cd sub"}`)
		first, _ := tr.Call("a", in)
		second, _ := tr.Call("a", in)
		So(first, ShouldEqual, "/w/sub")
		So(second, ShouldEqual, "/w/sub")
		So(tr.Dir(), ShouldEqual, "/w/sub")
	})
	Convey("a repeated worktree-add call keeps its path on the input", t, func() {
		tr := NewTracker("/w/r")
		in := []byte(`{"command":"git worktree add ../wt"}`)
		_, a := tr.Call("a", in)
		_, b := tr.Call("a", in)
		So(string(b), ShouldEqual, string(a))
	})
	Convey("Here records a non-shell call", t, func() {
		tr := NewTracker("/w")
		So(tr.Here("x"), ShouldEqual, "/w")
		tr.Call("y", []byte(`{"command":"cd /q"}`))
		So(tr.Result("x"), ShouldEqual, "/w")
	})
}
