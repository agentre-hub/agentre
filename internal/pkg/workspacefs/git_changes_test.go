package workspacefs_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/agentre-hub/agentre/internal/pkg/workspacefs"
)

func changesByPath(changes []workspacefs.Change) map[string]workspacefs.Change {
	m := make(map[string]workspacefs.Change, len(changes))
	for _, c := range changes {
		m[c.Path] = c
	}
	return m
}

func TestGitChanges_UncommittedScope_FiveStatuses(t *testing.T) {
	dir := initRepo(t)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "b.txt"), []byte("world\n"), 0o644))
	require.NoError(t, os.Mkdir(filepath.Join(dir, "sub"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "sub", "c.txt"), []byte("nested\n"), 0o644))
	runGit(t, dir, "add", "b.txt", "sub/c.txt")
	runGit(t, dir, "commit", "-q", "-m", "seed")

	// modified
	require.NoError(t, os.WriteFile(filepath.Join(dir, "b.txt"), []byte("world\nmodified line\n"), 0o644))
	// renamed (staged)
	runGit(t, dir, "mv", "sub/c.txt", "sub/d.txt")
	// added (staged, binary)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "f.bin"), []byte("bin\x00data"), 0o644))
	runGit(t, dir, "add", "f.bin")
	// deleted
	require.NoError(t, os.Remove(filepath.Join(dir, "b.txt")))
	// untracked
	require.NoError(t, os.WriteFile(filepath.Join(dir, "e.txt"), []byte("untracked content\n"), 0o644))

	res, err := workspacefs.GitChanges(context.Background(), dir, workspacefs.ScopeUncommitted, "", workspacefs.DefaultMaxEntries)
	require.NoError(t, err)
	assert.False(t, res.NotARepo)
	assert.False(t, res.Truncated)

	byPath := changesByPath(res.Changes)

	require.Contains(t, byPath, "b.txt")
	assert.Equal(t, workspacefs.ChangeDeleted, byPath["b.txt"].Status)

	require.Contains(t, byPath, "sub/d.txt")
	assert.Equal(t, workspacefs.ChangeRenamed, byPath["sub/d.txt"].Status)
	assert.Equal(t, "sub/c.txt", byPath["sub/d.txt"].OldPath)

	require.Contains(t, byPath, "f.bin")
	assert.Equal(t, workspacefs.ChangeAdded, byPath["f.bin"].Status)
	assert.True(t, byPath["f.bin"].Binary, "tracked binary file: numstat reports '-' for both counts")
	assert.Equal(t, 0, byPath["f.bin"].Added)

	require.Contains(t, byPath, "e.txt")
	assert.Equal(t, workspacefs.ChangeUntracked, byPath["e.txt"].Status)
	assert.Equal(t, 1, byPath["e.txt"].Added, "one line, go-side count for untracked file")
	assert.False(t, byPath["e.txt"].Binary)
}

func TestGitChanges_UncommittedScope_ModifiedLineCounts(t *testing.T) {
	dir := initRepo(t)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "a.txt"), []byte("hello\n"), 0o644))
	runGit(t, dir, "add", "a.txt")
	runGit(t, dir, "commit", "-q", "-m", "seed")

	require.NoError(t, os.WriteFile(filepath.Join(dir, "a.txt"), []byte("hello\nmodified\n"), 0o644))

	res, err := workspacefs.GitChanges(context.Background(), dir, workspacefs.ScopeUncommitted, "", workspacefs.DefaultMaxEntries)
	require.NoError(t, err)
	byPath := changesByPath(res.Changes)
	require.Contains(t, byPath, "a.txt")
	assert.Equal(t, workspacefs.ChangeModified, byPath["a.txt"].Status)
	assert.Equal(t, 1, byPath["a.txt"].Added)
	assert.Equal(t, 0, byPath["a.txt"].Deleted)
}

