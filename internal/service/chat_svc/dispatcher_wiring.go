package chat_svc

import (
	"github.com/agentre-hub/agentre/internal/pkg/transcript"
	"github.com/agentre-hub/agentre/internal/pkg/transcript/turn"
)

// newPackageDispatcher 构造 chat_svc 的 dispatcher:注册表归共享包
// (transcript.NewTurnDispatcher —— 两个宿主共用的那一张),这里只把 chat_svc 的
// 持久化适配器(usage/error/context_window/permission_mode/plan/compact)交进去。
// 每个 chatSvc 实例调一次,svc-bound 让适配器能持 *chatSvc 引用。
//
// SteerConsumed 与 ErrorEvent 不经 dispatcher —— 由 chat.go runTurn 的 switch
// 提前拦截(turn-segmentation / streamStopErr 紧耦合 local state)。
func newPackageDispatcher(svc *chatSvc) *turn.Dispatcher {
	return transcript.NewTurnDispatcher(buildAdapters(svc))
}

// packageDispatcher 用零值 svc(nil)注册;运行时调用方应当用 newPackageDispatcher(svc)
// 拿到 svc-bound 实例(Steer/Usage 等才能落库)。本变量留作脚手架 + 单测用。
var packageDispatcher = newPackageDispatcher(nil)
