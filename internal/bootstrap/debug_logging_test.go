package bootstrap

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"
)

func TestLogsDir(t *testing.T) {
	dataDir := t.TempDir()
	t.Setenv("AGENTRE_DATA_DIR", dataDir)

	got, err := LogsDir()
	if err != nil {
		t.Fatalf("LogsDir() error = %v", err)
	}
	want := filepath.Join(dataDir, "logs")
	if got != want {
		t.Fatalf("LogsDir() = %q, want %q", got, want)
	}
}

// TestSetDebugLoggingTogglesLevelAndPersists 验证开关既改全局 logger 级别（热重载、立即生效），
// 又把状态持久化到 app_settings，供下次启动恢复。
func TestSetDebugLoggingTogglesLevelAndPersists(t *testing.T) {
	dataDir := t.TempDir()
	t.Setenv("AGENTRE_DATA_DIR", dataDir)
	t.Setenv("AGENTRE_ENV", "test")
	ctx := context.Background()

	runtime, err := Init(ctx)
	if err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	t.Cleanup(runtime.Close)

	// 默认关闭：debug 级别不启用，持久化值为 false。
	if logger.Default().Core().Enabled(zap.DebugLevel) {
		t.Fatal("debug level enabled by default, want disabled")
	}
	if on, err := DebugLoggingEnabled(ctx); err != nil || on {
		t.Fatalf("DebugLoggingEnabled() default = %v, %v; want false, nil", on, err)
	}

	// 打开：logger 立即记 debug，持久化值翻 true。
	if err := SetDebugLogging(ctx, true); err != nil {
		t.Fatalf("SetDebugLogging(true) error = %v", err)
	}
	if !logger.Default().Core().Enabled(zap.DebugLevel) {
		t.Fatal("debug level disabled after SetDebugLogging(true)")
	}
	if on, err := DebugLoggingEnabled(ctx); err != nil || !on {
		t.Fatalf("DebugLoggingEnabled() after on = %v, %v; want true, nil", on, err)
	}

	// 关闭：恢复 info，debug 级别不再启用。
	if err := SetDebugLogging(ctx, false); err != nil {
		t.Fatalf("SetDebugLogging(false) error = %v", err)
	}
	if logger.Default().Core().Enabled(zap.DebugLevel) {
		t.Fatal("debug level still enabled after SetDebugLogging(false)")
	}
	if on, err := DebugLoggingEnabled(ctx); err != nil || on {
		t.Fatalf("DebugLoggingEnabled() after off = %v, %v; want false, nil", on, err)
	}
}

// TestInitAppliesPersistedDebugLogging 验证持久化的开关在下一次启动时被恢复：
// 第二次 Init 会先把 logger 重置为 info，再读 app_settings 把它热重载回 debug。
func TestInitAppliesPersistedDebugLogging(t *testing.T) {
	dataDir := t.TempDir()
	t.Setenv("AGENTRE_DATA_DIR", dataDir)
	t.Setenv("AGENTRE_ENV", "test")
	ctx := context.Background()

	runtime, err := Init(ctx)
	if err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	if err := SetDebugLogging(ctx, true); err != nil {
		t.Fatalf("SetDebugLogging(true) error = %v", err)
	}
	runtime.Close()

	runtime2, err := Init(ctx)
	if err != nil {
		t.Fatalf("second Init() error = %v", err)
	}
	t.Cleanup(runtime2.Close)

	if !logger.Default().Core().Enabled(zap.DebugLevel) {
		t.Fatal("persisted debug flag not applied on boot")
	}
}
