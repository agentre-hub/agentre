package auth

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const patVerdictBody = `{"code":0,"msg":"ok","data":{"account_id":"42","device_id":0,"kind":"personal_token","peer_fingerprint":"pat:0123456789abcdef0123456789abcdef","expires_in":0,"scopes":["read","chat"],"token_name":"ci-bot"}}`

// Given the server vouches for a personal token, When it is verified, Then the
// verdict carries the token's kind, scopes and name next to its own fingerprint.
func TestIntrospector_GivenAPersonalToken_ThenTheVerdictCarriesItsKindScopesAndName(t *testing.T) {
	introspector, _ := setupIntrospectorTest(t, answer(http.StatusOK, patVerdictBody))

	got, err := introspector.Verify(context.Background(), "pat-secret")

	require.NoError(t, err)
	assert.Equal(t, Introspection{
		AccountID: "42", Kind: KindPersonalToken,
		PeerFingerprint: "pat:0123456789abcdef0123456789abcdef",
		Scopes:          []string{"read", "chat"}, TokenName: "ci-bot",
	}, got)
}

// Given a never-expiring personal token (expires_in 0), Then its verdict is still
// re-checked online after 60 seconds: revocation reaches an open relay within the
// cache window, never later.
func TestIntrospector_GivenANeverExpiringPersonalToken_ThenItIsReverifiedAfterTheCacheWindow(t *testing.T) {
	introspector, rig := setupIntrospectorTest(t, answer(http.StatusOK, patVerdictBody))
	ctx := context.Background()

	_, err := introspector.Verify(ctx, "pat-secret")
	require.NoError(t, err)
	rig.clock.Advance(59 * time.Second)
	_, err = introspector.Verify(ctx, "pat-secret")
	require.NoError(t, err)
	assert.Equal(t, int32(1), rig.calls.Load())

	rig.clock.Advance(2 * time.Second)
	_, err = introspector.Verify(ctx, "pat-secret")
	require.NoError(t, err)
	assert.Equal(t, int32(2), rig.calls.Load(), "expires_in 0 means never expires, not cache forever")
}

// Given a personal token of the daemon's account, When it handshakes, Then the
// account result hands the connection its kind, scopes and name.
func TestAuth_Account_GivenAPersonalToken_ThenTheResultCarriesItsKindScopesAndName(t *testing.T) {
	ah, st, _ := setupAccountAuthTest(t, answer(http.StatusOK, patVerdictBody))
	loginReceiver(st, "42")

	result, err := ah.HandleAccount(context.Background(), AccountParams{Credential: "pat-secret"})

	require.NoError(t, err)
	assert.Equal(t, &AccountResult{
		OK: true, InstanceUUID: st.DaemonInstanceUUID,
		PeerFingerprint: "pat:0123456789abcdef0123456789abcdef",
		Kind:            KindPersonalToken, Scopes: []string{"read", "chat"}, TokenName: "ci-bot",
	}, result)
}
