package agentredipc

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGivenDataDirectoryWhenDerivingEndpointsThenUnixPathIsPreservedAndWindowsPipeIsOpaque(t *testing.T) {
	dataDir := filepath.Join("tmp", "agentred", "alice-private")

	assert.Equal(t, filepath.Join(dataDir, "agentred.sock"), unixSocketPath(dataDir))

	first := windowsPipePath(`C:\Users\Alice\AppData\Roaming\agentred`)
	second := windowsPipePath(`c:/users/alice/appdata/roaming/agentred/`)
	require.Equal(t, first, second, "equivalent Windows data-directory spellings must address the same daemon")
	assert.True(t, strings.HasPrefix(first, `\\.\pipe\agentred-`))
	assert.NotContains(t, strings.ToLower(first), "alice")
	assert.NotContains(t, strings.ToLower(first), "appdata")
	assert.NotEqual(t, first, windowsPipePath(`C:\Users\Alice\AppData\Roaming\agentred-other`))
}

func TestGivenCurrentUserSIDWhenBuildingPipeACLThenOnlyThatSIDGetsFullAccess(t *testing.T) {
	sid := "S-1-5-21-111-222-333-1001"

	assert.Equal(t, "D:P(A;;GA;;;"+sid+")", securityDescriptorForSID(sid))
}
