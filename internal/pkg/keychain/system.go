package keychain

import (
	"errors"

	"github.com/zalando/go-keyring"
)

// keyringGet/keyringSet/keyringDelete are the go-keyring package functions systemKC
// calls through. They're package-level vars (not direct calls) so tests can replace
// them via SetSystemKeyringForTest and never touch the real OS keychain.
var (
	keyringGet    = keyring.Get
	keyringSet    = keyring.Set
	keyringDelete = keyring.Delete
)

type systemKC struct {
	// service is the keychain "application" slot this instance reads and writes.
	// Each build channel gets its own service name (see Channel.Identity().
	// KeychainService) so stable/beta/nightly/dev never share credentials.
	service string
}

// NewSystem returns an OS-native keychain implementation scoped to service:
// macOS Keychain / Windows Credential Manager / Linux Secret Service. Production
// builds pass the current build channel's Identity().KeychainService; headless /
// some container environments should opt into NewFile() instead.
func NewSystem(service string) Keychain { return &systemKC{service: service} }

func (s *systemKC) Get(account string) (string, error) {
	v, err := keyringGet(s.service, account)
	if errors.Is(err, keyring.ErrNotFound) {
		return "", ErrNotFound
	}
	return v, err
}

func (s *systemKC) Set(account, secret string) error {
	return keyringSet(s.service, account, secret)
}

func (s *systemKC) Delete(account string) error {
	err := keyringDelete(s.service, account)
	if errors.Is(err, keyring.ErrNotFound) {
		return ErrNotFound
	}
	return err
}
