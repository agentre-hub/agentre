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

// hitPaths 把命中列表折成"路径 → 是否目录",断言时按集合看而不依赖切片下标。
func hitPaths(hits []workspacefs.SearchHit) map[string]bool {
	m := make(map[string]bool, len(hits))
	for _, h := range hits {
		m[h.Path] = h.IsDir
	}
	return m
}

// gitOutput 是只读取 git 输出的测试 helper(runGit 只判成败不回输出)。
func gitOutput(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...) //nolint:gosec // G204: test helper, args 来自测试内常量
	cmd.Dir = dir
	out, err := cmd.Output()
	require.NoErrorf(t, err, "git %v", args)
	return string(out)
}

func search(t *testing.T, root, query string, includeIgnored bool) *workspacefs.SearchFilesResult {
	t.Helper()
	res, err := workspacefs.SearchFiles(context.Background(), root, query, includeIgnored,
		workspacefs.DefaultMaxSearchHits, workspacefs.DefaultMaxSearchDirs)
	require.NoError(t, err)
	return res
}

func TestSearchFiles_BasenameSubstring_CaseInsensitive(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "Alpha-dir", "deep"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "Alpha.go"), nil, 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "beta.txt"), nil, 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "Alpha-dir", "deep", "alphaBETA.md"), nil, 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "Alpha-dir", "plain.txt"), nil, 0o644))

	t.Run("大小写不敏感的子串匹配", func(t *testing.T) {
		got := hitPaths(search(t, dir, "alpha", false).Hits)
		assert.Contains(t, got, "Alpha.go")
		assert.Contains(t, got, "Alpha-dir/deep/alphaBETA.md")
		assert.NotContains(t, got, "beta.txt")
	})

	t.Run("查询串本身的大小写不影响结果", func(t *testing.T) {
		assert.Equal(t, hitPaths(search(t, dir, "BeTa", false).Hits),
			hitPaths(search(t, dir, "beta", false).Hits))
	})

	t.Run("只匹配 basename,不匹配目录路径前缀", func(t *testing.T) {
		got := hitPaths(search(t, dir, "alpha", false).Hits)
		assert.NotContains(t, got, "Alpha-dir/plain.txt", "父目录名命中不应把里面的文件也算命中")
		require.Contains(t, got, "Alpha-dir")
		assert.True(t, got["Alpha-dir"], "目录命中带 IsDir 标记")
	})

	t.Run("空查询不遍历也不命中", func(t *testing.T) {
		res := search(t, dir, "  ", false)
		assert.Empty(t, res.Hits)
		assert.False(t, res.Truncated)
	})

	t.Run("无命中返回空结果而不是错误", func(t *testing.T) {
		res := search(t, dir, "zzz-nothing", false)
		assert.Empty(t, res.Hits)
		assert.False(t, res.Truncated)
	})
}

func TestSearchFiles_ShallowerHitsFirst(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "a", "b"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "a", "b", "target-deep.txt"), nil, 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "target-top.txt"), nil, 0o644))

	res := search(t, dir, "target", false)
	require.Len(t, res.Hits, 2)
	assert.Equal(t, "target-top.txt", res.Hits[0].Path, "逐层遍历:浅的先出")
	assert.Equal(t, "a/b/target-deep.txt", res.Hits[1].Path)
}

func TestSearchFiles_NeverEntersGitDir(t *testing.T) {
	dir := initRepo(t)
	// 真仓库的 .git 里恒有 config / HEAD / description 之类文件。
	require.FileExists(t, filepath.Join(dir, ".git", "config"))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "config.yaml"), nil, 0o644))

	for _, includeIgnored := range []bool{false, true} {
		res := search(t, dir, "config", includeIgnored)
		got := hitPaths(res.Hits)
		assert.Contains(t, got, "config.yaml")
		for p := range got {
			assert.NotContains(t, p, ".git/", "includeIgnored=%v 时也不得进入 .git", includeIgnored)
			assert.NotEqual(t, ".git", p)
		}
	}
}

func TestSearchFiles_IgnoredDirPrunedWholeAndIgnoredFileDropped(t *testing.T) {
	dir := initRepo(t)
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".gitignore"), []byte("node_modules/\n*.log\n"), 0o644))
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "node_modules", "pkg"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "node_modules", "pkg", "target.ts"), nil, 0o644))
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "src"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "src", "target.ts"), nil, 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "target.log"), nil, 0o644))

	before := gitOutput(t, dir, "status", "--porcelain")

	t.Run("默认剪掉被忽略的整棵目录与被忽略的文件", func(t *testing.T) {
		got := hitPaths(search(t, dir, "target", false).Hits)
		assert.Contains(t, got, "src/target.ts")
		assert.NotContains(t, got, "node_modules/pkg/target.ts", "被忽略目录应整棵剪枝")
		assert.NotContains(t, got, "target.log", "被忽略的文件不计入")
	})

	t.Run("被忽略目录自身也不算命中", func(t *testing.T) {
		got := hitPaths(search(t, dir, "node_modules", false).Hits)
		assert.Empty(t, got)
	})

	t.Run("显示忽略项时忽略项全部回到结果里", func(t *testing.T) {
		got := hitPaths(search(t, dir, "target", true).Hits)
		assert.Contains(t, got, "src/target.ts")
		assert.Contains(t, got, "node_modules/pkg/target.ts")
		assert.Contains(t, got, "target.log")
	})

	t.Run("搜索是纯读:不改工作区也不改 git 状态", func(t *testing.T) {
		assert.Equal(t, before, gitOutput(t, dir, "status", "--porcelain"))
		assert.NoFileExists(t, filepath.Join(dir, ".git", "index.lock"))
	})
}

