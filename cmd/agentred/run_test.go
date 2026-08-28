package main

import (
	"bytes"
	"context"
	"log"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/agentre-hub/agentre/internal/daemon"
	"github.com/agentre-hub/agentre/internal/daemon/state"
)

type fakeRunDaemon struct{}

func (fakeRunDaemon) Run(context.Context) error { return nil }

func clearRunEnvironment(t *testing.T) {
	t.Helper()
	for _, name := range []string{
		"AGENTRED_HOST",
		"AGENTRED_PORT",
		"AGENTRED_TLS_CERT",
		"AGENTRED_TLS_KEY",
		"AGENTRED_SERVER_URL",
		"AGENTRED_LOG_LEVEL",
	} {
		value, exists := os.LookupEnv(name)
		require.NoError(t, os.Unsetenv(name))
		t.Cleanup(func() {
			if exists {
				_ = os.Setenv(name, value)
				return
			}
			_ = os.Unsetenv(name)
		})
	}
}

func executeRunForOptions(t *testing.T, dir string, args ...string) (daemon.Options, error) {
	t.Helper()
	var got daemon.Options
	cmd := newRunCmdWithDeps(runDeps{
		dataDir: func() (string, error) { return dir, nil },
		newDaemon: func(opts daemon.Options) (runDaemon, error) {
			got = opts
			return fakeRunDaemon{}, nil
		},
	})
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs(args)
	err := cmd.Execute()
	return got, err
}

func TestGivenPersistedRuntimeConfigurationWhenRunHasNoOverridesThenConfigurationIsRestored(t *testing.T) {
	clearRunEnvironment(t)
	dir := t.TempDir()
	st, err := state.Load(dir)
	require.NoError(t, err)
	st.Mutate(func(s *state.State) {
		s.Listen = state.ListenPrefs{
			LanHost:     "192.0.2.10",
			LanPort:     8123,
			TLSCertFile: "/persisted/cert.pem",
			TLSKeyFile:  "/persisted/key.pem",
		}
		s.HubServerURL = "https://persisted.example"
	})
	require.NoError(t, st.Save())

	got, err := executeRunForOptions(t, dir)
	require.NoError(t, err)
	assert.Equal(t, "192.0.2.10", got.LANHost)
	assert.Equal(t, 8123, got.LANPort)
	assert.Equal(t, "/persisted/cert.pem", got.TLSCertFile)
	assert.Equal(t, "/persisted/key.pem", got.TLSKeyFile)
	assert.Equal(t, "https://persisted.example", got.HubServerURL)
}

func TestGivenFlagsEnvironmentAndStateWhenRunStartsThenPriorityIsFlagsEnvironmentStateDefault(t *testing.T) {
	clearRunEnvironment(t)
	dir := t.TempDir()
	st, err := state.Load(dir)
	require.NoError(t, err)
	st.Mutate(func(s *state.State) {
		s.Listen = state.ListenPrefs{
			LanHost:     "state-host",
			LanPort:     7001,
			TLSCertFile: "/state/cert.pem",
			TLSKeyFile:  "/state/key.pem",
		}
		s.HubServerURL = "https://state.example"
	})
	require.NoError(t, st.Save())
	t.Setenv("AGENTRED_HOST", "env-host")
	t.Setenv("AGENTRED_PORT", "7002")
	t.Setenv("AGENTRED_TLS_CERT", "/env/cert.pem")
	t.Setenv("AGENTRED_SERVER_URL", "https://env.example/")

	got, err := executeRunForOptions(t, dir,
		"--host", "flag-host",
		"--tls-key", "/flag/key.pem",
	)
	require.NoError(t, err)
	assert.Equal(t, "flag-host", got.LANHost, "flag must override environment")
	assert.Equal(t, 7002, got.LANPort, "environment must override state")
	assert.Equal(t, "/env/cert.pem", got.TLSCertFile)
	assert.Equal(t, "/flag/key.pem", got.TLSKeyFile)
	assert.Equal(t, "https://env.example", got.HubServerURL)

	reloaded, err := state.Load(dir)
	require.NoError(t, err)
	assert.Equal(t, state.ListenPrefs{
		LanHost:     "flag-host",
		LanPort:     7002,
		TLSCertFile: "/env/cert.pem",
		TLSKeyFile:  "/flag/key.pem",
	}, reloaded.Listen, "resolved explicit configuration must survive service startup")
	assert.Equal(t, "https://env.example", reloaded.HubServerURL)
}

func TestGivenInvalidPortEnvironmentWhenRunStartsThenItReturnsUsageErrorWithoutStartingDaemon(t *testing.T) {
	clearRunEnvironment(t)
	t.Setenv("AGENTRED_PORT", "not-a-port")
	started := false
	cmd := newRunCmdWithDeps(runDeps{
		dataDir: func() (string, error) { return t.TempDir(), nil },
		newDaemon: func(daemon.Options) (runDaemon, error) {
			started = true
			return fakeRunDaemon{}, nil
		},
	})
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})

	err := cmd.Execute()
	require.Error(t, err)
	var usage *usageError
	assert.ErrorAs(t, err, &usage)
	assert.Contains(t, err.Error(), "AGENTRED_PORT")
	assert.False(t, started)
}

