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
