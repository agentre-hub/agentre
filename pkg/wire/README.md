# `pkg/wire` — agentre 线上协议的主人

这个目录是 **agentre ↔ agentred 协议在 Go 这一侧的唯一主人**。桌面端、agentred 与
`agentre-server` 都消费它,谁都不再自己抄一份。

> 同一个仓库里还有一个共享 module:`pkg/syncwire`,那是桌面端 ↔ server 工作区同步的
> HTTP/JSON 契约。**两条协议,两个 module** —— 这条是 agentre ↔ agentred 的 Protobuf
> RPC,那条连 protobuf 都不需要,分开才不会互相拖着依赖与版本节奏走。

## 为什么它是一个独立 module

`agentre-server` 是一个独立后端,**不允许**依赖桌面应用的 Go module
(`github.com/agentre-hub/agentre`)。可协议本身是两边共用的。

解法是把协议放进一个嵌套 module `github.com/agentre-hub/agentre/pkg/wire`:

- 桌面仓通过本地 `replace` 用它;
- `agentre-server` 钉一个**已推送的不可变 revision**;
- 这里的代码**从不 import 宿主代码** —— 上面的依赖方向表就是这条约束的形状。

整个工作区只有一份 `wire.pb.go`,`internal/guard` 与 `pkg/wire/guard` 各有守卫,
出现第二份就判红。

## schema 就在这里

```
proto/agentre/wire/wire.proto   唯一的 schema
proto/baseline.binpb            buf breaking 的基线(棘轮点)
buf.yaml / buf.gen.yaml         buf 模块与生成模板
agentrewire/                    Go 生成物(clean: true,别往里放手写文件)
```

协议的主人自己带着 schema:读这个目录就能回答「这份协议长什么样」,不必先跳去消费方
的目录。`agentre-server` 钉的那个不可变 revision 里也因此含着 schema 本身。

生成的入口仍在 TS 包里 —— buf 与 `protoc-gen-es` 是那个包的 devDependency,`pnpm` 先把
`node_modules/.bin` 放进 PATH,脚本再 `cd` 到这里跑 `buf generate`。一次产出两侧:

```bash
cd frontend/packages/agentre-wire
pnpm run proto:generate   # → pkg/wire/agentrewire/ 与 该包的 src/gen/
pnpm run proto:check      # buf lint + buf breaking + 重新生成 + git diff --exit-code
```

## 子包与依赖方向

依赖只向下,没有一条反向边:

```
wirelimits ────┐                    (叶子:尺寸预算,谁都能取)
relayenvelope  │                    (叶子:中继通道信封)
rpcerror ──────┤                    (叶子:结构化 RPC 失败)
agentrewire ───┤                    (生成物:消息、方法枚举、字段/文件选项)
               ├──> protorpc        (RPC 引擎:分帧、请求号、取消、保活)
               ├──> eventkind       (事件判别值,从 descriptor 读)
               └──> protocolversion (协议版本号,从 descriptor 读)
                    wirecall        (调用侧 typed 面,依赖 agentrewire + protorpc)
                    portforwardhost (端口转发宿主侧 Handler,依赖 wirecall + wirelimits)
turnstate                           (叶子:一轮怎么收场)
devicefp                            (叶子:设备指纹的四个角色各一个类型)
guard                               (本 module 自己的守卫测试)
```