func TestGivenOutOfRangePortWhenRunStartsThenItReturnsUsageErrorWithoutStartingDaemon(t *testing.T) {
	clearRunEnvironment(t)
	started := false
	cmd := newRunCmdWithDeps(runDeps{
		dataDir: func() (string, error) { return t.TempDir(), nil },
		newDaemon: func(daemon.Options) (runDaemon, error) {
			started = true
			return fakeRunDaemon{}, nil
		},
	})
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"--port", "70000"})

	err := cmd.Execute()
	require.Error(t, err)
	var usage *usageError
	assert.ErrorAs(t, err, &usage)
	assert.Contains(t, err.Error(), "port must be between 1 and 65535")
	assert.False(t, started)
}

func TestGivenOnlyOneTLSPathWhenRunStartsThenItReturnsUsageErrorWithoutPersistingConfiguration(t *testing.T) {
	clearRunEnvironment(t)
	dir := t.TempDir()
	st, err := state.Load(dir)
	require.NoError(t, err)
	original := st.Snapshot()
	started := false
	cmd := newRunCmdWithDeps(runDeps{
		dataDir: func() (string, error) { return dir, nil },
		newDaemon: func(daemon.Options) (runDaemon, error) {
			started = true
			return fakeRunDaemon{}, nil
		},
	})
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"--tls-cert", "/tmp/cert.pem"})

	err = cmd.Execute()
	require.Error(t, err)
	var usage *usageError
	assert.ErrorAs(t, err, &usage)
	assert.Contains(t, err.Error(), "both --tls-cert and --tls-key")
	assert.False(t, started)

	reloaded, err := state.Load(dir)
	require.NoError(t, err)
	assert.Equal(t, original.Listen, reloaded.Listen)
}

// Given 一个数据目录，When agentred run 启动守护进程，
// Then 日志落到 <dataDir>/logs/agentred.log（此前 agentred 全程用 zap 的 no-op logger，
// 什么都不写，launchd 也没接管 stdout）。
func TestGivenRunWhenDaemonBootsThenLogsLandInDataDirLogFile(t *testing.T) {
	clearRunEnvironment(t)
	dir := t.TempDir()

	_, err := executeRunForOptions(t, dir)
	require.NoError(t, err)

	data, err := os.ReadFile(filepath.Join(dir, "logs", "agentred.log")) //nolint:gosec // G304: dir is this test's t.TempDir, not untrusted input.
	require.NoError(t, err)
	assert.Contains(t, string(data), "agentred.run: daemon starting")
	assert.Contains(t, string(data), "agentred.run: daemon stopped")
}

// Given --log-level=debug，When run 启动，Then debug 明细进日志文件；默认 info 时不进。
func TestGivenDebugLogLevelWhenRunStartsThenResolvedConfigurationIsLogged(t *testing.T) {
	clearRunEnvironment(t)
	verbose := t.TempDir()
	_, err := executeRunForOptions(t, verbose, "--log-level", "debug")
	require.NoError(t, err)
	data, err := os.ReadFile(filepath.Join(verbose, "logs", "agentred.log")) //nolint:gosec // G304: verbose is this test's t.TempDir, not untrusted input.
	require.NoError(t, err)
	assert.Contains(t, string(data), "agentred.run: resolved configuration")

	clearRunEnvironment(t)
	quiet := t.TempDir()
	_, err = executeRunForOptions(t, quiet)
	require.NoError(t, err)
	data, err = os.ReadFile(filepath.Join(quiet, "logs", "agentred.log")) //nolint:gosec // G304: quiet is this test's t.TempDir, not untrusted input.
	require.NoError(t, err)
	assert.NotContains(t, string(data), "agentred.run: resolved configuration")
}

// Given AGENTRED_LOG_LEVEL=debug，When run 启动，Then 环境变量与 flag 等效。
func TestGivenLogLevelEnvironmentWhenRunStartsThenItSelectsTheLevel(t *testing.T) {
	clearRunEnvironment(t)
	t.Setenv("AGENTRED_LOG_LEVEL", "debug")
	dir := t.TempDir()

	_, err := executeRunForOptions(t, dir)
	require.NoError(t, err)

	data, err := os.ReadFile(filepath.Join(dir, "logs", "agentred.log")) //nolint:gosec // G304: dir is this test's t.TempDir, not untrusted input.
	require.NoError(t, err)
	assert.Contains(t, string(data), "agentred.run: resolved configuration")
}

// Given 一个拼错的级别，When run 启动，Then 报 usage error 而不是静默退回 info。
func TestGivenUnknownLogLevelWhenRunStartsThenUsageErrorIsReturned(t *testing.T) {
	clearRunEnvironment(t)

	_, err := executeRunForOptions(t, t.TempDir(), "--log-level", "verbose")

	require.Error(t, err)
	var usage *usageError
	assert.ErrorAs(t, err, &usage)
}

// Given daemon 内仍有约十处 stdlib log.Printf(panic 恢复、shutdown 失败、重启清扫),
// When 日志初始化完成,Then 它们也被重定向进同一个日志文件,而不是只写 stderr。
func TestGivenRunWhenStdlibLogIsUsedThenItAlsoLandsInTheLogFile(t *testing.T) {
	clearRunEnvironment(t)
	dir := t.TempDir()

	_, err := executeRunForOptions(t, dir)
	require.NoError(t, err)
	log.Printf("daemon rpc handler panic: %v", "smoke")

	data, err := os.ReadFile(filepath.Join(dir, "logs", "agentred.log")) //nolint:gosec // G304: dir is this test's t.TempDir, not untrusted input.
	require.NoError(t, err)
	assert.Contains(t, string(data), "daemon rpc handler panic: smoke")
}
