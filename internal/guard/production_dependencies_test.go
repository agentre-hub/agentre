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
		err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.IsDir() && (entry.Name() == ".git" || entry.Name() == "node_modules") {
				return filepath.SkipDir
			}
			if entry.IsDir() || filepath.Ext(path) != ".go" {
				return nil
			}
			raw, readErr := os.ReadFile(path) //nolint:gosec // G304: WalkDir supplies paths beneath the repository root
			if readErr != nil {
				return readErr
			}
			for _, line := range strings.Split(string(raw), "\n") {
				trimmed := strings.TrimSpace(line)
				if trimmed == "//go:build e2e" || trimmed == "//go:build !e2e" {
					t.Errorf("E2E build tag remains in %s", path)
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	})
}

func repositoryRoot(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Clean(filepath.Join(wd, "..", ".."))
}
