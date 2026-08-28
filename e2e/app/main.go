package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"

	"github.com/agentre-hub/agentre/e2e/composition"
	"github.com/agentre-hub/agentre/e2e/preflight"
	"github.com/agentre-hub/agentre/internal/app"
	"github.com/agentre-hub/agentre/internal/bootstrap"
	"github.com/agentre-hub/agentre/internal/desktop"
)

var assets = os.DirFS("frontend/dist")

type e2eDependencies struct {
	validate       func(preflight.Environment) (preflight.Config, error)
	resolveDataDir func() (string, error)
	runtimeDataDir func(*bootstrap.Runtime) string
	install        func(context.Context, preflight.Config) error
	runDesktop     func(context.Context, desktop.Options) error
}

func defaultE2EDependencies() e2eDependencies {
	return e2eDependencies{
		validate:       preflight.Validate,
		resolveDataDir: bootstrap.AppDataDir,
		runtimeDataDir: (*bootstrap.Runtime).DataDir,
		install:        composition.Install,
		runDesktop:     desktop.Run,
	}
}

func main() {
	ctx := context.Background()
	if err := runE2E(ctx, preflight.FromEnvironment(), defaultE2EDependencies()); err != nil {
		log.Fatalf("e2e app: %v", err)
	}
}

func runE2E(ctx context.Context, env preflight.Environment, deps e2eDependencies) error {
	config, err := deps.validate(env)
	if err != nil {
		return fmt.Errorf("preflight: %w", err)
	}
	for _, name := range []string{"AGENTRE_E2E_MANIFEST", "AGENTRE_E2E_TOKEN"} {
		if err := os.Unsetenv(name); err != nil {
			return fmt.Errorf("consume %s: %w", name, err)
		}
	}

	manifestDataDir, err := canonicalExistingPath(config.DataDir)
	if err != nil {
		return storageIsolation("manifest data directory is invalid")
	}
	resolvedDataDir, err := deps.resolveDataDir()
	if err != nil {
		return storageIsolation("bootstrap data directory could not be resolved")
	}
	if err := attestDataDir("bootstrap resolver", resolvedDataDir, manifestDataDir); err != nil {
		return err
	}

	if err := deps.runDesktop(ctx, desktop.Options{
		Assets:      assets,
		RuntimeMode: app.RuntimeModeHeadless,
		AfterBootstrap: func(ctx context.Context, runtime *bootstrap.Runtime) error {
			if err := attestDataDir("bootstrap runtime", deps.runtimeDataDir(runtime), manifestDataDir); err != nil {
				return err
			}
			return deps.install(ctx, config)
		},
	}); err != nil {
		if errors.Is(err, errStorageIsolation) {
			return storageIsolation("desktop bootstrap rejected unsafe storage")
		}
		return fmt.Errorf("desktop run: %w", err)
	}
	return nil
}

func attestDataDir(source, actual string, expectedCanonical string) error {
	actualCanonical, err := canonicalExistingPath(actual)
	if err != nil {
		return storageIsolation(source + " data directory is invalid")
	}
	if actualCanonical != expectedCanonical {
		return storageIsolation(source + " data directory does not match the runner manifest")
	}
	return nil
}

var errStorageIsolation = errors.New("storage isolation")

func storageIsolation(condition string) error {
	return fmt.Errorf("%w: %s; use make e2e (the E2E runner)", errStorageIsolation, condition)
}

func canonicalExistingPath(path string) (string, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return "", err
	}
	return filepath.Clean(resolved), nil
}
