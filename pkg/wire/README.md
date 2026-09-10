# `pkg/wire` — agentre 线上协议的主人

这个目录是 **agentre ↔ agentred 协议在 Go 这一侧的唯一主人**。桌面端、agentred 与
`agentre-server` 都消费它,谁都不再自己抄一份。

## 为什么它是一个独立 module

`agentre-server` 是一个独立后端,**不允许**依赖桌面应用的 Go module
(`github.com/agentre-hub/agentre`)。可协议本身是两边共用的。

解法是把协议放进一个嵌套 module `github.com/agentre-hub/agentre/pkg/wire`:

- 桌面仓通过本地 `replace` 用它;
- `agentre-server` 钉一个**已推送的不可变 revision**;
- 这里的代码**从不 import 宿主代码** —— 上面的依赖方向表就是这条约束的形状。

整个工作区只有一份 `wire.pb.go`,`internal/guard` 与 `pkg/wire/guard` 各有守卫,
出现第二份就判红。

## 子包与依赖方向

依赖只向下,没有一条反向边:

```
wirelimits ────┐                    (叶子:尺寸预算,谁都能取)
relayenvelope  │                    (叶子:中继通道信封)
rpcerror ──────┤                    (叶子:结构化 RPC 失败)
agentrewire ───┤                    (生成物:消息、方法枚举、字段选项)
               ├──> protorpc        (RPC 引擎:分帧、请求号、取消、保活)
               └──> eventkind       (事件判别值,从 descriptor 读)
                    wirecall        (调用侧 typed 面,依赖 agentrewire + protorpc)
turnstate                           (叶子:一轮怎么收场)
guard                               (本 module 自己的守卫测试)
```

| 子包 | 它拥有什么 |
| --- | --- |
| `agentrewire` | buf 生成的消息、`RpcMethod` 枚举、`event_kind` 字段选项。**不要手改。** |
| `protorpc` | RPC 引擎:分帧、请求号对应、取消、保活、panic 兜底、通知分发 |
| `wirecall` | 调用侧一个方法一个函数;method ID 与消息类型的配对**整个工作区只在这里出现一次** |
| `rpcerror` | `Error{Code,Message,Details}` 与错误码 |
| `eventkind` | `RuntimeEventNotification` 每条 oneof 分支的转录判别值,从 descriptor 读 |
| `relayenvelope` | 中继通道信封(2 字节长度 + 通道 ID + 载荷)的唯一实现与唯一一套校验 |
| `wirelimits` | 载荷预算。整条链路共用一个数,三处曾经不同源,后果是超限打掉**整条物理连接** |
| `turnstate` | 一轮执行怎么收的场 |

## TypeScript 那一侧

`frontend/packages/agentre-wire`(`@agentre-hub/agentre-wire`)是同一份协议的 TS 侧:
`.proto` schema 就住在那里,Go 与 TS 的生成物都由它的 `buf generate` 产出。消费方钉
一个已推送的 commit。

两侧要成对存在的东西:事件判别值(`eventkind` ↔ `event-kind.ts`,都从 descriptor 读)
与中继信封(`relayenvelope` ↔ `relay-envelope.ts`,同一格式同一套校验)。

## 加一个 RPC 方法

1. 在 `frontend/packages/agentre-wire/proto/agentre/wire/wire.proto` 里加方法枚举值
   与请求/响应消息,跑 `pnpm proto:generate`(Go 与 TS 生成物一起出)。
2. 在 `pkg/wire/wirecall/methods.go` 加一行 —— **漏了会判红**:完备性守卫要求每个
   枚举值有且只有一个 typed 调用函数,命名守卫还会核对请求/响应类型符合约定。
3. 在宿主里注册 handler(桌面端 `internal/daemon/protobuf_*`,peer 侧
   `internal/peer/protobuf_inbound.go`)。
4. 方法集变了就要考虑 `wireversion.MinSupported` —— 见那个包的注释里的守恒律。

## 版本与 pin

协议版本号的主人是 `frontend/packages/agentre-wire/package.json` 的 `version`。
两个仓库的 Go 各自复述一份(`internal/pkg/wireversion`),各自被守卫钉在自己看得见
的那个来源上(桌面仓盯 `package.json`,server 盯 `pnpm-lock.yaml`)。

跨仓库升级的顺序是固定的:**先在本仓改完、验证、推送**,消费方才能钉到那个不可变
revision。绝不能在共享包的 revision 可用之前先删掉消费方那份能跑的实现。
