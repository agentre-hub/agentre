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
		"AGENTRED_TLS",
		"AGENTRED_TLS_CERT",
		"AGENTRED_TLS_KEY",
		"AGENTRED_SERVER_URL",
		"AGENTRED_LOG_LEVEL",
		"AGENTRED_ADVERTISE_ADDR",
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
		s.AccountServerURL = "https://persisted.example"
	})
	require.NoError(t, st.Save())

	got, err := executeRunForOptions(t, dir)
	require.NoError(t, err)
	assert.Equal(t, "192.0.2.10", got.LANHost)
	assert.Equal(t, 8123, got.LANPort)
	assert.Equal(t, "/persisted/cert.pem", got.TLSCertFile)
	assert.Equal(t, "/persisted/key.pem", got.TLSKeyFile)
	assert.Equal(t, "https://persisted.example", got.AccountServerURL)
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
		s.AccountServerURL = "https://state.example"
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
	assert.Equal(t, "https://env.example", got.AccountServerURL)

	reloaded, err := state.Load(dir)
	require.NoError(t, err)
	assert.Equal(t, state.ListenPrefs{
		LanHost:     "flag-host",
		LanPort:     7002,
		TLSCertFile: "/env/cert.pem",
		TLSKeyFile:  "/flag/key.pem",
	}, reloaded.Listen, "resolved explicit configuration must survive service startup")
	assert.Equal(t, "https://env.example", reloaded.AccountServerURL)
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

// --tls 不必配证书;它和其它监听参数一样记进 state.json,之后不带参数的 run(包括
// service start)照样只接受 wss。
func TestGivenTLSFlagWithoutCertificateWhenRunStartsThenTLSReachesDaemonAndIsRestoredLater(t *testing.T) {
	clearRunEnvironment(t)
	dir := t.TempDir()

	got, err := executeRunForOptions(t, dir, "--tls")
	require.NoError(t, err)
	assert.True(t, got.TLS)
	assert.Empty(t, got.TLSCertFile)
	assert.Empty(t, got.TLSKeyFile)

	restored, err := executeRunForOptions(t, dir)
	require.NoError(t, err)
	assert.True(t, restored.TLS, "a run without flags keeps the persisted --tls")
}

func TestGivenTLSEnvironmentWhenRunStartsThenItSelectsTLS(t *testing.T) {
	clearRunEnvironment(t)
	t.Setenv("AGENTRED_TLS", "true")

	got, err := executeRunForOptions(t, t.TempDir())
	require.NoError(t, err)
	assert.True(t, got.TLS)
}

func TestGivenPersistedTLSWhenRunPassesTLSFalseThenTLSIsTurnedOffAndRemembered(t *testing.T) {
	clearRunEnvironment(t)
	dir := t.TempDir()
	st, err := state.Load(dir)
	require.NoError(t, err)
	st.Mutate(func(s *state.State) { s.Listen = state.ListenPrefs{LanHost: "192.0.2.10", LanPort: 8123, TLS: true} })
	require.NoError(t, st.Save())

	got, err := executeRunForOptions(t, dir, "--tls=false")
	require.NoError(t, err)
	assert.False(t, got.TLS, "an explicit false must override the persisted true")

	reloaded, err := state.Load(dir)
	require.NoError(t, err)
	assert.False(t, reloaded.Listen.TLS)
}

func TestGivenUnparsableTLSEnvironmentWhenRunStartsThenItReturnsUsageErrorWithoutStartingDaemon(t *testing.T) {
	clearRunEnvironment(t)
	t.Setenv("AGENTRED_TLS", "sometimes")
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
	assert.Contains(t, err.Error(), "AGENTRED_TLS")
	assert.False(t, started)
}

func TestGivenTLSDisabledWithCertificateWhenRunStartsThenItReturnsUsageErrorWithoutPersistingConfiguration(t *testing.T) {
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
	cmd.SetArgs([]string{"--tls=false", "--tls-cert", "/tmp/cert.pem", "--tls-key", "/tmp/key.pem"})

	err = cmd.Execute()
	require.Error(t, err)
	var usage *usageError
	assert.ErrorAs(t, err, &usage)
	assert.Contains(t, err.Error(), "--tls=false")
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

// ── 已登录的 daemon 不许被 run 悄悄指到另一套 server ─────────────────────────
//
// 账号标识与凭据全都是**上一套 server 签发的**：指到另一套之后，中继登记
// 与刷新一律被拒，而 credentialRefresher 拿到 invalid_grant 只是停掉中继续期并写
// 一行日志，daemon 自己仍然认为「我已登录」，LAN 照常。用户看到的只有「这台机器
// 就是不上线」，线索全在日志里。
//
// login 那条路早有同一道闸门（已登录必须先 logout），run 这条一直没有——而
// AGENTRED_SERVER_URL 留在 service 单元里正是它最容易被踩到的方式。
func TestGivenLoggedInDaemonWhenRunPointsAtAnotherServerThenItRefusesWithoutRepointingState(t *testing.T) {
	clearRunEnvironment(t)
	dir := t.TempDir()
	st, err := state.Load(dir)
	require.NoError(t, err)
	st.Login("account-a", state.AccountCredential{DeviceID: 1, AccessToken: "a", RefreshToken: "r"})
	st.Mutate(func(s *state.State) { s.AccountServerURL = "https://a.example" })
	require.NoError(t, st.Save())

	got, err := executeRunForOptions(t, dir, "--server", "https://b.example")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "logout", "错误要指出解法,而不是只说不行")
	assert.Equal(t, daemon.Options{}, got, "daemon 一步都不该起")

	reloaded, err := state.Load(dir)
	require.NoError(t, err)
	assert.Equal(t, "https://a.example", reloaded.AccountServerURL,
		"被拒的这次不许改掉登录时记下的 server 地址")
}

