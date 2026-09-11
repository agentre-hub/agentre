package lancert_test

import (
	"context"
	"crypto/x509"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/agentre-hub/agentre/internal/daemon/lancert"
)

func TestLoadOrCreate_GivenNoStoredCertificate_ThenGeneratesPersistsAndReusesIt(t *testing.T) {
	dir := t.TempDir()

	first, err := lancert.LoadOrCreate(context.Background(), dir)
	require.NoError(t, err)
	require.NotEmpty(t, first.Certificate)
	leaf, err := x509.ParseCertificate(first.Certificate[0])
	require.NoError(t, err)
	assert.Contains(t, leaf.ExtKeyUsage, x509.ExtKeyUsageServerAuth, "the certificate is served by a TLS server")

	certFile, keyFile := lancert.Paths(dir)
	assert.FileExists(t, certFile)
	if runtime.GOOS != "windows" {
		info, err := os.Stat(keyFile)
		require.NoError(t, err)
		assert.Equal(t, os.FileMode(0o600), info.Mode().Perm(), "the private key is readable by the daemon's user only")
	}

	second, err := lancert.LoadOrCreate(context.Background(), dir)
	require.NoError(t, err)
	assert.Equal(t, first.Certificate[0], second.Certificate[0], "a restart must present the certificate a desktop already pinned")
}

func TestLoadOrCreate_GivenAnUnreadableStoredKey_ThenReplacesThePairAndReusesTheReplacement(t *testing.T) {
	dir := t.TempDir()
	original, err := lancert.LoadOrCreate(context.Background(), dir)
	require.NoError(t, err)
	_, keyFile := lancert.Paths(dir)
	require.NoError(t, os.WriteFile(keyFile, []byte("not a key"), 0o600))

	replaced, err := lancert.LoadOrCreate(context.Background(), dir)
	require.NoError(t, err)
	assert.NotEqual(t, original.Certificate[0], replaced.Certificate[0])

	again, err := lancert.LoadOrCreate(context.Background(), dir)
	require.NoError(t, err)
	assert.Equal(t, replaced.Certificate[0], again.Certificate[0], "the replacement is persisted, not regenerated on every start")
}

// An intact pair that merely cannot be read right now (permissions, EIO, fd
// exhaustion) is not a missing or broken certificate: replacing it would
// silently break every desktop's pin, so the error surfaces instead.
func TestLoadOrCreate_GivenAnIntactPairThatCannotBeReadRightNow_ThenReturnsAnErrorAndKeepsIt(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("file mode 000 does not deny reads here")
	}
	dir := t.TempDir()
	original, err := lancert.LoadOrCreate(context.Background(), dir)
	require.NoError(t, err)
	certFile, _ := lancert.Paths(dir)
	require.NoError(t, os.Chmod(certFile, 0o000))
	t.Cleanup(func() { _ = os.Chmod(certFile, 0o600) })

	_, err = lancert.LoadOrCreate(context.Background(), dir)
	require.Error(t, err)

	require.NoError(t, os.Chmod(certFile, 0o600))
	kept, err := lancert.LoadOrCreate(context.Background(), dir)
	require.NoError(t, err)
	assert.Equal(t, original.Certificate[0], kept.Certificate[0], "the pinned certificate must not be replaced")
}

func TestLoadOrCreate_GivenADataDirThatCannotHoldTheFiles_ThenReturnsAnError(t *testing.T) {
	t.Run("the data dir path runs through a regular file", func(t *testing.T) {
		blocker := filepath.Join(t.TempDir(), "file")
		require.NoError(t, os.WriteFile(blocker, nil, 0o600))

		_, err := lancert.LoadOrCreate(context.Background(), filepath.Join(blocker, "agentred"))
		require.Error(t, err)
	})

	t.Run("the key path is occupied by a directory", func(t *testing.T) {
		dir := t.TempDir()
		_, keyFile := lancert.Paths(dir)
		require.NoError(t, os.Mkdir(keyFile, 0o700))

		_, err := lancert.LoadOrCreate(context.Background(), dir)
		require.Error(t, err)
	})
}
