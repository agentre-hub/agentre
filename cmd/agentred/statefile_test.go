package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// 拒绝信里的那条建议是用户唯一拿得到的出路,所以它必须在两个平台上都真的走得通。
//
// macOS 的 LaunchAgent 写死了 KeepAlive=true(service_launchd.go),`pkill` 之后 launchd
// 秒级把 daemon 拉回来,用户再跑 login 撞见的还是同一句拒绝 —— 建议本身把人锁进死循环。
// 两个平台都成立的停法只有 `agentred service stop`;而停完之后还得有人告诉用户把服务
// 起回来,否则这台盒子登录成功却从此不再上线(systemd 的 Restart=on-failure 不会替他补上)。
func TestGivenRunningDaemonWhenRefusingStateWriteThenSuggestsServiceStopAndStart(t *testing.T) {
	err := requireNoRunningDaemon(func() bool { return true })

	require.Error(t, err)
	msg := err.Error()
	assert.Contains(t, msg, "agentred service stop", "拒绝信必须给出装了服务时唯一有效的停法")
	assert.Contains(t, msg, "agentred service start", "停完不告诉用户起回来,盒子会一直不上线")
	assert.NotContains(t, msg, "pkill", "pkill 在 macOS 上被 launchd KeepAlive 立刻撤销,不能当建议")
}

// 探测说没有 daemon 在跑就必须放行:误判成「在跑」会把一台好机器锁在无法登录的状态里。
func TestGivenNoRunningDaemonWhenCheckingStateOwnershipThenAllowsWrite(t *testing.T) {
	assert.NoError(t, requireNoRunningDaemon(func() bool { return false }))
	assert.NoError(t, requireNoRunningDaemon(nil))
}
