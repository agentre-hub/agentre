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
	"sync/atomic"
	"testing"

	"time"

	"github.com/agentre-hub/agentre/internal/pkg/rpcerror"

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
	return NewAuthHandlers(st, pm, rl), st, pm
}

func TestAuth_AccountCredential_GivenClaimedDaemon_WhenValidCredential_ThenAuthenticates(t *testing.T) {
	ah, st, _ := setupAuthTest(t)
	privateKey, publicKeyPEM := testRSAKeyPair(t)
	st.Claim("42", publicKeyPEM, state.AccountCredential{})
	credential := testAccountCredential(t, privateKey, int64(42), time.Now().Add(time.Hour))

	result, err := ah.HandleAccount(context.Background(), AccountParams{Credential: credential})

	require.NoError(t, err)
	assert.Equal(t, &AccountResult{OK: true, InstanceUUID: st.DaemonInstanceUUID, PeerFingerprint: "sha256:account-client"}, result)
}

func TestAuth_AccountCredential_GivenVersionedKeySet_WhenVerifyingThenSelectsKIDAndEnforcesLifetime(t *testing.T) {
	ah, st, _ := setupAuthTest(t)
	oldPrivate, oldPublic := testRSAKeyPair(t)
	_, currentPublic := testRSAKeyPair(t)
	st.ClaimWithKeySet("42", "current", map[string]string{
		"old": oldPublic, "current": currentPublic,
	}, 900, state.AccountCredential{})

	validOld := testVersionedAccountCredential(t, oldPrivate, "old", 42,
		time.Now(), time.Now().Add(15*time.Minute))
	result, err := ah.HandleAccount(context.Background(), AccountParams{Credential: validOld})
	require.NoError(t, err)
	assert.True(t, result.OK, "正常轮换窗口内应按 kid 使用旧公钥")

	unknown := testVersionedAccountCredential(t, oldPrivate, "retired", 42,
		time.Now(), time.Now().Add(15*time.Minute))
	_, err = ah.HandleAccount(context.Background(), AccountParams{Credential: unknown})
	assertAccountCredentialRejection(t, err, "account credential invalid")

	overlong := testVersionedAccountCredential(t, oldPrivate, "old", 42,
		time.Now(), time.Now().Add(16*time.Minute))
	_, err = ah.HandleAccount(context.Background(), AccountParams{Credential: overlong})
	assertAccountCredentialRejection(t, err, "account credential invalid")
}

func TestAuth_AccountCredential_GivenCredentialWithinClockSkew_WhenAuthenticating_ThenAuthenticates(t *testing.T) {
	ah, st, _ := setupAuthTest(t)
	privateKey, publicKeyPEM := testRSAKeyPair(t)
	st.Claim("42", publicKeyPEM, state.AccountCredential{})
	credential := testAccountCredential(t, privateKey, int64(42), time.Now().Add(-30*time.Second))

	result, err := ah.HandleAccount(context.Background(), AccountParams{Credential: credential})

	require.NoError(t, err)
	assert.Equal(t, &AccountResult{OK: true, InstanceUUID: st.DaemonInstanceUUID, PeerFingerprint: "sha256:account-client"}, result)
}

func TestAuth_AccountCredential_GivenExpiredCredential_WhenAuthenticating_ThenRejectsExpiry(t *testing.T) {
	ah, st, _ := setupAuthTest(t)
	privateKey, publicKeyPEM := testRSAKeyPair(t)
	st.Claim("42", publicKeyPEM, state.AccountCredential{})
	credential := testAccountCredential(t, privateKey, int64(42), time.Now().Add(-61*time.Second))

	_, err := ah.HandleAccount(context.Background(), AccountParams{Credential: credential})

	assertAccountCredentialRejection(t, err, "account credential expired")
}

func TestAuth_AccountCredential_GivenWrongSignature_WhenAuthenticating_ThenRejectsSignature(t *testing.T) {
	ah, st, _ := setupAuthTest(t)
	_, publicKeyPEM := testRSAKeyPair(t)
	wrongPrivateKey, _ := testRSAKeyPair(t)
	st.Claim("42", publicKeyPEM, state.AccountCredential{})
	credential := testAccountCredential(t, wrongPrivateKey, int64(42), time.Now().Add(time.Hour))

	_, err := ah.HandleAccount(context.Background(), AccountParams{Credential: credential})

	assertAccountCredentialRejection(t, err, "account credential signature invalid")
}