// 同一套 server 照常启动：末尾斜杠这类写法差异不是「换 server」。
func TestGivenLoggedInDaemonWhenRunKeepsTheSameServerThenItStarts(t *testing.T) {
	clearRunEnvironment(t)
	dir := t.TempDir()
	st, err := state.Load(dir)
	require.NoError(t, err)
	st.Login("account-a", state.AccountCredential{DeviceID: 1, AccessToken: "a", RefreshToken: "r"})
	st.Mutate(func(s *state.State) { s.AccountServerURL = "https://a.example" })
	require.NoError(t, st.Save())

	got, err := executeRunForOptions(t, dir, "--server", "https://a.example/")
	require.NoError(t, err)
	assert.Equal(t, "https://a.example", got.AccountServerURL)
}

// 没登录的 daemon 随便指：它手上没有任何属于某个账号的东西，指到哪都只是配置。
func TestGivenLoggedOutDaemonWhenRunPointsAtAnotherServerThenItStarts(t *testing.T) {
	clearRunEnvironment(t)
	dir := t.TempDir()
	st, err := state.Load(dir)
	require.NoError(t, err)
	st.Mutate(func(s *state.State) { s.AccountServerURL = "https://a.example" })
	require.NoError(t, st.Save())

	got, err := executeRunForOptions(t, dir, "--server", "https://b.example")
	require.NoError(t, err)
	assert.Equal(t, "https://b.example", got.AccountServerURL)
}

// 跑在 NAT 后面的 daemon(容器网桥、宿主端口映射)自己推不出对外地址,这个值是运维
// 直接说出来的;和其余运行配置一样,命令行盖过环境变量,环境变量盖过状态文件,并且
// 落盘留给下一次 service 启动。
func TestGivenAdvertiseAddressWhenRunStartsThenItReachesTheDaemonAndIsPersisted(t *testing.T) {
	clearRunEnvironment(t)
	dir := t.TempDir()
	st, err := state.Load(dir)
	require.NoError(t, err)
	st.Mutate(func(s *state.State) { s.Listen = state.ListenPrefs{AdvertiseAddr: "state.example:7456"} })
	require.NoError(t, st.Save())
	t.Setenv("AGENTRED_ADVERTISE_ADDR", "env.example:7456")

	got, err := executeRunForOptions(t, dir, "--advertise-addr", "203.0.113.7:9443")
	require.NoError(t, err)
	assert.Equal(t, "203.0.113.7:9443", got.AdvertiseAddr, "flag must override environment and state")

	reloaded, err := state.Load(dir)
	require.NoError(t, err)
	assert.Equal(t, "203.0.113.7:9443", reloaded.Listen.AdvertiseAddr)
}

func TestGivenPersistedAdvertiseAddressWhenRunHasNoOverridesThenItIsRestored(t *testing.T) {
	clearRunEnvironment(t)
	dir := t.TempDir()
	st, err := state.Load(dir)
	require.NoError(t, err)
	st.Mutate(func(s *state.State) { s.Listen = state.ListenPrefs{AdvertiseAddr: "203.0.113.7:9443"} })
	require.NoError(t, st.Save())

	got, err := executeRunForOptions(t, dir)
	require.NoError(t, err)
	assert.Equal(t, "203.0.113.7:9443", got.AdvertiseAddr)
}

// 一个别的机器够不着的地址是配错了,不是「先跑起来再说」:直连会被静默丢掉,现场只剩
// 「这台就是连不上直连」。启动时就拒。
func TestGivenUnreachableAdvertiseAddressWhenRunStartsThenItReturnsUsageErrorWithoutStartingDaemon(t *testing.T) {
	for name, address := range map[string]string{
		"localhost":      "localhost:7456",
		"loopback":       "127.0.0.1:7456",
		"unspecified":    "0.0.0.0:7456",
		"link local":     "169.254.10.1:7456",
		"port not a num": "203.0.113.7:not-a-port",
		"port out of ra": "203.0.113.7:70000",
	} {
		t.Run(name, func(t *testing.T) {
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
			cmd.SetArgs([]string{"--advertise-addr", address})

			err := cmd.Execute()

			require.Error(t, err)
			assert.Contains(t, err.Error(), "--advertise-addr")
			assert.False(t, started, "a rejected address must not start the daemon")
		})
	}
}
