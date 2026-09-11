package auth

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/agentre-hub/agentre/pkg/wire/rpcerror"

	"github.com/agentre-hub/agentre/internal/daemon/pairing"
	"github.com/agentre-hub/agentre/internal/daemon/state"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func testSha256Sum(value string) []byte {
	sum := sha256.Sum256([]byte(value))
	return sum[:]
}

func setupAuthTest(t *testing.T) (*AuthHandlers, *state.State, *pairing.Manager) {
	t.Helper()
	dir := t.TempDir()
	st, err := state.Load(dir)
	require.NoError(t, err)
	pm := pairing.NewManager(pairing.ManagerOpts{TTL: time.Minute})
	rl := pairing.NewRateLimiter(pairing.RateLimitOpts{MaxAttempts: 3, Window: time.Minute})
	return NewAuthHandlers(st, pm, rl, nil), st, pm
}

// setupAccountAuthTest 起一台假的 agentre-server 核验端点,并把一个真的 Introspector
// 接进 AuthHandlers —— Mode C 的判定就在这条 HTTP 边界上发生。
func setupAccountAuthTest(t *testing.T, handler http.HandlerFunc) (*AuthHandlers, *state.State, *atomic.Int32) {
	t.Helper()
	st, err := state.Load(t.TempDir())
	require.NoError(t, err)
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		handler(w, r)
	}))
	t.Cleanup(server.Close)
	introspector := NewIntrospector(IntrospectorOptions{
		HTTP:        server.Client(),
		ServerURL:   func() string { return server.URL },
		AccessToken: func() string { return st.Snapshot().Credential.AccessToken },
	})
	pm := pairing.NewManager(pairing.ManagerOpts{TTL: time.Minute})
	rl := pairing.NewRateLimiter(pairing.RateLimitOpts{MaxAttempts: 3, Window: time.Minute})
	return NewAuthHandlers(st, pm, rl, introspector), st, &calls
}

// peerPresented 是对端出示的不透明凭据:测试里它只是一个假 server 认得的名字。
const peerPresented = "opaque-credential"

func loginReceiver(st *state.State, accountID string) {
	st.Mutate(func(s *state.State) {
		s.AccountID = accountID
		s.Credential = state.AccountCredential{AccessToken: "receiver-token"}
	})
}

func assertRPCCode(t *testing.T, err error, code int32) {
	t.Helper()
	var rpcErr *rpcerror.Error
	require.ErrorAs(t, err, &rpcErr)
	assert.Equal(t, code, rpcErr.Code)
}

func TestAuth_Account_GivenLoggedInDaemon_WhenTheServerConfirmsTheSameAccount_ThenAuthenticatesWithTheServersPeerFingerprint(t *testing.T) {
	ah, st, _ := setupAccountAuthTest(t, answer(http.StatusOK,
		`{"code":0,"msg":"ok","data":{"account_id":"42","device_id":7,"kind":"desktop","peer_fingerprint":"sha256:from-server","expires_in":900}}`))
	loginReceiver(st, "42")

	result, err := ah.HandleAccount(context.Background(), AccountParams{Credential: peerPresented})

	require.NoError(t, err)
	assert.Equal(t, &AccountResult{OK: true, InstanceUUID: st.DaemonInstanceUUID, PeerFingerprint: "sha256:from-server"}, result)
}

func TestAuth_Account_GivenTheServerConfirmsAnotherAccount_WhenAuthenticating_ThenRejectsAsCredentialInvalid(t *testing.T) {
	ah, st, _ := setupAccountAuthTest(t, answer(http.StatusOK,
		`{"code":0,"msg":"ok","data":{"account_id":"99","device_id":7,"kind":"desktop","peer_fingerprint":"sha256:other","expires_in":900}}`))
	loginReceiver(st, "42")

	_, err := ah.HandleAccount(context.Background(), AccountParams{Credential: "other-account-credential"})

	require.ErrorIs(t, err, ErrCredentialInvalid)
	assertRPCCode(t, err, rpcerror.CodeUnauthorized)
}

