package clienv

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuildEnvAddsBinaryDirBeforeLaunchdStylePATH(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("PATH separator and Unix-style common dirs differ on Windows")
	}

	binDir := t.TempDir()
	binary := filepath.Join(binDir, "codex")
	t.Setenv("PATH", "/usr/bin:/bin")
	t.Setenv("AGENTRE_CLIENV_PARENT", "from-parent")

	env := BuildEnv(map[string]string{"AGENTRE_CLIENV_PARENT": "from-extra"}, binary)

	path, ok := Lookup(env, "PATH")
	require.True(t, ok)
	parts := filepath.SplitList(path)
	require.NotEmpty(t, parts)
	assert.Equal(t, binDir, parts[0])
	assert.Contains(t, parts, "/usr/bin")
	assert.Contains(t, parts, "/bin")

	got, ok := Lookup(env, "AGENTRE_CLIENV_PARENT")
	require.True(t, ok)
	assert.Equal(t, "from-extra", got)
}

func TestSearchPathFromIncludesCommonMacCLIPaths(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix-style common dirs do not apply to Windows")
	}

	path := SearchPathFrom("/usr/bin:/bin", "")
	parts := filepath.SplitList(path)

	assert.Contains(t, parts, "/opt/homebrew/bin")
	assert.Contains(t, parts, "/usr/local/bin")
	assert.Contains(t, parts, "/usr/bin")
	assert.Contains(t, parts, "/bin")
}

func TestResolveBinaryForEnvUsesAugmentedPATH(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("exec-bit lookup differs on Windows")
	}

	binDir := t.TempDir()
	binary := filepath.Join(binDir, "agentre-cli-test")
	require.NoError(t, os.WriteFile(binary, []byte("#!/bin/sh\nexit 0\n"), 0o755))

	env := BuildEnv(map[string]string{"PATH": binDir}, "agentre-cli-test")
	got, ok := ResolveBinaryForEnv("agentre-cli-test", env)

	require.True(t, ok)
	assert.Equal(t, binary, got)
}

// Windows 的环境变量名不分大小写，系统里一般写作 Path；exec 去重同名不同写法时只留最后
// 一个，而输出按键排序后 "Path" 排在 "PATH" 之后。调用方（env_json、会话 agrctl 目录）
// 给的 PATH 必须是子进程最终看到的那一个，不能被继承来的 Path 盖掉。
func TestBuildEnv_GivenCaseInsensitiveEnv_ThenCallerPathIsTheOnlyPathEntry(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses ':'-separated sample dirs")
	}
	pathEntries := func(env []string) []string {
		var out []string
		for _, kv := range env {
			if k, _, _ := strings.Cut(kv, "="); strings.EqualFold(k, "PATH") {
				out = append(out, kv)
			}
		}
		return out
	}

	t.Run("调用方写 PATH，继承的是 Path", func(t *testing.T) {
		env := buildEnv([]string{"Path=/sys"}, map[string]string{"PATH": "/agr:/sys"}, "", true)
		entries := pathEntries(env)
		require.Len(t, entries, 1)
		assert.True(t, strings.HasPrefix(entries[0], "PATH=/agr:/sys"), entries[0])
	})
	t.Run("调用方写 Path（env_json），继承的是 Path", func(t *testing.T) {
		env := buildEnv([]string{"Path=/sys"}, map[string]string{"Path": "/user"}, "", true)
		entries := pathEntries(env)
		require.Len(t, entries, 1)
		assert.True(t, strings.HasPrefix(entries[0], "PATH=/user"), entries[0])
	})
	t.Run("调用方不写，继承的 Path 仍是基底", func(t *testing.T) {
		env := buildEnv([]string{"Path=/sys"}, nil, "", true)
		entries := pathEntries(env)
		require.Len(t, entries, 1)
		assert.True(t, strings.HasPrefix(entries[0], "PATH=/sys"), entries[0])
	})
	t.Run("大小写敏感的平台上 Path 只是另一个变量", func(t *testing.T) {
		env := buildEnv([]string{"Path=/other", "PATH=/sys"}, map[string]string{"PATH": "/agr:/sys"}, "", false)
		assert.Contains(t, env, "Path=/other")
		v, _ := Lookup(env, "PATH")
		assert.True(t, strings.HasPrefix(v, "/agr:/sys"), v)
	})
}

// 契约：BuildEnvWithout 的 unset 只删**继承来**的变量 —— 调用方 extra 里同名的键照样写进去
// （antigravity 先清掉继承的 GEMINI_API_KEY，再写入会话网关 token）。
func TestBuildEnvWithout_GivenInheritedKeys_WhenUnset_ThenOnlyInheritedCopiesAreRemoved(t *testing.T) {
	t.Setenv("AGENTRE_CLIENV_DROP", "inherited")
	t.Setenv("AGENTRE_CLIENV_REWRITE", "inherited")
	t.Setenv("AGENTRE_CLIENV_KEEP", "inherited")

	env := BuildEnvWithout(
		map[string]string{"AGENTRE_CLIENV_REWRITE": "from-extra"},
		[]string{"AGENTRE_CLIENV_DROP", "AGENTRE_CLIENV_REWRITE"},
		"",
	)

	_, ok := Lookup(env, "AGENTRE_CLIENV_DROP")
	assert.False(t, ok, "an unset key must not survive from the parent environment")
	got, ok := Lookup(env, "AGENTRE_CLIENV_REWRITE")
	require.True(t, ok)
	assert.Equal(t, "from-extra", got, "extra still applies after the inherited copy is cleared")
	got, ok = Lookup(env, "AGENTRE_CLIENV_KEEP")
	require.True(t, ok)
	assert.Equal(t, "inherited", got, "keys outside unset are inherited unchanged")
	_, ok = Lookup(env, "PATH")
	assert.True(t, ok, "PATH augmentation still runs")
}

// Windows 的变量名不区分大小写：继承来的 google_api_key 子进程照样读得到，unset 必须按
// 不区分大小写匹配；大小写敏感的平台上它只是另一个变量。
func TestWithoutKeys_GivenCaseInsensitiveEnv_ThenAnySpellingIsRemoved(t *testing.T) {
	environ := []string{"google_api_key=a", "GOOGLE_API_KEY=b", "OTHER=c"}

	assert.Equal(t, []string{"OTHER=c"}, withoutKeys(environ, []string{"GOOGLE_API_KEY"}, true))
	assert.Equal(t, []string{"google_api_key=a", "OTHER=c"}, withoutKeys(environ, []string{"GOOGLE_API_KEY"}, false))
	assert.Equal(t, environ, withoutKeys(environ, nil, true), "no keys = environment untouched")
}
