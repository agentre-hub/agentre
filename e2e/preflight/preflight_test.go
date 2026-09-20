package preflight

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidateRejectsUnsafeStartupBeforeBootstrap(t *testing.T) {
	originalWorkingDirectory, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := os.Chdir(originalWorkingDirectory); err != nil {
			t.Errorf("restore working directory: %v", err)
		}
	}()

	t.Run("Given a valid isolated manifest When validated Then its canonical config is accepted once", func(t *testing.T) {
		env, want := validEnvironment(t)
		got, err := Validate(env)
		if err != nil {
			t.Fatalf("Validate: %v", err)
		}
		if got.RunRoot != want.RunRoot || got.DataDir != want.DataDir || got.KeychainDir != want.KeychainDir {
			t.Fatalf("Config = %+v, want %+v", got, want)
		}
		sanitized := readManifest(t, env.ManifestPath)
		if sanitized.Token != "" {
			t.Fatal("validated manifest must not retain the token on disk")
		}
		if _, err := Validate(env); err == nil || !strings.Contains(err.Error(), "replay") {
			t.Fatalf("second Validate error = %v, want replay rejection", err)
		}
	})

	for _, tc := range []struct {
		name   string
		mutate func(t *testing.T, env *Environment, manifest *Manifest)
	}{
		{"missing token", func(_ *testing.T, env *Environment, _ *Manifest) { env.Token = "" }},
		{"token mismatch", func(_ *testing.T, env *Environment, _ *Manifest) { env.Token = "wrong" }},
		{"missing data override from the data directory", func(t *testing.T, env *Environment, m *Manifest) {
			if err := os.Chdir(m.DataDir); err != nil {
				t.Fatal(err)
			}
			env.DataDir = ""
		}},
		{"missing keychain override from the keychain directory", func(t *testing.T, env *Environment, m *Manifest) {
			if err := os.Chdir(m.KeychainDir); err != nil {
				t.Fatal(err)
			}
			env.KeychainDir = ""
		}},
		{"data path escape", func(t *testing.T, env *Environment, m *Manifest) {
			outside := t.TempDir()
			m.DataDir = outside
			env.DataDir = outside
		}},
		{"symlink escape", func(t *testing.T, env *Environment, m *Manifest) {
			outside := t.TempDir()
			link := filepath.Join(m.RunRoot, "link")
			if err := os.Symlink(outside, link); err != nil {
				t.Skipf("symlink unavailable: %v", err)
			}
			m.DataDir = link
			env.DataDir = link
		}},
		{"default production directory", func(t *testing.T, env *Environment, m *Manifest) {
			base := t.TempDir()
			dir := filepath.Join(base, "agentre")
			if err := os.Mkdir(dir, 0o700); err != nil {
				t.Fatal(err)
			}
			m.RunRoot = base
			m.DataDir = dir
			m.KeychainDir = filepath.Join(base, "keychain")
			if err := os.Mkdir(m.KeychainDir, 0o700); err != nil {
				t.Fatal(err)
			}
			env.DataDir = dir
			env.KeychainDir = m.KeychainDir
		}},
		{"unsafe permissions", func(t *testing.T, _ *Environment, m *Manifest) {
			if err := os.Chmod(m.DataDir, 0o755); err != nil {
				t.Fatal(err)
			}
		}},
	} {
		t.Run("Given "+tc.name+" When validated Then startup is rejected without leaking the token", func(t *testing.T) {
			env, _ := validEnvironment(t)
			manifest := readManifest(t, env.ManifestPath)
			tc.mutate(t, &env, &manifest)
			writeManifest(t, env.ManifestPath, manifest)
			_, err := Validate(env)
			if err == nil {
				t.Fatal("Validate succeeded, want rejection")
			}
			if strings.Contains(err.Error(), env.Token) && env.Token != "" {
				t.Fatalf("error leaked token: %v", err)
			}
		})
	}
}

func TestForbiddenDesktopPathUnderConfigRoot(t *testing.T) {
	base := t.TempDir()
	leaves := []string{"agentre", "agentre-beta", "agentre-nightly", "agentre-dev"}
	candidates := make([]string, 0, 1+2*len(leaves))
	candidates = append(candidates, base)
	for _, leaf := range leaves {
		dir := filepath.Join(base, leaf)
		candidates = append(candidates, dir, filepath.Join(dir, "nested"))
	}
	for _, candidate := range candidates {
		if !forbiddenDesktopPathUnder(base, candidate) {
			t.Errorf("forbiddenDesktopPathUnder(%q, %q) = false, want true", base, candidate)
		}
	}
	if forbiddenDesktopPathUnder(base, filepath.Join(t.TempDir(), "isolated")) {
		t.Fatal("unrelated isolated path must remain allowed")
	}
}

func validEnvironment(t *testing.T) (Environment, Config) {
	t.Helper()
	root := t.TempDir()
	if err := os.Chmod(root, 0o700); err != nil {
		t.Fatal(err)
	}
	data := filepath.Join(root, "data")
	keychain := filepath.Join(root, "keychain")
	if err := os.Mkdir(data, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(keychain, 0o700); err != nil {
		t.Fatal(err)
	}
	manifestPath := filepath.Join(root, "manifest.json")
	token := "test-secret-token"
	m := Manifest{RunRoot: root, DataDir: data, KeychainDir: keychain, Token: token}
	writeManifest(t, manifestPath, m)
	canonicalRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	return Environment{ManifestPath: manifestPath, Token: token, DataDir: data, KeychainDir: keychain}, Config{RunRoot: canonicalRoot, DataDir: filepath.Join(canonicalRoot, "data"), KeychainDir: filepath.Join(canonicalRoot, "keychain")}
}
func writeManifest(t *testing.T, path string, m Manifest) {
	t.Helper()
	raw, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
}
func readManifest(t *testing.T, path string) Manifest {
	t.Helper()
	raw, err := os.ReadFile(path) //nolint:gosec // G304: test helper reads its own run-scoped manifest path
	if err != nil {
		t.Fatal(err)
	}
	var m Manifest
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	return m
}
