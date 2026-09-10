package workspacefs_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/agentre-hub/agentre/internal/pkg/workspacefs"
)

func TestGitFileContent_ReadsHEADVersion(t *testing.T) {
	dir := initRepo(t)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "a.txt"), []byte("v1\n"), 0o644))
	runGit(t, dir, "add", "a.txt")
	runGit(t, dir, "commit", "-q", "-m", "seed")
	// 工作区被改动后,对比档左列必须仍取 HEAD 版本,而不是工作区内容。
	require.NoError(t, os.WriteFile(filepath.Join(dir, "a.txt"), []byte("v2\n"), 0o644))

	res, err := workspacefs.GitFileContent(context.Background(), dir, "a.txt")
	require.NoError(t, err)
	assert.False(t, res.NotARepo)
	assert.True(t, res.HasHead)
	assert.Equal(t, "v1\n", res.Content)
}

func TestGitFileContent_UntrackedFile_EmptyBaseline(t *testing.T) {
	dir := initRepo(t)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "new.txt"), []byte("untracked\n"), 0o644))

	res, err := workspacefs.GitFileContent(context.Background(), dir, "new.txt")
	require.NoError(t, err)
	assert.False(t, res.NotARepo)
	assert.False(t, res.HasHead, "未跟踪文件不在 HEAD → 空基线,对比档左列留空、全部新增")
	assert.Equal(t, "", res.Content)
}

// 仅暂存未提交的文件同样不在 HEAD(HEAD 是上一次提交),对比档左列应读不到。
func TestGitFileContent_StagedButUncommitted_EmptyBaseline(t *testing.T) {
	dir := initRepo(t)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "staged.txt"), []byte("s\n"), 0o644))
	runGit(t, dir, "add", "staged.txt")

	res, err := workspacefs.GitFileContent(context.Background(), dir, "staged.txt")
	require.NoError(t, err)
	assert.False(t, res.NotARepo)
	assert.False(t, res.HasHead)
	assert.Equal(t, "", res.Content)
}

// 工作区已删除、但文件仍提交在 HEAD 里:对比档左列必须还能读到 HEAD 版本
// (这正是对比删除文件要看的)。git show 读对象库,不依赖工作区文件存在。
func TestGitFileContent_DeletedInWorktree_StillReadsHEAD(t *testing.T) {
	dir := initRepo(t)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "gone.txt"), []byte("old\n"), 0o644))
	runGit(t, dir, "add", "gone.txt")
	runGit(t, dir, "commit", "-q", "-m", "seed")
	require.NoError(t, os.Remove(filepath.Join(dir, "gone.txt")))

	res, err := workspacefs.GitFileContent(context.Background(), dir, "gone.txt")
	require.NoError(t, err)
	assert.False(t, res.NotARepo)
	assert.True(t, res.HasHead)
	assert.Equal(t, "old\n", res.Content)
}

// 会话工作目录常常只是仓库里的一个子目录。git 的 <rev>:<path> 路径恒相对
// 仓库根(无视命令在哪个子目录跑),所以 relPath 必须先经 workspacePrefix
// 换算成仓库根相对路径,否则 git 会在仓库根下找不到该路径。
func TestGitFileContent_WorkspaceInRepoSubdir(t *testing.T) {
	root := initRepo(t)
	require.NoError(t, os.Mkdir(filepath.Join(root, "sub"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "sub", "inner.txt"), []byte("v1\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(root, "outside.txt"), []byte("o\n"), 0o644))
	runGit(t, root, "add", "sub/inner.txt", "outside.txt")
	runGit(t, root, "commit", "-q", "-m", "seed")
	require.NoError(t, os.WriteFile(filepath.Join(root, "sub", "inner.txt"), []byte("v2\n"), 0o644))

	dir := filepath.Join(root, "sub")
	res, err := workspacefs.GitFileContent(context.Background(), dir, "inner.txt")
	require.NoError(t, err)
	assert.False(t, res.NotARepo)
	assert.True(t, res.HasHead)
	assert.Equal(t, "v1\n", res.Content, "relPath 相对 cwd,换算成仓库根相对路径后取到 sub/inner.txt 的 HEAD 版本")

	// 越界仍拒绝:不能用 ".." 从子目录折回仓库根去读 outside.txt。
	_, err = workspacefs.GitFileContent(context.Background(), dir, "../outside.txt")
	assert.True(t, errors.Is(err, workspacefs.ErrPathRefused))
}

func TestGitFileContent_NonRepo_NotARepo(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "a.txt"), []byte("x\n"), 0o644))

	res, err := workspacefs.GitFileContent(context.Background(), dir, "a.txt")
	require.NoError(t, err)
	assert.True(t, res.NotARepo)
	assert.False(t, res.HasHead)
	assert.Equal(t, "", res.Content)
}