func TestAuth_Account_GivenLoggedOutDaemon_WhenAuthenticating_ThenRejectsAsReceiverNotReadyWithoutContactingTheServer(t *testing.T) {
	ah, _, calls := setupAccountAuthTest(t, answer(http.StatusOK, introspectSuccessBody))

	_, err := ah.HandleAccount(context.Background(), AccountParams{Credential: peerPresented})

	require.ErrorIs(t, err, ErrReceiverNotReady)
	assertRPCCode(t, err, rpcerror.CodeUnauthorized)
	assert.Zero(t, calls.Load())
}

func TestAuth_Account_GivenTheAccountServerIsUnreachable_WhenAuthenticating_ThenRejectsWithTheUnreachableCode(t *testing.T) {
	ah, st, _ := setupAccountAuthTest(t, answer(http.StatusServiceUnavailable, `unavailable`))
	loginReceiver(st, "42")

	_, err := ah.HandleAccount(context.Background(), AccountParams{Credential: peerPresented})

	require.ErrorIs(t, err, ErrAccountServerUnreachable)
	assertRPCCode(t, err, rpcerror.CodeAccountServerUnreachable)
}

// ── VerifyAccountCredential:桌面端入站(internal/peer)仍在用的本地 RS256 验签 ──

func TestVerifyAccountCredential_GivenAValidCredential_ThenReturnsItsAccountAndPeerFingerprint(t *testing.T) {
	privateKey, publicKeyPEM := testRSAKeyPair(t)
	credential := testAccountCredential(t, privateKey, int64(42), time.Now().Add(time.Hour))

	verified, err := VerifyAccountCredential(credential, KeySet{CurrentPEM: publicKeyPEM})

	require.NoError(t, err)
	assert.Equal(t, "42", verified.AccountID)
	assert.Equal(t, "sha256:account-client", verified.PeerFingerprint)
}

func TestVerifyAccountCredential_GivenVersionedKeySet_ThenSelectsKIDAndEnforcesLifetime(t *testing.T) {
	oldPrivate, oldPublic := testRSAKeyPair(t)
	_, currentPublic := testRSAKeyPair(t)
	keys := KeySet{ByKID: map[string]string{"old": oldPublic, "current": currentPublic}, MaxLifetime: 900 * time.Second}

	validOld := testVersionedAccountCredential(t, oldPrivate, "old", 42, time.Now(), time.Now().Add(15*time.Minute))
	_, err := VerifyAccountCredential(validOld, keys)
	require.NoError(t, err, "正常轮换窗口内应按 kid 使用旧公钥")

	unknown := testVersionedAccountCredential(t, oldPrivate, "retired", 42, time.Now(), time.Now().Add(15*time.Minute))
	_, err = VerifyAccountCredential(unknown, keys)
	assertAccountCredentialRejection(t, err, "account credential invalid")

	overlong := testVersionedAccountCredential(t, oldPrivate, "old", 42, time.Now(), time.Now().Add(16*time.Minute))
	_, err = VerifyAccountCredential(overlong, keys)
	assertAccountCredentialRejection(t, err, "account credential invalid")
}

func TestVerifyAccountCredential_GivenCredentialWithinClockSkew_ThenAccepts(t *testing.T) {
	privateKey, publicKeyPEM := testRSAKeyPair(t)
	credential := testAccountCredential(t, privateKey, int64(42), time.Now().Add(-30*time.Second))

	_, err := VerifyAccountCredential(credential, KeySet{CurrentPEM: publicKeyPEM})

	require.NoError(t, err)
}

func TestVerifyAccountCredential_GivenExpiredCredential_ThenRejectsExpiry(t *testing.T) {
	privateKey, publicKeyPEM := testRSAKeyPair(t)
	credential := testAccountCredential(t, privateKey, int64(42), time.Now().Add(-61*time.Second))

	_, err := VerifyAccountCredential(credential, KeySet{CurrentPEM: publicKeyPEM})

	assertAccountCredentialRejection(t, err, "account credential expired")
}

