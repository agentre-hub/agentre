package shellloc

import (
	"testing"

	. "github.com/smartystreets/goconvey/convey"
)

func TestParseShellLocation(t *testing.T) {
	type row struct {
		name, cmd, cwd string
		dir, worktree  string
		ok             bool
	}
	rows := []row{
		{"cd absolute", "cd /srv/app", "/w", "/srv/app", "", true},
		{"cd relative", "cd sub/dir", "/w", "/w/sub/dir", "", true},
		{"cd parent", "cd ../other", "/w/a", "/w/other", "", true},
		{"cd dot-dot cleaned", "cd /a/b/../c", "/w", "/a/c", "", true},
		{"cd with option terminator", "cd -- /x", "/w", "/x", "", true},
		{"cd -P", "cd -P /x", "/w", "/x", "", true},
		{"cd and command", "cd /x && make test", "/w", "/x", "", true},
		{"cd semicolon", "cd /x; ls", "/w", "/x", "", true},
		{"cd newline", "cd /x\nls", "/w", "/x", "", true},
		{"chained cd", "cd a && cd b", "/w", "/w/a/b", "", true},
		{"cd single quoted", "cd '/my dir'", "/w", "/my dir", "", true},
		{"cd double quoted", `cd "/my dir/x"`, "/w", "/my dir/x", "", true},
		{"cd escaped space", `cd /my\ dir`, "/w", "/my dir", "", true},
		{"cd then piped command", "cd /x && make 2>&1 | tail", "/w", "/x", "", true},
		{"no location effect", "ls -la", "/w", "/w", "", true},
		{"plain git status", "git status", "/w", "/w", "", true},
		{"git -C absolute", "git -C /repo/wt status", "/w", "/repo/wt", "", true},
		{"git -C relative", "git -C ../wt status", "/w/a", "/w/wt", "", true},
		{"git -C after cd", "cd /r && git -C wt log", "/w", "/r/wt", "", true},
		{"git -C quoted", `git -C "/my repo" diff`, "/w", "/my repo", "", true},
		{"worktree add relative with -b after path", "git worktree add ../wt -b b", "/w/repo", "/w/repo", "/w/wt", true},
		{"worktree add -b before path", "git worktree add -b b ../wt", "/w/repo", "/w/repo", "/w/wt", true},
		{"worktree add absolute", "git worktree add /tmp/x main", "/w", "/w", "/tmp/x", true},
		{"worktree add --detach", "git worktree add --detach ../wt HEAD", "/w/r", "/w/r", "/w/wt", true},
		{"worktree add -B value", "git worktree add -B br ../wt", "/w/r", "/w/r", "/w/wt", true},
		{"worktree add quoted path", `git worktree add "../my wt"`, "/w/r", "/w/r", "/w/my wt", true},
		{"git -C then worktree add", "git -C /repo worktree add ../wt", "/w", "/repo", "/wt", true},
		{"worktree add then cd", "git worktree add ../wt && cd ../wt", "/w/r", "/w/wt", "/w/wt", true},
		{"worktree list is not add", "git worktree list", "/w", "/w", "", true},

		{"cd home", "cd", "/w", "/w", "", false},
		{"cd tilde", "cd ~/x", "/w", "/w", "", false},
		{"cd dash", "cd -", "/w", "/w", "", false},
		{"cd variable", "cd $HOME/x", "/w", "/w", "", false},
		{"cd double-quoted variable", `cd "$DIR"`, "/w", "/w", "", false},
		{"cd command substitution", "cd $(mktemp -d)", "/w", "/w", "", false},
		{"cd backticks", "cd `pwd`/x", "/w", "/w", "", false},
		{"cd glob", "cd /x/*", "/w", "/w", "", false},
		{"cd two operands", "cd a b", "/w", "/w", "", false},
		{"cd empty operand", `cd ""`, "/w", "/w", "", false},
		{"relative cd without known cwd", "cd sub", "", "", "", false},
		{"subshell", "(cd /x && make)", "/w", "/w", "", false},
		{"cd in pipeline", "cd /x | cat", "/w", "/w", "", false},
		{"cd after ||", "false || cd /x", "/w", "/w", "", false},
		{"cd backgrounded", "cd /x &", "/w", "/w", "", false},
		{"cd in if", "if true; then cd /x; fi", "/w", "/w", "", false},
		{"unterminated quote", `cd "/x`, "/w", "/w", "", false},
		{"git -C variable", "git -C $R status", "/w", "/w", "", false},
		{"git -C without operand", "git -C", "/w", "/w", "", false},
		{"worktree add variable path", "git worktree add $P", "/w", "/w", "", false},
	}
	for _, r := range rows {
		Convey(r.name, t, func() {
			dir, wt, ok := ParseShellLocation(r.cmd, r.cwd)
			So(ok, ShouldEqual, r.ok)
			So(dir, ShouldEqual, r.dir)
			So(wt, ShouldEqual, r.worktree)
		})
	}

	Convey("a worktree add path found before an undeterminable cd is still reported", t, func() {
		dir, wt, ok := ParseShellLocation("git worktree add ../wt && cd $X", "/w/r")
		So(ok, ShouldBeFalse)
		So(dir, ShouldEqual, "/w/r")
		So(wt, ShouldEqual, "/w/wt")
	})
}