// agent 新建的文件常常连目录一起是新的。git status 默认(-unormal)把这种目录
// 折成一条 "?? newdir/" 记录,列表里既看不到具体文件、也没有行数——而
// "agent 新建的文件是最需要看行数的"(设计决策 10),且行模型的 basename 会变成
// 空串。两档共用这条规则,所以两档都要断言。
func TestGitChanges_UntrackedInsideNewDirectory_ListsEachFile(t *testing.T) {
	dir := initRepo(t)
	require.NoError(t, os.Mkdir(filepath.Join(dir, "newdir"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "newdir", "one.txt"), []byte("a\nb\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "newdir", "two.txt"), []byte("c\n"), 0o644))

	for _, scope := range []workspacefs.GitChangesScope{workspacefs.ScopeUncommitted, workspacefs.ScopeBranch} {
		baseline := ""
		if scope == workspacefs.ScopeBranch {
			baseline = "main"
		}
		res, err := workspacefs.GitChanges(context.Background(), dir, scope, baseline, workspacefs.DefaultMaxEntries)
		require.NoError(t, err)
		byPath := changesByPath(res.Changes)

		require.Containsf(t, byPath, "newdir/one.txt", "scope=%s", scope)
		assert.Equal(t, workspacefs.ChangeUntracked, byPath["newdir/one.txt"].Status)
		assert.Equal(t, 2, byPath["newdir/one.txt"].Added)

		require.Containsf(t, byPath, "newdir/two.txt", "scope=%s", scope)
		assert.Equal(t, 1, byPath["newdir/two.txt"].Added)

		assert.NotContainsf(t, byPath, "newdir/", "the collapsed directory record must not become a row (scope=%s)", scope)
	}
}

// 「被忽略的文件不出现在此列表」是两档共用的约定。它靠 status 不带 --ignored
// 成立,而未跟踪文件的枚举范围恰恰是 -u 这一族开关在管的——上面那条 -uall 的
// 修复就动过它,所以这里给忽略语义留一条独立的守卫,别只靠人工跑一次 git 确认。
func TestGitChanges_IgnoredEntries_NeverListed(t *testing.T) {
	dir := initRepo(t)
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".gitignore"), []byte("ignored/\nsecret.txt\n"), 0o644))
	runGit(t, dir, "add", ".gitignore")
	runGit(t, dir, "commit", "-q", "-m", "add gitignore")

	require.NoError(t, os.Mkdir(filepath.Join(dir, "ignored"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "ignored", "inner.txt"), []byte("x\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "secret.txt"), []byte("x\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "visible.txt"), []byte("x\n"), 0o644))

	for _, scope := range []workspacefs.GitChangesScope{workspacefs.ScopeUncommitted, workspacefs.ScopeBranch} {
		baseline := ""
		if scope == workspacefs.ScopeBranch {
			baseline = "main"
		}
		res, err := workspacefs.GitChanges(context.Background(), dir, scope, baseline, workspacefs.DefaultMaxEntries)
		require.NoError(t, err)
		byPath := changesByPath(res.Changes)

		require.Containsf(t, byPath, "visible.txt", "scope=%s", scope)
		assert.NotContainsf(t, byPath, "secret.txt", "ignored file must not be listed (scope=%s)", scope)
		assert.NotContainsf(t, byPath, "ignored/inner.txt", "file inside an ignored directory must not be listed (scope=%s)", scope)
		assert.NotContainsf(t, byPath, "ignored/", "the ignored directory itself must not be listed (scope=%s)", scope)
	}
}

func TestGitChanges_UntrackedBinary_NULByte(t *testing.T) {
	dir := initRepo(t)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "bin.dat"), []byte("a\x00b"), 0o644))

	res, err := workspacefs.GitChanges(context.Background(), dir, workspacefs.ScopeUncommitted, "", workspacefs.DefaultMaxEntries)
	require.NoError(t, err)
	byPath := changesByPath(res.Changes)
	require.Contains(t, byPath, "bin.dat")
	assert.True(t, byPath["bin.dat"].Binary)
	assert.Equal(t, 0, byPath["bin.dat"].Added)
}

