package identity_test

import (
	"testing"

	"github.com/agentre-hub/agentre/internal/daemon/identity"

	"github.com/stretchr/testify/require"
)

func TestDaemonFingerprint_GivenInstanceUUID_WhenDerived_ThenUsesCanonicalSHA256Identity(t *testing.T) {
	require.Equal(t, "sha256:6ca13d52ca70c883e0f0bb101e425a89e8624de51db2d2392593af6a84118090", identity.DaemonFingerprint("abc123"))
}
