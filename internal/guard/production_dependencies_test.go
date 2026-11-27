package guard

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestProductionEntrypointsExcludeE2EDependenciesAndBuildTags(t *testing.T) {
	root := repositoryRoot(t)

	t.Run("Given production entrypoints When dependencies are inspected Then E2E packages are unreachable", func(t *testing.T) {
		for _, pkg := range []string{".", "./cmd/agentred"} {
			cmd := exec.Command("go", "list", "-deps", pkg) //nolint:gosec // G204: package values are fixed literals owned by this guard
			cmd.Dir = root
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("go list %s: %v\n%s", pkg, err, out)
			}
			for _, line := range strings.Split(string(out), "\n") {
				if strings.Contains(line, "github.com/agentre-hub/agentre/e2e/") ||
					strings.Contains(line, "internal/pkg/agentruntime/runtimes/fake") {
					t.Fatalf("production package %s depends on %s", pkg, line)
				}
			}
		}
	})

	t.Run("Given the agrctl companion CLI When its dependencies are inspected Then no service, bootstrap, app, Wails or cago package is reachable", func(t *testing.T) {
		cmd := exec.Command("go", "list", "-deps", "./cmd/agrctl")
		cmd.Dir = root
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("go list ./cmd/agrctl: %v\n%s", err, out)
		}
		if offenders := agrctlForbiddenDeps(string(out)); len(offenders) > 0 {
			t.Fatalf("agrctl must stay a slim client (spec 2026-11-25 agrctl account peer); it reaches %v", offenders)
		}
	})

	t.Run("Given the agrctl dependency rule When it sees a forbidden package Then it names it", func(t *testing.T) {
		listing := strings.Join([]string{
			"github.com/agentre-hub/agentre/internal/pkg/ctlclient",
			"github.com/agentre-hub/agentre/internal/service/server_svc",
			"github.com/agentre-hub/agentre/internal/bootstrap",
			"github.com/agentre-hub/agentre/internal/app",
			"github.com/wailsapp/wails/v2/pkg/runtime",
			"github.com/cago-frame/cago/configs",
		}, "\n")
		if got := agrctlForbiddenDeps(listing); len(got) != 5 {
			t.Fatalf("agrctlForbiddenDeps = %v, want the five forbidden packages and not ctlclient", got)
		}
	})

	t.Run("Given production entrypoints When source imports are inspected Then they contain no E2E imports", func(t *testing.T) {
		for _, source := range []string{filepath.Join(root, "main.go"), filepath.Join(root, "cmd", "agentred", "run.go")} {
			raw, err := os.ReadFile(source) //nolint:gosec // G304: fixed repository-owned entrypoint paths
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(raw), "github.com/agentre-hub/agentre/e2e/") ||
				strings.Contains(string(raw), "internal/pkg/agentruntime/runtimes/fake") {
				t.Fatalf("production entrypoint imports E2E code: %s", source)
			}
		}
	})

	t.Run("Given the rebuilt harness When Go sources are inspected Then no E2E build-tag seam remains", func(t *testing.T) {
		err := walkRepositoryGoFiles(root, func(rel string, content []byte) error {
			for _, line := range strings.Split(string(content), "\n") {
				trimmed := strings.TrimSpace(line)
				if trimmed == "//go:build e2e" || trimmed == "//go:build !e2e" {
					t.Errorf("E2E build tag remains in %s", rel)
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	})
}

// agrctlForbiddenPrefixes 是 agrctl 不许依赖到的包：它只能是一个瘦客户端（ctlclient /
// agrctlcred / 协议包），不能把桌面端的服务、启动装配、Wails 绑定或 cago 运行时拖进来。
var agrctlForbiddenPrefixes = []string{
	"github.com/agentre-hub/agentre/internal/service/",
	"github.com/agentre-hub/agentre/internal/repository/",
	"github.com/agentre-hub/agentre/internal/bootstrap/",
	"github.com/agentre-hub/agentre/internal/app/",
	"github.com/wailsapp/",
	"github.com/cago-frame/cago/",
}

// agrctlForbiddenDeps 从 `go list -deps` 的输出里挑出 agrctl 不许依赖的包。
func agrctlForbiddenDeps(listing string) []string {
	var offenders []string
	for _, line := range strings.Split(listing, "\n") {
		pkg := strings.TrimSpace(line)
		for _, prefix := range agrctlForbiddenPrefixes {
			if pkg == strings.TrimSuffix(prefix, "/") || strings.HasPrefix(pkg, prefix) {
				offenders = append(offenders, pkg)
				break
			}
		}
	}
	return offenders
}

func repositoryRoot(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Clean(filepath.Join(wd, "..", ".."))
}
