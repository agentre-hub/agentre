// Module github.com/agentre-hub/agentre/pkg/syncwire 是桌面端 ↔ server 工作区同步
// 协议(HTTP/JSON)的线上契约。
//
// 它与 pkg/wire 是**两条不同的协议**:那条是 agentre ↔ agentred 的 Protobuf RPC,
// 这条是桌面端上行/下行同步的 JSON API。分成两个 module 而不是合并,是为了不让任何
// 一方被另一方的依赖与版本节奏牵着走 —— 同步契约这一份连 protobuf 都不需要。
//
// 它是独立 module 而不是桌面仓的一个普通包,理由与 pkg/wire 相同:agentre-server
// 必须能 import 它,而放在 internal/ 下 Go 的可见性规则会挡死跨仓引用,让后端整个
// 依赖桌面 module 又违反 AGENTS.md 的跨仓不变式。
//
// 它刻意零外部依赖。binding 标签是**惰性的结构体标签**,只有 gin 在绑定时才会读它,
// 因此承载校验规则不需要把 gin 拉进来。
module github.com/agentre-hub/agentre/pkg/syncwire

go 1.26.0

require github.com/stretchr/testify v1.11.1

require (
	github.com/davecgh/go-spew v1.1.1 // indirect
	github.com/pmezard/go-difflib v1.0.0 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
)
