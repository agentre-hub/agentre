package main

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"

	"github.com/agentre-hub/agentre/internal/service/update_svc"
)

// update 命令把「下载、校验、替换、生效」收成一条命令：重跑安装脚本之后还要记得
// 重启服务这件事，不该留给用户记。判定与替换都在 update_svc 里，这里只管出入口。

type updateReleaseResolver func(ctx context.Context, channel string) (*update_svc.AgentredRelease, error)
type updateApplier func(ctx context.Context, release *update_svc.AgentredRelease) error
type activeTurnCounter func(ctx context.Context) (int64, error)

type updateCommandDeps struct {
	resolve        updateReleaseResolver
	apply          updateApplier
	activeTurns    activeTurnCounter
	managerFactory serviceManagerFactory
	localStatus    serviceStatusLoader
}

func newUpdateCmd() *cobra.Command {
	return newUpdateCmdWithDeps(updateCommandDeps{
		resolve:     resolveAgentredRelease,
		apply:       applyAgentredUpdate,
		activeTurns: localActiveTurnCount,
		managerFactory: func() (ServiceManager, error) {
			return newPlatformServiceManager()
		},
		localStatus: func(ctx context.Context) (map[string]any, error) {
			return loadLocalDaemonStatus(ctx, localClient())
		},
	})
}

func newUpdateCmdWithDeps(deps updateCommandDeps) *cobra.Command {
	var (
		checkOnly bool
		channel   string
		force     bool
	)
	cmd := &cobra.Command{
		Use:   "update",
		Short: "Download, verify and install the latest agentred release",
		Long: `Resolve the latest release for a channel, download the asset for this platform,
verify its SHA256, replace this binary and restart the registered service.

Set AGENTRED_RELEASE_BASE_URL to install from an internal or self-hosted release
source; without it the release is resolved from GitHub, falling back to the
built-in mirrors.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			resolved, err := update_svc.NormalizeChannel(channel)
			if err != nil {
				return newUsageError("%s", err.Error())
			}
			return runUpdate(cmd.Context(), cmd.OutOrStdout(), deps, resolved, checkOnly, force)
		},
	}
	cmd.Flags().BoolVar(&checkOnly, "check", false, "only report current / latest / whether an update exists")
	cmd.Flags().StringVar(&channel, "channel", update_svc.ChannelStable,
		"release channel: stable, beta or nightly")
	cmd.Flags().BoolVar(&force, "force", false,
		"upgrade even while conversations are running on this machine")
	return cmd
}

func runUpdate(ctx context.Context, out io.Writer, deps updateCommandDeps,
	channel string, checkOnly, force bool) error {
	// 闸门在解析发布之前：有轮次在跑时连下载都不该发生。
	if !checkOnly {
		if err := guardActiveTurnsForCLI(ctx, deps, force); err != nil {
			return err
		}
	}

	release, err := deps.resolve(ctx, channel)
	if err != nil {
		return err
	}
	printReleaseComparison(out, release)
	if checkOnly {
		return nil
	}
	if !release.HasUpdate {
		_, _ = fmt.Fprintf(out, "agentred is already up to date on the %s channel.\n", release.Channel)
		return nil
	}

	if err := deps.apply(ctx, release); err != nil {
		return err
	}
	_, _ = fmt.Fprintf(out, "Upgraded agentred to %s.\n", release.LatestVersion)
	if err := activateAgentredUpdate(ctx, out, deps, release.LatestVersion); err != nil {
		return err
	}
	// 决策 6a：容器里的替换写在可写层，重建容器就回到镜像里的版本。
	_, _ = fmt.Fprintln(out,
		"Note: inside a container this upgrade lives in the writable layer; recreating the container restores the version baked into the image.")
	return nil
}

func guardActiveTurnsForCLI(ctx context.Context, deps updateCommandDeps, force bool) error {
	count, err := deps.activeTurns(ctx)
	if err != nil {
		return fmt.Errorf("count running conversations: %w", err)
	}
	if err := update_svc.GuardActiveTurns(count, force); err != nil {
		return fmt.Errorf("%w; re-run with --force to upgrade anyway", err)
	}
	return nil
}

func printReleaseComparison(out io.Writer, release *update_svc.AgentredRelease) {
	available := "no"
	if release.HasUpdate {
		available = "yes"
	}
	_, _ = fmt.Fprintf(out, "Current version:  %s\n", release.CurrentVersion)
	_, _ = fmt.Fprintf(out, "Latest version:   %s (%s)\n", release.LatestVersion, release.Channel)
	_, _ = fmt.Fprintf(out, "Update available: %s\n", available)
}

// activateAgentredUpdate 让新二进制生效：注册过服务就重启它，没有服务时说清楚
// 「运行中的 agentred 需要重启才会生效」——命令行进程替不了另一个前台进程做这件事。
func activateAgentredUpdate(ctx context.Context, out io.Writer, deps updateCommandDeps, version string) error {
	manager, err := deps.managerFactory()
	if err != nil {
		printManualRestart(out, version)
		_, _ = fmt.Fprintf(out, "Could not inspect the service registration: %v\n", err)
		return nil
	}
	status, err := manager.Status(ctx)
	if err != nil {
		printManualRestart(out, version)
		_, _ = fmt.Fprintf(out, "Could not inspect the service registration: %v\n", err)
		return nil
	}
	if !status.Installed {
		printManualRestart(out, version)
		return nil
	}
	if _, err := restartService(ctx, manager, deps.localStatus); err != nil {
		return fmt.Errorf("agentred was upgraded to %s but restarting the service failed: %w", version, err)
	}
	_, _ = fmt.Fprintf(out, "Restarted the agentred service; it now runs %s.\n", version)
	return nil
}

func printManualRestart(out io.Writer, version string) {
	_, _ = fmt.Fprintf(out,
		"No registered agentred service found; a running agentred must be restarted for %s to take effect (agentred service restart once it is registered).\n",
		version)
}

func resolveAgentredRelease(ctx context.Context, channel string) (*update_svc.AgentredRelease, error) {
	return update_svc.ResolveAgentredRelease(ctx, update_svc.AgentredReleaseOptions{
		Channel: channel,
		BaseURL: os.Getenv(update_svc.AgentredReleaseBaseURLEnv),
	})
}

func applyAgentredUpdate(ctx context.Context, release *update_svc.AgentredRelease) error {
	return update_svc.ApplyAgentredUpdate(ctx, release, update_svc.ApplyAgentredUpdateOptions{})
}

// localActiveTurnCount 问运行中的 daemon「此刻有几条会话在跑」。
//
// daemon 连不上时按 0 处理:那说明没有 daemon 在跑,也就没有轮次会被这次升级掐掉。
// 应答里的字段读不成数才是错误——那时数不清有几条,不能冒充「一条都没有」。
func localActiveTurnCount(ctx context.Context) (int64, error) {
	status, err := loadLocalDaemonStatus(ctx, localClient())
	if err != nil {
		return 0, nil //nolint:nilerr // 连不上 daemon 就是没有 daemon 在跑,也就没有轮次会被掐掉。
	}
	value, ok := status["activeSessions"]
	if !ok || value == nil {
		return 0, nil
	}
	count, ok := value.(float64)
	if !ok {
		return 0, fmt.Errorf("local daemon reported an unreadable activeSessions value %v", value)
	}
	return int64(count), nil
}
