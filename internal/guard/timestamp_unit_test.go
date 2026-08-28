package guard

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// secondsClockCall 是「按秒取当前时间」的调用形状。桌面库里每一列时间戳都是
// 毫秒 epoch(chat/project/issue/sync 一直如此),用它给实体赋值会让同一列里
// 混进相差一千倍的两种值——app_settings.updatetime 就这样混过,而整表换算
// 又会把已是毫秒的行推到公元 58000 年。单位只能在写入端保持唯一。
const secondsClockCall = "time.Now().Unix()"

// timestampScanRoots 是「写桌面主库」的三层。internal/daemon 不在其中:
// 它的 OAuth 凭据落在 JSON state 文件而非数据库列,expires_at 的秒语义
// 由 OAuth 的 expires_in 决定,不归本仓库的存储契约管。
var timestampScanRoots = []string{
	filepath.Join("internal", "service"),
	filepath.Join("internal", "repository"),
	filepath.Join("internal", "bootstrap"),
}

func TestDatabaseTimestampsAreWrittenInMilliseconds(t *testing.T) {
	root := repositoryRoot(t)

	t.Run("Given the layers that write the desktop database When their sources are inspected Then none reads the clock in seconds", func(t *testing.T) {
		for _, rel := range timestampScanRoots {
			walkGoSources(t, filepath.Join(root, rel), func(path string, raw []byte) {
				for i, line := range strings.Split(string(raw), "\n") {
					if strings.Contains(line, secondsClockCall) {
						t.Errorf("%s:%d writes a seconds timestamp: %s\nuse time.Now().UnixMilli() — every stored time column is a millisecond epoch",
							mustRel(t, root, path), i+1, strings.TrimSpace(line))
					}
				}
			})
		}
	})
}

func walkGoSources(t *testing.T, dir string, visit func(path string, raw []byte)) {
	t.Helper()
	err := filepath.WalkDir(dir, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || filepath.Ext(path) != ".go" || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		raw, readErr := os.ReadFile(path) //nolint:gosec // G304: WalkDir supplies paths beneath the repository root
		if readErr != nil {
			return readErr
		}
		visit(path, raw)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func mustRel(t *testing.T, root, path string) string {
	t.Helper()
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return path
	}
	return rel
}
