package auth

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
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
