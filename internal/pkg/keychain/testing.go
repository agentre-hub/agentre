package keychain

// SetSystemKeyringForTest replaces the go-keyring package functions systemKC calls
// through, and restores them when the test ends. It exists so tests can prove system
// keychain behavior (per-channel service scoping, not-found mapping) without ever
// touching the real OS keychain backend. Production code must not call it.
func SetSystemKeyringForTest(
	tb interface{ Cleanup(func()) },
	get func(service, user string) (string, error),
	set func(service, user, password string) error,
	del func(service, user string) error,
) {
	prevGet, prevSet, prevDel := keyringGet, keyringSet, keyringDelete
	keyringGet, keyringSet, keyringDelete = get, set, del
	tb.Cleanup(func() {
		keyringGet, keyringSet, keyringDelete = prevGet, prevSet, prevDel
	})
}
