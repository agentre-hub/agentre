// Package logfile 是 Agentre 落盘日志的唯一构造点：桌面端(agentre)与守护进程
// (agentred)共用同一套 JSON 编码、lumberjack 轮转与保留策略,避免两个进程各写一份
// 会各自漂移的配置。它是 internal/pkg 的叶子层,不反向依赖 service/repository。
package logfile

import (
	"errors"
	"io"
	"io/fs"
	"path/filepath"
	"sync"

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
//
// 第二个返回值持有那两个落盘文件:调用方收尾(进程退出、或者换一份 logger)时
// Close 一次,把它们交还给系统。
func New(console io.Writer, logsDir, name, level string) (*zap.Logger, io.Closer, error) {
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
	appCore, appFile := newFileCore(lvl, filepath.Join(logsDir, name+".log"))
	errCore, errFile := newFileCore(zapcore.ErrorLevel, filepath.Join(logsDir, ErrorLogName))
	cores = append(cores, appCore, errCore)
	l, err := logger.New(logger.AppendCore(cores...))
	if err != nil {
		return nil, nil, errors.Join(err, appFile.Close(), errFile.Close())
	}
	return l, files{appFile, errFile}, nil
}

func newFileCore(level zapcore.Level, filename string) (zapcore.Core, io.Closer) {
	encoderConfig := zap.NewProductionEncoderConfig()
	encoderConfig.EncodeTime = zapcore.ISO8601TimeEncoder
	sink := &closableSyncer{file: rotator(filename)}
	return zapcore.NewCore(zapcore.NewJSONEncoder(encoderConfig), sink, level), sink
}

// files 把一份 logger 的落盘文件收成一个 Closer,让调用方只收尾一次。
type files []io.Closer

func (f files) Close() error {
	errs := make([]error, 0, len(f))
	for _, c := range f {
		errs = append(errs, c.Close())
	}
	return errors.Join(errs...)
}

// closableSyncer 是落盘 core 的写出口:在 lumberjack 之上加一道关闭状态。关闭之后
// 的写入直接报错,而不是让 lumberjack 把文件重新建出来 —— 关日志只发生在收尾,
// 而收尾之后复活的那一份文件没人再读,在 Windows 上还会拿句柄占着数据目录,让
// <dataDir> 整个删不掉。
type closableSyncer struct {
	mu     sync.Mutex
	closed bool
	file   *lumberjack.Logger
}

func (s *closableSyncer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return 0, fs.ErrClosed
	}
	return s.file.Write(p)
}

// Sync 是空操作:lumberjack 每次 Write 直接落到文件,自己不缓冲。
func (s *closableSyncer) Sync() error { return nil }

func (s *closableSyncer) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	return s.file.Close()
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
