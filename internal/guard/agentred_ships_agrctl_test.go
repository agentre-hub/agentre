package guard

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// agentred 启动时从「自己旁边」装 agrctl(agrctlinstall.BundledSourcePath:可执行文件同目录
// 的 agrctl),所以每一种把 agentred 交出去的形态都得把同平台的 agrctl 放在它旁边 ——
// 少了它编译、单测全绿,只有装到机器上才发现会话里没有 agrctl(规格 2026-09-22
// agrctl-resource-management「Executors / agentred」)。

func TestGivenTheAgentredReleaseArchiveWhenPackagedThenItShipsAgrctlForTheSamePlatform(t *testing.T) {
	t.Parallel()
	recipe := makeRecipe(t, "agentred-package")

	builds := regexp.MustCompile(`GOOS=\$\(AGENTRED_GOOS\) GOARCH=\$\(AGENTRED_GOARCH\) go build [^\n]*-o "[^"]*/agrctl(\.exe)?" \./cmd/agrctl`).FindAllString(recipe, -1)
	if len(builds) != 2 {
		t.Fatalf("agentred-package must cross-build ./cmd/agrctl with AGENTRED_GOOS/AGENTRED_GOARCH on both the windows and the unix branch; found %d:\n%s", len(builds), recipe)
	}
	if !strings.Contains(recipe, "zip -q \"../$(AGENTRED_PACKAGE_NAME).zip\" agentred.exe agrctl.exe") {
		t.Errorf("the windows zip must carry agrctl.exe next to agentred.exe:\n%s", recipe)
	}
	if !strings.Contains(recipe, "-czf \"$(AGENTRED_DIST_DIR)/$(AGENTRED_PACKAGE_NAME).tar.gz\" agentred agrctl") {
		t.Errorf("the tarball must carry agrctl next to agentred:\n%s", recipe)
	}
}

func TestGivenTheAgentredImageWhenBuiltFromSourceThenAgrctlSitsNextToAgentred(t *testing.T) {
	t.Parallel()
	root := repositoryRoot(t)
	raw, err := os.ReadFile(filepath.Join(root, "deploy", "Dockerfile")) //nolint:gosec // 守卫读取仓库内固定相对路径。
	if err != nil {
		t.Fatal(err)
	}
	dockerfile := string(raw)
	if !strings.Contains(dockerfile, "-o /out/agrctl ./cmd/agrctl") {
		t.Error("deploy/Dockerfile's build stage must build ./cmd/agrctl to /out/agrctl")
	}
	if !strings.Contains(dockerfile, "COPY --from=build /out/agrctl /usr/local/bin/agrctl") {
		t.Error("deploy/Dockerfile's full image must put agrctl next to /usr/local/bin/agentred")
	}
}

// makeRecipe 取出 Makefile 里某个 target 的 recipe(直到下一个空行后的非缩进行)。
func makeRecipe(t *testing.T, target string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(repositoryRoot(t), "Makefile"))
	if err != nil {
		t.Fatal(err)
	}
	_, rest, ok := strings.Cut(string(raw), "\n"+target+":")
	if !ok {
		t.Fatalf("Makefile has no %s target", target)
	}
	var lines []string
	for _, line := range strings.Split(rest, "\n")[1:] {
		if line != "" && !strings.HasPrefix(line, "\t") && !strings.HasPrefix(line, "ifeq") &&
			!strings.HasPrefix(line, "else") && !strings.HasPrefix(line, "endif") {
			break
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}
