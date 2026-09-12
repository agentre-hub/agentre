package bootstrap

import (
	"context"
	"os"
	"path/filepath"
	"time"

	"github.com/agentre-hub/agentre/internal/model/entity/app_setting_entity"
	"github.com/agentre-hub/agentre/internal/repository/app_setting_repo"

	"github.com/agentre-hub/agentre/internal/pkg/logfile"

	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"
)

// LogsDir 返回 Agentre 写日志的目录（<dataDir>/logs）。
// Init 建目录、运行时热重载 logger、以及前端「打开日志」都复用这里，避免散写路径。
func LogsDir() (string, error) {
	dataDir, err := AppDataDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dataDir, "logs"), nil
}

// rebuildLogger 用给定 level 重建全局 cago logger，保留 控制台 + agentre.log/error.log
// 三个 core（编码、轮转与保留策略由 internal/pkg/logfile 统一持有，与 agentred 同源），
// 这样运行时切 Debug 开关即可改变日志详尽度而无需重启。
func rebuildLogger(level, logsDir string) error {
	l, err := logfile.New(os.Stdout, logsDir, "agentre", level)
	if err != nil {
		return err
	}
	logger.SetLogger(l)
	return nil
}

// applyLogLevel 把布尔开关翻译成 level 并重建 logger。
func applyLogLevel(debugEnabled bool) error {
	level := "info"
	if debugEnabled {
		level = "debug"
	}
	logsDir, err := LogsDir()
	if err != nil {
		return err
	}
	return rebuildLogger(level, logsDir)
}

// DebugLoggingEnabled 读取持久化的 debug 日志开关；key 缺省时返回 false。
func DebugLoggingEnabled(ctx context.Context) (bool, error) {
	got, err := app_setting_repo.AppSetting().Get(ctx, app_setting_entity.KeyDebugLogging)
	if err != nil {
		return false, err
	}
	if got == nil {
		return false, nil
	}
	return app_setting_entity.ParseDebugLogging(got.Value), nil
}

// SetDebugLogging 持久化 debug 日志开关并立即热重载 logger。
func SetDebugLogging(ctx context.Context, enabled bool) error {
	val := "false"
	if enabled {
		val = "true"
	}
	if err := app_setting_repo.AppSetting().Set(ctx, &app_setting_entity.AppSetting{
		Key:        app_setting_entity.KeyDebugLogging,
		Value:      val,
		Updatetime: time.Now().UnixMilli(),
	}); err != nil {
		return err
	}
	return applyLogLevel(enabled)
}

// applyDebugLoggingOnBoot 在启动时按持久化的开关恢复日志级别（取代旧 AGENTRE_DEBUG 环境变量），
// 并统一换成 Agentre 的 30 MB 文件轮转配置。best-effort：读不到设置时按 info 重建，
// 重建失败只 warn，不阻断启动。
func applyDebugLoggingOnBoot(ctx context.Context) {
	enabled, err := DebugLoggingEnabled(ctx)
	if err != nil {
		logger.Default().Warn("read debug logging setting", zap.Error(err))
		enabled = false
	}
	if err := applyLogLevel(enabled); err != nil {
		logger.Default().Warn("apply debug logging on boot", zap.Error(err))
	}
}