// TestSearchFiles_IgnoredDirCostsNoBudget 把"整棵剪枝"与"逐个文件过滤"区分开:
// 两者的命中集合一样,差别在有没有走进那棵子树。用目录预算当观测点——被忽略的
// 子树若被走过,预算会被它吃掉,结果就会变成截断。
func TestSearchFiles_IgnoredDirCostsNoBudget(t *testing.T) {
	dir := initRepo(t)
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".gitignore"), []byte("ignored/\n"), 0o644))
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "ignored", "a", "b"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "src"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "src", "target.ts"), nil, 0o644))

	// 预算恰好够走 root + src 两层目录:剪枝生效时遍历完整,一旦走进 ignored/
	// 就会多花预算并留下未展开的目录 → 截断。
	res, err := workspacefs.SearchFiles(context.Background(), dir, "target", false,
		workspacefs.DefaultMaxSearchHits, 3)
	require.NoError(t, err)
	assert.False(t, res.Truncated, "被忽略的目录整棵剪枝,不应该消耗遍历预算")
	assert.Contains(t, hitPaths(res.Hits), "src/target.ts")
}

func TestSearchFiles_NonGitDir_NoIgnoreJudgement(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "node_modules", "pkg"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "node_modules", "pkg", "target.ts"), nil, 0o644))

	got := hitPaths(search(t, dir, "target", false).Hits)
	assert.Contains(t, got, "node_modules/pkg/target.ts", "非 git 目录不做忽略判定")
}

func TestSearchFiles_TruncatedByHitCap(t *testing.T) {
	dir := t.TempDir()
	for i := 0; i < 5; i++ {
		require.NoError(t, os.WriteFile(filepath.Join(dir, "target"+strconv.Itoa(i)+".txt"), nil, 0o644))
	}

	res, err := workspacefs.SearchFiles(context.Background(), dir, "target", false, 3, workspacefs.DefaultMaxSearchDirs)
	require.NoError(t, err)
	assert.Len(t, res.Hits, 3)
	assert.True(t, res.Truncated, "超上限必须显式回报截断,不能静默返回不完整结果")
}

func TestSearchFiles_TruncatedByDirBudget(t *testing.T) {
	dir := t.TempDir()
	// 一条 4 层深的目录链,最深处才放命中文件:目录预算为 2 时够不到。
	deep := filepath.Join(dir, "a", "b", "c", "d")
	require.NoError(t, os.MkdirAll(deep, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(deep, "target.txt"), nil, 0o644))

	res, err := workspacefs.SearchFiles(context.Background(), dir, "target", false, workspacefs.DefaultMaxSearchHits, 2)
	require.NoError(t, err)
	assert.True(t, res.Truncated, "触及目录预算必须回报截断,而不是当作'没有更多命中'")
	assert.Empty(t, res.Hits)

	// 预算充足时同一棵树能搜到,证明上面的空结果确实来自预算而非匹配逻辑。
	full := search(t, dir, "target", false)
	assert.False(t, full.Truncated)
	assert.Contains(t, hitPaths(full.Hits), "a/b/c/d/target.txt")
}

func TestSearchFiles_EmptyCwdRejected(t *testing.T) {
	_, err := workspacefs.SearchFiles(context.Background(), "", "x", false,
		workspacefs.DefaultMaxSearchHits, workspacefs.DefaultMaxSearchDirs)
	assert.True(t, errors.Is(err, workspacefs.ErrNoCwd))
}

// TestSearchFiles_DoesNotEscapeCwdViaSymlink 锁死"搜索根不得逃出会话工作目录":
// cwd 里指向外面的目录符号链接不被跟随,外面的文件因此不可能出现在结果里。
// 链接自身仍是 cwd 内的一个条目,按名命中时照常出现(与 ListDir 的取舍一致)。
func TestSearchFiles_DoesNotEscapeCwdViaSymlink(t *testing.T) {
	outside := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(outside, "secret-target.txt"), nil, 0o644))
	dir := t.TempDir()
	require.NoError(t, os.Symlink(outside, filepath.Join(dir, "link-target")))

	got := hitPaths(search(t, dir, "target", false).Hits)
	assert.NotContains(t, got, "link-target/secret-target.txt", "不得跟随符号链接走出 cwd")
	assert.Len(t, got, 1)
	assert.Contains(t, got, "link-target")
}

func TestSearchFiles_ContextCanceled_Errors(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "target.txt"), nil, 0o644))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := workspacefs.SearchFiles(ctx, dir, "target", false,
		workspacefs.DefaultMaxSearchHits, workspacefs.DefaultMaxSearchDirs)
	require.Error(t, err, "超时/取消要报错,不能返回一份看起来完整的空结果")
	assert.True(t, errors.Is(err, context.Canceled))
}
