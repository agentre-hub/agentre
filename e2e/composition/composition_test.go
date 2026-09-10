package composition

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/agentre-hub/agentre/e2e/preflight"
	daemonidentity "github.com/agentre-hub/agentre/internal/daemon/identity"
)

func setValidRunnerIdentity(t *testing.T) {
	t.Helper()
	t.Setenv(syncServerURLEnv, "http://127.0.0.1:43210")
	t.Setenv(syncServerUserIDEnv, "7001")
	t.Setenv(syncDeviceIDEnv, "7101")
	t.Setenv(syncDeviceFPEnv, "sha256:test-device")
	t.Setenv(syncRefreshTokenEnv, "test-refresh-token")
	t.Setenv(remotePeerURLEnv, "ws://127.0.0.1:43211/rpc")
	t.Setenv(remoteDaemonFPEnv, daemonidentity.DaemonFingerprint("test-daemon-instance"))
	t.Setenv(remoteInstanceUUIDEnv, "test-daemon-instance")
	t.Setenv(remoteDeviceTokenEnv, "test-pairing-token")
}

func TestFromPreflightGivenRunnerIdentityReturnsLoggedInLoopbackConfig(t *testing.T) {
	setValidRunnerIdentity(t)
	validated := preflight.Config{RunRoot: "/run", DataDir: "/run/data", KeychainDir: "/run/keychain"}

	got, err := FromPreflight(validated)
	if err != nil {
		t.Fatalf("FromPreflight: %v", err)
	}
	if got.Config != validated {
		t.Fatalf("validated config = %+v, want %+v", got.Config, validated)
	}
	if got.Identity.ServerURL != "http://127.0.0.1:43210" || got.Identity.ServerUserID != 7001 ||
		got.Identity.DeviceID != 7101 || got.Identity.DeviceFingerprint != "sha256:test-device" ||
		got.Identity.RefreshToken != "test-refresh-token" {
		t.Fatalf("identity = %+v", got.Identity)
	}
	if got.Remote.URL != "ws://127.0.0.1:43211/rpc" ||
		got.Remote.DaemonFingerprint != daemonidentity.DaemonFingerprint("test-daemon-instance") ||
		got.Remote.InstanceUUID != "test-daemon-instance" || got.Remote.DeviceToken != "test-pairing-token" {
		t.Fatalf("remote = %+v", got.Remote)
	}
}

func TestFromPreflightGivenNonLoopbackOrMissingIdentityRejectsComposition(t *testing.T) {
	validated := preflight.Config{RunRoot: "/run", DataDir: "/run/data", KeychainDir: "/run/keychain"}
	for name, serverURL := range map[string]string{
		"missing":      "",
		"non-loopback": "https://example.com",
	} {
		t.Run(name, func(t *testing.T) {
			setValidRunnerIdentity(t)
			t.Setenv(syncServerURLEnv, serverURL)

			if _, err := FromPreflight(validated); err == nil {
				t.Fatal("FromPreflight error = nil, want unsafe identity rejection")
			}
		})
	}
}

func TestInstallGivenFakeSetupFailureReturnsErrorToTheDedicatedEntrypoint(t *testing.T) {
	setValidRunnerIdentity(t)
	previous := installFakes
	t.Cleanup(func() { installFakes = previous })
	want := errors.New("seed failed")
	installFakes = func(context.Context) error { return want }

	err := Install(context.Background(), preflight.Config{
		RunRoot: "/run", DataDir: "/run/data", KeychainDir: "/run/keychain",
	})
	if !errors.Is(err, want) {
		t.Fatalf("Install error = %v, want %v", err, want)
	}
	if got := os.Getenv(syncRefreshTokenEnv); got != "" {
		t.Fatalf("%s = %q, want consumed on failure", syncRefreshTokenEnv, got)
	}
	if got := os.Getenv(remoteDeviceTokenEnv); got != "" {
		t.Fatalf("%s = %q, want consumed on failure", remoteDeviceTokenEnv, got)
	}
}

func TestFromPreflightGivenMissingOrNonLoopbackRemotePeerRejectsComposition(t *testing.T) {
	validated := preflight.Config{RunRoot: "/run", DataDir: "/run/data", KeychainDir: "/run/keychain"}
	for name, peerURL := range map[string]string{
		"missing":      "",
		"non-loopback": "ws://192.168.1.5:43211/rpc",
		"wrong-path":   "ws://127.0.0.1:43211/other",
	} {
		t.Run(name, func(t *testing.T) {
			setValidRunnerIdentity(t)
			t.Setenv(remotePeerURLEnv, peerURL)
			if _, err := FromPreflight(validated); err == nil {
				t.Fatal("FromPreflight error = nil, want unsafe remote peer rejection")
			}
		})
	}
}
