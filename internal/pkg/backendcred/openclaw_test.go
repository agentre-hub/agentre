package backendcred

import (
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/agentre-hub/agentre/internal/pkg/keychain"
)

func TestOpenClawTokenAccount_GivenSyncIDs_ThenEachBackendHasItsOwnStableSlot(t *testing.T) {
	first := OpenClawTokenAccount("01J8SYNCA")
	assert.Equal(t, first, OpenClawTokenAccount(" 01J8SYNCA "), "the slot must not depend on surrounding whitespace")
	assert.NotEqual(t, first, OpenClawTokenAccount("01J8SYNCB"))
	assert.NotContains(t, first, "\n")
}

func TestOpenClawTokenAccount_GivenBlankSyncID_ThenNoSharedSlot(t *testing.T) {
	assert.Empty(t, OpenClawTokenAccount(""))
	assert.Empty(t, OpenClawTokenAccount("   "))
}

func TestOpenClawIdentity_GivenEmptyStore_ThenGeneratesOnceAndReusesTheSeed(t *testing.T) {
	store := keychain.NewMemory()

	first, err := OpenClawIdentity(store)
	require.NoError(t, err)
	seed, err := store.Get(OpenClawIdentityAccount)
	require.NoError(t, err)
	require.NotEmpty(t, seed)

	second, err := OpenClawIdentity(store)
	require.NoError(t, err)
	assert.Equal(t, first.ID(), second.ID())
}

func TestOpenClawIdentity_GivenConcurrentFirstUse_ThenOneIdentityWins(t *testing.T) {
	store := keychain.NewMemory()
	ids := make([]string, 8)
	var wg sync.WaitGroup
	for i := range ids {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			identity, err := OpenClawIdentity(store)
			if err == nil {
				ids[i] = identity.ID()
			}
		}(i)
	}
	wg.Wait()
	for _, id := range ids {
		assert.Equal(t, ids[0], id)
	}
}

func TestOpenClawIdentity_GivenCorruptSeed_ThenInvalidWithoutEchoingIt(t *testing.T) {
	store := keychain.NewMemory()
	require.NoError(t, store.Set(OpenClawIdentityAccount, "!!not-base64!!"))

	_, err := OpenClawIdentity(store)

	require.Error(t, err)
	assert.False(t, strings.Contains(err.Error(), "not-base64"))
}

type brokenStore struct{ err error }

func (b brokenStore) Get(string) (string, error) { return "", b.err }
func (b brokenStore) Set(string, string) error   { return b.err }
func (b brokenStore) Delete(string) error        { return b.err }

func TestOpenClawIdentity_GivenUnreadableStore_ThenErrorAndNoRegeneration(t *testing.T) {
	storeErr := errors.New("keychain locked")

	_, err := OpenClawIdentity(brokenStore{err: storeErr})

	require.ErrorIs(t, err, storeErr)
}

func TestOpenClawIdentity_GivenNilStore_ThenUnavailable(t *testing.T) {
	_, err := OpenClawIdentity(nil)
	require.ErrorIs(t, err, ErrStoreUnavailable)
}
