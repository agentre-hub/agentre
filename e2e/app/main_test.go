package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/agentre-hub/agentre/e2e/preflight"
	"github.com/agentre-hub/agentre/internal/app"
	"github.com/agentre-hub/agentre/internal/bootstrap"
	"github.com/agentre-hub/agentre/internal/desktop"
)

func TestRunE2EValidatesBeforeDesktopBootstrap(t *testing.T) {
	preflightErr := errors.New("invalid runner manifest")
	desktopCalled := false

	err := runE2E(context.Background(), preflight.Environment{}, e2eDependencies{
		validate: func(preflight.Environment) (preflight.Config, error) {
			return preflight.Config{}, preflightErr
		},
		resolveDataDir: func() (string, error) {
			t.Fatal("bootstrap data directory resolver must not run when preflight fails")
			return "", nil
		},
		runDesktop: func(context.Context, desktop.Options) error {
			desktopCalled = true
			return nil
		},
	})
	if !errors.Is(err, preflightErr) {
		t.Fatalf("runE2E error = %v, want %v", err, preflightErr)
	}
	if desktopCalled {
		t.Fatal("desktop bootstrap must not run when preflight fails")
	}
}

func TestRunE2ERejectsBootstrapResolverMismatchBeforeDesktopBootstrap(t *testing.T) {
	runRoot := t.TempDir()
	manifestDataDir := filepath.Join(runRoot, "manifest-data")
	resolvedDataDir := filepath.Join(runRoot, "resolved-data")
	for _, path := range []string{manifestDataDir, resolvedDataDir, filepath.Join(runRoot, "keychain")} {
		if err := os.Mkdir(path, 0o700); err != nil {
			t.Fatalf("Mkdir(%q): %v", path, err)
		}
	}
	desktopCalled := false
	compositionCalled := false

	err := runE2E(context.Background(), preflight.Environment{}, e2eDependencies{
		validate: func(preflight.Environment) (preflight.Config, error) {
			return preflight.Config{
				RunRoot:     runRoot,
				DataDir:     manifestDataDir,
				KeychainDir: filepath.Join(runRoot, "keychain"),
			}, nil
		},
		resolveDataDir: func() (string, error) { return resolvedDataDir, nil },
		runtimeDataDir: func(*bootstrap.Runtime) string {
			t.Fatal("runtime data directory must not be read before bootstrap")
			return ""
		},
		install: func(context.Context, preflight.Config) error {
			compositionCalled = true
			return nil
		},
		runDesktop: func(context.Context, desktop.Options) error {
			desktopCalled = true
			return nil
		},
	})
	assertSafeStorageIsolationError(t, err, manifestDataDir, resolvedDataDir)
	if desktopCalled {
		t.Fatal("desktop bootstrap must not run when bootstrap resolver disagrees with the manifest")
	}
	if compositionCalled {
		t.Fatal("composition must not run when bootstrap resolver disagrees with the manifest")
	}
}

func TestRunE2ERedactsBootstrapResolverErrorsBeforeDesktopBootstrap(t *testing.T) {
	runRoot := t.TempDir()
	manifestDataDir := filepath.Join(runRoot, "manifest-data")
	keychainDir := filepath.Join(runRoot, "keychain")
	for _, path := range []string{manifestDataDir, keychainDir} {
		if err := os.Mkdir(path, 0o700); err != nil {
			t.Fatalf("Mkdir(%q): %v", path, err)
		}
	}
	secret := "resolver-secret-must-not-leak"
	secretPath := filepath.Join(runRoot, secret, "bootstrap-data")
	desktopCalled := false

	err := runE2E(context.Background(), preflight.Environment{}, e2eDependencies{
		validate: func(preflight.Environment) (preflight.Config, error) {
			return preflight.Config{
				RunRoot:     runRoot,
				DataDir:     manifestDataDir,
				KeychainDir: keychainDir,
			}, nil
		},
		resolveDataDir: func() (string, error) {
			return "", fmt.Errorf("resolver failed for %s with %s", secretPath, secret)
		},
		runDesktop: func(context.Context, desktop.Options) error {
			desktopCalled = true
			return nil
		},
	})
	assertSafeStorageIsolationError(t, err, manifestDataDir, secretPath, secret)
	if desktopCalled {
		t.Fatal("desktop bootstrap must not run when bootstrap storage resolution fails")
	}
}

