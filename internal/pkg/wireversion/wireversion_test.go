package wireversion_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/agentre-hub/agentre/internal/pkg/wireversion"
)

// Given the wire protocol version is owned by @agentre-hub/agentre-wire's
// package.json, When the Go constant is read, Then the two must be byte
// identical — Go cannot read package.json at build time, so this guard is the
// only thing stopping the handshake from advertising a version nobody else
// speaks.
func TestProtocol_GivenWirePackageJSON_WhenCompared_ThenGoConstantMatchesVerbatim(t *testing.T) {
	t.Parallel()

	_, filename, _, ok := runtime.Caller(0)
	require.True(t, ok, "resolve guard test path")
	repoRoot := filepath.Clean(filepath.Join(filepath.Dir(filename), "..", "..", ".."))
	manifestPath := filepath.Join(repoRoot, "frontend", "packages", "agentre-wire", "package.json")

	// manifestPath is derived from this test file's own location inside the repo.
	raw, err := os.ReadFile(manifestPath)
	require.NoError(t, err)

	var manifest struct {
		Version string `json:"version"`
	}
	require.NoError(t, json.Unmarshal(raw, &manifest))
	require.NotEmpty(t, manifest.Version, "agentre-wire package.json carries no version")

	require.Equal(t, manifest.Version, wireversion.Protocol,
		"wireversion.Protocol must be updated together with frontend/packages/agentre-wire/package.json")
}

// Given the version window this round collapses to a single point (both ends
// pinned to 0.1.0 in the spec, see docs/specs/2026-08-31-conversation-centric-
// addressing.md "协议版本窗口"), When the Go constant is read, Then
// MinSupported must be pinned exactly as tightly as Protocol is: to the same
// package.json — leaving it to drift independently would silently open (or
// close) the window without anyone having decided to.
func TestMinSupported_GivenWirePackageJSON_WhenCompared_ThenGoConstantMatchesVerbatim(t *testing.T) {
	t.Parallel()

	_, filename, _, ok := runtime.Caller(0)
	require.True(t, ok, "resolve guard test path")
	repoRoot := filepath.Clean(filepath.Join(filepath.Dir(filename), "..", "..", ".."))
	manifestPath := filepath.Join(repoRoot, "frontend", "packages", "agentre-wire", "package.json")

	raw, err := os.ReadFile(manifestPath)
	require.NoError(t, err)

	var manifest struct {
		Version string `json:"version"`
	}
	require.NoError(t, json.Unmarshal(raw, &manifest))
	require.NotEmpty(t, manifest.Version, "agentre-wire package.json carries no version")

	require.Equal(t, manifest.Version, wireversion.MinSupported,
		"wireversion.MinSupported must be updated together with frontend/packages/agentre-wire/package.json")
}
