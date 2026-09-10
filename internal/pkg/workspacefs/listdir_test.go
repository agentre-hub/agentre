package workspacefs_test

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/agentre-hub/agentre/internal/pkg/workspacefs"
)

// runGit 在 dir 下执行 git args。测试 helper,失败直接 t.Fatal。
// 与 internal/service/chat_svc/git_state_test.go 的同名 helper 保持同一形态。
func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...) //nolint:gosec // G204: test helper, args 来自测试内常量
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func initRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	runGit(t, dir, "init", "-q", "-b", "main")
	runGit(t, dir, "config", "user.email", "t@t")
	runGit(t, dir, "config", "user.name", "t")
	runGit(t, dir, "commit", "--allow-empty", "-m", "init")
	return dir
}

func entryNames(entries []workspacefs.Entry) map[string]workspacefs.Entry {
	m := make(map[string]workspacefs.Entry, len(entries))
	for _, e := range entries {
		m[e.Name] = e
	}
	return m
}

func TestListDir_OneLevel(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(dir, "sub"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "a.txt"), []byte("hi"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "sub", "nested.txt"), []byte("nested"), 0o644))

	res, err := workspacefs.ListDir(context.Background(), dir, "", false, workspacefs.DefaultMaxEntries)
	require.NoError(t, err)
	assert.Equal(t, dir, res.Path)
	assert.False(t, res.Truncated)

	names := entryNames(res.Entries)
	require.Contains(t, names, "sub")
	assert.True(t, names["sub"].IsDir)
	require.Contains(t, names, "a.txt")
	assert.False(t, names["a.txt"].IsDir)
	assert.Equal(t, int64(2), names["a.txt"].Size)
	assert.NotContains(t, names, "nested.txt", "只列一层,不递归进 sub")
}

func TestListDir_RelPath(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(dir, "sub"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "sub", "nested.txt"), []byte("x"), 0o644))

	res, err := workspacefs.ListDir(context.Background(), dir, "sub", false, workspacefs.DefaultMaxEntries)
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(dir, "sub"), res.Path)
	require.Len(t, res.Entries, 1)
	assert.Equal(t, "nested.txt", res.Entries[0].Name)
}

func TestListDir_EscapeRejected(t *testing.T) {
	dir := t.TempDir()
	cases := []string{"..", "../etc", "sub/../..", "/etc", "a/../../../etc"}
	for _, rp := range cases {
		_, err := workspacefs.ListDir(context.Background(), dir, rp, false, workspacefs.DefaultMaxEntries)
		assert.Truef(t, errors.Is(err, workspacefs.ErrPathRefused), "relPath=%q err=%v", rp, err)
	}
}

func TestListDir_GitDirAlwaysHidden(t *testing.T) {
	dir := initRepo(t)
	res, err := workspacefs.ListDir(context.Background(), dir, "", true, workspacefs.DefaultMaxEntries)
	require.NoError(t, err)
	for _, e := range res.Entries {
		assert.NotEqual(t, ".git", e.Name)
	}
}

func TestListDir_GitIgnoreMarking(t *testing.T) {
	dir := initRepo(t)
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".gitignore"), []byte("*.log\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "keep.txt"), []byte("k"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "debug.log"), []byte("l"), 0o644))

	t.Run("默认隐藏忽略项", func(t *testing.T) {
		res, err := workspacefs.ListDir(context.Background(), dir, "", false, workspacefs.DefaultMaxEntries)
		require.NoError(t, err)
		names := entryNames(res.Entries)
		assert.Contains(t, names, "keep.txt")
		assert.NotContains(t, names, "debug.log")
	})

	t.Run("显示忽略项时标记GitIgnored", func(t *testing.T) {
		res, err := workspacefs.ListDir(context.Background(), dir, "", true, workspacefs.DefaultMaxEntries)
		require.NoError(t, err)
		names := entryNames(res.Entries)
		require.Contains(t, names, "debug.log")
		assert.True(t, names["debug.log"].GitIgnored)
		assert.False(t, names["keep.txt"].GitIgnored)
	})
}

func TestListDir_NestedGitignore(t *testing.T) {
	dir := initRepo(t)
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".gitignore"), []byte("*.log\n"), 0o644))
	require.NoError(t, os.Mkdir(filepath.Join(dir, "sub"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "sub", ".gitignore"), []byte("*.tmp\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "sub", "a.log"), []byte("l"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "sub", "b.tmp"), []byte("t"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "sub", "c.txt"), []byte("c"), 0o644))

	res, err := workspacefs.ListDir(context.Background(), dir, "sub", true, workspacefs.DefaultMaxEntries)
	require.NoError(t, err)
	names := entryNames(res.Entries)
	assert.True(t, names["a.log"].GitIgnored, "父层 .gitignore 规则应对子目录生效")
	assert.True(t, names["b.tmp"].GitIgnored, "当层 .gitignore 规则应生效")
	assert.False(t, names["c.txt"].GitIgnored)
}

func TestListDir_NonGitDirNotMarked(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "x.log"), []byte("l"), 0o644))
	res, err := workspacefs.ListDir(context.Background(), dir, "", true, workspacefs.DefaultMaxEntries)
	require.NoError(t, err)
	for _, e := range res.Entries {
		assert.False(t, e.GitIgnored, "非 git 目录不应做忽略判定")
	}
}

func TestListDir_Truncated(t *testing.T) {
	dir := t.TempDir()
	for i := 0; i < 5; i++ {
		require.NoError(t, os.WriteFile(filepath.Join(dir, "f"+strconv.Itoa(i)), nil, 0o644))
	}
	res, err := workspacefs.ListDir(context.Background(), dir, "", false, 3)
	require.NoError(t, err)
	assert.True(t, res.Truncated)
	assert.Len(t, res.Entries, 3)
}