func TestRunE2ERejectsRuntimeDataDirMismatchBeforeComposition(t *testing.T) {
	runRoot := t.TempDir()
	manifestDataDir := filepath.Join(runRoot, "manifest-data")
	runtimeDataDir := filepath.Join(runRoot, "runtime-data")
	for _, path := range []string{manifestDataDir, runtimeDataDir, filepath.Join(runRoot, "keychain")} {
		if err := os.Mkdir(path, 0o700); err != nil {
			t.Fatalf("Mkdir(%q): %v", path, err)
		}
	}
	compositionCalled := false

	err := runE2E(context.Background(), preflight.Environment{}, e2eDependencies{
		validate: func(preflight.Environment) (preflight.Config, error) {
			return preflight.Config{
				RunRoot:     runRoot,
				DataDir:     manifestDataDir,
				KeychainDir: filepath.Join(runRoot, "keychain"),
			}, nil
		},
		resolveDataDir: func() (string, error) { return manifestDataDir, nil },
		runtimeDataDir: func(*bootstrap.Runtime) string { return runtimeDataDir },
		install: func(context.Context, preflight.Config) error {
			compositionCalled = true
			return nil
		},
		runDesktop: func(ctx context.Context, opts desktop.Options) error {
			return opts.AfterBootstrap(ctx, &bootstrap.Runtime{})
		},
	})
	assertSafeStorageIsolationError(t, err, manifestDataDir, runtimeDataDir)
	if compositionCalled {
		t.Fatal("composition must not run when bootstrap runtime disagrees with the manifest")
	}
}

func TestRunE2ERejectsUncanonicalRuntimeDataDirWithoutDisclosingFilesystemDetails(t *testing.T) {
	runRoot := t.TempDir()
	manifestDataDir := filepath.Join(runRoot, "manifest-data")
	keychainDir := filepath.Join(runRoot, "keychain")
	for _, path := range []string{manifestDataDir, keychainDir} {
		if err := os.Mkdir(path, 0o700); err != nil {
			t.Fatalf("Mkdir(%q): %v", path, err)
		}
	}
	secret := "runtime-secret-must-not-leak"
	missingRuntimeDataDir := filepath.Join(runRoot, secret, "missing-runtime-data")
	compositionCalled := false

	err := runE2E(context.Background(), preflight.Environment{}, e2eDependencies{
		validate: func(preflight.Environment) (preflight.Config, error) {
			return preflight.Config{
				RunRoot:     runRoot,
				DataDir:     manifestDataDir,
				KeychainDir: keychainDir,
			}, nil
		},
		resolveDataDir: func() (string, error) { return manifestDataDir, nil },
		runtimeDataDir: func(*bootstrap.Runtime) string { return missingRuntimeDataDir },
		install: func(context.Context, preflight.Config) error {
			compositionCalled = true
			return nil
		},
		runDesktop: func(ctx context.Context, opts desktop.Options) error {
			return opts.AfterBootstrap(ctx, &bootstrap.Runtime{})
		},
	})
	assertSafeStorageIsolationError(t, err, manifestDataDir, missingRuntimeDataDir, secret)
	if compositionCalled {
		t.Fatal("composition must not run when bootstrap runtime storage cannot be canonicalized")
	}
}

func TestRunE2ERedactsWrappedStorageIsolationErrorsFromDesktop(t *testing.T) {
	runRoot := t.TempDir()
	dataDir := filepath.Join(runRoot, "data")
	keychainDir := filepath.Join(runRoot, "keychain")
	for _, path := range []string{dataDir, keychainDir} {
		if err := os.Mkdir(path, 0o700); err != nil {
			t.Fatalf("Mkdir(%q): %v", path, err)
		}
	}
	secret := "desktop-storage-secret-must-not-leak"
	secretPath := filepath.Join(runRoot, secret, "unsafe-data")

	err := runE2E(context.Background(), preflight.Environment{}, e2eDependencies{
		validate: func(preflight.Environment) (preflight.Config, error) {
			return preflight.Config{RunRoot: runRoot, DataDir: dataDir, KeychainDir: keychainDir}, nil
		},
		resolveDataDir: func() (string, error) { return dataDir, nil },
		runDesktop: func(context.Context, desktop.Options) error {
			return fmt.Errorf("desktop rejected %s containing %s: %w", secretPath, secret, errStorageIsolation)
		},
	})
	assertSafeStorageIsolationError(t, err, dataDir, secretPath, secret)
}

func TestRunE2ERejectsUncanonicalBootstrapDataDirWithoutDisclosingFilesystemDetails(t *testing.T) {
	runRoot := t.TempDir()
	manifestDataDir := filepath.Join(runRoot, "manifest-data")
	keychainDir := filepath.Join(runRoot, "keychain")
	for _, path := range []string{manifestDataDir, keychainDir} {
		if err := os.Mkdir(path, 0o700); err != nil {
			t.Fatalf("Mkdir(%q): %v", path, err)
		}
	}
	secret := "runner-token-must-not-leak"
	missingDataDir := filepath.Join(runRoot, secret, "missing-data")
	desktopCalled := false

	err := runE2E(context.Background(), preflight.Environment{}, e2eDependencies{
		validate: func(preflight.Environment) (preflight.Config, error) {
			return preflight.Config{
				RunRoot:     runRoot,
				DataDir:     manifestDataDir,
				KeychainDir: keychainDir,
			}, nil
		},
		resolveDataDir: func() (string, error) { return missingDataDir, nil },
		runDesktop: func(context.Context, desktop.Options) error {
			desktopCalled = true
			return nil
		},
	})
	assertSafeStorageIsolationError(t, err, manifestDataDir, missingDataDir, secret)
	if desktopCalled {
		t.Fatal("desktop bootstrap must not run when bootstrap storage cannot be canonicalized")
	}
}

