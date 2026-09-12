package claudecode

import "os"

// SetHookCLIPath 由 bootstrap 注入 PostToolUse hook 要 exec 的 CLI 路径
// （桌面端 = <AppDataDir>/bin/agrctl）。为空则 hookBin 回落 os.Executable()。
func (r *Runtime) SetHookCLIPath(p string) { r.hookCLIPath = p }

// hookBin 返回注册进 PostToolUse hook 的可执行文件路径：优先注入路径，否则回落当前可执行文件
// （agentred 守护进程走此回落，它自带 claudecode 子命令）。
func (r *Runtime) hookBin() (string, error) {
	if r.hookCLIPath != "" {
		return r.hookCLIPath, nil
	}
	return os.Executable()
}

// SteerInboxConfigured 回答「这份 runtime 拿到投递插话的信箱了没有」。
//
// 它是**接线的可观测缝**：注入发生在各自的进程启动路径上（桌面端
// internal/bootstrap、agentred internal/daemon），而漏注入的表现是
// runtime.steer 一律返回「steer inbox not configured」——别处一个编译错误都没有，
// 静态检查也看不出来。agentred 上就这么坏了很久：桌面端注入了、它没有，同一条
// 「轮中插话」在两台宿主上一个能用一个不能。两处启动路径各有一条回归测试盯着它。
func (r *Runtime) SteerInboxConfigured() bool { return r.steer != nil }
