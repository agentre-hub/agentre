package bootstrap

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/agentre-hub/agentre/internal/pkg/keychain"
)

// initKeychain 是 bootstrap 在装配 Server / Remote Device 之前选择 keychain 后端的接缝。
// 独立 E2E main 与正式 main 的本地真实验证都必须落在隔离目录里,生产 system keychain
// 永远不是 fallback。这些用例钉死这条边界:设置 AGENTRE_KEYCHAIN_DIR 时建立 file keychain;
// 目录缺失 / 权限不安全 → 启动失败,绝不偷偷换后端。
//
// 环境变量名在这里用字面量而不是 KeychainDirEnv 常量:启动脚本(e2e/lib/target.mjs)和文档
// 写的是这个字符串,改名会静默让所有隔离失效,所以测试独立钉死它。

func TestInitKeychainGivenKeychainDirSelectsFileBackend(t *testing.T) {
	// 启动脚本用 mode 0o700 预创建隔离目录;测试同样显式建 0700(t.TempDir() 是 0755,
	// 不能直接当 keychain 目录)。
	dir := filepath.Join(t.TempDir(), "kc")
	require.NoError(t, os.MkdirAll(dir, 0o700))
	original := keychain.Default()
	t.Cleanup(func() { keychain.SetDefault(original) })
	t.Setenv("AGENTRE_KEYCHAIN_DIR", dir)

	require.NoError(t, initKeychain(context.Background()))

	// 先断言后端类型,再 probe —— 后端仍是系统 keychain 时不得误写生产凭据。
	require.Equal(t, reflect.TypeOf(keychain.NewFile(dir)), reflect.TypeOf(keychain.Default()))
	require.NoError(t, keychain.Default().Set("probe-account", "generated-test-value"))
	got, err := os.ReadFile(filepath.Join(dir, "probe-account")) //nolint:gosec // G304: dir 来自 t.TempDir()。
	require.NoError(t, err)
	assert.Equal(t, "generated-test-value", string(got))
}

func TestInitKeychainGivenMissingDirFailsWithoutSwappingBackend(t *testing.T) {
	original := keychain.Default()
	t.Cleanup(func() { keychain.SetDefault(original) })
	sentinel := keychain.NewMemory()
	keychain.SetDefault(sentinel)
	t.Setenv("AGENTRE_KEYCHAIN_DIR", filepath.Join(t.TempDir(), "does-not-exist"))

	err := initKeychain(context.Background())
	require.Error(t, err)
	// 失败不得偷偷换后端(file 或 system):调用方应终止启动,而不是继续跑。
	assert.Same(t, sentinel, keychain.Default())
}

func TestInitKeychainGivenUnsafePermsFails(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "kc")
	require.NoError(t, os.MkdirAll(dir, 0o755))
	t.Setenv("AGENTRE_KEYCHAIN_DIR", dir)

	err := initKeychain(context.Background())
	require.Error(t, err)
}

func TestInitKeychainGivenNoEnvKeepsSystemKeychain(t *testing.T) {
	original := keychain.Default()
	t.Cleanup(func() { keychain.SetDefault(original) })
	t.Setenv("AGENTRE_KEYCHAIN_DIR", "")

	require.NoError(t, initKeychain(context.Background()))
	assert.Equal(t, reflect.TypeOf(keychain.NewSystem()), reflect.TypeOf(keychain.Default()))
}

// TestInitGivenKeychainDirSelectsFileBackendBeforeServerWiring 验证真实启动顺序里
// (隔离目录 → Init → Server / Remote Device 装配)file keychain 在装配之前就确定,而且
// 后续装配不会把它覆盖回系统 keychain —— ConnPool / watcher / Add 构造时捕获的就是同一个
// file backend。跑真实 Init(与同包的 cago_test.go 一样)才能覆盖这个顺序。
func TestInitGivenKeychainDirSelectsFileBackendBeforeServerWiring(t *testing.T) {
	dataDir := t.TempDir()
	keychainDir := filepath.Join(t.TempDir(), "kc")
	require.NoError(t, os.MkdirAll(keychainDir, 0o700))
	t.Setenv("AGENTRE_DATA_DIR", dataDir)
	t.Setenv("AGENTRE_ENV", "test")
	t.Setenv("AGENTRE_KEYCHAIN_DIR", keychainDir)

	runtime, err := Init(context.Background())
	require.NoError(t, err)
	t.Cleanup(runtime.Close)

	require.Equal(t, reflect.TypeOf(keychain.NewFile(keychainDir)), reflect.TypeOf(keychain.Default()))
}