func TestAuth_AccountCredential_GivenDifferentAccount_WhenAuthenticating_ThenRejectsMismatch(t *testing.T) {
	ah, st, _ := setupAuthTest(t)
	privateKey, publicKeyPEM := testRSAKeyPair(t)
	st.Claim("42", publicKeyPEM, state.AccountCredential{})
	credential := testAccountCredential(t, privateKey, int64(99), time.Now().Add(time.Hour))

	_, err := ah.HandleAccount(context.Background(), AccountParams{Credential: credential})

	assertAccountCredentialRejection(t, err, "account credential account mismatch")
}

func TestAuth_AccountCredential_GivenUnclaimedDaemon_WhenAuthenticating_ThenRejectsMissingCachedKey(t *testing.T) {
	ah, _, _ := setupAuthTest(t)
	privateKey, _ := testRSAKeyPair(t)
	credential := testAccountCredential(t, privateKey, int64(42), time.Now().Add(time.Hour))

	_, err := ah.HandleAccount(context.Background(), AccountParams{Credential: credential})

	assertAccountCredentialRejection(t, err, "account credential rejected: cached verification key unavailable")
}

// R3:「上述验签在 daemon 与 server 之间零网络往返」。这是离线可用性的支点 ——
// 一旦验签路径上混进任何一次 HTTP 调用,server 挂掉时内网就连不上了,而这在
// 单测里表现为「照样通过」,只有把 transport 换成绊线才拦得住。
func TestAuth_AccountCredential_WhenVerifying_ThenMakesNoNetworkRoundTrip(t *testing.T) {
	ah, st, _ := setupAuthTest(t)
	privateKey, publicKeyPEM := testRSAKeyPair(t)
	st.Claim("42", publicKeyPEM, state.AccountCredential{})
	credential := testAccountCredential(t, privateKey, int64(42), time.Now().Add(time.Hour))

	originalTransport := http.DefaultTransport
	var networkCalls atomic.Int32
	http.DefaultTransport = roundTripFunc(func(*http.Request) (*http.Response, error) {
		networkCalls.Add(1)
		return nil, errors.New("network is not allowed during account credential verification")
	})
	t.Cleanup(func() { http.DefaultTransport = originalTransport })

	result, err := ah.HandleAccount(context.Background(), AccountParams{Credential: credential})

	require.NoError(t, err)
	assert.True(t, result.OK)
	assert.Zero(t, networkCalls.Load(), "account credential verification must not contact the server")
}

// R4 的 daemon 一半:daemon 定期把账号的吊销列表拉到本地,验签通过之后还要看一眼
// 凭据的 jti 在不在列表里。命中时的拒绝理由必须与既有五种各自可区分 —— 运维看日志
// 时「被吊销」和「签名不符」是两件完全不同的事。
func TestAuth_AccountCredential_GivenRevokedJTI_WhenAuthenticating_ThenRejectsRevoked(t *testing.T) {
	ah, st, _ := setupAuthTest(t)
	privateKey, publicKeyPEM := testRSAKeyPair(t)
	st.Claim("42", publicKeyPEM, state.AccountCredential{})
	st.Mutate(func(s *state.State) {
		s.RevokedJTIs = []string{"01JBYSTANDER", "01JREVOKED"}
		s.RevocationsAsOf = time.Now().UnixMilli()
	})
	credential := testAccountCredentialWithJTI(t, privateKey, int64(42), time.Now().Add(time.Hour), "01JREVOKED")

	_, err := ah.HandleAccount(context.Background(), AccountParams{
		Credential: credential,
	})

	assertAccountCredentialRejection(t, err, "account credential revoked")
}

