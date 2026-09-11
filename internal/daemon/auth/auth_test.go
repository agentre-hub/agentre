package auth

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
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
	return NewAuthHandlers(st, pm, rl, nil, nil), st, pm
}

// setupAccountAuthTest 起一台假的 agentre-server 核验端点,并把一个真的 Introspector
// 接进 AuthHandlers —— Mode C 的判定就在这条 HTTP 边界上发生。
func setupAccountAuthTest(t *testing.T, handler http.HandlerFunc) (*AuthHandlers, *state.State, *atomic.Int32) {
	t.Helper()
	return setupAccountAuthTestWithDirect(t, handler, nil)
}

// setupAccountAuthTestWithDirect 同上,另接一个本机直连端点(地址与证书)。
func setupAccountAuthTestWithDirect(t *testing.T, handler http.HandlerFunc, direct DirectEndpoint) (*AuthHandlers, *state.State, *atomic.Int32) {
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
	return NewAuthHandlers(st, pm, rl, introspector, direct), st, &calls
}

// fakeDirectEndpoint 是 LAN server 那一侧的替身:此刻可被他机访问的 wss 地址与证书。
// 字段可以在两次握手之间改,用来演「地址 / 证书以最新值下发」。
type fakeDirectEndpoint struct {
	urls    []string
	certPEM string
}

func (e *fakeDirectEndpoint) current() ([]string, string) { return e.urls, e.certPEM }

const deliveredPeer = "sha256:desk-1"

func introspectAs(kind string) http.HandlerFunc {
	return answer(http.StatusOK,
		`{"code":0,"msg":"ok","data":{"account_id":"42","device_id":7,"kind":"`+kind+`","peer_fingerprint":"`+deliveredPeer+`","expires_in":900}}`)
}

// setupDirectAuthTest 是一台已登录账号 42、有可路由地址的 daemon,核验端点把出示的
// 凭据认作 kind 类型的设备 deliveredPeer。
func setupDirectAuthTest(t *testing.T, kind string) (*AuthHandlers, *state.State, *atomic.Int32, *fakeDirectEndpoint) {
	t.Helper()
	endpoint := &fakeDirectEndpoint{urls: []string{"wss://192.168.1.5:7456/rpc"}, certPEM: "cert-1"}
	ah, st, calls := setupAccountAuthTestWithDirect(t, introspectAs(kind), endpoint.current)
	loginReceiver(st, "42")
	return ah, st, calls, endpoint
}

// D3:同账号的桌面端完成账号握手,应答带上地址、证书与为它签发的本地直连凭据;
// agentred 把凭据记在这台桌面端的指纹名下,连同签发时的账号。
func TestAuth_Account_GivenADesktopOfTheSameAccount_WhenTheDaemonHasARoutableAddress_ThenDeliversAddressesCertificateAndARecordedCredential(t *testing.T) {
	ah, st, _, _ := setupDirectAuthTest(t, "desktop")

	result, err := ah.HandleAccount(context.Background(), AccountParams{Credential: peerPresented})

	require.NoError(t, err)
	require.NotNil(t, result.Direct)
	assert.Equal(t, []string{"wss://192.168.1.5:7456/rpc"}, result.Direct.URLs)
	assert.Equal(t, "cert-1", result.Direct.CertPEM)
	assert.NotEmpty(t, result.Direct.Credential)
	assert.NotEqual(t, peerPresented, result.Direct.Credential, "the local direct credential is agentred's own, not the account credential")
	assert.Equal(t, map[string]state.DirectCredential{deliveredPeer: {Credential: result.Direct.Credential, AccountID: "42"}},
		st.Snapshot().DirectCredentials)
}

// D4:同一台桌面端再次握手 —— 凭据沿用已有那一张,地址与证书取此刻的值。
func TestAuth_Account_GivenTheSameDesktopHandshakesAgain_ThenReusesItsCredentialWithTheCurrentAddressesAndCertificate(t *testing.T) {
	ah, _, _, endpoint := setupDirectAuthTest(t, "desktop")
	first, err := ah.HandleAccount(context.Background(), AccountParams{Credential: peerPresented})
	require.NoError(t, err)
	require.NotNil(t, first.Direct)
	endpoint.urls = []string{"wss://10.0.0.9:7456/rpc", "wss://[fd00::9]:7456/rpc"}
	endpoint.certPEM = "cert-2"

	second, err := ah.HandleAccount(context.Background(), AccountParams{Credential: peerPresented})

	require.NoError(t, err)
	require.NotNil(t, second.Direct)
	assert.Equal(t, first.Direct.Credential, second.Direct.Credential)
	assert.Equal(t, []string{"wss://10.0.0.9:7456/rpc", "wss://[fd00::9]:7456/rpc"}, second.Direct.URLs)
	assert.Equal(t, "cert-2", second.Direct.CertPEM)
}

