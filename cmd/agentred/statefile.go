package main

import "errors"

// state.json 的所有权：**运行中的 daemon 独占它。**
//
// daemon 在内存里持有一份完整的 state.State，`state.Save` 是把那一整份序列化后原子
// 替换掉文件（internal/daemon/state/state.go）——没有 merge。而它在正常干活时就会存盘：
// 对端握手 / lastSeen（internal/daemon/rpc/auth.go）、吊销拉取与凭据续期
// （internal/daemon/daemon.go）。
//
// login / unclaim 是**另外的进程**，各自 load → mutate → save。两边并存就是一个典型的
// lost update：CLI 写进去的新归属，会被 daemon 下一次 Save 按它内存里的旧快照覆盖掉。
// 更糟的是这一切悄无声息 —— 服务端已经签发了凭据、CLI 已经打印「登录成功」，而 daemon
// 手上仍是旧的那份，且它认为自己「已归属」，永远不会回头重读磁盘
// （见 daemon.relayServerURL 只在未归属时 AdoptClaimFromDisk）。
//
// 所以这两个命令在 daemon 活着时**当场拒绝**，一个字节都不写。让用户先停进程，比让他
// 几分钟后对着一台「登录过但仍然离线」的机器重来一遍要诚实得多。
//
// 拒绝信里给的停法只能是 `agentred service stop`：daemon 通常是被服务管理器托管的，
// macOS 的 LaunchAgent 写死了 KeepAlive=true（service_launchd.go），`pkill` 之后 launchd
// 秒级把它拉回来 —— 用户会撞见一模一样的拒绝，陷进死循环。而停完必须把服务起回来，
// 否则这台盒子登录成功却从此不上线（systemd 那侧 Restart=on-failure 也不会替他补上），
// 所以 start 那半句和 stop 一样是出路的一部分。
var errDaemonHoldsStateFile = errors.New(
	"agentred is running and owns state.json; stop it with `agentred service stop` " +
		"(or stop the process directly if you started it by hand), re-run this command, " +
		"then bring the daemon back with `agentred service start`")

// daemonIsRunning 探一次本地 IPC 端点。探不通即认为没有 daemon 在跑——这正是要放行的
// 那种情形，而误判成「在跑」会把一台好机器锁在无法登录的状态里。
func daemonIsRunning() bool {
	_, err := localGET("/local/status")
	return err == nil
}

// requireNoRunningDaemon 是 login / unclaim 落盘前的统一闸门。probe 为 nil 表示调用方
// 没有注入探测（只出现在窄口径单测里），此时不拦。
func requireNoRunningDaemon(probe func() bool) error {
	if probe == nil || !probe() {
		return nil
	}
	return errDaemonHoldsStateFile
}