func TestGitChanges_UntrackedOversized_TreatedAsBinary(t *testing.T) {
	dir := initRepo(t)
	big := bytes.Repeat([]byte("a"), (1<<20)+1)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "big.txt"), big, 0o644))

	res, err := workspacefs.GitChanges(context.Background(), dir, workspacefs.ScopeUncommitted, "", workspacefs.DefaultMaxEntries)
	require.NoError(t, err)
	byPath := changesByPath(res.Changes)
	require.Contains(t, byPath, "big.txt")
	assert.True(t, byPath["big.txt"].Binary, ">1MiB must be treated as binary without reading full content")
	assert.Equal(t, 0, byPath["big.txt"].Added)
}

// 未跟踪的符号链接必须按链接本身计数,不能跟进目标去读文件内容:git 会把符号
// 链接当成一条 "??" 记录列出来,而目标可以指到工作目录之外(仓库外文件的行数
// 会漏进面板),也可以指到 /dev/zero 这类字符设备——跟随后 Size 为 0,过得了
// 1MiB 那道闸门,读取则永远不会结束。
func TestGitChanges_UntrackedSymlink_NotFollowed(t *testing.T) {
	dir := initRepo(t)
	outside := filepath.Join(t.TempDir(), "outside.txt")
	require.NoError(t, os.WriteFile(outside, []byte("a\nb\nc\n"), 0o644))
	if err := os.Symlink(outside, filepath.Join(dir, "link.txt")); err != nil {
		t.Skipf("symlink unsupported on this platform: %v", err)
	}

	res, err := workspacefs.GitChanges(context.Background(), dir, workspacefs.ScopeUncommitted, "", workspacefs.DefaultMaxEntries)
	require.NoError(t, err)
	byPath := changesByPath(res.Changes)
	require.Contains(t, byPath, "link.txt")
	assert.Equal(t, workspacefs.ChangeUntracked, byPath["link.txt"].Status)
	assert.Equal(t, 0, byPath["link.txt"].Added, "不跟随链接去数工作目录之外那个文件的行数")
	assert.False(t, byPath["link.txt"].Binary)
}