func TestRunE2EAttestsCanonicalStorageBeforeHeadlessCompositionAndWails(t *testing.T) {
	runRoot := t.TempDir()
	dataDir := filepath.Join(runRoot, "data")
	keychainDir := filepath.Join(runRoot, "keychain")
	for _, path := range []string{dataDir, keychainDir} {
		if err := os.Mkdir(path, 0o700); err != nil {
			t.Fatalf("Mkdir(%q): %v", path, err)
		}
	}
	dataAlias := filepath.Join(runRoot, "data-alias")
	if err := os.Symlink(dataDir, dataAlias); err != nil {
		t.Skipf("directory symlink unavailable: %v", err)
	}
	var order []string
	wantConfig := preflight.Config{RunRoot: runRoot, DataDir: dataDir, KeychainDir: keychainDir}

	err := runE2E(context.Background(), preflight.Environment{}, e2eDependencies{
		validate: func(preflight.Environment) (preflight.Config, error) {
			order = append(order, "preflight")
			return wantConfig, nil
		},
		resolveDataDir: func() (string, error) {
			order = append(order, "resolver")
			return dataAlias, nil
		},
		runtimeDataDir: func(*bootstrap.Runtime) string {
			order = append(order, "runtime-data-dir")
			return dataAlias
		},
		install: func(_ context.Context, got preflight.Config) error {
			order = append(order, "composition")
			if got != wantConfig {
				t.Fatalf("composition config = %+v, want %+v", got, wantConfig)
			}
			return nil
		},
		runDesktop: func(ctx context.Context, opts desktop.Options) error {
			order = append(order, "bootstrap")
			if opts.RuntimeMode != app.RuntimeModeHeadless {
				t.Fatalf("RuntimeMode = %q, want %q", opts.RuntimeMode, app.RuntimeModeHeadless)
			}
			if opts.AfterBootstrap == nil {
				t.Fatal("AfterBootstrap must attest storage and install E2E composition")
			}
			if err := opts.AfterBootstrap(ctx, &bootstrap.Runtime{}); err != nil {
				return err
			}
			order = append(order, "wails")
			return nil
		},
	})
	if err != nil {
		t.Fatalf("runE2E: %v", err)
	}
	wantOrder := []string{"preflight", "resolver", "bootstrap", "runtime-data-dir", "composition", "wails"}
	if len(order) != len(wantOrder) {
		t.Fatalf("order = %v, want %v", order, wantOrder)
	}
	for i := range wantOrder {
		if order[i] != wantOrder[i] {
			t.Fatalf("order = %v, want %v", order, wantOrder)
		}
	}
}

func TestRunE2EConsumesManifestCredentialsBeforeBootstrap(t *testing.T) {
	runRoot := t.TempDir()
	dataDir := filepath.Join(runRoot, "data")
	keychainDir := filepath.Join(runRoot, "keychain")
	for _, path := range []string{dataDir, keychainDir} {
		if err := os.Mkdir(path, 0o700); err != nil {
			t.Fatalf("Mkdir(%q): %v", path, err)
		}
	}
	t.Setenv("AGENTRE_E2E_MANIFEST", filepath.Join(runRoot, "manifest.json"))
	t.Setenv("AGENTRE_E2E_TOKEN", "one-time-token")

	err := runE2E(context.Background(), preflight.Environment{}, e2eDependencies{
		validate: func(preflight.Environment) (preflight.Config, error) {
			return preflight.Config{RunRoot: runRoot, DataDir: dataDir, KeychainDir: keychainDir}, nil
		},
		resolveDataDir: func() (string, error) { return dataDir, nil },
		runtimeDataDir: func(*bootstrap.Runtime) string { return dataDir },
		install:        func(context.Context, preflight.Config) error { return nil },
		runDesktop: func(ctx context.Context, opts desktop.Options) error {
			if got := os.Getenv("AGENTRE_E2E_MANIFEST"); got != "" {
				t.Fatalf("AGENTRE_E2E_MANIFEST = %q, want consumed before bootstrap", got)
			}
			if got := os.Getenv("AGENTRE_E2E_TOKEN"); got != "" {
				t.Fatalf("AGENTRE_E2E_TOKEN = %q, want consumed before bootstrap", got)
			}
			return opts.AfterBootstrap(ctx, &bootstrap.Runtime{})
		},
	})
	if err != nil {
		t.Fatalf("runE2E: %v", err)
	}
}

func assertSafeStorageIsolationError(t *testing.T, err error, sensitiveValues ...string) {
	t.Helper()
	if err == nil {
		t.Fatal("runE2E error = nil, want storage isolation failure")
	}
	message := err.Error()
	if !strings.Contains(message, "storage isolation") {
		t.Fatalf("runE2E error = %q, want failed storage isolation condition", message)
	}
	if !strings.Contains(message, "make e2e") {
		t.Fatalf("runE2E error = %q, want canonical runner entry", message)
	}
	for _, sensitive := range sensitiveValues {
		if sensitive != "" && strings.Contains(message, sensitive) {
			t.Fatalf("runE2E error disclosed sensitive filesystem detail")
		}
	}
}
