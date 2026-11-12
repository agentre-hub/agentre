// Package clienv builds a desktop-app-safe environment for local CLI tools.
package clienv

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"
)

const loginShellPathTimeout = 800 * time.Millisecond

var (
	loginShellPathOnce sync.Once
	loginShellPathVal  string
)

// BuildEnv returns os.Environ merged with caller-provided variables, then
// augments PATH so GUI-launched app bundles can still run Homebrew/npm CLIs.
func BuildEnv(extra map[string]string, binary string) []string {
	return buildEnv(os.Environ(), extra, binary, envFoldCase)
}

// envFoldCase: Windows env names are case-insensitive (the system spells it
// "Path"), and exec keeps only the last of several spellings — after sorting,
// an inherited "Path" would land after, and silently replace, our "PATH".
var envFoldCase = runtime.GOOS == "windows"

// PathValue returns env's PATH as the child process would see it: on Windows
// any spelling counts (an exact "PATH" wins).
func PathValue(env map[string]string) (string, bool) {
	return pathValue(env, envFoldCase)
}

func pathValue(env map[string]string, foldCase bool) (string, bool) {
	if v, ok := env["PATH"]; ok {
		return v, true
	}
	if foldCase {
		for k, v := range env {
			if strings.EqualFold(k, "PATH") {
				return v, true
			}
		}
	}
	return "", false
}

func isPathKey(k string, foldCase bool) bool {
	return k == "PATH" || foldCase && strings.EqualFold(k, "PATH")
}

