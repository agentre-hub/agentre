package goldenvectors_test

import (
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/agentre-hub/agentre/pkg/wire/goldenvectors"
)

// writeVectors 把全部向量写进 dir。落盘与守卫的临时目录共用这一条路径,
// 守卫比的因此就是生成器此刻会写出的东西。
func writeVectors(t *testing.T, dir string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(dir, 0o755), "创建向量目录")
	for _, v := range goldenvectors.Vectors() {
		bin, err := goldenvectors.Binary(v)
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(filepath.Join(dir, v.Name+".bin"), bin, 0o644))

		expected, err := goldenvectors.ExpectedJSON(v)
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(filepath.Join(dir, v.Name+".json"), expected, 0o644))
	}
}

func listNames(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	require.NoError(t, err, "读向量目录 %s", dir)
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	return names
}

// TestVectorsFresh 新鲜度守卫:已签入的向量必须就是生成器此刻会写出的东西。
//
// 没有它,给 schema 加一个字段、向量却还是旧形状时,Swift 侧对着旧向量照样全绿 ——
// 新字段就在另一个语言实现里静默消失了,而两端都说自己没问题。
func TestVectorsFresh(t *testing.T) {
	t.Parallel()

	fresh := filepath.Join(t.TempDir(), goldenvectors.Dir)
	writeVectors(t, fresh)

	committed := goldenvectors.Dir
	require.Equal(t, listNames(t, fresh), listNames(t, committed),
		"向量文件集与生成器不一致;重新生成:%s", goldenvectors.RegenCmd)

	for _, name := range listNames(t, fresh) {
		//nolint:gosec // 两个目录都是本测试自己写出来的,文件名取自它们的目录列表。
		want, err := os.ReadFile(filepath.Join(fresh, name))
		require.NoError(t, err)
		//nolint:gosec // 同上:committed 是本包下签入的向量目录。
		got, err := os.ReadFile(filepath.Join(committed, name))
		require.NoError(t, err)
		require.Equal(t, want, got, "向量 %s 已过期;重新生成:%s", name, goldenvectors.RegenCmd)
	}
}

// TestVectorNamesAreUnique 自证向量集没有同名条目 —— 同名会在落盘时互相覆盖,
// 文件数对不上但每个文件都"新鲜",上面那条守卫看不出来。
func TestVectorNamesAreUnique(t *testing.T) {
	t.Parallel()

	seen := map[string]bool{}
	for _, v := range goldenvectors.Vectors() {
		require.False(t, seen[v.Name], "向量名 %q 重复", v.Name)
		seen[v.Name] = true
	}
	require.NotEmpty(t, seen, "向量集为空,所有对拍都会空过")
}

// TestWriteVectors 重新生成这个动作本身,带 WIRE_VECTORS_WRITE=1 才执行。
func TestWriteVectors(t *testing.T) {
	if os.Getenv(goldenvectors.WriteEnv) != "1" {
		t.Skipf("设 %s=1 重新生成金向量", goldenvectors.WriteEnv)
	}
	writeVectors(t, goldenvectors.Dir)
}