| 子包 | 它拥有什么 |
| --- | --- |
| `agentrewire` | buf 生成的消息、`RpcMethod` 枚举、`event_kind` 字段选项与 `protocol_version` 文件选项。**不要手改。** |
| `protorpc` | RPC 引擎:分帧、请求号对应、取消、保活、panic 兜底、通知分发 |
| `wirecall` | 调用侧一个方法一个函数;method ID 与消息类型的配对**整个工作区只在这里出现一次** |
| `portforwardhost` | 端口转发协议宿主那一侧的消费者:请求 → open + 三条通知 + 累计 ack + 101 交接。**不是契约,是契约的消费者** —— 它住在这里是因为这个 module 是 Go 侧唯一被批准的跨仓共享通道,桌面端与 agentre-server 控制台共用同一份 |
| `rpcerror` | `Error{Code,Message,Details}` 与错误码 |
| `eventkind` | `RuntimeEventNotification` 每条 oneof 分支的转录判别值,从 descriptor 读 |
| `protocolversion` | 协议自己声明的版本号(`(agentre.wire.protocol_version)` 文件选项),从 descriptor 读 |
| `relayenvelope` | 中继通道信封(2 字节长度 + 通道 ID + 载荷)的唯一实现与唯一一套校验 |
| `wirelimits` | 载荷预算。整条链路共用一个数,三处曾经不同源,后果是超限打掉**整条物理连接** |
| `turnstate` | 一轮执行怎么收的场 |
| `devicefp` | 设备指纹四个角色(承载者/发起方/最后写者/待授权客户端)各一个定义类型,混用即编译错误 |

## TypeScript 那一侧

`frontend/packages/agentre-wire`(`@agentre-hub/agentre-wire`)是同一份协议的 TS 侧:
它从**这里**的 `.proto` 生成 `src/gen/`,自己不再存一份 schema。消费方钉一个已推送的
commit。

两侧要成对存在的东西:事件判别值(`eventkind` ↔ `event-kind.ts`)、协议版本号
(`protocolversion` ↔ `protocol-version.ts`)—— 这两对都从 descriptor 读同一格 ——
与中继信封(`relayenvelope` ↔ `relay-envelope.ts`,同一格式同一套校验)。

## 加一个 RPC 方法

1. 在 `proto/agentre/wire/wire.proto` 里加方法枚举值与请求/响应消息,跑
   `pnpm proto:generate`(Go 与 TS 生成物一起出)。
2. 在 `pkg/wire/wirecall/methods.go` 加一行 —— **漏了会判红**:完备性守卫要求每个
   枚举值有且只有一个 typed 调用函数,命名守卫还会核对请求/响应类型符合约定。
3. 在宿主里注册 handler(agentred 侧 `internal/daemon/protobuf_*`,桌面端
   `internal/peer/protobuf_inbound.go`)。
4. 方法集变了就要抬协议版本号 —— 握手判据是版本号相等,版本号因此必须代表方法集,
   由 `internal/pkg/wireversion/methodset_test.go` 的方法集指纹守卫钉住。

## 版本与 pin

协议版本号的主人也在这里:它写在 `.proto` 的 `(agentre.wire.protocol_version)` 文件
选项上,和 schema 同住。两侧都从 descriptor 读同一格 —— Go 走 `protocolversion.Protocol()`,
TS 走 `src/protocol-version.ts` —— 谁 import 这个 module 谁就拿到了版本号,不必解析
任何文件。它从前住在 `frontend/packages/agentre-wire/package.json` 的 `version` 里,
于是两个仓库的 Go 各自复述一份,各自靠一条解析文件的守卫钉住自己看得见的那个来源。

抬版本就是改 `.proto` 上那一格再重新生成。`package.json` 的 `version` 仍要跟着改
(它是个真的 npm 包,消费方按它 pin),但它现在是**复述**,由
`src/__tests__/protocol-version.test.ts` 钉住;各宿主 `wireversion.MinSupported`
(本 build 填进握手那一格的号)跟着抬到同一个新号。

**兼容判据是相等**:两端的 `protocol_version` 逐字相同才握得上手,没有区间可谈。判定本身
不在这里,在各宿主自己的 `wireversion` 包里(`Match` / `Reject`)。

跨仓库升级的顺序是固定的:**先在本仓改完、验证、推送**,消费方才能钉到那个不可变
revision。绝不能在共享包的 revision 可用之前先删掉消费方那份能跑的实现。
