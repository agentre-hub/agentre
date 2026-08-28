package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"github.com/cago-frame/cago/pkg/logger"
	"github.com/spf13/cobra"
	"go.uber.org/zap"

	"github.com/agentre-hub/agentre/internal/daemon"
	"github.com/agentre-hub/agentre/internal/daemon/state"
	"github.com/agentre-hub/agentre/internal/pkg/logfile"
	"github.com/agentre-hub/agentre/internal/pkg/paths"
)

const (
	defaultAgentredHost = "0.0.0.0"
	defaultAgentredPort = 7456
	defaultLogLevel     = "info"

	// logsDirName 是 agentred 的落盘日志目录,位于 paths.AgentredDataDir() 下。
	// 守护进程通常由 launchd / systemd 拉起,stdout 无人接管,文件是唯一能回看的现场。
	logsDirName = "logs"

	// agentredLogName 决定应用日志文件名(<logsDirName>/agentred.log);error 及以上
	// 另有旁路 error.log,见 logfile.New。
	agentredLogName = "agentred"
)

type runDaemon interface {
	Run(context.Context) error
}

type runDeps struct {
	dataDir   func() (string, error)
	newDaemon func(daemon.Options) (runDaemon, error)
}

type resolvedRunConfig struct {
	listen       state.ListenPrefs
	serverURL    string
	hasOverrides bool
}

func newRunCmd() *cobra.Command {
	return newRunCmdWithDeps(runDeps{
		dataDir: paths.AgentredDataDir,
		newDaemon: func(opts daemon.Options) (runDaemon, error) {
			return daemon.New(opts)
		},
	})
}

func newRunCmdWithDeps(deps runDeps) *cobra.Command {
	var (
		tlsCert   string
		tlsKey    string
		host      string
		port      int
		serverURL string
		logLevel  string
	)
	cmd := &cobra.Command{
		Use:   "run",
		Short: "Boot the daemon (foreground; SIGINT/SIGTERM to stop)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			level, err := resolveLogLevel(cmd, logLevel)
			if err != nil {
				return err
			}
			dir, err := deps.dataDir()
			if err != nil {
				return err
			}
			if err := initLogging(cmd.OutOrStdout(), dir, level); err != nil {
				return err
			}
			st, err := state.Load(dir)
			if err != nil {
				return err
			}
			config, err := resolveRunConfig(cmd, st.Snapshot(), host, port, tlsCert, tlsKey, serverURL)
			if err != nil {
				return err
			}
			if config.hasOverrides {
				st.Mutate(func(s *state.State) {
					s.Listen = config.listen
					s.HubServerURL = config.serverURL
				})
				if err := st.Save(); err != nil {
					return fmt.Errorf("save daemon runtime configuration: %w", err)
				}
			}

			ctx, cancel := signal.NotifyContext(cmd.Context(), syscall.SIGINT, syscall.SIGTERM)
			defer cancel()
			logger.Ctx(ctx).Debug("agentred.run: resolved configuration",
				zap.String("lanHost", config.listen.LanHost),
				zap.Int("lanPort", config.listen.LanPort),
				zap.String("tlsCertFile", config.listen.TLSCertFile),
				zap.String("tlsKeyFile", config.listen.TLSKeyFile),
				zap.String("serverURL", config.serverURL),
				zap.String("dataDir", dir))

			d, err := deps.newDaemon(daemon.Options{
				DataDir:      dir,
				LANHost:      config.listen.LanHost,
				LANPort:      config.listen.LanPort,
				TLSCertFile:  config.listen.TLSCertFile,
				TLSKeyFile:   config.listen.TLSKeyFile,
				HubServerURL: config.serverURL,
			})
			if err != nil {
				logger.Ctx(ctx).Error("agentred.run: daemon construction failed", zap.Error(err))
				return err
			}
			logger.Ctx(ctx).Info("agentred.run: daemon starting",
				zap.String("lanHost", config.listen.LanHost),
				zap.Int("lanPort", config.listen.LanPort),
				zap.Bool("tlsEnabled", config.listen.TLSCertFile != ""),
				zap.String("logLevel", level))
			if err := d.Run(ctx); err != nil {
				logger.Ctx(ctx).Error("agentred.run: daemon stopped", zap.Error(err))
				return err
			}
			logger.Ctx(ctx).Info("agentred.run: daemon stopped")
			return nil
		},
	}
	cmd.Flags().StringVar(&tlsCert, "tls-cert", "", "PEM certificate path (or AGENTRED_TLS_CERT); enables wss://")
	cmd.Flags().StringVar(&tlsKey, "tls-key", "", "PEM private key path (or AGENTRED_TLS_KEY); required with --tls-cert")
	cmd.Flags().StringVar(&host, "host", defaultAgentredHost, "LAN listen host (or AGENTRED_HOST)")
	cmd.Flags().IntVar(&port, "port", defaultAgentredPort, "LAN listen port (or AGENTRED_PORT)")
	cmd.Flags().StringVar(&logLevel, "log-level", defaultLogLevel,
		"file/console log verbosity: debug|info|warn|error (or AGENTRED_LOG_LEVEL)")
	cmd.Flags().StringVar(&serverURL, "server", strings.TrimSpace(os.Getenv("AGENTRED_SERVER_URL")), "account server base URL (or AGENTRED_SERVER_URL)")
	return cmd
}

