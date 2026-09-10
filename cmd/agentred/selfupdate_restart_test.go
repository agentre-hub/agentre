package main

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/agentre-hub/agentre/internal/daemon/handlers"
)

// 远程一键升级替换完二进制之后,daemon 唯一能为「生效」做的事就是退出,由监管者把它
// 拉回来(spec「远程一键升级」:「受理 → …随后让自己被监管者拉起来(装了服务就重启
// 服务,否则退出)」)。spec 把「退出之后不会回来」这条代价**只**留给前台裸跑
// (「没有监管者的形态(前台裸跑)下 daemon 退出后不会自己回来」),装了服务的那台机器
// 必须回来。
//
// 这条守卫把两处钉在一起:daemon 退出用的那个状态码,与本仓库写给 systemd 的那份 unit
// 里的重启策略。`Restart=on-failure` 只在**非零**退出时拉起进程 —— 干净退出(0)在
// systemd 眼里是「它自己不想跑了」,一次远程升级会因此把整台机器停在升级后的第一秒。
// 两处任何一边改了都会在这里红,而不是等到某台联调机升完级就再也没上线。
func TestSelfUpdateRestart_GivenTheInstalledSystemdService_WhenTheDaemonExitsToTakeTheNewBinary_ThenTheUnitBringsItBack(t *testing.T) {
	t.Parallel()

	manager, ok := newSystemdServiceManager(serviceManagerConfig{
		BinaryPath: "/usr/local/bin/agentred",
		DataDir:    "/var/lib/agentred",
		HomeDir:    "/home/agentre",
	}).(*systemdServiceManager)
	require.True(t, ok, "systemd 管理器的具体类型变了,这条守卫读不到那份 unit")

	var policy string
	for _, line := range strings.Split(manager.unit(), "\n") {
		if strings.HasPrefix(line, "Restart=") {
			policy = strings.TrimPrefix(line, "Restart=")
			break
		}
	}
	require.NotEmpty(t, policy, "unit 里必须写明重启策略,否则升级后的 daemon 不会回来")

	switch policy {
	case "always":
		// 任何退出码都拉起来,无需再看状态码。
	case "on-failure":
		assert.NotZero(t, handlers.SelfUpdateRestartExitCode,
			"Restart=on-failure 下,daemon 必须以非零状态码退出,systemd 才会把它拉起来")
	default:
		t.Fatalf("未知的重启策略 %q:它会不会在自更新退出后拉起 daemon,需要重新论证", policy)
	}
}
