package main

import (
	"bytes"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/cago-frame/cago/configs"

	"github.com/agentre-hub/agentre/internal/buildinfo"
	"github.com/agentre-hub/agentre/internal/daemon/state"
	"github.com/agentre-hub/agentre/internal/pkg/paths"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

func setAgentredBuildIdentityForTest(t *testing.T, version, commit string) {
	t.Helper()
	previousVersion, previousCommit := configs.Version, buildinfo.CommitID
	configs.Version, buildinfo.CommitID = version, commit
	t.Cleanup(func() {
		configs.Version, buildinfo.CommitID = previousVersion, previousCommit
	})
}

// TestRootSubcommands locks in the public CLI surface: any accidental rename
// or removal of a subcommand here breaks the test, which is much louder than
// users discovering it at runtime.
func TestRootSubcommands(t *testing.T) {
	root := newRootCmd()
	got := map[string]bool{}
	for _, c := range root.Commands() {
		got[c.Name()] = true
	}
	for _, want := range []string{"run", "status", "pair", "login", "unclaim", "llm", "claudecode", "service"} {
		assert.True(t, got[want], "missing subcommand %q", want)
	}

	llm, _, err := root.Find([]string{"llm"})
	assert.NoError(t, err)
	llmSubs := map[string]bool{}
	for _, c := range llm.Commands() {
		llmSubs[c.Name()] = true
	}
	for _, want := range []string{"list", "add", "remove"} {
		assert.True(t, llmSubs[want], "missing llm subcommand %q", want)
	}
}

// TestUnclaimClearsAccountLocally verifies that unclaim returns the daemon to the
// unclaimed state by removing every account-derived local cache — credential,
// verification key, and revocation list — exclusively through state.json.
//
// This fixture records no HubServerURL, so there is nowhere to notify and the
// command stays purely local: the default HTTP transport is a tripwire proving
// the local clear itself never depends on the network. A daemon that *does* know
// its server best-effort revokes the authorization first — see
// TestUnclaimBestEffortRevokesTheAccountAuthorizationBeforeClearingLocalState.
func TestUnclaimClearsAccountLocally(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("AGENTRED_DATA_DIR", dir)
	st, err := state.Load(dir)
	require.NoError(t, err)
	st.Mutate(func(s *state.State) {
		s.AccountID = "account-42"
		s.VerificationPublicKeyPEM = "-----BEGIN PUBLIC KEY-----cached"
		s.Credential = state.AccountCredential{
			AccessToken: "access", RefreshToken: "refresh", DeviceID: 7,
		}
		// The revocation list is pulled from — and only meaningful under — the
		// claimed account, so returning to 未认领状态 has to drop it too.
		s.RevokedJTIs = []string{"jti-revoked-1", "jti-revoked-2"}
		s.RevocationsAsOf = 1_716_003_600_000
	})
	require.NoError(t, st.Save())

	originalTransport := http.DefaultTransport
	var networkCalls atomic.Int32
	http.DefaultTransport = roundTripFunc(func(*http.Request) (*http.Response, error) {
		networkCalls.Add(1)
		return nil, assert.AnError
	})
	t.Cleanup(func() { http.DefaultTransport = originalTransport })

	cmd := newUnclaimCmd()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	require.NoError(t, cmd.Execute())

	got, err := state.Load(dir)
	require.NoError(t, err)
	assert.Empty(t, got.AccountID)
	assert.Empty(t, got.VerificationPublicKeyPEM)
	assert.Equal(t, state.AccountCredential{}, got.Credential)
	assert.Empty(t, got.RevokedJTIs, "the previous account's revocation list must not survive unclaim")
	assert.Zero(t, got.RevocationsAsOf, "the previous account's revocation timestamp must not survive unclaim")
	assert.Zero(t, networkCalls.Load(), "with no server recorded, unclaim has nowhere to notify")

	gotDir, err := paths.AgentredDataDir()
	require.NoError(t, err)
	assert.Equal(t, dir, gotDir)
}

func TestLoginServerFlagUsesEnvironmentFallback(t *testing.T) {
	t.Setenv("AGENTRED_SERVER_URL", "https://account.example")
	login, _, err := newRootCmd().Find([]string{"login"})
	require.NoError(t, err)
	serverURL, err := login.Flags().GetString("server")
	require.NoError(t, err)
	assert.Equal(t, "https://account.example", serverURL)
}

func TestRunServerFlagUsesEnvironmentFallback(t *testing.T) {
	t.Setenv("AGENTRED_SERVER_URL", "https://account.example")
	run := newRunCmd()
	serverURL, err := run.Flags().GetString("server")
	require.NoError(t, err)
	assert.Equal(t, "https://account.example", serverURL)
}

func TestLLMAddRequiresUUIDKey(t *testing.T) {
	root := newRootCmd()

	// --key not provided → cobra required-flag error (not usageError, but still an error)
	root.SetArgs([]string{"llm", "add", "--name=test", "--type=openai"})
	root.SetOut(&bytes.Buffer{})
	root.SetErr(&bytes.Buffer{})
	err := root.Execute()
	assert.Error(t, err, "missing --key should be an error")

	// --key is not a valid UUID → usageError
	root2 := newRootCmd()
	root2.SetArgs([]string{"llm", "add", "--key=not-a-uuid", "--name=test", "--type=openai", "--api-key=x"})
	root2.SetOut(&bytes.Buffer{})
	root2.SetErr(&bytes.Buffer{})
	err2 := root2.Execute()
	assert.Error(t, err2)
	_, ok := err2.(*usageError)
	assert.True(t, ok, "invalid UUID should produce usageError, got %T: %v", err2, err2)
}

func TestLLMRemoveRequiresUUIDKey(t *testing.T) {
	root := newRootCmd()

	// old positional numeric arg is gone; --key with non-UUID → usageError
	root.SetArgs([]string{"llm", "remove", "--key=not-a-uuid"})
	root.SetOut(&bytes.Buffer{})
	root.SetErr(&bytes.Buffer{})
	err := root.Execute()
	assert.Error(t, err)
	_, ok := err.(*usageError)
	assert.True(t, ok, "non-UUID --key should produce usageError, got %T: %v", err, err)
}

func TestUsageErrorExitCode(t *testing.T) {
	// old positional-arg numeric remove is gone; --key with non-UUID produces usageError
	root := newRootCmd()
	root.SetArgs([]string{"llm", "remove", "--key=not-a-uuid"})
	root.SetOut(&bytes.Buffer{})
	root.SetErr(&bytes.Buffer{})

	err := root.Execute()
	assert.Error(t, err)
	_, ok := err.(*usageError)
	assert.True(t, ok, "expected usageError, got %T", err)
}

func TestRunFlagDefaults(t *testing.T) {
	root := newRootCmd()
	runCmd, _, err := root.Find([]string{"run"})
	assert.NoError(t, err)

	port, err := runCmd.Flags().GetInt("port")
	assert.NoError(t, err)
	assert.Equal(t, 7456, port)

	host, err := runCmd.Flags().GetString("host")
	assert.NoError(t, err)
	assert.Equal(t, "0.0.0.0", host)

	assert.Nil(t, runCmd.Flags().Lookup("key-storage"))
}

func TestGivenInjectedBuildIdentityWhenRequestingVersionThenReportsStableFormat(t *testing.T) {
	setAgentredBuildIdentityForTest(t, "v1.2.3", "abcdef1234567890")

	root := newRootCmd()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetArgs([]string{"--version"})
	require.NoError(t, root.Execute())
	assert.Equal(t, "agentred v1.2.3 (abcdef1)\n", buf.String())
}

func TestRootHelpMentionsBinary(t *testing.T) {
	root := newRootCmd()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetArgs([]string{"--help"})
	assert.NoError(t, root.Execute())
	assert.True(t, strings.Contains(buf.String(), "agentred"))
}
