package daemon

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/cago-frame/cago/configs"
	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"

	"github.com/agentre-hub/agentre/internal/buildinfo"
	"github.com/agentre-hub/agentre/internal/pkg/agrctlinstall"
	"github.com/agentre-hub/agentre/internal/pkg/ctlskill"
	"github.com/agentre-hub/agentre/internal/pkg/paths"
)

// CtlTools 是一次 agrctl + ctlskill 安装的全部输入(规格 2026-09-22
// agrctl-resource-management「Executors / agentred」:agentred 启动时把 agrctl 和
// ctlskill 装到该主机用户的目录下,与桌面端的安装位置相同)。
type CtlTools struct {
	// Home 是主机用户的 home 目录(ctlskill 的两种形态都在它下面)。
	Home string
	// AppDataDir 是 Agentre 桌面端的数据目录;agrctl 装到 <AppDataDir>/bin/agrctl,
	// 与桌面端同一个位置。
	AppDataDir string
	// Source 是随发布包放在 agentred 旁边的 agrctl;空 = 这份构建没带它,什么都不装。
	Source string
	// Version 是安装标记里的版本,变了才重装。
	Version string
}

// InstallCtlTools 装 agrctl 并铺 ctlskill,返回装好的 agrctl 路径。没有随包的 agrctl 时
// 什么都不装(返回空路径):技能文档里写的是 agrctl 的绝对路径,指向一个不存在的文件
// 只会让 agent 白跑一趟。安装本身与桌面端共用 agrctlinstall / ctlskill,不另起一份。
func InstallCtlTools(opts CtlTools) (string, error) {
	if opts.Source == "" {
		return "", nil
	}
	path, _, err := agrctlinstall.EnsureInstalled(opts.AppDataDir, opts.Source, opts.Version)
	if err != nil {
		return "", fmt.Errorf("install agrctl: %w", err)
	}
	if err := ctlskill.Install(ctlskill.Options{Home: opts.Home, AgrctlPath: path, Version: opts.Version}); err != nil {
		return path, fmt.Errorf("install ctl skill: %w", err)
	}
	return path, nil
}

// InstallCtlToolsOnBoot 是 `agentred run` 启动时的那一次安装:路径按本机现取,失败只记
// warn —— 没装上 agrctl 不妨碍 daemon 执行会话,只是会话里用不了 agrctl。AGENTRE_ENV=test
// 时跳过(与桌面端 ctlskill_svc.InstallOnBoot 同一道闸),测试进程不写用户 home。
func InstallCtlToolsOnBoot(ctx context.Context) {
	if strings.EqualFold(strings.TrimSpace(os.Getenv("AGENTRE_ENV")), "test") {
		return
	}
	src, ok := agrctlinstall.BundledSourcePath()
	if !ok {
		logger.Ctx(ctx).Info("daemon.InstallCtlToolsOnBoot: no bundled agrctl next to agentred, skip")
		return
	}
	home, err := os.UserHomeDir()
	if err != nil {
		logger.Ctx(ctx).Warn("daemon.InstallCtlToolsOnBoot: resolve home", zap.Error(err))
		return
	}
	appData, err := paths.AppDataDir()
	if err != nil {
		logger.Ctx(ctx).Warn("daemon.InstallCtlToolsOnBoot: resolve app data dir", zap.Error(err))
		return
	}
	path, err := InstallCtlTools(CtlTools{Home: home, AppDataDir: appData, Source: src, Version: ctlToolsVersion()})
	if err != nil {
		logger.Ctx(ctx).Warn("daemon.InstallCtlToolsOnBoot: install", zap.Error(err))
		return
	}
	logger.Ctx(ctx).Info("daemon.InstallCtlToolsOnBoot: installed", zap.String("agrctlPath", path))
}

// ctlToolsVersion 与桌面端 bootstrap.ctlSkillVersion 同一口径:应用版本,构建注入了
// commit 就再缀上它,每次发布构建都重铺一次。
func ctlToolsVersion() string {
	if commit := buildinfo.ShortCommitID(); commit != "" {
		return configs.Version + "+" + commit
	}
	return configs.Version
}
