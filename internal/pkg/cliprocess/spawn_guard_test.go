package cliprocess

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// cliSpawnPackages 是所有会起 CLI 子进程的包(这个底座 + 三个协议包)。
var cliSpawnPackages = []string{
	".",
	"../../../pkg/claudecode",
	"../../../pkg/codex",
	"../../../pkg/piagent",
}

// 守卫:起 CLI 子进程的地方一律不用 exec.CommandContext。
//
// CommandContext 把进程寿命绑死在传进来的 ctx 上。CLI 会话是跨轮常驻的,而开它的
// 那一轮 ctx 每轮结束都会 cancel —— 绑上去就是每轮结束都 SIGKILL 掉池里留着复用的
// 进程,下一轮拿到的是个死进程。codex 正是这么坏掉的,而它坏得没人发现:池复用的
// 单测全部注入替身工厂,从不起真进程。
//
// 契约改成「ctx 只守 spawn 阶段」之后,这条守卫拦的是它悄悄长回来。
func TestSpawn_GivenEveryCLISpawningPackage_WhenReadingSource_ThenNoneBindsProcessLifetimeToAContext(t *testing.T) {
	for _, pkg := range cliSpawnPackages {
		entries, err := os.ReadDir(pkg)
		require.NoError(t, err)
		for _, entry := range entries {
			// 只看实现文件:测试文件里出现这个名字是在描述被禁的写法。
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") ||
				strings.HasSuffix(entry.Name(), "_test.go") {
				continue
			}
			path := filepath.Join(pkg, entry.Name())
			source, readErr := os.ReadFile(path) //nolint:gosec // G304: 路径来自本仓库内固定的包列表。
			require.NoError(t, readErr)
			// 断言写成布尔而不是 NotContains:后者失败时会把整份源码打进报告。
			require.False(t, strings.Contains(string(source), "exec.CommandContext"),
				"%s 用了 exec.CommandContext:子进程寿命不能绑在 ctx 上 —— 用 exec.Command 加一道 spawn 前的取消检查", path)
		}
	}
}