// initLogging 把全局 cago logger 换成写 <dataDir>/logs/ 的实例。在此之前 agentred
// 全程用 zap 的 no-op logger,所有 logger.Ctx(...) 调用都无声丢弃。
func initLogging(console io.Writer, dataDir, level string) error {
	l, err := logfile.New(console, filepath.Join(dataDir, logsDirName), agentredLogName, level)
	if err != nil {
		return fmt.Errorf("init agentred logger: %w", err)
	}
	logger.SetLogger(l)
	// daemon 内部仍有约十处 stdlib log.Printf(panic 恢复、shutdown 失败、重启清扫),
	// 它们默认只写 stderr —— 而 launchd 不接管 stderr,那正是最需要回看的现场。
	// 重定向后它们与 zap 记录落进同一个文件;进程活到退出,不需要还原。
	zap.RedirectStdLog(l)
	return nil
}

// resolveLogLevel 按 flag > 环境变量 > 默认解析级别。级别不落盘到 state:它是排查
// 开关,不是守护进程的运行配置。拼错的级别直接报错,不静默退回 info。
func resolveLogLevel(cmd *cobra.Command, flagValue string) (string, error) {
	level, _ := resolveString(
		cmd.Flags().Changed("log-level"), flagValue, "AGENTRED_LOG_LEVEL", "", defaultLogLevel,
	)
	level = strings.ToLower(strings.TrimSpace(level))
	switch level {
	case "debug", "info", "warn", "error":
		return level, nil
	default:
		return "", newUsageError("--log-level must be one of debug, info, warn, error")
	}
}

func resolveRunConfig(cmd *cobra.Command, persisted state.State, flagHost string, flagPort int,
	flagTLSCert, flagTLSKey, flagServerURL string) (resolvedRunConfig, error) {
	config := resolvedRunConfig{}
	config.listen.LanHost, config.hasOverrides = resolveString(
		cmd.Flags().Changed("host"), flagHost, "AGENTRED_HOST", persisted.Listen.LanHost, defaultAgentredHost,
	)

	port, portOverride, err := resolvePort(cmd.Flags().Changed("port"), flagPort, persisted.Listen.LanPort)
	if err != nil {
		return resolvedRunConfig{}, err
	}
	config.listen.LanPort = port
	config.hasOverrides = config.hasOverrides || portOverride

	config.listen.TLSCertFile, portOverride = resolveString(
		cmd.Flags().Changed("tls-cert"), flagTLSCert, "AGENTRED_TLS_CERT", persisted.Listen.TLSCertFile, "",
	)
	config.hasOverrides = config.hasOverrides || portOverride
	config.listen.TLSKeyFile, portOverride = resolveString(
		cmd.Flags().Changed("tls-key"), flagTLSKey, "AGENTRED_TLS_KEY", persisted.Listen.TLSKeyFile, "",
	)
	config.hasOverrides = config.hasOverrides || portOverride
	config.serverURL, portOverride = resolveString(
		cmd.Flags().Changed("server"), flagServerURL, "AGENTRED_SERVER_URL", persisted.HubServerURL, "",
	)
	config.hasOverrides = config.hasOverrides || portOverride

	config.listen.LanHost = strings.TrimSpace(config.listen.LanHost)
	config.listen.TLSCertFile = strings.TrimSpace(config.listen.TLSCertFile)
	config.listen.TLSKeyFile = strings.TrimSpace(config.listen.TLSKeyFile)
	config.serverURL = strings.TrimSpace(config.serverURL)
	if (config.listen.TLSCertFile == "") != (config.listen.TLSKeyFile == "") {
		return resolvedRunConfig{}, newUsageError("both --tls-cert and --tls-key must be set or neither")
	}
	if config.serverURL != "" {
		config.serverURL, err = validServerURL(config.serverURL)
		if err != nil {
			return resolvedRunConfig{}, err
		}
	}
	return config, nil
}

func resolveString(flagChanged bool, flagValue, envName, persisted, fallback string) (string, bool) {
	if flagChanged {
		return flagValue, true
	}
	if value, ok := os.LookupEnv(envName); ok {
		return value, true
	}
	if persisted != "" {
		return persisted, false
	}
	return fallback, false
}

func resolvePort(flagChanged bool, flagValue, persisted int) (int, bool, error) {
	if flagChanged {
		return validatePort(flagValue, true)
	}
	if raw, ok := os.LookupEnv("AGENTRED_PORT"); ok {
		port, err := strconv.Atoi(strings.TrimSpace(raw))
		if err != nil {
			return 0, false, newUsageError("AGENTRED_PORT must be an integer")
		}
		return validatePort(port, true)
	}
	if persisted != 0 {
		return validatePort(persisted, false)
	}
	return defaultAgentredPort, false, nil
}

func validatePort(port int, override bool) (int, bool, error) {
	if port < 1 || port > 65535 {
		return 0, false, newUsageError("port must be between 1 and 65535")
	}
	return port, override, nil
}
