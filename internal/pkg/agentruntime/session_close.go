package agentruntime

import "context"

// SessionCloser 是 runtime 可选实现的「会话释放口」:把某条 chat 会话在本机常驻的
// CLI 子进程放掉。没有常驻子进程的后端(builtin / remote)不实现它。
type SessionCloser interface {
	CloseSession(ctx context.Context, sessionID int64)
}

// CloseSessionEverywhere 让每个认领了释放口的已注册 runtime 释放这条会话。
//
// 会话删除时哪个后端在池里留着子进程,调用方并不知道(会话行已经没了),所以按注册表
// 广播而不是逐个后端硬写 —— 后者每加一个有常驻子进程的后端就会静默漏一处,漏掉的
// 表现是机器上多一个永不退出的 CLI。
func CloseSessionEverywhere(ctx context.Context, sessionID int64) {
	if sessionID <= 0 {
		return
	}
	for _, rt := range RegisteredRuntimes() {
		if closer, ok := rt.(SessionCloser); ok {
			closer.CloseSession(ctx, sessionID)
		}
	}
}
