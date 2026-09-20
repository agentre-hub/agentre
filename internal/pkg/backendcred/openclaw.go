package backendcred

import (
	"encoding/base64"
	"errors"
	"strings"
	"sync"

	"github.com/agentre-hub/agentre/internal/pkg/openclawgateway"
)

// OpenClawIdentityAccount holds this device's OpenClaw identity seed. There is
// one per device; it is generated locally and never leaves the device.
const OpenClawIdentityAccount = "agentre.openclaw.device.identity.seed"

// OpenClawTokenAccount is the Gateway token slot of one backend, keyed by its
// account-wide sync_id so every device refers to the same backend the same way.
// A blank sync_id has no slot and yields "": callers must not read or write it,
// otherwise unrelated drafts would share one token.
func OpenClawTokenAccount(syncID string) string {
	syncID = strings.TrimSpace(syncID)
	if syncID == "" {
		return ""
	}
	return "agentre.openclaw.backend." + syncID + ".token"
}

// identityMu serializes first-use generation so concurrent callers in one
// process agree on a single seed.
var identityMu sync.Mutex

// OpenClawIdentity loads this device's identity from store, generating and
// persisting a fresh seed on first use. A store read failure is returned as-is
// rather than silently minting a new identity.
func OpenClawIdentity(store Store) (*openclawgateway.DeviceIdentity, error) {
	if store == nil {
		return nil, ErrStoreUnavailable
	}
	identityMu.Lock()
	defer identityMu.Unlock()
	encoded, err := store.Get(OpenClawIdentityAccount)
	if err == nil {
		seed, decodeErr := base64.RawURLEncoding.DecodeString(encoded)
		if decodeErr != nil {
			return nil, errors.New("openclaw device identity is invalid")
		}
		return openclawgateway.NewDeviceIdentityFromSeed(seed)
	}
	if !errors.Is(err, ErrNotFound) {
		return nil, err
	}
	identity, err := openclawgateway.GenerateDeviceIdentity()
	if err != nil {
		return nil, err
	}
	if err := store.Set(OpenClawIdentityAccount, base64.RawURLEncoding.EncodeToString(identity.Seed())); err != nil {
		return nil, err
	}
	return identity, nil
}