// 会话工作目录常常只是仓库里的一个子目录(项目路径指到 monorepo 的
// frontend/ 之类)。git status --porcelain / git diff 的路径恒相对仓库根、且
// 覆盖整个仓库,而本包对外的 Change.Path 相对 dir —— countUntrackedFileLines
// 拿它 filepath.Join(dir, path) 读文件,前端拿它拼 cwd 打开文件。两者只在
// dir 恰是仓库根时重合,所以子目录这一档要单独钉死。
func TestGitChanges_WorkspaceInRepoSubdir_PathsRelativeToWorkspace(t *testing.T) {
	root := initRepo(t)
	require.NoError(t, os.Mkdir(filepath.Join(root, "sub"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "outside.txt"), []byte("o\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(root, "sub", "inner.txt"), []byte("i\n"), 0o644))
	runGit(t, root, "add", "outside.txt", "sub/inner.txt")
	runGit(t, root, "commit", "-q", "-m", "seed")

	require.NoError(t, os.WriteFile(filepath.Join(root, "outside.txt"), []byte("o\nchanged\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(root, "sub", "inner.txt"), []byte("i\nchanged\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(root, "outside-new.txt"), []byte("x\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(root, "sub", "fresh.txt"), []byte("a\nb\n"), 0o644))

	dir := filepath.Join(root, "sub")
	for _, scope := range []workspacefs.GitChangesScope{workspacefs.ScopeUncommitted, workspacefs.ScopeBranch} {
		baseline := ""
		if scope == workspacefs.ScopeBranch {
			baseline = "main"
		}
		res, err := workspacefs.GitChanges(context.Background(), dir, scope, baseline, workspacefs.DefaultMaxEntries)
		require.NoError(t, err)
		byPath := changesByPath(res.Changes)

		require.Containsf(t, byPath, "inner.txt", "跟踪文件的路径要相对工作目录,而不是仓库根 (scope=%s)", scope)
		assert.Equal(t, workspacefs.ChangeModified, byPath["inner.txt"].Status)
		assert.Equal(t, 1, byPath["inner.txt"].Added)

		require.Containsf(t, byPath, "fresh.txt", "未跟踪文件同样相对工作目录 (scope=%s)", scope)
		assert.Equalf(t, 2, byPath["fresh.txt"].Added,
			"行数靠 filepath.Join(dir, path) 读文件,路径错了就恒为 0 (scope=%s)", scope)

		assert.NotContainsf(t, byPath, "sub/inner.txt", "仓库根相对路径不能漏出去 (scope=%s)", scope)
		assert.NotContainsf(t, byPath, "outside.txt", "工作目录之外的变动不属于这个面板 (scope=%s)", scope)
		assert.NotContainsf(t, byPath, "outside-new.txt", "工作目录之外的未跟踪文件同样不列 (scope=%s)", scope)
	}
}

// setupDivergedBranches builds: main (init commit) -> feature branch checked
// out with one committed change on top of main, plus a further uncommitted
// worktree edit to the same file, a staged new file, and an untracked file.
// Returns the repo dir; caller passes baseline="main".
func setupDivergedBranches(t *testing.T) string {
	t.Helper()
	dir := initRepo(t) // main branch, "init" commit (empty)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "a.txt"), []byte("hello\n"), 0o644))
	runGit(t, dir, "add", "a.txt")
	runGit(t, dir, "commit", "-q", "-m", "seed on main")

	runGit(t, dir, "checkout", "-q", "-b", "feature")
	require.NoError(t, os.WriteFile(filepath.Join(dir, "a.txt"), []byte("hello\nmore\n"), 0o644))
	runGit(t, dir, "add", "a.txt")
	runGit(t, dir, "commit", "-q", "-m", "feature commit")

	// further uncommitted edit on top of the feature commit
	require.NoError(t, os.WriteFile(filepath.Join(dir, "a.txt"), []byte("hello\nmore\nwip\n"), 0o644))
	// staged new tracked file
	require.NoError(t, os.WriteFile(filepath.Join(dir, "newfile.txt"), []byte("new tracked file\n"), 0o644))
	runGit(t, dir, "add", "newfile.txt")
	// untracked file
	require.NoError(t, os.WriteFile(filepath.Join(dir, "untracked.txt"), []byte("untracked here\n"), 0o644))

	return dir
}

func TestGitChanges_BranchScope_MergeBaseIncludesCommittedAndUncommitted(t *testing.T) {
	dir := setupDivergedBranches(t)

	res, err := workspacefs.GitChanges(context.Background(), dir, workspacefs.ScopeBranch, "main", workspacefs.DefaultMaxEntries)
	require.NoError(t, err)
	assert.False(t, res.NotARepo)

	byPath := changesByPath(res.Changes)

	require.Contains(t, byPath, "a.txt", "modified relative to merge-base, combining the feature commit and the wip edit")
	assert.Equal(t, workspacefs.ChangeModified, byPath["a.txt"].Status)
	assert.Equal(t, 2, byPath["a.txt"].Added, "both the committed 'more' line and the uncommitted 'wip' line count")

	require.Contains(t, byPath, "newfile.txt")
	assert.Equal(t, workspacefs.ChangeAdded, byPath["newfile.txt"].Status)
	assert.Equal(t, 1, byPath["newfile.txt"].Added)

	require.Contains(t, byPath, "untracked.txt", "untracked files must still be listed in branch scope")
	assert.Equal(t, workspacefs.ChangeUntracked, byPath["untracked.txt"].Status)
	assert.Equal(t, 1, byPath["untracked.txt"].Added)
}

// baseline 是过 wire 进来的调用方输入(daemon 侧 handler 把 req.BaseRef 原样
// 交给 GitChanges),不能直接当 git 的位置参数使:`--octopus` 这类以 "-" 开头
// 的值会被 git 当选项吃掉——`git merge-base --octopus HEAD` 退出码 0 且返回
// HEAD 自己,「本分支档」于是静默变成「对 HEAD 比较」,出一份看着正常、语义
// 却是另一档的清单。git 本身就不允许这种引用名,拒掉即可。
func TestGitChanges_BranchScope_OptionLikeBaselineRefused(t *testing.T) {
	dir := setupDivergedBranches(t)

	res, err := workspacefs.GitChanges(context.Background(), dir, workspacefs.ScopeBranch, "--octopus", workspacefs.DefaultMaxEntries)
	require.NoError(t, err)
	assert.Empty(t, res.Changes, "求不出基线时退化为空变动,而不是拿 HEAD 冒充基线")
}

func TestGitChanges_BranchScope_RequiresBaseline(t *testing.T) {
	dir := initRepo(t)
	_, err := workspacefs.GitChanges(context.Background(), dir, workspacefs.ScopeBranch, "", workspacefs.DefaultMaxEntries)
	assert.Truef(t, errors.Is(err, workspacefs.ErrBaselineRequired), "err=%v", err)
}

func TestGitChanges_NonRepo_Degrades(t *testing.T) {
	dir := t.TempDir()
	res, err := workspacefs.GitChanges(context.Background(), dir, workspacefs.ScopeUncommitted, "", workspacefs.DefaultMaxEntries)
	require.NoError(t, err)
	assert.True(t, res.NotARepo)
	assert.Empty(t, res.Changes)
}

func TestGitChanges_Truncated(t *testing.T) {
	dir := initRepo(t)
	for i := 0; i < 5; i++ {
		require.NoError(t, os.WriteFile(filepath.Join(dir, "u"+string(rune('a'+i))+".txt"), []byte("x"), 0o644))
	}
	res, err := workspacefs.GitChanges(context.Background(), dir, workspacefs.ScopeUncommitted, "", 3)
	require.NoError(t, err)
	assert.True(t, res.Truncated)
	assert.Len(t, res.Changes, 3)
}

func TestDefaultBaseline_PrefersOriginHEAD(t *testing.T) {
	dir := initRepo(t)
	// simulate a remote-tracking origin/main + symbolic origin/HEAD without a
	// real network remote, mirroring what a clone leaves behind.
	require.NoError(t, os.MkdirAll(filepath.Join(dir, ".git", "refs", "remotes", "origin"), 0o755))
	runGit(t, dir, "update-ref", "refs/remotes/origin/main", "HEAD")
	runGit(t, dir, "symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/main")
	// initRepo already leaves a local "main" branch checked out, so this also
	// proves origin/HEAD wins over the local branch of the same name.

	got := workspacefs.DefaultBaseline(context.Background(), dir)
	assert.Equal(t, "origin/main", got)
}

func TestDefaultBaseline_FallsBackToMain(t *testing.T) {
	dir := initRepo(t) // initRepo creates branch "main" already, no origin
	got := workspacefs.DefaultBaseline(context.Background(), dir)
	assert.Equal(t, "main", got)
}

func TestDefaultBaseline_FallsBackToMaster(t *testing.T) {
	dir := t.TempDir()
	runGit(t, dir, "init", "-q", "-b", "trunk")
	runGit(t, dir, "config", "user.email", "t@t")
	runGit(t, dir, "config", "user.name", "t")
	runGit(t, dir, "commit", "-q", "--allow-empty", "-m", "init")
	runGit(t, dir, "branch", "master")

	got := workspacefs.DefaultBaseline(context.Background(), dir)
	assert.Equal(t, "master", got)
}

func TestDefaultBaseline_NoneFound(t *testing.T) {
	dir := t.TempDir()
	runGit(t, dir, "init", "-q", "-b", "trunk")
	runGit(t, dir, "config", "user.email", "t@t")
	runGit(t, dir, "config", "user.name", "t")
	runGit(t, dir, "commit", "-q", "--allow-empty", "-m", "init")

	got := workspacefs.DefaultBaseline(context.Background(), dir)
	assert.Equal(t, "", got)
}

func TestRefExists(t *testing.T) {
	dir := initRepo(t)
	assert.True(t, workspacefs.RefExists(context.Background(), dir, "main"))
	assert.False(t, workspacefs.RefExists(context.Background(), dir, "does-not-exist"))
	assert.False(t, workspacefs.RefExists(context.Background(), dir, ""))
	assert.False(t, workspacefs.RefExists(context.Background(), dir, "--octopus"), "以 - 开头的值是 git 选项而不是引用名")
}

func TestGitBranches_ListsLocalAndRemote(t *testing.T) {
	dir := initRepo(t)
	require.NoError(t, os.MkdirAll(filepath.Join(dir, ".git", "refs", "remotes", "origin"), 0o755))
	runGit(t, dir, "update-ref", "refs/remotes/origin/main", "HEAD")
	runGit(t, dir, "symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/main")
	runGit(t, dir, "branch", "develop")

	res, err := workspacefs.GitBranches(context.Background(), dir)
	require.NoError(t, err)
	assert.False(t, res.NotARepo)

	names := make([]string, 0, len(res.Branches))
	byName := map[string]workspacefs.Branch{}
	for _, b := range res.Branches {
		names = append(names, b.Name)
		byName[b.Name] = b
	}
	assert.Contains(t, names, "main")
	assert.False(t, byName["main"].Remote)
	assert.Contains(t, names, "develop")
	assert.Contains(t, names, "origin/main")
	assert.True(t, byName["origin/main"].Remote)
	assert.NotContains(t, strings.Join(names, ","), "HEAD", "the symbolic origin/HEAD pointer must not be listed as a branch")
}

func TestGitBranches_NonRepo_Degrades(t *testing.T) {
	dir := t.TempDir()
	res, err := workspacefs.GitBranches(context.Background(), dir)
	require.NoError(t, err)
	assert.True(t, res.NotARepo)
	assert.Empty(t, res.Branches)
	assert.Empty(t, res.CurrentBranch)
	assert.Empty(t, res.DefaultBaseline)
}

// GitBranches 一次调用就要带回"当前分支"与"推断出的默认基线" —— 远端会话
// 只能通过 workspacefs.gitBranches RPC 拿到这两个值(DefaultBaseline /
// RefExists 是 in-process API,跨不过 daemon 边界)。
func TestGitBranches_ReportsCurrentBranchAndDefaultBaseline(t *testing.T) {
	dir := setupDivergedBranches(t) // 停在 feature 分支上,存在本地 main

	res, err := workspacefs.GitBranches(context.Background(), dir)
	require.NoError(t, err)
	assert.False(t, res.NotARepo)
	assert.Equal(t, "feature", res.CurrentBranch)
	assert.Equal(t, "main", res.DefaultBaseline, "与 DefaultBaseline() 同一套三级回退")
}

func TestGitBranches_DefaultBaselinePrefersOriginHEAD(t *testing.T) {
	dir := initRepo(t)
	require.NoError(t, os.MkdirAll(filepath.Join(dir, ".git", "refs", "remotes", "origin"), 0o755))
	runGit(t, dir, "update-ref", "refs/remotes/origin/main", "HEAD")
	runGit(t, dir, "symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/main")

	res, err := workspacefs.GitBranches(context.Background(), dir)
	require.NoError(t, err)
	assert.Equal(t, "origin/main", res.DefaultBaseline)
}

// detached HEAD 下没有"当前分支"可言,留空而不是回显 "HEAD" 这种伪分支名。
func TestGitBranches_DetachedHead_CurrentBranchEmpty(t *testing.T) {
	dir := initRepo(t)
	runGit(t, dir, "checkout", "-q", "--detach")

	res, err := workspacefs.GitBranches(context.Background(), dir)
	require.NoError(t, err)
	assert.False(t, res.NotARepo)
	assert.Empty(t, res.CurrentBranch)
	assert.Equal(t, "main", res.DefaultBaseline, "detached 不影响基线推断")
}
