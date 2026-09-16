package logfile

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func readLog(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path) //nolint:gosec // G304: path is assembled under this test's t.TempDir.
	require.NoError(t, err)
	return string(data)
}

// Given 一个日志目录，When logger 记一条 info，Then 它落进 <dir>/<name>.log，
// 而只收 error 的 error.log 还没被创建。
func TestGivenInfoEntryWhenLoggedThenItLandsInAppLogOnly(t *testing.T) {
	dir := t.TempDir()
	l, _, err := New(io.Discard, dir, "agentred", "info")
	require.NoError(t, err)

	l.Info("daemon.Run: started")

	assert.Contains(t, readLog(t, filepath.Join(dir, "agentred.log")), "daemon.Run: started")
	_, statErr := os.Stat(filepath.Join(dir, "error.log"))
	assert.True(t, os.IsNotExist(statErr), "error.log should stay untouched by info entries")
}

// Given 同一个 logger，When 记一条 error，Then 应用日志与 error.log 都收到它。
func TestGivenErrorEntryWhenLoggedThenItLandsInBothFiles(t *testing.T) {
	dir := t.TempDir()
	l, _, err := New(io.Discard, dir, "agentred", "info")
	require.NoError(t, err)

	l.Error("daemon.Run: listen failed")

	assert.Contains(t, readLog(t, filepath.Join(dir, "agentred.log")), "daemon.Run: listen failed")
	assert.Contains(t, readLog(t, filepath.Join(dir, "error.log")), "daemon.Run: listen failed")
}

// Given level=info，When 记一条 debug，Then 文件里没有它；level=debug 时才有。
func TestGivenLevelWhenLoggingDebugThenFileHonorsThreshold(t *testing.T) {
	quiet := t.TempDir()
	l, _, err := New(io.Discard, quiet, "agentred", "info")
	require.NoError(t, err)
	l.Debug("daemon.Run: frame")
	l.Info("daemon.Run: started")
	assert.NotContains(t, readLog(t, filepath.Join(quiet, "agentred.log")), "daemon.Run: frame")

	verbose := t.TempDir()
	l, _, err = New(io.Discard, verbose, "agentred", "debug")
	require.NoError(t, err)
	l.Debug("daemon.Run: frame")
	assert.Contains(t, readLog(t, filepath.Join(verbose, "agentred.log")), "daemon.Run: frame")
}

// Given console writer，When 记日志，Then 控制台也拿到同一条（前台运行 / journald 依赖它）。
func TestGivenConsoleWriterWhenLoggedThenConsoleReceivesEntry(t *testing.T) {
	dir := t.TempDir()
	console := &syncBuffer{}
	l, _, err := New(console, dir, "agentred", "info")
	require.NoError(t, err)

	l.Info("daemon.Run: started")

	assert.Contains(t, console.String(), "daemon.Run: started")
}

// Guard: 保留策略是运维承诺（单文件 30 MB × 10 份 × 30 天、不压缩），
// 改动必须是显式的，不能被顺手改掉。
func TestGivenRotatorWhenBuiltThenRetentionPolicyIsPinned(t *testing.T) {
	w := rotator(filepath.Join(t.TempDir(), "app.log"))

	assert.Equal(t, MaxSizeMB, w.MaxSize)
	assert.Equal(t, MaxBackups, w.MaxBackups)
	assert.Equal(t, MaxAgeDays, w.MaxAge)
	assert.True(t, w.LocalTime)
	assert.False(t, w.Compress)
	assert.Equal(t, 30, MaxSizeMB)
	assert.Equal(t, 10, MaxBackups)
	assert.Equal(t, 30, MaxAgeDays)
}

func TestRotatingFileCoreUsesThirtyMegabyteFiles(t *testing.T) {
	logFile := filepath.Join(t.TempDir(), "app.log")
	core, closer := newFileCore(zap.DebugLevel, logFile)
	defer func() { _ = closer.Close() }()
	log := zap.New(core)
	chunk := strings.Repeat("x", 1<<20)

	// Given 29 one-megabyte debug records, When they are written, Then the
	// active file grows past cago's old 2 MB default without rotating.
	for range 29 {
		log.Debug("raw frame", zap.String("frame", chunk))
	}
	if err := log.Sync(); err != nil {
		t.Fatalf("Sync() before boundary error = %v", err)
	}
	if backups, err := filepath.Glob(filepath.Join(filepath.Dir(logFile), "app-*.log")); err != nil {
		t.Fatalf("Glob() before boundary error = %v", err)
	} else if len(backups) != 0 {
		t.Fatalf("rotated before 30 MB boundary: %v", backups)
	}

	// When the next record crosses 30 MB, Then the completed file is rotated.
	log.Debug("raw frame", zap.String("frame", chunk))
	if err := log.Sync(); err != nil {
		t.Fatalf("Sync() after boundary error = %v", err)
	}
	if backups, err := filepath.Glob(filepath.Join(filepath.Dir(logFile), "app-*.log")); err != nil {
		t.Fatalf("Glob() after boundary error = %v", err)
	} else if len(backups) != 1 {
		t.Fatalf("rotated backups = %v, want exactly one after crossing 30 MB", backups)
	}
}

// Given 一份写过日志的 logger，When 调用方关掉它、把日志文件删掉之后又记了一条，
// Then 文件不会被重新建出来——关闭的语义是「这两个文件已经交还给系统」。
//
// 没有这道语义时，收尾之后任何一条迟到的记录都会让 lumberjack 把文件重新打开：
// 在 Windows 上那个句柄会一直占着 <dataDir>，让整个数据目录删不掉（CI 上 13 个
// cmd/agentred 测试就是死在 t.TempDir 的清理上，"The process cannot access the
// file because it is being used by another process"）。
func TestGivenClosedLoggerWhenItLogsAgainThenTheFilesStayGone(t *testing.T) {
	dir := t.TempDir()
	l, files, err := New(io.Discard, dir, "agentred", "info")
	require.NoError(t, err)
	l.Error("daemon.Run: listen failed")
	appLog := filepath.Join(dir, "agentred.log")
	errLog := filepath.Join(dir, ErrorLogName)
	require.FileExists(t, appLog)
	require.FileExists(t, errLog)

	require.NoError(t, files.Close())
	require.NoError(t, os.Remove(appLog))
	require.NoError(t, os.Remove(errLog))

	l.Error("daemon.Run: a goroutine that outlived shutdown")

	assert.NoFileExists(t, appLog)
	assert.NoFileExists(t, errLog)
}