func buildEnv(environ []string, extra map[string]string, binary string, foldCase bool) []string {
	env := environMap(environ)
	path, ok := pathValue(extra, foldCase)
	if !ok {
		path, _ = pathValue(env, foldCase)
	}
	for k := range env {
		if isPathKey(k, foldCase) {
			delete(env, k)
		}
	}
	for k, v := range extra {
		if !isPathKey(k, foldCase) {
			env[k] = v
		}
	}
	env["PATH"] = SearchPathFrom(path, binary)
	if _, ok := env["HOME"]; !ok {
		if home, err := os.UserHomeDir(); err == nil && strings.TrimSpace(home) != "" {
			env["HOME"] = home
		}
	}

	keys := make([]string, 0, len(env))
	for k := range env {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	out := make([]string, 0, len(keys))
	for _, k := range keys {
		out = append(out, k+"="+env[k])
	}
	return out
}

// ResolveBinary resolves a CLI binary through Agentre's augmented search path.
// Explicit paths are returned unchanged and left for exec.Start to validate.
func ResolveBinary(binary string) (string, bool) {
	binary = strings.TrimSpace(binary)
	if binary == "" {
		return "", false
	}
	if strings.ContainsAny(binary, `/\`) {
		return binary, true
	}
	return ResolveBinaryInPath(binary, SearchPathFrom(os.Getenv("PATH"), binary))
}

// ResolveBinaryForEnv resolves binary using the PATH contained in env.
func ResolveBinaryForEnv(binary string, env []string) (string, bool) {
	binary = strings.TrimSpace(binary)
	if binary == "" {
		return "", false
	}
	if strings.ContainsAny(binary, `/\`) {
		return binary, true
	}
	path, _ := Lookup(env, "PATH")
	return ResolveBinaryInPath(binary, path)
}

// Lookup returns the last value for key in an exec-style env list.
func Lookup(env []string, key string) (string, bool) {
	for i := len(env) - 1; i >= 0; i-- {
		k, v, ok := strings.Cut(env[i], "=")
		if ok && k == key {
			return v, true
		}
	}
	return "", false
}

// ResolveBinaryInPath walks searchPath and returns the first executable file
// that is not inside a macOS .app bundle wrapper.
func ResolveBinaryInPath(binary, searchPath string) (string, bool) {
	binary = strings.TrimSpace(binary)
	if binary == "" {
		return "", false
	}
	if strings.ContainsAny(binary, `/\`) {
		return binary, true
	}
	for _, dir := range filepath.SplitList(searchPath) {
		if dir == "" {
			dir = "."
		}
		candidate := filepath.Join(dir, binary)
		if !IsExecutableFile(candidate) {
			continue
		}
		if IsAppBundleWrapper(candidate) {
			continue
		}
		return candidate, true
	}
	return "", false
}

// SearchPathFrom returns basePath plus CLI locations that GUI-launched macOS
// apps usually miss: Homebrew, npm/node managers, local user bins, and the
// directory containing an explicit binary path.
func SearchPathFrom(basePath, binary string) string {
	seen := map[string]struct{}{}
	entries := make([]string, 0, 32)
	add := func(dir string) {
		dir = strings.TrimSpace(dir)
		if dir == "" {
			return
		}
		if _, ok := seen[dir]; ok {
			return
		}
		seen[dir] = struct{}{}
		entries = append(entries, dir)
	}
	addPath := func(p string) {
		for _, dir := range filepath.SplitList(p) {
			if dir == "" {
				dir = "."
			}
			add(dir)
		}
	}

	add(binaryDir(binary))
	addPath(basePath)
	addPath(loginShellPath())
	for _, dir := range commonPathDirs() {
		add(dir)
	}
	for _, dir := range userToolDirs() {
		add(dir)
	}
	return strings.Join(entries, string(filepath.ListSeparator))
}

// IsExecutableFile matches the standard Unix lookup contract: regular file
// with any execute bit. Windows has no mode-bit equivalent here.
func IsExecutableFile(p string) bool {
	//nolint:gosec // Intentional PATH probing; candidates are only stat'ed.
	info, err := os.Stat(p)
	if err != nil || info.IsDir() {
		return false
	}
	if runtime.GOOS == "windows" {
		return true
	}
	return info.Mode()&0o111 != 0
}

// IsAppBundleWrapper checks both the candidate and its symlink target for a
// macOS app bundle path.
func IsAppBundleWrapper(candidate string) bool {
	if ContainsAppBundle(candidate) {
		return true
	}
	resolved, err := filepath.EvalSymlinks(candidate)
	if err != nil {
		return false
	}
	return ContainsAppBundle(resolved)
}

// ContainsAppBundle reports whether p contains a "<name>.app/Contents/" segment.
func ContainsAppBundle(p string) bool {
	marker := ".app" + string(filepath.Separator) + "Contents" + string(filepath.Separator)
	return strings.Contains(p, marker)
}

func environMap(items []string) map[string]string {
	out := make(map[string]string, len(items))
	for _, item := range items {
		k, v, ok := strings.Cut(item, "=")
		if !ok {
			continue
		}
		out[k] = v
	}
	return out
}

func binaryDir(binary string) string {
	binary = strings.TrimSpace(binary)
	if binary == "" || !strings.ContainsAny(binary, `/\`) {
		return ""
	}
	dir := filepath.Dir(binary)
	if dir == "." {
		return ""
	}
	return dir
}

func loginShellPath() string {
	loginShellPathOnce.Do(func() {
		for _, shell := range candidateShells() {
			if out := readLoginShellPath(shell); strings.TrimSpace(out) != "" {
				loginShellPathVal = strings.TrimSpace(out)
				return
			}
		}
	})
	return loginShellPathVal
}

func candidateShells() []string {
	candidates := []string{}
	if shell := strings.TrimSpace(os.Getenv("SHELL")); shell != "" {
		candidates = append(candidates, shell)
	}
	candidates = append(candidates, "/bin/zsh", "/bin/bash")

	seen := map[string]struct{}{}
	out := make([]string, 0, len(candidates))
	for _, shell := range candidates {
		if !filepath.IsAbs(shell) || !IsExecutableFile(shell) {
			continue
		}
		if _, ok := seen[shell]; ok {
			continue
		}
		seen[shell] = struct{}{}
		out = append(out, shell)
	}
	return out
}

func readLoginShellPath(shell string) string {
	ctx, cancel := context.WithTimeout(context.Background(), loginShellPathTimeout)
	defer cancel()
	// #nosec G204 -- shell is the user's login shell or a fixed system shell;
	// reading PATH from it is the intended desktop-app environment recovery.
	cmd := exec.CommandContext(ctx, shell, "-l", "-c", `printf %s "$PATH"`)
	cmd.Env = os.Environ()
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return string(out)
}

func commonPathDirs() []string {
	return []string{
		"/opt/homebrew/bin",
		"/opt/homebrew/sbin",
		"/usr/local/bin",
		"/usr/local/sbin",
		"/usr/bin",
		"/bin",
		"/usr/sbin",
		"/sbin",
	}
}

func userToolDirs() []string {
	home, err := os.UserHomeDir()
	if err != nil || strings.TrimSpace(home) == "" {
		return nil
	}

	out := []string{
		filepath.Join(home, ".volta", "bin"),
		filepath.Join(home, ".asdf", "shims"),
		filepath.Join(home, ".mise", "shims"),
		filepath.Join(home, ".local", "bin"),
		filepath.Join(home, "bin"),
		filepath.Join(home, ".bun", "bin"),
		filepath.Join(home, ".cargo", "bin"),
	}
	for _, pattern := range []string{
		filepath.Join(home, ".nvm", "versions", "node", "*", "bin"),
		filepath.Join(home, ".fnm", "node-versions", "*", "installation", "bin"),
		filepath.Join(home, ".local", "share", "fnm", "node-versions", "*", "installation", "bin"),
	} {
		matches, _ := filepath.Glob(pattern)
		sort.Strings(matches)
		out = append(out, matches...)
	}
	return out
}
