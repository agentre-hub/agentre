// Module github.com/agentre-hub/agentre/pkg/wire 是 agentre ↔ agentred wire 协议
// 生成的 Go 侧，唯一来源是 frontend/packages/agentre-wire/proto/agentre/wire/wire.proto。
//
// 它是一个独立 module 而不是桌面仓的一个普通包，因为 agentre-server 必须能 import 它：
// 放在 internal/ 下 Go 的可见性规则会挡死跨仓引用，而让后端整个依赖桌面 module 又违反
// AGENTS.md 的跨仓不变式。独立 module 让消费方只钉一个已推送的不可变 revision。
module github.com/agentre-hub/agentre/pkg/wire

go 1.26.0

require google.golang.org/protobuf v1.36.11
