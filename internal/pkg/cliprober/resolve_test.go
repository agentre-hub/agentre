package cliprober

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/agentre-hub/agentre/internal/pkg/clienv"
)

func writeExecutable(t *testing.T, dir, name, body string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("shell-script fake binary not portable to windows")
	}
	p := filepath.Join(dir, name)
	require.NoError(t, os.WriteFile(p, []byte("#!/bin/sh\n"+body+"\n"), 0o755))
	return p
}

func TestResolveCLIPath_FoundInFakePATH(t *testing.T) {
	dir := t.TempDir()
	want := writeExecutable(t, dir, "claude", "echo claude")
	t.Setenv("PATH", dir)

	path, found, err := ResolveCLIPath("claudecode")
	require.NoError(t, err)
	assert.True(t, found)
	assert.Equal(t, want, path)
}

func TestResolveCLIPath_NotFound_NoError(t *testing.T) {
	// clienv.ResolveBinary 会把 /opt/homebrew/bin、~/.local/bin 等都补进搜索路径,
	// 这是桌面 GUI 进程恢复 PATH 的有意行为,无法用 t.Setenv("PATH", ...) 屏蔽。
	// 在装了 claude 的开发机上跳过,只在干净环境真正验证 not-found 语义。
	if path, ok := clienv.ResolveBinary("claude"); ok {
		t.Skipf("system has claude at %q; cannot test not-found on this host", path)
	}
	t.Setenv("PATH", t.TempDir())
	path, found, err := ResolveCLIPath("claudecode")
	require.NoError(t, err)
	assert.False(t, found)
	assert.Empty(t, path)
}

func TestResolveCLIPath_InvalidType(t *testing.T) {
	_, _, err := ResolveCLIPath("nonsense")
	require.Error(t, err)
}

func TestScanAllCLIs_ReturnsAllThreeTypes(t *testing.T) {
	results := ScanAllCLIs()
	require.Len(t, results, 3)

	// Must be in sorted order.
	assert.Equal(t, "claudecode", results[0].BackendType)
	assert.Equal(t, "codex", results[1].BackendType)
	assert.Equal(t, "piagent", results[2].BackendType)

	// All must have correct binary names.
	assert.Equal(t, "claude", results[0].BinaryName)
	assert.Equal(t, "codex", results[1].BinaryName)
	assert.Equal(t, "pi", results[2].BinaryName)

	// At minimum Found/Path must be consistent: Found=true → Path != ""
	for _, r := range results {
		if r.Found {
			assert.NotEmpty(t, r.Path, "%s: Found=true but Path is empty", r.BackendType)
		} else {
			assert.Empty(t, r.Path, "%s: Found=false but Path is %q", r.BackendType, r.Path)
		}
	}
}

func TestScanAllCLIs_FoundIsConsistentWithResolveCLIPath(t *testing.T) {
	for _, bt := range []string{"claudecode", "codex", "piagent"} {
		wantPath, wantFound, err := ResolveCLIPath(bt)
		require.NoError(t, err)

		results := ScanAllCLIs()
		var got CLIProbeResult
		for _, r := range results {
			if r.BackendType == bt {
				got = r
				break
			}
		}
		assert.Equal(t, wantFound, got.Found, "%s: Found mismatch", bt)
		assert.Equal(t, wantPath, got.Path, "%s: Path mismatch", bt)
	}
}

func TestResolveCLIPath_SkipAppBundleWrapper(t *testing.T) {
	wrapperDir := filepath.Join(t.TempDir(), "Claude.app", "Contents", "MacOS")
	require.NoError(t, os.MkdirAll(wrapperDir, 0o755))
	writeExecutable(t, wrapperDir, "claude", "echo wrapper")

	realDir := t.TempDir()
	want := writeExecutable(t, realDir, "claude", "echo real")

	t.Setenv("PATH", wrapperDir+string(os.PathListSeparator)+realDir)
	path, found, err := ResolveCLIPath("claudecode")
	require.NoError(t, err)
	assert.True(t, found)
	assert.Equal(t, want, path)
}