func TestVerifyAccountCredential_GivenWrongSignature_ThenRejectsSignature(t *testing.T) {
	_, publicKeyPEM := testRSAKeyPair(t)
	wrongPrivateKey, _ := testRSAKeyPair(t)
	credential := testAccountCredential(t, wrongPrivateKey, int64(42), time.Now().Add(time.Hour))

	_, err := VerifyAccountCredential(credential, KeySet{CurrentPEM: publicKeyPEM})

	assertAccountCredentialRejection(t, err, "account credential signature invalid")
}

// 缺 pfp 的凭据与签名不合法同一形态被拒 —— 不回退到请求体,因为回退等于这条要求
// 不存在。
func TestVerifyAccountCredential_GivenNoPeerFingerprintClaim_ThenRejectsUnauthorized(t *testing.T) {
	privateKey, publicKeyPEM := testRSAKeyPair(t)
	credential := testAccountCredentialWithPeerFingerprint(t, privateKey, int64(42), time.Now().Add(time.Hour), "")

	_, err := VerifyAccountCredential(credential, KeySet{CurrentPEM: publicKeyPEM})

	assertAccountCredentialRejection(t, err, "account credential missing peer fingerprint")
}

func assertAccountCredentialRejection(t *testing.T, err error, reason string) {
	t.Helper()
	var rpcErr *rpcerror.Error
	require.ErrorAs(t, err, &rpcErr)
	assert.Equal(t, rpcerror.ErrUnauthorized.Code, rpcErr.Code)
	assert.Equal(t, reason, rpcErr.Message)
}

func testRSAKeyPair(t *testing.T) (*rsa.PrivateKey, string) {
	t.Helper()
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	der, err := x509.MarshalPKIXPublicKey(&privateKey.PublicKey)
	require.NoError(t, err)
	return privateKey, string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der}))
}

func testAccountCredential(t *testing.T, privateKey *rsa.PrivateKey, accountID any, expiresAt time.Time) string {
	t.Helper()
	return testAccountCredentialWithPeerFingerprint(t, privateKey, accountID, expiresAt, "sha256:account-client")
}

