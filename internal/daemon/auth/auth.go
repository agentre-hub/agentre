package auth

import (
	"context"
	"time"

	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"

	"github.com/agentre-hub/agentre/internal/daemon/identity"
	"github.com/agentre-hub/agentre/internal/daemon/pairing"
	"github.com/agentre-hub/agentre/internal/daemon/state"
	"github.com/agentre-hub/agentre/pkg/wire/rpcerror"

	"github.com/agentre-hub/agentre/pkg/wire/devicefp"
)

// PairParams is the payload of an auth.pair request (Mode A).
type PairParams struct {
	Code              string `json:"code"`
	DeviceName        string `json:"deviceName"`
	DeviceFingerprint string `json:"deviceFingerprint"`
}

// PairResult is returned to the client after a successful pair, providing
// the deviceToken (used in subsequent connects) and the daemonFingerprint
// for TOFU pinning.
type PairResult struct {
	DeviceToken       string `json:"deviceToken"`
	DaemonFingerprint string `json:"daemonFingerprint"`
	InstanceUUID      string `json:"instanceUUID"`
}

// ConnectParams is the payload of an auth.connect request (Mode B).
type ConnectParams struct {
	DeviceFingerprint         string `json:"deviceFingerprint"`
	DeviceToken               string `json:"deviceToken"`
	ExpectedDaemonFingerprint string `json:"expectedDaemonFingerprint"`
}

// ConnectResult is returned after a successful Mode B handshake.
type ConnectResult struct {
	OK           bool   `json:"ok"`
	InstanceUUID string `json:"instanceUUID"`
}

// AccountParams is the payload of an auth.account request (Mode C).
// Credential is an opaque account credential issued by agentre-server; only
// the server can say whose it is (spec H1).
//
// 它**没有**对端指纹字段:Mode C 的对端身份只从 server 对凭据的核验结论取(决策 8)。
// 请求体里自报的那个字符串从来没有被任何东西验证过,留着就等于「说了不算」。
type AccountParams struct {
	Credential string `json:"credential"`
}

// AccountResult is returned after a successful Mode C handshake.
//
// 它与 ConnectResult 分开正是因为多出来的这个身份:Mode B 的指纹由 device token
// 绑定、来自请求体,Mode C 的只能来自凭据。共用一个结构会让「哪一条路上的指纹被
// 验过」这件事又变得看不出来。
type AccountResult struct {
	OK           bool   `json:"ok"`
	InstanceUUID string `json:"instanceUUID"`
	// PeerFingerprint 是 server 核验凭据后给出的对端身份 —— 对端会话落进
	// peer_fingerprint 的那个值,也回写给调用方(AuthAccountResponse.peer_fingerprint)。
	PeerFingerprint string `json:"peerFingerprint"`
	// Direct is the auto-direct delivery for a desktop caller (D3); nil when the
	// caller is not a desktop or the daemon has no address to offer (D5).
	Direct *DirectDelivery `json:"-"`
}

// DirectDelivery is what an account handshake hands a desktop so it can later
// connect directly: the daemon's current wss addresses, the certificate they
// present, and the local direct credential issued for that desktop.
type DirectDelivery struct {
	URLs       []string
	CertPEM    string
	Credential string
}

// DirectEndpoint reports how another machine reaches this daemon directly: its
// routable wss addresses and the PEM certificate those addresses present. No
// addresses means none another machine could reach.
type DirectEndpoint func() (urls []string, certPEM string)

// DirectParams is the payload of an auth.direct request. It names no peer:
// the credential alone identifies the desktop it was issued to.
type DirectParams struct {
	Credential string
}

// DirectResult is returned after a successful auth.direct handshake. Its peer
// identity is the fingerprint the credential was issued under — the value a
// relayed auth.account named for that same desktop.
type DirectResult struct {
	OK              bool
	InstanceUUID    string
	PeerFingerprint string
}

// deviceKindDesktop is the introspection kind of a desktop's device token —
// the only caller auto-direct is delivered to. Browser relay tickets
// (relay_client) and server-issued credentials (server_mirror) are not.
const deviceKindDesktop = "desktop"

// ErrDirectCredentialInvalid rejects a local direct credential agentred does
// not hold for its current account: the same -32001 "credential rejected".
var ErrDirectCredentialInvalid = &rpcerror.Error{Code: rpcerror.CodeUnauthorized, Message: "local direct credential rejected"}