func TestGitFileContent_EmptyCwdRejected(t *testing.T) {
	_, err := workspacefs.GitFileContent(context.Background(), "", "a.txt")
	assert.True(t, errors.Is(err, workspacefs.ErrNoCwd), "cwd 为空应拒绝,与 ReadFile 同一 sentinel")
}

func TestGitFileContent_EscapeRejected(t *testing.T) {
	dir := initRepo(t)
	cases := []string{"..", "../a.txt", "/etc/passwd", "sub/../..", "a/../../../etc/passwd"}
	for _, rp := range cases {
		_, err := workspacefs.GitFileContent(context.Background(), dir, rp)
		assert.Truef(t, errors.Is(err, workspacefs.ErrPathRefused), "relPath=%q err=%v", rp, err)
	}
}

// 跟随符号链接逃出 cwd 必须拒绝:即使 git show 读的是对象库,relPath 在
// 工作区解析到 cwd 之外也不放行(与 ReadFile 同一道链接逃逸闸门)。
func TestGitFileContent_SymlinkEscapeRejected(t *testing.T) {
	parent := t.TempDir()
	dir := filepath.Join(parent, "work")
	require.NoError(t, os.Mkdir(dir, 0o755))
	outside := filepath.Join(parent, "secret.txt")
	require.NoError(t, os.WriteFile(outside, []byte("secret"), 0o644))
	require.NoError(t, os.Symlink(outside, filepath.Join(dir, "link.txt")))
	if _, err := os.Lstat(filepath.Join(dir, "link.txt")); err != nil {
		t.Skipf("symlink unsupported on this platform: %v", err)
	}

	_, err := workspacefs.GitFileContent(context.Background(), dir, "link.txt")
	assert.True(t, errors.Is(err, workspacefs.ErrPathRefused), "跟随符号链接逃出 cwd 应拒绝")
}

// cwd 内的符号链接不拒绝。HEAD 里链接以"目标路径"为 blob 内容存储,git show
// 返回的就是这条链接 blob(而不是去工作区跟随链接读目标文件)——这恰好证明
// GitFileContent 读的是 HEAD 快照,不经工作区。
func TestGitFileContent_SymlinkInsideCwd_ReadsHEADBlob(t *testing.T) {
	dir := initRepo(t)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "real.txt"), []byte("v1\n"), 0o644))
	require.NoError(t, os.Symlink("real.txt", filepath.Join(dir, "link.txt")))
	if _, err := os.Lstat(filepath.Join(dir, "link.txt")); err != nil {
		t.Skipf("symlink unsupported on this platform: %v", err)
	}
	runGit(t, dir, "add", "real.txt", "link.txt")
	runGit(t, dir, "commit", "-q", "-m", "sym")

	res, err := workspacefs.GitFileContent(context.Background(), dir, "link.txt")
	require.NoError(t, err)
	assert.False(t, res.NotARepo)
	assert.True(t, res.HasHead)
	assert.Equal(t, "real.txt", res.Content, "HEAD 里符号链接的 blob 是目标路径字符串,不跟随到 real.txt 的正文")
}

// 文件在 HEAD 里、工作区版本里是逃逸链接:git show 读的是对象库,本不会漏
// 内容,但链接逃逸闸门必须先于 git 判据,拒绝而不是返回 HEAD blob。
func TestGitFileContent_HEADExistsButWorktreeSymlinkEscapes_StillRefused(t *testing.T) {
	parent := t.TempDir()
	dir := filepath.Join(parent, "work")
	require.NoError(t, os.Mkdir(dir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "tracked.txt"), []byte("v1\n"), 0o644))
	runGit(t, dir, "init", "-q", "-b", "main")
	runGit(t, dir, "config", "user.email", "t@t")
	runGit(t, dir, "config", "user.name", "t")
	runGit(t, dir, "add", "tracked.txt")
	runGit(t, dir, "commit", "-q", "-m", "seed")
	// 工作区把 tracked.txt 换成指向 cwd 之外的符号链接。
	require.NoError(t, os.Remove(filepath.Join(dir, "tracked.txt")))
	outside := filepath.Join(parent, "secret.txt")
	require.NoError(t, os.WriteFile(outside, []byte("secret"), 0o644))
	require.NoError(t, os.Symlink(outside, filepath.Join(dir, "tracked.txt")))
	if _, err := os.Lstat(filepath.Join(dir, "tracked.txt")); err != nil {
		t.Skipf("symlink unsupported on this platform: %v", err)
	}

	_, err := workspacefs.GitFileContent(context.Background(), dir, "tracked.txt")
	assert.True(t, errors.Is(err, workspacefs.ErrPathRefused))
}
