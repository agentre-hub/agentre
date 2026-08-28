package auth

import (
	"bytes"
	"context"
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"strings"
	"time"

	"github.com/agentre-hub/agentre/internal/daemon/identity"
	"github.com/agentre-hub/agentre/internal/daemon/pairing"
	"github.com/agentre-hub/agentre/internal/daemon/state"
	"github.com/agentre-hub/agentre/internal/pkg/rpcerror"
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
// Credential is the account access-token JWT issued by agentre-server.
type AccountParams struct {
	Credential        string `json:"credential"`
	DeviceFingerprint string `json:"deviceFingerprint"`
}

const (
	accountCredentialKeyUnavailable = "account credential rejected: cached verification key unavailable"
	accountCredentialInvalid        = "account credential invalid"
	accountCredentialExpired        = "account credential expired"
	accountCredentialSignature      = "account credential signature invalid"
	accountCredentialMismatch       = "account credential account mismatch"
	accountCredentialRevoked        = "account credential revoked"

	accountCredentialClockSkew = time.Minute
)

// AuthHandlers owns the pre-authentication gate. The daemon wires these
// into the registry under method names "auth.pair" / "auth.connect" /
// "auth.account" / "auth.revoke".
type AuthHandlers struct {
	st      *state.State
	pairing *pairing.Manager
	rl      *pairing.RateLimiter
}

// NewAuthHandlers constructs an AuthHandlers wired to the given state,
// pairing manager, and rate limiter.
func NewAuthHandlers(st *state.State, pm *pairing.Manager, rl *pairing.RateLimiter) *AuthHandlers {
	return &AuthHandlers{st: st, pairing: pm, rl: rl}
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
		DaemonFingerprint: identity.DaemonFingerprint(a.st.DaemonInstanceUUID),
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
	if p.ExpectedDaemonFingerprint != "" && p.ExpectedDaemonFingerprint != want {
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

// HandleAccount implements Mode C. It verifies an account credential entirely
// from the daemon's cached public key and cached revocation list, without
// contacting agentre-server.
func (a *AuthHandlers) HandleAccount(ctx context.Context, p AccountParams) (*ConnectResult, error) {
	snapshot := a.st.Snapshot()
	if snapshot.AccountID == "" || snapshot.VerificationPublicKeyPEM == "" {
		return nil, accountCredentialError(accountCredentialKeyUnavailable)
	}
	publicKeyPEM := snapshot.VerificationPublicKeyPEM
	if len(snapshot.VerificationPublicKeys) != 0 {
		kid, err := accountCredentialKID(p.Credential)
		if err != nil {
			return nil, err
		}
		publicKeyPEM = snapshot.VerificationPublicKeys[kid]
		if publicKeyPEM == "" {
			return nil, accountCredentialError(accountCredentialInvalid)
		}
	}
	publicKey, err := accountPublicKey(publicKeyPEM)
	if err != nil {
		return nil, accountCredentialError(accountCredentialKeyUnavailable)
	}
	accountID, jti, err := verifyAccountCredentialWithMaxLifetime(p.Credential, publicKey,
		time.Duration(snapshot.MaxTokenLifetimeSeconds)*time.Second)
	if err != nil {
		return nil, err
	}
	if accountID != snapshot.AccountID {
		return nil, accountCredentialError(accountCredentialMismatch)
	}
	// The revocation list is consulted from the cached snapshot only. A jti the
	// daemon has not pulled yet still authenticates — that is R4's acknowledged
	// delay (R19), and it is what keeps the handshake free of network round
	// trips when the account server is unreachable (R3).
	if isRevokedCredential(jti, snapshot.RevokedJTIs) {
		return nil, accountCredentialError(accountCredentialRevoked)
	}
	return &ConnectResult{OK: true, InstanceUUID: snapshot.DaemonInstanceUUID}, nil
}

func isRevokedCredential(jti string, revoked []string) bool {
	if jti == "" {
		return false
	}
	for _, candidate := range revoked {
		if candidate == jti {
			return true
		}
	}
	return false
}

func accountCredentialError(reason string) *rpcerror.Error {
	return &rpcerror.Error{Code: rpcerror.ErrUnauthorized.Code, Message: reason}
}

func accountPublicKey(publicKeyPEM string) (*rsa.PublicKey, error) {
	block, rest := pem.Decode([]byte(publicKeyPEM))
	if block == nil || strings.TrimSpace(string(rest)) != "" {
		return nil, accountCredentialError(accountCredentialKeyUnavailable)
	}
	key, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return x509.ParsePKCS1PublicKey(block.Bytes)
	}
	publicKey, ok := key.(*rsa.PublicKey)
	if !ok {
		return nil, accountCredentialError(accountCredentialKeyUnavailable)
	}
	return publicKey, nil
}

// verifyAccountCredential returns the credential's account id and its jti (the
// identity the account's revocation list refers to) once signature and expiry
// hold. Verification is purely local — see HandleAccount.
func verifyAccountCredentialWithMaxLifetime(credential string, publicKey *rsa.PublicKey,
	maxLifetime time.Duration) (string, string, error) {
	parts := strings.Split(credential, ".")
	if len(parts) != 3 {
		return "", "", accountCredentialError(accountCredentialInvalid)
	}
	var header map[string]json.RawMessage
	headerJSON, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil || json.Unmarshal(headerJSON, &header) != nil {
		return "", "", accountCredentialError(accountCredentialInvalid)
	}
	var algorithm string
	if json.Unmarshal(header["alg"], &algorithm) != nil || algorithm != "RS256" {
		return "", "", accountCredentialError(accountCredentialInvalid)
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return "", "", accountCredentialError(accountCredentialInvalid)
	}
	var claims map[string]json.RawMessage
	if json.Unmarshal(payload, &claims) != nil {
		return "", "", accountCredentialError(accountCredentialInvalid)
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return "", "", accountCredentialError(accountCredentialInvalid)
	}
	digest := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if rsa.VerifyPKCS1v15(publicKey, crypto.SHA256, digest[:], signature) != nil {
		return "", "", accountCredentialError(accountCredentialSignature)
	}
	expiresAt, err := accountCredentialExpiry(claims)
	if err != nil {
		return "", "", err
	}
	if time.Now().After(expiresAt.Add(accountCredentialClockSkew)) {
		return "", "", accountCredentialError(accountCredentialExpired)
	}
	if maxLifetime > 0 {
		issuedAt, err := accountCredentialTime(claims, "iat")
		if err != nil || expiresAt.Sub(issuedAt) > maxLifetime {
			return "", "", accountCredentialError(accountCredentialInvalid)
		}
	}
	accountID, err := accountIDFromCredentialClaims(claims)
	if err != nil {
		return "", "", err
	}
	return accountID, accountCredentialJTI(claims), nil
}

func accountCredentialKID(credential string) (string, error) {
	parts := strings.Split(credential, ".")
	if len(parts) != 3 {
		return "", accountCredentialError(accountCredentialInvalid)
	}
	headerJSON, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return "", accountCredentialError(accountCredentialInvalid)
	}
	var header struct {
		KID string `json:"kid"`
	}
	if json.Unmarshal(headerJSON, &header) != nil || header.KID == "" {
		return "", accountCredentialError(accountCredentialInvalid)
	}
	return header.KID, nil
}

// accountCredentialJTI reads the standard JWT jti claim (agentre-server signs a
// ULID there). A credential without one is simply never on the revocation list;
// its short expiry remains the only thing that ends it.
func accountCredentialJTI(claims map[string]json.RawMessage) string {
	rawJTI, ok := claims["jti"]
	if !ok {
		return ""
	}
	var jti string
	if json.Unmarshal(rawJTI, &jti) != nil {
		return ""
	}
	return jti
}

func accountCredentialExpiry(claims map[string]json.RawMessage) (time.Time, error) {
	return accountCredentialTime(claims, "exp")
}

func accountCredentialTime(claims map[string]json.RawMessage, name string) (time.Time, error) {
	rawExpiry, ok := claims[name]
	if !ok {
		return time.Time{}, accountCredentialError(accountCredentialInvalid)
	}
	decoder := json.NewDecoder(bytes.NewReader(rawExpiry))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return time.Time{}, accountCredentialError(accountCredentialInvalid)
	}
	expiresAt, ok := value.(json.Number)
	if !ok {
		return time.Time{}, accountCredentialError(accountCredentialInvalid)
	}
	seconds, err := expiresAt.Int64()
	if err != nil {
		return time.Time{}, accountCredentialError(accountCredentialInvalid)
	}
	return time.Unix(seconds, 0), nil
}

func accountIDFromCredentialClaims(claims map[string]json.RawMessage) (string, error) {
	rawAccountID, ok := claims["uid"]
	if !ok {
		return "", accountCredentialError(accountCredentialInvalid)
	}
	var accountID string
	if err := json.Unmarshal(rawAccountID, &accountID); err == nil {
		if accountID != "" && accountID != "0" {
			return accountID, nil
		}
		return "", accountCredentialError(accountCredentialInvalid)
	}
	decoder := json.NewDecoder(bytes.NewReader(rawAccountID))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return "", accountCredentialError(accountCredentialInvalid)
	}
	accountNumber, ok := value.(json.Number)
	if !ok || accountNumber.String() == "" || accountNumber.String() == "0" {
		return "", accountCredentialError(accountCredentialInvalid)
	}
	return accountNumber.String(), nil
}

// HandleRevoke removes a paired peer from state. Used by future "remove
// device" UI on the desktop side.
func (a *AuthHandlers) HandleRevoke(ctx context.Context, fingerprint string) error {
	a.st.Mutate(func(s *state.State) {
		delete(s.PairedPeers, fingerprint)
	})
	return a.st.Save()
}
