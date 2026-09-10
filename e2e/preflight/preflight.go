// Package preflight validates the dedicated E2E entrypoint before any
// persistent bootstrap side effect occurs.
package preflight

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/agentre-hub/agentre/internal/pkg/paths"
)

const replayMarkerName = ".agentre-e2e-consumed"

// Environment is the complete process input accepted by the E2E entrypoint.
type Environment struct {
	ManifestPath string
	Token        string
	DataDir      string
	KeychainDir  string
}

// Manifest is written by the runner inside its random run root.
type Manifest struct {
	RunRoot     string `json:"runRoot"`
	DataDir     string `json:"dataDir"`
	KeychainDir string `json:"keychainDir"`
	Token       string `json:"token"`
}

// Config contains only validated, canonical paths. The token is intentionally
// omitted so later composition cannot log it accidentally.
type Config struct {
	RunRoot     string
	DataDir     string
	KeychainDir string
}

// FromEnvironment reads the dedicated entrypoint variables.
func FromEnvironment() Environment {
	return Environment{
		ManifestPath: os.Getenv("AGENTRE_E2E_MANIFEST"),
		Token:        os.Getenv("AGENTRE_E2E_TOKEN"),
		DataDir:      os.Getenv("AGENTRE_DATA_DIR"),
		KeychainDir:  os.Getenv("AGENTRE_KEYCHAIN_DIR"),
	}
}

// Validate rejects unsafe or replayed startup without repairing paths or
// falling back to production defaults.
func Validate(env Environment) (Config, error) {
	manifestPath := strings.TrimSpace(env.ManifestPath)
	if manifestPath == "" {
		return Config{}, unsafe("runner manifest is required")
	}
	if strings.TrimSpace(env.Token) == "" {
		return Config{}, unsafe("runner token is required")
	}

	raw, err := os.ReadFile(manifestPath) //nolint:gosec // path is the untrusted input being validated before bootstrap
	if err != nil {
		return Config{}, unsafe("runner manifest is not readable")
	}
	manifestDir := filepath.Dir(manifestPath)
	if _, err := os.Stat(filepath.Join(manifestDir, replayMarkerName)); err == nil {
		return Config{}, unsafe("runner manifest replay is forbidden")
	}

	var manifest Manifest
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&manifest); err != nil {
		return Config{}, unsafe("runner manifest is invalid")
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return Config{}, unsafe("runner manifest is invalid")
	}
	if manifest.Token == "" || manifest.Token != env.Token {
		return Config{}, unsafe("runner token does not match the manifest")
	}
	manifest.Token = ""

	manifestCanonical, err := canonicalExisting(manifestPath)
	if err != nil {
		return Config{}, unsafe("runner manifest path is invalid")
	}
	runRoot, err := canonicalExisting(manifest.RunRoot)
	if err != nil {
		return Config{}, unsafe("run root is invalid")
	}
	dataDir, err := canonicalExisting(manifest.DataDir)
	if err != nil {
		return Config{}, unsafe("data directory is invalid")
	}
	keychainDir, err := canonicalExisting(manifest.KeychainDir)
	if err != nil {
		return Config{}, unsafe("keychain directory is invalid")
	}

	for label, candidate := range map[string]string{
		"manifest": manifestCanonical,
		"data":     dataDir,
		"keychain": keychainDir,
	} {
		if !within(runRoot, candidate) {
			return Config{}, unsafe(label + " path escapes the run root")
		}
	}

	if strings.TrimSpace(env.DataDir) == "" {
		return Config{}, unsafe("AGENTRE_DATA_DIR must equal the manifest data directory")
	}
	envData, err := canonicalExisting(env.DataDir)
	if err != nil || envData != dataDir {
		return Config{}, unsafe("AGENTRE_DATA_DIR must equal the manifest data directory")
	}
	if strings.TrimSpace(env.KeychainDir) == "" {
		return Config{}, unsafe("AGENTRE_KEYCHAIN_DIR must equal the manifest keychain directory")
	}
	envKeychain, err := canonicalExisting(env.KeychainDir)
	if err != nil || envKeychain != keychainDir {
		return Config{}, unsafe("AGENTRE_KEYCHAIN_DIR must equal the manifest keychain directory")
	}

	if forbiddenDesktopPath(dataDir) || forbiddenDesktopPath(keychainDir) {
		return Config{}, unsafe("storage directories must not be production or development application paths")
	}
	for _, dir := range []string{runRoot, dataDir, keychainDir} {
		if err := requirePrivateDirectory(dir); err != nil {
			return Config{}, unsafe("run directories must exist and be private")
		}
	}

	sanitized, err := json.Marshal(manifest)
	if err != nil || os.WriteFile(manifestCanonical, sanitized, 0o600) != nil {
		return Config{}, unsafe("runner manifest cannot be sanitized")
	}

	marker := filepath.Join(runRoot, replayMarkerName)
	file, err := os.OpenFile(marker, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600) //nolint:gosec // G304: marker is derived from the validated private run root
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			return Config{}, unsafe("runner manifest replay is forbidden")
		}
		return Config{}, unsafe("run root cannot record replay protection")
	}
	if err := file.Close(); err != nil {
		return Config{}, unsafe("run root cannot record replay protection")
	}

	return Config{RunRoot: runRoot, DataDir: dataDir, KeychainDir: keychainDir}, nil
}

func canonicalExisting(path string) (string, error) {
	clean, err := filepath.Abs(strings.TrimSpace(path))
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(filepath.Clean(clean))
}

func within(root, candidate string) bool {
	rel, err := filepath.Rel(root, candidate)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func forbiddenDesktopPath(candidate string) bool {
	base, err := os.UserConfigDir()
	if err != nil {
		return true
	}
	return forbiddenDesktopPathUnder(base, candidate)
}

func forbiddenDesktopPathUnder(base, candidate string) bool {
	base = filepath.Clean(base)
	candidate = filepath.Clean(candidate)
	if candidate == base {
		return true
	}
	for _, dev := range []bool{false, true} {
		forbidden := filepath.Clean(paths.DefaultAppDataDir(base, dev))
		if within(forbidden, candidate) || within(candidate, forbidden) {
			return true
		}
	}
	return false
}

func requirePrivateDirectory(path string) error {
	info, err := os.Stat(path)
	if err != nil || !info.IsDir() {
		return fmt.Errorf("not a directory")
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0 {
		return fmt.Errorf("directory is accessible to other users")
	}
	return nil
}

func unsafe(condition string) error {
	return fmt.Errorf("unsafe E2E startup: %s; start through the E2E runner", condition)
}
