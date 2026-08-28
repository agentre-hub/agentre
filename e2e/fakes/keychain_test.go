package fakes

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/agentre-hub/agentre/internal/bootstrap"
	"github.com/agentre-hub/agentre/internal/pkg/keychain"
)

// e2e keychain 边界(docs/specs/2026-08-12-agentred-service-runtime-fixes.md「E2E
// keychain 初始化与安全边界」):AGENTRE_KEYCHAIN_DIR 必须在 bootstrap 装配 Server /
// Remote Device 之前生效,让 Server Add、ConnPool、watcher 与 e2e seeding 共享同一个
// file backend;失败则启动直接终止,绝不回退生产 system keychain。keychain 的选择在
// bootstrap(internal/bootstrap/keychain.go,不带 build tag)完成,这里验证它与 fakes
// 装配的整合。

func TestGivenE2EKeychainDirWhenBootstrapInitThenSeedingSharesFileBackend(t *testing.T) {
	dataDir := t.TempDir()
	// e2e runner 以 0700 预创建隔离目录;测试同样显式建 0700(本机 t.TempDir() 是 0755)。
	keychainDir := filepath.Join(t.TempDir(), "kc")
	require.NoError(t, os.MkdirAll(keychainDir, 0o700))
	t.Setenv("AGENTRE_DATA_DIR", dataDir)
	t.Setenv("AGENTRE_ENV", "test")
	t.Setenv(bootstrap.KeychainDirEnv, keychainDir)

	runtime, err := bootstrap.Init(context.Background())
	require.NoError(t, err)
	t.Cleanup(runtime.Close)

	// bootstrap.Init 在 InitServer / InitRemoteDevice 之前就选中 file backend:
	// Add / ConnPool / watcher 构造时捕获的就是它。先做类型断言再 probe,避免后端
	// 仍是系统 keychain 时误写生产凭据。
	require.Equal(t, reflect.TypeOf(keychain.NewFile(keychainDir)), reflect.TypeOf(keychain.Default()))

	// fakes 装配(e2e login seed / backend 播种)读到的必须是同一个 file backend:
	// probe 写入落在隔离目录,而不是生产 system keychain。
	require.NoError(t, Install(context.Background()))
	require.Equal(t, reflect.TypeOf(keychain.NewFile(keychainDir)), reflect.TypeOf(keychain.Default()))
	require.NoError(t, keychain.Default().Set("probe-account", "generated-test-value"))
	got, err := os.ReadFile(filepath.Join(keychainDir, "probe-account")) //nolint:gosec // G304: path is beneath this test's private TempDir
	require.NoError(t, err)
	assert.Equal(t, "generated-test-value", string(got))
}

func TestGivenE2EKeychainDirUnusableWhenBootstrapInitThenStartupFails(t *testing.T) {
	dataDir := t.TempDir()
	t.Setenv("AGENTRE_DATA_DIR", dataDir)
	t.Setenv("AGENTRE_ENV", "test")
	// 目录缺失(e2e runner 预创建 0700 目录才启动;缺失 = 配置错),启动必须失败,
	// 不得静默回退系统 keychain。
	t.Setenv(bootstrap.KeychainDirEnv, filepath.Join(t.TempDir(), "does-not-exist"))

	_, err := bootstrap.Init(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "isolated keychain")
}
