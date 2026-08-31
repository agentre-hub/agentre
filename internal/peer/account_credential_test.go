package peer

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func testVerifierKeyPair(t *testing.T) (*rsa.PrivateKey, string) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	der, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	require.NoError(t, err)
	return key, string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der}))
}

func testVerifierCredential(t *testing.T, key *rsa.PrivateKey, kid string, claims map[string]any) string {
	t.Helper()
	headerJSON, err := json.Marshal(map[string]any{"alg": "RS256", "typ": "JWT", "kid": kid})
	require.NoError(t, err)
	claimsJSON, err := json.Marshal(claims)
	require.NoError(t, err)
	signingInput := base64.RawURLEncoding.EncodeToString(headerJSON) + "." +
		base64.RawURLEncoding.EncodeToString(claimsJSON)
	digest := sha256.Sum256([]byte(signingInput))
	signature, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, digest[:])
	require.NoError(t, err)
	return signingInput + "." + base64.RawURLEncoding.EncodeToString(signature)
}

// keysServer 冒充 server 的 /v1/keys（cago 信封形态），并记录被抓了几次。
func keysServer(t *testing.T, publicKeyPEM string, hits *atomic.Int32) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/v1/keys", r.URL.Path)
		if hits != nil {
			hits.Add(1)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": map[string]any{
			"current_kid": "current", "keys": map[string]string{"current": publicKeyPEM},
			"max_token_lifetime_seconds": 3600,
		}})
	}))
	t.Cleanup(server.Close)
	return server
}

func TestAccountCredentialVerifier_GivenAValidCredential_ThenIdentityComesFromThePfpClaim(t *testing.T) {
	key, publicKeyPEM := testVerifierKeyPair(t)
	server := keysServer(t, publicKeyPEM, nil)
	verifier := newAccountCredentialVerifier(func(context.Context) (string, string, error) {
		return server.URL, "7", nil
	})
	credential := testVerifierCredential(t, key, "current", map[string]any{
		"uid": 7, "iat": time.Now().Unix(), "exp": time.Now().Add(time.Hour).Unix(), "pfp": "sha256:web-peer-7",
	})

	fingerprint, err := verifier.Verify(context.Background(), credential)

	require.NoError(t, err)
	require.Equal(t, "sha256:web-peer-7", fingerprint)
}

func TestAccountCredentialVerifier_GivenAForgedSignature_ThenRefuses(t *testing.T) {
	_, publicKeyPEM := testVerifierKeyPair(t)
	otherKey, _ := testVerifierKeyPair(t)
	server := keysServer(t, publicKeyPEM, nil)
	verifier := newAccountCredentialVerifier(func(context.Context) (string, string, error) {
		return server.URL, "7", nil
	})
	credential := testVerifierCredential(t, otherKey, "current", map[string]any{
		"uid": 7, "iat": time.Now().Unix(), "exp": time.Now().Add(time.Hour).Unix(), "pfp": "sha256:attacker",
	})

	_, err := verifier.Verify(context.Background(), credential)

	require.Error(t, err)
}

func TestAccountCredentialVerifier_GivenAnotherAccountsCredential_ThenRefuses(t *testing.T) {
	key, publicKeyPEM := testVerifierKeyPair(t)
	server := keysServer(t, publicKeyPEM, nil)
	verifier := newAccountCredentialVerifier(func(context.Context) (string, string, error) {
		return server.URL, "7", nil
	})
	credential := testVerifierCredential(t, key, "current", map[string]any{
		"uid": 99, "iat": time.Now().Unix(), "exp": time.Now().Add(time.Hour).Unix(), "pfp": "sha256:other-account",
	})

	_, err := verifier.Verify(context.Background(), credential)

	require.ErrorContains(t, err, "not 7")
}

func TestAccountCredentialVerifier_GivenACredentialWithoutPfp_ThenRefuses(t *testing.T) {
	key, publicKeyPEM := testVerifierKeyPair(t)
	server := keysServer(t, publicKeyPEM, nil)
	verifier := newAccountCredentialVerifier(func(context.Context) (string, string, error) {
		return server.URL, "7", nil
	})
	credential := testVerifierCredential(t, key, "current", map[string]any{
		"uid": 7, "iat": time.Now().Unix(), "exp": time.Now().Add(time.Hour).Unix(),
	})

	_, err := verifier.Verify(context.Background(), credential)

	require.ErrorContains(t, err, "missing peer fingerprint")
}

func TestAccountCredentialVerifier_GivenNoLogin_ThenRefusesWithoutTouchingTheNetwork(t *testing.T) {
	var hits atomic.Int32
	_, publicKeyPEM := testVerifierKeyPair(t)
	server := keysServer(t, publicKeyPEM, &hits)
	verifier := newAccountCredentialVerifier(func(context.Context) (string, string, error) {
		return "", "", nil
	})
	_ = server

	_, err := verifier.Verify(context.Background(), "anything")

	require.ErrorIs(t, err, errNotSignedIn)
	require.Zero(t, hits.Load())
}

// TestAccountCredentialVerifier_GivenRepeatedBadCredentials_ThenDoesNotHammerTheKeysEndpoint
// 一串验不过的凭据不该把桌面端变成 /v1/keys 的压测客户端:补抓有频率下限。
func TestAccountCredentialVerifier_GivenRepeatedBadCredentials_ThenDoesNotHammerTheKeysEndpoint(t *testing.T) {
	var hits atomic.Int32
	_, publicKeyPEM := testVerifierKeyPair(t)
	otherKey, _ := testVerifierKeyPair(t)
	server := keysServer(t, publicKeyPEM, &hits)
	verifier := newAccountCredentialVerifier(func(context.Context) (string, string, error) {
		return server.URL, "7", nil
	})
	credential := testVerifierCredential(t, otherKey, "current", map[string]any{
		"uid": 7, "iat": time.Now().Unix(), "exp": time.Now().Add(time.Hour).Unix(), "pfp": "sha256:attacker",
	})

	for range 5 {
		_, err := verifier.Verify(context.Background(), credential)
		require.Error(t, err)
	}

	require.Equal(t, int32(1), hits.Load())
}