// AuthHandlers owns the pre-authentication gate. The daemon wires these
// into the registry under method names "auth.pair" / "auth.connect" /
// "auth.account" / "auth.direct" / "auth.revoke".
type AuthHandlers struct {
	st       *state.State
	pairing  *pairing.Manager
	rl       *pairing.RateLimiter
	accounts AccountVerifier
	direct   DirectEndpoint
}

// NewAuthHandlers constructs an AuthHandlers wired to the given state,
// pairing manager, rate limiter, account verifier and direct endpoint (nil
// means auto-direct is never delivered).
func NewAuthHandlers(st *state.State, pm *pairing.Manager, rl *pairing.RateLimiter, accounts AccountVerifier, direct DirectEndpoint) *AuthHandlers {
	return &AuthHandlers{st: st, pairing: pm, rl: rl, accounts: accounts, direct: direct}
}

// HandlePair implements Mode A. The ip arg is the source remote address
// used by the per-IP rate limiter.
func (a *AuthHandlers) HandlePair(ctx context.Context, ip string, p PairParams) (*PairResult, error) {
	if !a.rl.Allow(ip) {
		return nil, &rpcerror.Error{Code: rpcerror.ErrPairing.Code, Message: "Pairing rate-limited"}
	}
	if !a.pairing.Consume(p.Code) {
		return nil, rpcerror.ErrPairing
	}
	tok, err := pairing.NewDeviceToken()
	if err != nil {
		return nil, &rpcerror.Error{Code: rpcerror.ErrInternal.Code, Message: err.Error()}
	}
	now := time.Now().UnixMilli()
	a.st.Mutate(func(s *state.State) {
		s.PairedPeers[p.DeviceFingerprint] = state.PairedPeer{
			DeviceName:  p.DeviceName,
			DeviceToken: tok,
			PairedAt:    now,
			LastSeenAt:  now,
		}
	})
	if err := a.st.Save(); err != nil {
		return nil, &rpcerror.Error{Code: rpcerror.ErrInternal.Code, Message: err.Error()}
	}
	return &PairResult{
		DeviceToken:       tok,
		DaemonFingerprint: string(identity.DaemonFingerprint(a.st.DaemonInstanceUUID)),
		InstanceUUID:      a.st.DaemonInstanceUUID,
	}, nil
}

// HandleConnect implements Mode B. It verifies the presented deviceToken
// (constant-time) and, when supplied, the TOFU daemonFingerprint pin.
func (a *AuthHandlers) HandleConnect(ctx context.Context, p ConnectParams) (*ConnectResult, error) {
	peer, ok := a.st.PairedPeers[p.DeviceFingerprint]
	if !ok {
		return nil, rpcerror.ErrUnauthorized
	}
	if !pairing.VerifyDeviceToken(peer.DeviceToken, p.DeviceToken) {
		return nil, rpcerror.ErrUnauthorized
	}
	want := identity.DaemonFingerprint(a.st.DaemonInstanceUUID)
	if p.ExpectedDaemonFingerprint != "" && devicefp.Carrier(p.ExpectedDaemonFingerprint) != want {
		return nil, &rpcerror.Error{Code: rpcerror.ErrUnauthorized.Code,
			Message: "daemon fingerprint mismatch (TOFU)"}
	}
	a.st.Mutate(func(s *state.State) {
		p2 := s.PairedPeers[p.DeviceFingerprint]
		p2.LastSeenAt = time.Now().UnixMilli()
		s.PairedPeers[p.DeviceFingerprint] = p2
	})
	_ = a.st.Save()
	return &ConnectResult{OK: true, InstanceUUID: a.st.DaemonInstanceUUID}, nil
}

