// Package backendcred owns the device-local credentials of the network agent
// backends: the Hermes refresh-token store, the OpenClaw Gateway token slots and
// the OpenClaw device identity seed.
//
// It is a side-effect-free leaf: it registers no runtime and touches no
// database, so both the desktop (system keychain) and agentred (state.json) can
// back it with their own Store. Secrets never leave the returned values; callers
// must not log or serialize them.
package backendcred

import (
	"errors"

	"github.com/agentre-hub/agentre/internal/pkg/keychain"
)

// Store is the secret slot storage. keychain.Keychain satisfies it; Get must
// return ErrNotFound for an empty slot.
type Store interface {
	Get(account string) (string, error)
	Set(account, secret string) error
	Delete(account string) error
}

// ErrNotFound is the empty-slot sentinel every Store must return.
var ErrNotFound = keychain.ErrNotFound

// ErrStoreUnavailable means no secret store is wired on this device.
var ErrStoreUnavailable = errors.New("backend credential store unavailable")
