package claudecode

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// countingFinder 返回一个把全量遍历次数记进 *walks 的 finder,用来证明快路径/缓存生效。
func countingFinder(walks *int) *sessionJSONLFinder {
	f := newSessionJSONLFinder()
	inner := f.walk
	f.walk = func(root, sid string) (string, error) {
		*walks++
		return inner(root, sid)
	}
	return f
}

func writeSessionFile(t *testing.T, root, dir, sid string) string {
	t.Helper()
	d := filepath.Join(root, dir)
	require.NoError(t, os.MkdirAll(d, 0o755))
	p := filepath.Join(d, sid+".jsonl")
	require.NoError(t, os.WriteFile(p, []byte("{}\n"), 0o600))
	return p
}

func TestProjectDirSlug_MatchesClaudeCodeNaming(t *testing.T) {
	assert.Equal(t,
		"-Users-codfrm-Code-agentre-agentre--dev-kit-worktrees-perf-sweep",
		projectDirSlug("/Users/codfrm/Code/agentre/agentre/.dev-kit/worktrees/perf-sweep"))
	assert.Equal(t, "-tmp-a-b-c", projectDirSlug("/tmp/a_b/c"))
	// CLI 的替换跑在 JS 字符串上(按 UTF-16 码元):BMP 内的非 ASCII 字符(中文、é)各换成
	// 一个 "-",BMP 之外的(emoji,代理对)换成两个 —— 不是按 UTF-8 字节数。
	assert.Equal(t, "-Users-me------x", projectDirSlug("/Users/me/项目_é/x"))
	assert.Equal(t, "-tmp---a", projectDirSlug("/tmp/😀a"))
}

func TestSessionJSONLFinder_CwdSlugDirHitSkipsFullWalk(t *testing.T) {
	root := t.TempDir()
	cwd := "/work/my.proj"
	want := writeSessionFile(t, root, projectDirSlug(cwd), "sid-1")
	writeSessionFile(t, root, "other", "sid-2")

	walks := 0
	got, err := countingFinder(&walks).find(root, cwd, "sid-1")
	require.NoError(t, err)
	assert.Equal(t, want, got)
	assert.Equal(t, 0, walks)
}

func TestSessionJSONLFinder_SecondLookupUsesCache(t *testing.T) {
	root := t.TempDir()
	want := writeSessionFile(t, root, "unrelated-slug", "sid-1")

	walks := 0
	f := countingFinder(&walks)
	got, err := f.find(root, "/no/such/cwd", "sid-1")
	require.NoError(t, err)
	assert.Equal(t, want, got)
	assert.Equal(t, 1, walks)

	got, err = f.find(root, "/no/such/cwd", "sid-1")
	require.NoError(t, err)
	assert.Equal(t, want, got)
	assert.Equal(t, 1, walks, "second lookup must not walk")
}

func TestSessionJSONLFinder_MissingSlugDirFallsBackToFullWalk(t *testing.T) {
	root := t.TempDir()
	want := writeSessionFile(t, root, "some-other-dir", "sid-1")

	walks := 0
	got, err := countingFinder(&walks).find(root, "/gone/cwd", "sid-1")
	require.NoError(t, err)
	assert.Equal(t, want, got)
	assert.Equal(t, 1, walks)
}

func TestSessionJSONLFinder_StaleCacheEntryFallsBack(t *testing.T) {
	root := t.TempDir()
	old := writeSessionFile(t, root, "dir-a", "sid-1")

	walks := 0
	f := countingFinder(&walks)
	_, err := f.find(root, "", "sid-1")
	require.NoError(t, err)

	// 文件被搬走:缓存路径失效,应重新遍历找到新位置,而不是报错或返回旧路径。
	require.NoError(t, os.Remove(old))
	moved := writeSessionFile(t, root, "dir-b", "sid-1")
	got, err := f.find(root, "", "sid-1")
	require.NoError(t, err)
	assert.Equal(t, moved, got)
	assert.Equal(t, 2, walks)

	// 文件彻底删除:返回 ErrSessionJSONLNotFound。
	require.NoError(t, os.Remove(moved))
	_, err = f.find(root, "", "sid-1")
	assert.ErrorIs(t, err, ErrSessionJSONLNotFound)
}