// HandleAccount implements Mode C. The daemon must itself belong to an account;
// the presented credential is then verified online by the account server
// (AccountVerifier) and accepted only when it belongs to that same account.
// The connection's peer identity is the one the server's verdict names.
func (a *AuthHandlers) HandleAccount(ctx context.Context, p AccountParams) (*AccountResult, error) {
	snapshot := a.st.Snapshot()
	if snapshot.AccountID == "" {
		return nil, ErrReceiverNotReady
	}
	verified, err := a.accounts.Verify(ctx, p.Credential)
	if err != nil {
		return nil, err
	}
	if verified.AccountID != snapshot.AccountID {
		logger.Ctx(ctx).Info("auth.HandleAccount: rejected a credential that belongs to another account",
			zap.String("accountId", snapshot.AccountID), zap.String("credentialAccountId", verified.AccountID))
		return nil, ErrCredentialInvalid
	}
	return &AccountResult{
		OK: true, InstanceUUID: snapshot.DaemonInstanceUUID,
		PeerFingerprint: verified.PeerFingerprint,
		Direct:          a.deliverDirect(ctx, verified, snapshot.AccountID),
	}, nil
}

// deliverDirect issues (or reuses, D4) the local direct credential of a
// verified desktop and pairs it with the daemon's current addresses and
// certificate. A failure here only withholds the delivery: the account
// handshake itself has already succeeded.
func (a *AuthHandlers) deliverDirect(ctx context.Context, verified Introspection, accountID string) *DirectDelivery {
	if verified.Kind != deviceKindDesktop || a.direct == nil {
		return nil
	}
	urls, certPEM := a.direct()
	if len(urls) == 0 {
		return nil
	}
	candidate, err := pairing.NewDeviceToken()
	if err != nil {
		logger.Ctx(ctx).Warn("auth.HandleAccount: could not mint a local direct credential, delivering none",
			zap.String("peerFingerprint", verified.PeerFingerprint), zap.Error(err))
		return nil
	}
	credential, issued, err := a.st.EnsureDirectCredential(verified.PeerFingerprint, accountID, candidate)
	if err != nil {
		logger.Ctx(ctx).Warn("auth.HandleAccount: could not record a local direct credential, delivering none",
			zap.String("peerFingerprint", verified.PeerFingerprint), zap.Error(err))
		return nil
	}
	if issued {
		logger.Ctx(ctx).Info("auth.HandleAccount: issued a local direct credential",
			zap.String("peerFingerprint", verified.PeerFingerprint), zap.String("accountId", accountID))
	}
	return &DirectDelivery{URLs: urls, CertPEM: certPEM, Credential: credential}
}

// HandleDirect implements auth.direct (D8). It never contacts the account
// server: the presented credential must be one this daemon issued, and the
// account it was issued under must be the account the daemon belongs to now.
func (a *AuthHandlers) HandleDirect(ctx context.Context, p DirectParams) (*DirectResult, error) {
	snapshot := a.st.Snapshot()
	if snapshot.AccountID == "" {
		return nil, ErrReceiverNotReady
	}
	fingerprint, record, ok := matchDirectCredential(snapshot.DirectCredentials, p.Credential)
	if !ok {
		logger.Ctx(ctx).Info("auth.HandleDirect: rejected a local direct credential this daemon does not hold")
		return nil, ErrDirectCredentialInvalid
	}
	if record.AccountID != snapshot.AccountID {
		logger.Ctx(ctx).Info("auth.HandleDirect: rejected a local direct credential issued under another account",
			zap.String("peerFingerprint", fingerprint), zap.String("accountId", snapshot.AccountID),
			zap.String("credentialAccountId", record.AccountID))
		return nil, ErrDirectCredentialInvalid
	}
	return &DirectResult{OK: true, InstanceUUID: snapshot.DaemonInstanceUUID, PeerFingerprint: fingerprint}, nil
}

// matchDirectCredential finds the desktop a presented credential was issued
// to. Every record is compared in constant time and the scan never stops
// early, so neither a byte position nor a record's place in the map is
// revealed by timing.
func matchDirectCredential(records map[string]state.DirectCredential, presented string) (string, state.DirectCredential, bool) {
	var (
		fingerprint string
		found       state.DirectCredential
		ok          bool
	)
	for candidate, record := range records {
		if pairing.VerifyDeviceToken(record.Credential, presented) && !ok {
			fingerprint, found, ok = candidate, record, true
		}
	}
	return fingerprint, found, ok
}

// HandleRevoke removes a paired peer from state. Used by future "remove
// device" UI on the desktop side.
func (a *AuthHandlers) HandleRevoke(ctx context.Context, fingerprint string) error {
	a.st.Mutate(func(s *state.State) {
		delete(s.PairedPeers, fingerprint)
	})
	return a.st.Save()
}