// testAccountCredentialWithPeerFingerprint 铸一枚带 pfp claim(决策 8 的对端身份)的
// 凭据。空 pfp 省略该 claim —— 那正是「凭据没说自己是谁」的形态。
func testAccountCredentialWithPeerFingerprint(t *testing.T, privateKey *rsa.PrivateKey, accountID any,
	expiresAt time.Time, peerFingerprint string) string {
	t.Helper()
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"RS256","typ":"JWT"}`))
	payloadClaims := map[string]any{"uid": accountID, "exp": expiresAt.Unix()}
	if peerFingerprint != "" {
		payloadClaims["pfp"] = peerFingerprint
	}
	claims, err := json.Marshal(payloadClaims)
	require.NoError(t, err)
	payload := base64.RawURLEncoding.EncodeToString(claims)
	signingInput := header + "." + payload
	digest := sha256.Sum256([]byte(signingInput))
	signature, err := rsa.SignPKCS1v15(rand.Reader, privateKey, crypto.SHA256, digest[:])
	require.NoError(t, err)
	return signingInput + "." + base64.RawURLEncoding.EncodeToString(signature)
}

func testVersionedAccountCredential(t *testing.T, privateKey *rsa.PrivateKey, kid string, accountID any,
	issuedAt, expiresAt time.Time) string {
	t.Helper()
	headerJSON, err := json.Marshal(map[string]any{"alg": "RS256", "typ": "JWT", "kid": kid})
	require.NoError(t, err)
	header := base64.RawURLEncoding.EncodeToString(headerJSON)
	claimsJSON, err := json.Marshal(map[string]any{
		"uid": accountID, "iat": issuedAt.Unix(), "exp": expiresAt.Unix(),
		"pfp": "sha256:account-client",
	})
	require.NoError(t, err)
	payload := base64.RawURLEncoding.EncodeToString(claimsJSON)
	signingInput := header + "." + payload
	digest := sha256.Sum256([]byte(signingInput))
	signature, err := rsa.SignPKCS1v15(rand.Reader, privateKey, crypto.SHA256, digest[:])
	require.NoError(t, err)
	return signingInput + "." + base64.RawURLEncoding.EncodeToString(signature)
}

func TestAuth_PairThenConnect(t *testing.T) {
	ah, st, pm := setupAuthTest(t)
	code, err := pm.Generate()
	require.NoError(t, err)

	pairResp, err := ah.HandlePair(context.Background(), "1.2.3.4", PairParams{
		Code:              code,
		DeviceName:        "mac-pro-m4",
		DeviceFingerprint: "sha256:test-fp",
	})
	require.NoError(t, err)
	assert.NotEmpty(t, pairResp.DeviceToken)
	expectedDaemonFp := "sha256:" + hex.EncodeToString(testSha256Sum(st.DaemonInstanceUUID))
	assert.Equal(t, expectedDaemonFp, pairResp.DaemonFingerprint)

	peer, ok := st.PairedPeers["sha256:test-fp"]
	require.True(t, ok)
	assert.Equal(t, pairResp.DeviceToken, peer.DeviceToken)

	_, err = ah.HandleConnect(context.Background(), ConnectParams{
		DeviceFingerprint:         "sha256:test-fp",
		DeviceToken:               pairResp.DeviceToken,
		ExpectedDaemonFingerprint: expectedDaemonFp,
	})
	assert.NoError(t, err)
}

func TestAuth_BadCode(t *testing.T) {
	ah, _, _ := setupAuthTest(t)
	_, err := ah.HandlePair(context.Background(), "1.2.3.4", PairParams{
		Code: "ZZZZZZ", DeviceName: "x", DeviceFingerprint: "sha256:y",
	})
	var rpcErr *rpcerror.Error
	require.True(t, errors.As(err, &rpcErr))
	assert.EqualValues(t, -32004, rpcErr.Code)
}

func TestAuth_RateLimitTriggers(t *testing.T) {
	ah, _, _ := setupAuthTest(t)
	for i := 0; i < 3; i++ {
		_, _ = ah.HandlePair(context.Background(), "1.2.3.4", PairParams{
			Code: "WRONG", DeviceName: "x", DeviceFingerprint: "sha256:y",
		})
	}
	_, err := ah.HandlePair(context.Background(), "1.2.3.4", PairParams{
		Code: "WHATEVER", DeviceName: "x", DeviceFingerprint: "sha256:y",
	})
	var rpcErr *rpcerror.Error
	require.True(t, errors.As(err, &rpcErr))
	assert.EqualValues(t, -32004, rpcErr.Code)
	assert.Contains(t, rpcErr.Message, "rate")
}

func TestAuth_ConnectFailsWithWrongToken(t *testing.T) {
	ah, _, pm := setupAuthTest(t)
	code, _ := pm.Generate()
	_, _ = ah.HandlePair(context.Background(), "1.2.3.4", PairParams{
		Code: code, DeviceName: "x", DeviceFingerprint: "sha256:f",
	})
	_, err := ah.HandleConnect(context.Background(), ConnectParams{
		DeviceFingerprint: "sha256:f", DeviceToken: "nope",
	})
	var rpcErr *rpcerror.Error
	require.True(t, errors.As(err, &rpcErr))
	assert.EqualValues(t, -32001, rpcErr.Code)
}

func TestAuth_TOFUFingerprintMismatch(t *testing.T) {
	ah, st, pm := setupAuthTest(t)
	code, _ := pm.Generate()
	resp, _ := ah.HandlePair(context.Background(), "1.2.3.4", PairParams{
		Code: code, DeviceName: "x", DeviceFingerprint: "sha256:f",
	})
	_ = st
	_, err := ah.HandleConnect(context.Background(), ConnectParams{
		DeviceFingerprint:         "sha256:f",
		DeviceToken:               resp.DeviceToken,
		ExpectedDaemonFingerprint: "sha256:tampered",
	})
	var rpcErr *rpcerror.Error
	require.True(t, errors.As(err, &rpcErr))
	assert.EqualValues(t, -32001, rpcErr.Code)
	assert.Contains(t, rpcErr.Message, "fingerprint")
}
