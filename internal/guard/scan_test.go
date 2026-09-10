package guard

import (
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// walkRepositoryGoFiles 是本包所有守卫共用的枚举:从仓库根走一遍,把每个 Go 源码文件
// 的仓内相对路径(一律 /)与内容交给 visit。
//
// 守卫要守的是**本仓跟踪的源码**。范围放宽一格的后果不是「多看了几个文件」——同一份
// canonical 文件会在副本里被再数一次,守卫于是把自己报成第二份声明。
func walkRepositoryGoFiles(root string, visit func(rel string, content []byte) error) error {
	return filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			if skipGuardDir(root, path, entry) {
				return filepath.SkipDir
			}
			return nil
		}
		if filepath.Ext(path) != ".go" {
			return nil
		}
		content, err := os.ReadFile(path) //nolint:gosec // 守卫读的是自己从仓库根枚举出的 Go 源码。
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		// 一律用 / 交出去:守卫拿它跟写死的 canonical 路径比(自排除就是这么做的),
		// 按 OS 分隔符比在 Windows 上会落空。
		return visit(filepath.ToSlash(rel), content)
	})
}

// skipGuardDir 说出哪些目录不进枚举。
//
// 构建产物之外还有**嵌套的检出**:本项目自己的 dev-kit 把每一轮的隔离工作区放在
// .dev-kit/worktrees/<name>(.gitignore 里的 /.dev-kit 与 .worktrees/),那是一份完整
// 的仓库副本 —— 走进去,守卫在任何开着 worktree 的检出里判红。那不是「有人抄了第二
// 份」,是守卫看错了范围:副本里的字节一行都不属于本仓。
//
// 两条判据都按「跟踪与否」立论,不写死具体目录名:仓内没有任何一个跟踪的 .go 落在点
// 开头的目录下(git ls-files '*.go' 里一条都没有),而带 .git 标记的子目录本身就是另
// 一个检出的根(worktree 那里是一个 .git 文件,克隆是一个 .git 目录)。
//
// 判据与 internal/pkg/transcript、internal/repository/transcript_repo 两处防漂移守卫
// 同源;那两处各自贴着一份,因为它们在别的包里。
func skipGuardDir(root, path string, entry fs.DirEntry) bool {
	switch entry.Name() {
	case "node_modules", "dist":
		return true
	}
	if path == root {
		return false
	}
	if strings.HasPrefix(entry.Name(), ".") {
		return true
	}
	_, err := os.Stat(filepath.Join(path, ".git"))
	return err == nil
}

// 本包的守卫都从仓库根枚举整棵树,再按源码文本立论。枚举的范围因此是它们共同的前提:
// 范围放宽一格,同一份 canonical 文件就会被数成第二份声明。
//
// 这条用例钉住的正是那个范围。它不依赖真仓库的形状,自己搭一棵树 —— 否则「今天红不红」
// 取决于本机恰好开着几个 worktree,而那不是契约。
func TestWalkRepositoryGoFiles_GivenNestedCheckoutsAndBuildArtifacts_ThenOnlyThisRepositorysSourcesAreVisited(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	write := func(rel, content string) {
		t.Helper()
		path := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	// 本仓自己的源码:唯一该被看见的。
	write("internal/keep/keep.go", "package keep")
	// 不是 Go 源码。
	write("internal/keep/README.md", "# nope")

	// dev-kit 把每一轮的隔离工作区放在 .dev-kit/worktrees/<name>,那是一份完整的仓库
	// 副本。走进去,守卫会在自己的副本里再数一遍同一份文件。
	write(".dev-kit/worktrees/round/internal/keep/keep.go", "package keep")
	// 嵌套检出不一定点开头:worktree 的标记是一个 .git 文件,克隆是一个 .git 目录,
	// 两种都意味着「这底下是另一个检出的根」。
	write("vendored-checkout/.git", "gitdir: /elsewhere")
	write("vendored-checkout/internal/keep/keep.go", "package keep")
	write("nested-clone/.git/HEAD", "ref: refs/heads/main")
	write("nested-clone/internal/keep/keep.go", "package keep")
	// 构建产物。
	write("frontend/node_modules/pkg/pkg.go", "package pkg")
	write("frontend/dist/bundle.go", "package bundle")

	var visited []string
	err := walkRepositoryGoFiles(root, func(rel string, content []byte) error {
		visited = append(visited, rel)
		if string(content) != "package keep" {
			t.Errorf("visited %s with unexpected content %q", rel, content)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(visited)

	want := []string{"internal/keep/keep.go"}
	if len(visited) != len(want) || visited[0] != want[0] {
		t.Errorf("walked files = %v, want %v", visited, want)
	}
}

// 相对路径一律用 / 交出去。守卫拿它跟写死的 canonical 路径比(自排除就是这么做的),
// 在 Windows 上按 OS 分隔符比会落空 —— 每一条声明都会把自己报成第二份。
func TestWalkRepositoryGoFiles_GivenANestedSource_ThenTheRelativePathUsesSlashes(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	path := filepath.Join(root, "internal", "guard", "sample.go")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("package guard"), 0o600); err != nil {
		t.Fatal(err)
	}

	var visited []string
	if err := walkRepositoryGoFiles(root, func(rel string, _ []byte) error {
		visited = append(visited, rel)
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	if len(visited) != 1 || visited[0] != "internal/guard/sample.go" {
		t.Errorf("walked files = %v, want [internal/guard/sample.go]", visited)
	}
}