// R19:「现有访问凭据在其短有效期内仍可能被离线的 daemon 接受」—— 这是 R4 明说的
// 延迟,不是 bug。支点是 R3 的零网络往返:握手期只查本地缓存的那份列表,server 上
// 刚吊销、daemon 还没拉到的 jti 照样放行,daemon 绝不会为了确认而临时去问一次。
func TestAuth_AccountCredential_GivenJTIRevokedOnlyOnServer_WhenAuthenticating_ThenAcceptsWithoutNetwork(t *testing.T) {
	ah, st, _ := setupAuthTest(t)
	privateKey, publicKeyPEM := testRSAKeyPair(t)
	st.Claim("42", publicKeyPEM, state.AccountCredential{})
	// 上一次拉到的列表里没有这个 jti(server 是在那之后才吊销的)。
	st.Mutate(func(s *state.State) {
		s.RevokedJTIs = []string{"01JSOMEONEELSE"}
		s.RevocationsAsOf = time.Now().Add(-time.Minute).UnixMilli()
	})
	credential := testAccountCredentialWithJTI(t, privateKey, int64(42), time.Now().Add(time.Hour), "01JREVOKEDJUSTNOW")

	originalTransport := http.DefaultTransport
	var networkCalls atomic.Int32
	http.DefaultTransport = roundTripFunc(func(*http.Request) (*http.Response, error) {
		networkCalls.Add(1)
		return nil, errors.New("network is not allowed during account credential verification")
	})
	t.Cleanup(func() { http.DefaultTransport = originalTransport })

	result, err := ah.HandleAccount(context.Background(), AccountParams{Credential: credential})

	require.NoError(t, err)
	assert.True(t, result.OK)
	assert.Zero(t, networkCalls.Load(), "握手期不得为了查吊销状态去访问 server")
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// TestAuth_AccountCredential_GivenPeerFingerprintClaim_ThenIdentityComesFromTheCredential
// 钉住决策 8:Mode C 的对端身份来自**已验签的凭据**,不是请求体。握手结果里那个
// 指纹必须逐字等于凭据的 pfp claim。
func TestAuth_AccountCredential_GivenPeerFingerprintClaim_ThenIdentityComesFromTheCredential(t *testing.T) {
	ah, st, _ := setupAuthTest(t)
	privateKey, publicKeyPEM := testRSAKeyPair(t)
	st.Claim("42", publicKeyPEM, state.AccountCredential{})
	credential := testAccountCredentialWithPeerFingerprint(t, privateKey, int64(42),
		time.Now().Add(time.Hour), "sha256:from-credential")

	result, err := ah.HandleAccount(context.Background(), AccountParams{Credential: credential})

	require.NoError(t, err)
	assert.Equal(t, "sha256:from-credential", result.PeerFingerprint)
}

// TestAuth_AccountCredential_GivenNoPeerFingerprintClaim_ThenRejectsUnauthorized
// 缺 pfp 的凭据与签名不合法同一形态被拒 —— 不回退到请求体,因为回退等于这条要求
// 不存在。
func TestAuth_AccountCredential_GivenNoPeerFingerprintClaim_ThenRejectsUnauthorized(t *testing.T) {
	ah, st, _ := setupAuthTest(t)
	privateKey, publicKeyPEM := testRSAKeyPair(t)
	st.Claim("42", publicKeyPEM, state.AccountCredential{})
	credential := testAccountCredentialWithPeerFingerprint(t, privateKey, int64(42),
		time.Now().Add(time.Hour), "")

	_, err := ah.HandleAccount(context.Background(), AccountParams{Credential: credential})

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
	return testAccountCredentialWithJTI(t, privateKey, accountID, expiresAt, "")
}

// testAccountCredentialWithJTI mints the same credential with the standard JWT
// jti claim agentre-server sets (a ULID), which is what the revocation list
// refers to. An empty jti omits the claim.
func testAccountCredentialWithJTI(t *testing.T, privateKey *rsa.PrivateKey, accountID any, expiresAt time.Time, jti string) string {
	t.Helper()
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"RS256","typ":"JWT"}`))
	// pfp 是决策 8 之后每枚账号凭据都带的对端身份;没有它的凭据由
	// testAccountCredentialWithPeerFingerprint 单独铸,那是「凭据没说自己是谁」那条。
	payloadClaims := map[string]any{"uid": accountID, "exp": expiresAt.Unix(), "pfp": "sha256:account-client"}
	if jti != "" {
		payloadClaims["jti"] = jti
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