// D3:浏览器票据、server 自用凭据与其它设备类型都不带下发内容,也不记任何凭据。
func TestAuth_Account_GivenACredentialThatIsNotADesktops_ThenDeliversNothingAndRecordsNothing(t *testing.T) {
	for _, kind := range []string{"relay_client", "server_mirror", "agentred"} {
		t.Run(kind, func(t *testing.T) {
			ah, st, _, _ := setupDirectAuthTest(t, kind)

			result, err := ah.HandleAccount(context.Background(), AccountParams{Credential: peerPresented})

			require.NoError(t, err)
			assert.Nil(t, result.Direct)
			assert.Empty(t, st.Snapshot().DirectCredentials)
		})
	}
}

// D5:找不到可被他机访问的地址时不下发地址,也不签发凭据。
func TestAuth_Account_GivenNoRoutableAddress_WhenADesktopHandshakes_ThenDeliversNothingAndRecordsNothing(t *testing.T) {
	ah, st, _, endpoint := setupDirectAuthTest(t, "desktop")
	endpoint.urls = nil

	result, err := ah.HandleAccount(context.Background(), AccountParams{Credential: peerPresented})

	require.NoError(t, err)
	assert.Nil(t, result.Direct)
	assert.Empty(t, st.Snapshot().DirectCredentials)
}

func deliverDirectCredential(t *testing.T, ah *AuthHandlers) string {
	t.Helper()
	result, err := ah.HandleAccount(context.Background(), AccountParams{Credential: peerPresented})
	require.NoError(t, err)
	require.NotNil(t, result.Direct)
	return result.Direct.Credential
}

// D8:出示下发过的凭据,不访问 server 即得到那台桌面端的身份。
func TestAuth_Direct_GivenTheCredentialADesktopWasDelivered_WhenPresented_ThenAuthenticatesAsThatDesktopWithoutContactingTheServer(t *testing.T) {
	ah, st, calls, _ := setupDirectAuthTest(t, "desktop")
	credential := deliverDirectCredential(t, ah)
	before := calls.Load()

	result, err := ah.HandleDirect(context.Background(), DirectParams{Credential: credential})

	require.NoError(t, err)
	assert.Equal(t, &DirectResult{OK: true, InstanceUUID: st.DaemonInstanceUUID, PeerFingerprint: deliveredPeer}, result)
	assert.Equal(t, before, calls.Load(), "auth.direct never asks the account server")
}

// D8:凭据不匹配以「凭据被拒」拒绝。
func TestAuth_Direct_GivenACredentialAgentredNeverIssued_WhenPresented_ThenRejectsAsCredentialRejected(t *testing.T) {
	ah, _, _, _ := setupDirectAuthTest(t, "desktop")
	credential := deliverDirectCredential(t, ah)
	last := "A"
	if strings.HasSuffix(credential, "A") {
		last = "B"
	}
	tampered := credential[:len(credential)-1] + last

	for name, presented := range map[string]string{"empty": "", "unknown": "never-issued", "one character off": tampered, "the account credential": peerPresented} {
		t.Run(name, func(t *testing.T) {
			_, err := ah.HandleDirect(context.Background(), DirectParams{Credential: presented})

			assertRPCCode(t, err, rpcerror.CodeUnauthorized)
		})
	}
}

// D8:凭据记录的账号不等于 agentred 当前登录的账号时拒绝。
func TestAuth_Direct_GivenTheRecordedAccountIsNotTheDaemonsAccount_WhenPresented_ThenRejectsAsCredentialRejected(t *testing.T) {
	ah, st, _, _ := setupDirectAuthTest(t, "desktop")
	st.Mutate(func(s *state.State) {
		s.DirectCredentials = map[string]state.DirectCredential{deliveredPeer: {Credential: "recorded-for-99", AccountID: "99"}}
	})

	_, err := ah.HandleDirect(context.Background(), DirectParams{Credential: "recorded-for-99"})

	assertRPCCode(t, err, rpcerror.CodeUnauthorized)
}

// D11:登出后、以及换到另一个账号后,签发过的凭据一律被拒。
func TestAuth_Direct_GivenTheDaemonLoggedOutOrSwitchedAccounts_WhenAnIssuedCredentialIsPresented_ThenRejects(t *testing.T) {
	ah, st, _, _ := setupDirectAuthTest(t, "desktop")
	credential := deliverDirectCredential(t, ah)

	st.Logout()
	_, err := ah.HandleDirect(context.Background(), DirectParams{Credential: credential})
	assertRPCCode(t, err, rpcerror.CodeUnauthorized)

	loginReceiver(st, "77")
	_, err = ah.HandleDirect(context.Background(), DirectParams{Credential: credential})
	assertRPCCode(t, err, rpcerror.CodeUnauthorized)
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
