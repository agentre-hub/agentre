package syncwire

import (
	wire "github.com/agentre-hub/agentre/pkg/syncwire"
)

// 载荷守卫归共享 module github.com/agentre-hub/agentre/pkg/syncwire 所有:桌面端在
// 上行前调用它、服务端在落库前调用它,跑的是**同一份实现**与同一份测试向量
// (pkg/syncwire/guard_test.go)。本文件只做别名再导出,调用点因此一行不用改。
//
// 守卫零外部依赖,而那个 module 正是为「服务端也能 import」而存在的。规则、错误值与
// 向量的说明都在 pkg/syncwire/guard.go,别在这里另起一份。
var (
	ErrPayloadLocalID       = wire.ErrPayloadLocalID
	ErrPayloadCredential    = wire.ErrPayloadCredential
	ErrPayloadNotObject     = wire.ErrPayloadNotObject
	ErrPayloadAvatarContent = wire.ErrPayloadAvatarContent
)

// GuardPayload 见 pkg/syncwire.GuardPayload。
func GuardPayload(kind string, payload []byte) error {
	return wire.GuardPayload(kind, payload)
}
