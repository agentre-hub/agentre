package guard_test

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// swiftGeneratedRel 是 wire 协议 Swift 产物在本仓唯一被允许的位置(相对仓库根),
// 与 buf.gen.yaml 里那条 Swift 插件的 out: 同一个目录。
const swiftGeneratedRel = "pkg/wire/swift/Sources/AgentreWire/Generated"

// repoRoot 从本包向上定位 agentre 仓库根。guard 住在 pkg/wire/guard 下,
// 因此固定是三级。
func repoRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", "..", ".."))
	require.NoError(t, err)
	require.DirExists(t, filepath.Join(root, "pkg", "wire", "proto"),
		"仓库根定位错了:%s 下没有 pkg/wire/proto", root)
	return root
}

// TestRepositoryShipsOneGeneratedWireSwiftCopy 守住本仓不会长出第二份生成的 wire Swift。
//
// 与 Go 侧「全工作区只有一份 wire.pb.go」同一条纪律,理由也一样:两份生成代码在编译期
// 同时合法,漂移只在两端同时上线时才暴露。Swift 侧尤其容易长出第二份 —— 消费方是另一个
// 仓库的 SwiftPM target,"先在本仓拷一份用着"比钉 revision 省事得多,而那正是 agentre-server
// 当年把 wire.pb.go 拷坏过的那条路。
func TestRepositoryShipsOneGeneratedWireSwiftCopy(t *testing.T) {
	t.Parallel()

	root := repoRoot(t)
	want := filepath.Join(root, filepath.FromSlash(swiftGeneratedRel), "wire.pb.swift")

	scanned := 0
	found := make([]string, 0, 1)
	require.NoError(t, filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			switch entry.Name() {
			// .build / .swiftpm 是 SwiftPM 的本地构建产物(依赖源码会被拷进去),
			// 不是本仓签入的东西。
			case ".git", "node_modules", "dist", ".build", ".swiftpm":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(entry.Name(), ".swift") {
			return nil
		}
		scanned++
		if strings.HasSuffix(entry.Name(), ".pb.swift") {
			found = append(found, path)
		}
		return nil
	}))

	// 自证不空过:一个 Swift 源文件都没扫到时全绿是没有意义的 —— 那既可能是"没人违规",
	// 也可能是 root 算错了而根本没走到任何文件。
	require.NotZero(t, scanned, "扫过 %s 却没有任何 Swift 源码,守卫会空过", root)

	rel := make([]string, 0, len(found))
	for _, p := range found {
		r, err := filepath.Rel(root, p)
		require.NoError(t, err)
		rel = append(rel, r)
	}
	wantRel, err := filepath.Rel(root, want)
	require.NoError(t, err)
	require.Equal(t, []string{wantRel}, rel,
		"生成的 wire Swift 只应有一份,住在 %s;消费方钉本仓已推送的 revision,不拷贝", swiftGeneratedRel)
}

// TestGeneratedSwiftDirectoryHoldsOnlyGeneratedFiles 守住那个目录里没有手写文件。
//
// buf.gen.yaml 带 clean: true —— 每次 `buf generate` 先清空全部 out: 目录。往里放一个
// 手写的扩展或辅助文件,它会在下一次重新生成时无声消失,而删除发生在别人的机器上、
// 别的任务里,现场早就不在了。
func TestGeneratedSwiftDirectoryHoldsOnlyGeneratedFiles(t *testing.T) {
	t.Parallel()

	dir := filepath.Join(repoRoot(t), filepath.FromSlash(swiftGeneratedRel))
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	require.NotEmpty(t, entries, "%s 是空的:Swift 产物没有生成", swiftGeneratedRel)

	for _, e := range entries {
		require.False(t, e.IsDir(), "%s 下不应有子目录:%s", swiftGeneratedRel, e.Name())
		require.True(t, strings.HasSuffix(e.Name(), ".pb.swift"),
			"%s 带 clean: true,只能放生成物;%s 会在下一次 buf generate 时被删掉,请移到目录之外",
			swiftGeneratedRel, e.Name())
	}
}
