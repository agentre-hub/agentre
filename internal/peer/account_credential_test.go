package peer

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/agentre-hub/agentre/internal/daemon/auth"
)

func TestAccountCredentialVerifier_GivenTheServerConfirmsTheSameAccount_ThenReturnsItsIntrospection(t *testing.T) {
	verifier := newAccountCredentialVerifier(
		func(context.Context) (string, error) { return "7", nil },
		func(context.Context, string) (auth.Introspection, error) {
			return auth.Introspection{AccountID: "7", PeerFingerprint: "sha256:web-peer-7"}, nil
		},
	)

	verified, err := verifier.Verify(context.Background(), "credential")

	require.NoError(t, err)
	require.Equal(t, "sha256:web-peer-7", verified.PeerFingerprint)
}

// TestAccountCredentialVerifier_GivenAnotherAccountsCredential_ThenRejectsAsCredentialInvalid
// 钉住 H1:server 核验通过不等于是这台桌面端的对端 —— 账号必须一致,不一致时以
// 今天「凭据被拒」的同一错误(ErrCredentialInvalid)拒绝,不额外泄露它属于谁。
func TestAccountCredentialVerifier_GivenAnotherAccountsCredential_ThenRejectsAsCredentialInvalid(t *testing.T) {
	verifier := newAccountCredentialVerifier(
		func(context.Context) (string, error) { return "7", nil },
		func(context.Context, string) (auth.Introspection, error) {
			return auth.Introspection{AccountID: "99", PeerFingerprint: "sha256:other-account"}, nil
		},
	)

	_, err := verifier.Verify(context.Background(), "credential")

	require.ErrorIs(t, err, auth.ErrCredentialInvalid)
}

// TestAccountCredentialVerifier_GivenNoLogin_ThenRejectsAsReceiverNotReadyWithoutTouchingTheNetwork
// H4:这台桌面端自己都不知道登录了哪个账号,没有账号可比,握手按「接收方未就绪」
// 拒绝 —— 且不必打一次 server 就知道答案。
func TestAccountCredentialVerifier_GivenNoLogin_ThenRejectsAsReceiverNotReadyWithoutTouchingTheNetwork(t *testing.T) {
	introspectCalls := 0
	verifier := newAccountCredentialVerifier(
		func(context.Context) (string, error) { return "", nil },
		func(context.Context, string) (auth.Introspection, error) {
			introspectCalls++
			return auth.Introspection{}, nil
		},
	)

	_, err := verifier.Verify(context.Background(), "anything")

	require.ErrorIs(t, err, auth.ErrReceiverNotReady)
	require.Zero(t, introspectCalls)
}

// TestAccountCredentialVerifier_GivenTheIntrospectorRejectsTheCredential_ThenPropagatesItsVerdict
// 核验本体(在线核验、缓存、错误分类)全在 auth.Introspector —— 这里只钉住结论
// 原样传回,不重新分类。
func TestAccountCredentialVerifier_GivenTheIntrospectorRejectsTheCredential_ThenPropagatesItsVerdict(t *testing.T) {
	verifier := newAccountCredentialVerifier(
		func(context.Context) (string, error) { return "7", nil },
		func(context.Context, string) (auth.Introspection, error) {
			return auth.Introspection{}, auth.ErrAccountServerUnreachable
		},
	)

	_, err := verifier.Verify(context.Background(), "credential")

	require.ErrorIs(t, err, auth.ErrAccountServerUnreachable)
}

func TestAccountCredentialVerifier_GivenTheAccountLookupFails_ThenPropagatesTheError(t *testing.T) {
	lookupErr := errors.New("server_state read failed")
	verifier := newAccountCredentialVerifier(
		func(context.Context) (string, error) { return "", lookupErr },
		func(context.Context, string) (auth.Introspection, error) {
			t.Fatal("must not introspect when this device's own account cannot be resolved")
			return auth.Introspection{}, nil
		},
	)

	_, err := verifier.Verify(context.Background(), "anything")

	require.ErrorIs(t, err, lookupErr)
}
