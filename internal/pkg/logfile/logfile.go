// Package logfile 是 Agentre 落盘日志的唯一构造点：桌面端(agentre)与守护进程
// (agentred)共用同一套 JSON 编码、lumberjack 轮转与保留策略,避免两个进程各写一份
// 会各自漂移的配置。它是 internal/pkg 的叶子层,不反向依赖 service/repository。
package logfile

import (
	"io"
	"path/filepath"

	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	lumberjack "gopkg.in/natefinch/lumberjack.v2"
)

// 保留策略:单个文件写到 30 MB 触发轮转,最多留 10 份历史,且不超过 30 天;不压缩,
// 让排查时可以直接 grep / jq。因此每个日志文件在磁盘上的上限约为 11 × 30 MB。
// 这三个值刻意大于 cago 默认的 2 MB —— 一条 debug 帧就能顶掉半个默认文件。
const (
	MaxSizeMB  = 30
	MaxBackups = 10
	MaxAgeDays = 30
)

// ErrorLogName 是只收 error 及以上的旁路文件名,与应用日志同目录。
const ErrorLogName = "error.log"

// New 构造写三处的 logger:console(传 nil 表示不写控制台)、<logsDir>/<name>.log
// (按 level 过滤)、<logsDir>/error.log(只收 error 及以上)。目录与文件都由
// lumberjack 在首次写入时创建,调用方不必预先 MkdirAll。
func New(console io.Writer, logsDir, name, level string) (*zap.Logger, error) {
	lvl := logger.ToLevel(level)
	cores := make([]zapcore.Core, 0, 3)
	if console != nil {
		// 控制台 core 刻意与 cago logger.Logger 启动时的非 debug 分支逐字对齐
		// (生产 JSON 编码 + Lock + 当前 level),这样切换级别不会让控制台格式漂移。
		cores = append(cores, zapcore.NewCore(
			zapcore.NewJSONEncoder(zap.NewProductionEncoderConfig()),
			zapcore.Lock(zapcore.AddSync(console)),
			lvl,
		))
	}
	cores = append(cores,
		NewCore(lvl, filepath.Join(logsDir, name+".log")),
		NewCore(zapcore.ErrorLevel, filepath.Join(logsDir, ErrorLogName)),
	)
	return logger.New(logger.AppendCore(cores...))
}

// NewCore 构造单个轮转文件 core,供需要自己拼 core 列表的调用方使用。
func NewCore(level zapcore.Level, filename string) zapcore.Core {
	encoderConfig := zap.NewProductionEncoderConfig()
	encoderConfig.EncodeTime = zapcore.ISO8601TimeEncoder
	return zapcore.NewCore(
		zapcore.NewJSONEncoder(encoderConfig),
		zapcore.AddSync(rotator(filename)),
		level,
	)
}

func rotator(filename string) *lumberjack.Logger {
	return &lumberjack.Logger{
		Filename:   filename,
		MaxSize:    MaxSizeMB,
		MaxBackups: MaxBackups,
		MaxAge:     MaxAgeDays,
		LocalTime:  true,
		Compress:   false,
	}
}
