package guard

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// 这两个守卫针对同一种失败形态：workflow 里的字符串与仓库现状悄悄脱节，而构建依然全绿。
//
// -X 指向一个不存在的符号时，Go linker 不报错也不警告，只是什么都不注入——发布产物里
// CommitID 就是空串。同理，一条 sed 的匹配串与被处理的文件不再对应时，sed 退出码是 0、
// 输出是原文，nightly 的安装脚本于是仍然指向稳定版下载地址。两者都不会让 CI 变红。

var ldflagSymbol = regexp.MustCompile(`-X\s+(\S*buildinfo\.CommitID)=`)

// TestReleaseWorkflowsInjectRealBuildInfoSymbol 守住所有 workflow 注入的 CommitID
// 符号路径与 internal/buildinfo 的真实 import path 一致。
func TestReleaseWorkflowsInjectRealBuildInfoSymbol(t *testing.T) {
	root := repositoryRoot(t)

	cmd := exec.Command("go", "list", "-f", "{{.ImportPath}}", "./internal/buildinfo")
	cmd.Dir = root
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go list ./internal/buildinfo: %v\n%s", err, out)
	}
	want := strings.TrimSpace(string(out)) + ".CommitID"

	found := 0
	for _, path := range workflowFiles(t, root) {
		content, err := os.ReadFile(path) //nolint:gosec // 测试守卫读取仓库内枚举出的 workflow 定义。
		if err != nil {
			t.Fatal(err)
		}
		for _, match := range ldflagSymbol.FindAllStringSubmatch(string(content), -1) {
			found++
			if match[1] != want {
				rel, relErr := filepath.Rel(root, path)
				if relErr != nil {
					t.Fatal(relErr)
				}
				t.Errorf("%s 注入 %q，但真实符号是 %q；linker 对未知 -X 符号既不报错也不注入，"+
					"产物的 CommitID 会是空串", rel, match[1], want)
			}
		}
	}

	// 自证不空过：一个 -X 都没找到时全绿是没有意义的，那既可能是「都对」，
	// 也可能是正则再也匹配不上 workflow 的写法而一处都没检查。
	if found == 0 {
		t.Fatal("no -X ...buildinfo.CommitID found in any workflow; the guard would pass vacuously")
	}
}

// sedRewrite 匹配形如 sed 's#<搜索>#<替换>#g' <文件> 的调用。
var sedRewrite = regexp.MustCompile(`sed\s+'s#([^#]+)#([^#]+)#g'\s+(\S+)`)

// TestWorkflowSedRewritesAreNotNoOps 守住 workflow 里对仓库内脚本做的字符串改写确实
// 还能匹配上——匹配不上时 sed 静默输出原文，nightly 安装脚本会指向稳定版而非 nightly。
func TestWorkflowSedRewritesAreNotNoOps(t *testing.T) {
	root := repositoryRoot(t)

	found := 0
	for _, path := range workflowFiles(t, root) {
		content, err := os.ReadFile(path) //nolint:gosec // 测试守卫读取仓库内枚举出的 workflow 定义。
		if err != nil {
			t.Fatal(err)
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			t.Fatal(err)
		}
		for _, match := range sedRewrite.FindAllStringSubmatch(string(content), -1) {
			search, target := match[1], match[3]
			// 只检查改写仓库内文件的那些；对流水线中间产物的改写这里看不到。
			script, err := os.ReadFile(filepath.Join(root, target)) //nolint:gosec // target 取自仓库内的 workflow 定义。
			if err != nil {
				continue
			}
			found++
			if !strings.Contains(string(script), search) {
				t.Errorf("%s 对 %s 的改写匹配不到 %q；sed 会静默输出原文", rel, target, search)
			}
		}
	}

	if found == 0 {
		t.Fatal("no in-repo sed rewrite found in any workflow; the guard would pass vacuously")
	}
}

func workflowFiles(t *testing.T, root string) []string {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join(root, ".github", "workflows", "*.yml"))
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) == 0 {
		t.Fatalf("no workflows under %s", filepath.Join(root, ".github", "workflows"))
	}
	return paths
}
