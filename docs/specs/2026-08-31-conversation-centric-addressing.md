# 以对话为中心的寻址：全局对话标识、可验证的对端身份与协议版本窗口

> Status: Approved
> Owner: 桌面端 chat 域 / agentred daemon 域 / server relay + agent_session 域
> Last updated: 2026-08-31

**Objective:** 让客户端**在已保存的对话上**寻址对话而不是承载它的机器——为对话引入全局唯一的
`conversation_id`（UUIDv7），把对端身份从"自报标签"改成"凭据里验出来的身份"，把中继的路由
目标从连接级降到通道级，并把账号信号并入同一条连接，使**一个账号一条 WebSocket** 成为字面
意义上的事实：`/v1/account/channel` 端点随之删除。

**Hard invariant:** 四条不得回退。

1. **local-first 不变。** 未登录、离线、纯 LAN 直连（Mode A/B）下仍可新建并运行对话；
   `conversation_id` 由发起端本地 mint，任何路径都不需要联网取号。
2. **隐私边界不变。** 未保存的对话在 server 上仍然一个字都不落库，
   `internal/api/savedsession/guard_test.go` 继续绿。本轮不为未保存的对话在 server 上
   建立任何行，包括只存指向的索引行。
3. **Mode A/B 行为不变。** LAN 配对握手的鉴权语义、`device_fingerprint` 的绑定方式与
   TOFU pinning 一字不改；本轮只动 Mode C（账号 / 中继）那一条路。
4. **R19 不变。** cwd 不下行；`conversation_id` 是不透明标识，不携带路径、机器名或任何
   可反推的宿主信息。
5. **账号信号的语义不变。** 并入同一条连接之后，它仍然是尽力而为、可合并、可丢弃的"该拉了"
   提示，权威仍在数据库；订阅仍然在 upgrade **之前**建立，不留"刚连上就漏掉一条"的窗口；
   通道不可用时客户端仍退回 30 秒轮询。变的只有它跑在哪条 socket 上。

本轮有**两处刻意的行为变化**，各自由决策载明：决策 9（浏览器对端身份改为账号级服务端派生，
清站点数据不再使旧对话成为孤儿）、决策 10（中继客户端连接不再绑定单一目标机器）。

## Problem

以下事实均于 2026-08-31 对工作区当前代码核实，非估算。

### A. 会话标识不是标识

1. **桌面端的会话 id 是本地库的自增主键。** `chat_entity/session.go:34` 的
   `ID int64 gorm:"primaryKey;autoIncrement"`，且 `chat_svc/peer_session.go:144`、`:185`
   把这个 `session.ID` 原样当作 wire 的 `SessionID` 送给 agentred。一个账号挂两台桌面机，
   两条 1 号对话必然并存。

2. **浏览器那一侧是刻意放弃跨发起端唯一性的。** `agentre-server/frontend/src/lib/dispatch.ts:145`
   的 `newSessionId()` 是 53 位雪花（41 位毫秒 + 12 位序列）。其注释自陈：低位选序列而非随机，
   是为了让批量导入循环确定不撞。注释把两格分开记：**跨标签页**同毫秒是 1/4096；**跨浏览器**
   那一格只说比此前的均匀随机"是退步的"，未给数字，理由是"那一格本来就不共享键空间，撞了
   也互不相干"。本轮要消灭的正是"不共享键空间"这个前提。

3. **daemon 已经在运行时为同号会话打补丁，且该补丁挡的是一类信息泄漏。**
   `internal/daemon/handlers/runtime.go:1124` 的 `runtimeSessionID(peer, sessionID)`
   把对端指纹 FNV 揉进 backend 会话键。其注释写明不隔离的后果："两个对端的同号会话就并成了
   一条 …… 一台设备读得到另一台的 requestID / 工具名 / 完整工具入参，还能照着那个 requestID
   替对方提交审批。"**一个需要在运行时靠哈希隔离才安全的标识，本身就不是标识。**

4. **三套 schema 都把"复合键"写进了主键，以绕开重号。** agentred 的
   `migrations/202608080011_daemon_sessions.go` 两张表主键分别是
   `(peer_fingerprint, peer_session_id)` 与 `(peer_fingerprint, peer_session_id, seq)`，
   注释直陈理由是"会话 id 是各客户端本地自增的，不同客户端必然重号"；server 的
   `migrations/202608280008_agent_sessions.go` 三张表 + `202608280007_agent_session_saves.go`
   共四张表以 `(user_id, peer_fingerprint, peer_session_id)` 为身份键。

5. **同一个词在两个仓库指两件事。** `202608280008_agent_sessions.go:27` 自己记着：
   `peer_session_id` 指的是发起端本地的 `chat_sessions.id`，而桌面端库里的 `session_id`
   指本机 `chat_sessions.id`——同一个词两个含义。

6. **客户端手里只有一半的键，于是只能猜。**
   `agentre-server/frontend/src/components/session/sessionMirror.ts:54` 有一整段启发式：
   "先认索引行给出的发起端；否则先认这台机器自己发起的那条；再否则认账号里唯一一条同号对话"，
   注释承认"再往下是歧义"。病根是路由按机器建（URL 只有承载机器 `deviceId` 与 `session_id`），
   身份键的另一半`peer_fingerprint`不在客户端手里。

### B. 对端身份是自报的，而且不稳定

7. **Mode C 的 `device_fingerprint` 没有被任何东西验证。**
   `internal/daemon/auth/auth.go:143` 的 `HandleAccount` 验凭据签名、账号归属与吊销列表，
   **全程没有读过 `p.DeviceFingerprint`**；紧接着 `internal/daemon/protobuf_registry.go:127`
   把请求体里那个字符串原样写进 `AuthState`。对照 Mode B：device token 在
   `protobuf_registry.go:98` 的 `HandleConnect` 里就对着 fingerprint 验过了，`:106` 只是再查
   `PairedPeers` 取 `DeviceName`——那一条是绑住的。

8. **凭据里有槽位但没填。** `agentre-server/internal/pkg/jwt/jwt.go:16` 的 `Claims`
   有 `DID`，而中继票只签 `{UID, Kind:"relay_client"}`
   （`internal/controller/device_ctr/device.go:122`），`DID` 恒为 0。浏览器不注册设备行，
   这是刻意的（`device_ctr/relay_ticket_test.go:23`
   `TestRelayTicket_FromSessionWithoutCreatingDevice`）。

9. **浏览器的对端身份存在 localStorage，清一次就换人。**
   `agentre-server/frontend/src/lib/relayTicket.ts` 的 `browserClientId()` 是
   `randomId()` 存 `agentre.browserClientId`。清站点数据后 id 改变，该浏览器发起过的全部
   对话在账号镜像里当场成为孤儿——身份键的一半没了。第 6 条那段猜测启发式正是在给这件事打补丁。

   边界说明：一个账号内的所有对端属于同一个用户，因此第 7 条**不是越权**，拿不到别人账号的
   东西。它的实际后果是这个标识承载不了校验的重量，以及本条的不稳定。

### C. 一台机器一条 WebSocket

10. **中继的路由目标在 upgrade 那一刻定死。**
    `agentre-server/internal/controller/relay_ctr/relay.go:129` 的 `Client()` 在 `:132` 读
    `?daemon_fingerprint=`，`relay_svc/relay.go:215` 的 `ConnectClient` 据此在 `:231` 解析出
    `Route` 并固定到整条连接；`relay_svc/framebus.go:770` 的 Redis stream key 也由
    `route.Fingerprint` 拼成。

11. **于是浏览器按机器建连接池。** `frontend/src/lib/relayClientPool.ts` 以 fingerprint
    为索引，`pages/chat/useMachineReachability.tsx` 为每台在线机器挂一个 `MachineSessionResolver`
    各连各的。三台机器 = 三张票、三条 socket、三次握手；一台离线，那一块 UI 就够不着。

    对照 daemon 那一侧：`internal/daemon/relaytransport/multiplexer.go` 本来就是一条物理
    链路多路复用 N 条虚拟通道。多开的那几条纯粹是浏览器这侧自己加的。

### D. 协议版本是悬崖，不是区间

12. **握手是逐字严格相等。** `internal/pkg/wireversion/wireversion.go:17`
    当前 `Protocol = "0.3.0"`，`internal/daemon/client/protobuf_client.go:92` 的
    `peerProtocolVersionError` 对任何不等都拒。`wireversion.go:22` 写明理由：
    "Today that is an exact match, **because agentre is unreleased and carries no
    compatibility burden**"，并预告了以后加 `min_supported_protocol_version` 放宽到 N-1 窗口。

13. **严格相等是替掉所有 per-method 降级分支才成立的。**
    `internal/pkg/wireversion/methodset_test.go:26` 的方法集指纹守卫自陈："严格相等的握手之所以能替掉所有 per-method 的 method-not-found 降级，前提是
    「方法集变了 ⇒ 版本号变了」"。任何放宽窗口的设计必须保住这个前提，否则被删掉的降级分支
    会以运行期崩溃的形态回来。

14. **agentred 没有自更新通道。** `cmd/agentred` 与 `internal` 下无任何 selfupdate /
    autoupdate 路径。发布之后的破坏性变更 = 用户手动更新每一台机器。本轮在**发布之前**做完，
    正是为了不欠这笔账。

### E. 两个端点已经几乎是同一个东西

15. **`/v1/account/channel` 与 `/v1/relay/client` 共用路由组、传输、心跳与下线策略。**
    `internal/api/router.go:299` 把前者挂在与后者**同一个** `tokenBridged` 组上；
    `internal/controller/accountchan_ctr/channel.go` 的传输是 `relayws.New(relayws.ClientReadLimit)`
    ——与 `relay_ctr` 的客户端传输同一个构造；心跳与凭据复查是同一套 `relayws.Hooks` +
    `connguard`；优雅下线是同一个 `Drain()` 1001，且两者本来就被一起收——
    `internal/api/router.go:86` 的 `drainers = []relayws.Drainer{relayCtr, accountChanCtr}`，
    经 `router.go:58` 的 `DrainRelays()` 由 `internal/task/relaydrain.go` 的 `Drainer` 接口
    驱动，后者注释写的就是"中继与账号通道"。

16. **它们唯一的结构性差异，正是本轮要取消的那一条。** `accountchan_ctr` 的包注释写明：
    "与中继的两个端点的根本区别是**不指定目标 daemon**"。决策 10 取消了中继客户端连接在
    建立时指定目标——那条根本区别随之消失，剩下的只有单向与双向之别，而单向是双向的子集。
    结果是每个账号在网页上要开两条 socket，两套连接生命周期代码（浏览器
    `frontend/src/lib/accountChannel.ts` 258 行、桌面端
    `agentre/internal/service/server_svc/accountchannel.go` 115 行）做同一件事。

## Actors and user stories

1. 作为**网页控制台用户**，我想打开一条对话就能读写它，而不必先知道也不必先选中它跑在哪台
   机器上；同时看多台机器上的对话时，浏览器只维护一条连接。
2. 作为**桌面端用户**，我想在未登录、断网、或只有 LAN 直连 agentred 的情况下照常新建与运行
   对话——本轮不得给这条路加任何联网前提。
3. 作为**换过浏览器或清过站点数据的用户**，我想我此前从网页发起的对话仍然认得我，而不是变成
   一批读不出转录的孤儿。
4. 作为**在这三个仓库里工作的开发者**，我想"这条对话是哪一条"在桌面端库、agentred 库、
   server 库与线格式上是同一个字段、同一个值、同一个名字。

## Design decisions

| # | Decision | Basis and rejected option |
|---|---|---|
| 1 | `conversation_id` 为 UUIDv7 字符串，由**发起端**在建档那一刻 mint | UUIDv7 的价值就是无需协调即全局唯一，天然满足多实例 / 分布式。Rejected: **server 发号**——会使新建对话需要联网 + 登录，打穿 Hard invariant 1；且 server 将知晓每条对话的存在，打穿 Hard invariant 2（Problem 第 9 条的边界说明）。反直觉但关键：server 发号**引入**了本来为零的协调需求 |
| 2 | 存量对话确定性回填：`UUIDv5(NS_AGENTRE_CONVERSATION, peer_fingerprint + "\0" + peer_session_id)`，三个仓库共用同一 namespace 常量 | 桌面端与 server 各持一份存量、迁移时互不通信，只有确定性派生才能让两边独立算出同一个值。Rejected: **各自随机 mint v7**——桌面端与 server 会给同一条对话生成两个不同 uuid，镜像存量全体成孤儿。新对话用 v7、存量用 v5，混用无碍：两者都是 UUID，只有版本位不同 |
| 3 | wire 上**破坏性替换**：24 处 `int64 session_id` → `string conversation_id` | Problem 第 12/14 条：未发布，无兼容负担，且没有自更新通道去偿还日后的债。Rejected: **加字段共存**——过渡期两个 id 同时在线，需要另起一轮清理，而这一轮的成本在发布前是零 |
| 4 | `wireversion.Protocol` 由 `0.3.0` 重置为 `0.1.0`，同时引入 `MinSupported`，`Match` 改为窗口交集判定 | 开发期抬上去的版本号没有对应的已发布构建，重置使版本号重新表达真实的发布历史。窗口机制在发布前建好、发布后才有消费者，届时不必再破坏一次握手去引入它。Rejected: **保持严格相等、等发布后再放宽**——那时引入窗口本身就是一次破坏性握手变更，而那正是没有自更新通道的时刻（Problem 第 14 条） |
| 5 | **窗口绝不跨越方法集变更**：方法集指纹一变，`MinSupported` 必须等于 `Protocol`，由守卫测试强制 | Problem 第 13 条。窗口只对可加性变更（加字段、加枚举值）放宽。Rejected: **接受任意 N-1**——那是假承诺，会让 method-not-found 以运行期崩溃的形态回到第一次调用新方法的时刻 |
| 6 | `agentruntime` 的进程内 `sessionID int64` **不动** | 它是进程本地的 runtime key，不上线；该子树 `sessionID int64` 命中 111 行，另有 **7 个** runtime 包，其中至少 6 个以 `map[int64]` 或 `sessionKey(id int64)` 为进程内会话键。改它与本轮目标无关。Rejected: **一路改成 string**——纯粹的连带成本 |
| 7 | `runtimeSessionID` 删除对端指纹混入，改为直接哈希 `conversation_id` 得到进程内 int64 键 | `conversation_id` 全局唯一，Problem 第 3 条那类跨对端并轨**由构造消失**，不再需要防御性哈希。Rejected: 原样保留——留着一段其存在理由已经消失的代码，且它仍以 `peer` 为输入，会让 `peer_fingerprint` 继续兼任身份职责 |
| 8 | 凭据新增 `pfp` claim（对端指纹）；`HandleAccount` 从已验签的凭据里取对端身份；`AuthAccountRequest.device_fingerprint` **删除** | 身份必须来自被验证的凭据而非请求体。删字段而非忽略字段：留一个"说了不算"的字段是下一个人踩的坑，且本轮本来就在破坏。Rejected: **把浏览器注册成 `devices` 行填 `DID`**——`relay_ticket_test.go:24` 那条边界是刻意的，浏览器不是可寻址设备，注册它会污染设备列表 |
| 9 | 浏览器的对端身份改为**账号级、服务端派生**，不再是 localStorage 里的随机数 | 修 Problem 第 9 条：跨浏览器、跨清缓存稳定。一个账号内的所有对端属于同一用户，A 落地后 `peer_fingerprint` 只剩授权与来源标注两个职责，不需要区分同一账号的两个浏览器。**这是一处刻意的行为变化**。Rejected: **每个浏览器一个服务端签发的标识**——要稳定就得让浏览器持久化它，于是清站点数据的问题原样回来；而它换来的"区分同一用户的两个浏览器"本轮没有任何消费者 |
| 10 | 中继的路由目标从**连接级**降到**通道级**：`daemon_fingerprint` 移出 URL，每条虚拟通道开通时声明自己要接哪条对话或哪台机器 | 直接解决 Problem 第 10/11 条。Rejected: **保持连接级、由客户端自己把 uuid 解析成机器**——客户端仍需按机器建池，socket 数不变，等于只改了 UI 措辞。**这是一处刻意的行为变化** |
| 11 | 按对话寻址**按入口分流**：已保存的对话走 `conversation:`，从机器轴点进去的走 `machine:`；Mode A/B（LAN 直连）整体保持机器寻址 | LAN 那条路上根本没有服务端去解析 uuid。机器轴列的是每台机器实时报的整份 `session.list`，其中"未保存的对话是大多数"（`SessionDetailView.tsx:465`、`:565` 的注释原文），它们在 `agent_session_saves` 里没有行，服务端解析不出承载机器。分流是自然的而非将就：**从机器轴进入时机器是用户刚选的、本来就在上下文里**，与决策 10 拒绝的"客户端自建 uuid→机器映射"不是一回事。两种通道共用同一条 socket，主要收益不受影响。Rejected: **server 为全部对话建只存指向的索引**——寻址统一，但 server 将知晓每条对话的存在，`savedsession/guard_test.go` 那句承诺要从"一个字都不落库"改写成"不落内容"，打穿 Hard invariant 2 |
| 12 | 桌面端 `chat_sessions.id` 自增主键**保留**，新增 `conversation_id` 唯一列 | 它是被 `chat_messages` 等表引用的本地主键；SQLite 换主键类型要重写每一张带外键的表。本地主键与全局标识是两件事，不必合并。Rejected: 换主键类型——巨大且无收益 |
| 13 | 账号信号并入同一条多路复用连接，`/v1/account/channel` 端点**删除**；三种对端（浏览器 / 桌面端 / agentred）都不再单开信号连接 | Problem 第 15/16 条：两个端点已共用路由组、传输、心跳与下线策略，唯一的结构性差异被决策 10 取消。不合并则 Objective 的"一条 WebSocket"不成立。Rejected: **只合并中继客户端（浏览器 + 桌面端）**——修好了真正有 N 条 socket 的两个宿主，却把端点与它的客户端实现留着，同一件事两套代码；agentred 那条链路上通道**本来就由服务端开**（`AttachClient` 分配 channelID，`Multiplexer.Accept()` 接住），保留通道不是新概念 |
| 14 | 账号信号走一个**保留通道号**，取一个不在通道 id 字母表内的字符作前缀 | 通道 id 两端各自生成：server 侧是 base64url（`relay_svc.newChannelID`），daemon 侧是 hex（`relaytransport.newRelayChannelID`）。两套字母表都不含 `~`，因此以 `~` 开头的保留号**由构造不可能与随机分配的通道相撞**，不需要重试或注册表。Rejected: 用固定的随机串或 0 号通道——前者仍需论证不撞，后者与"通道 id 非空"的既有校验冲突 |

## 会话身份

**建档即有 id。** 发起端在创建对话的那一刻 mint 一个 UUIDv7 并落库。**铸号点不止两处**，
必须全部覆盖：桌面端 `chat_repo.Session().Create` 有 5 个调用方（`chat_svc/chat.go:1072`、
`session_crud.go:134`、`:158`、`chat_svc/goal/goal.go:164`、`chat_import_svc/deps.go:54`），
浏览器 `newSessionId()` 有 2 个（`dispatch.ts:278`、`importPorts.ts:255`）。agentred 不发起
对话，只承载。

**导入路径的幂等语义要保住。** `transcriptimport.execute` 的契约允许 daemon 交回一个
**与调用方铸出来的不同**的 id（`wire.proto:727`），用于"这份转录早已导入过"的收敛；
conversation_id 方案必须保留这条语义，不能假定交回的就是送进去的那一个。

**线上 id 的合法性校验要换判据。** daemon 侧有 7 处以 `sessionID <= 0` 判定线上会话 id
合法性（`sessions/registry.go:41`、`:64`、`handlers/session_delete.go:53`、
`session_model_target.go:45`、`mcpproxy.go:113`、`transcriptimport/execute.go:65`、
`runtime.go:1141`），全部要改成 UUID 格式校验，并在 RPC 边界上给"非法 conversation_id"
一个明确的错误码。

**会话 id 会离开 daemon 进入 CLI 子进程。** `handlers/mcpproxy.go:55` 把它放进 MCP 隧道 URL
的 query 参数、`:111` 再解析回来，而这条 URL 随 `--mcp-config` 交给 claude-code / codex
子进程。换成 UUID 功能上无碍，但这是本轮唯一一处标识离开进程边界的地方，要一并改并测。

**一处顺带的收缩。** `daemon_sessions.peer_session_id` 本来就是 TEXT，所以 daemon 今天在 8 处
做 int64↔string 往返（`session_catchup.go:102`、`:161`、`:257`、`session_delete.go:61`、
`session_model_target.go:52`、`transcriptimport/execute.go:86`、`:92`、`mcpproxy.go:55`）。
线上值一旦是字符串，这些往返全部消失——这是"直接替换"相对"加字段共存"此前没算进去的收益。
注意迁移次序：`transcriptimport/execute.go:92` 在存量值解析不出 int64 时**返回硬错误**，
它必须与数据迁移同一次提交改掉。

前置条件是"用户新建一条对话"，动作是"发起端 mint 并落库"，可观察结果是"这条对话在三套库与
线格式上从第一刻起就用同一个值指称"，失败行为是"mint 不可能失败（无 I/O、无网络）；落库失败
即建档失败，与今天一致"。

**身份键收缩为一列。** 三套 schema 的复合身份键统一退化为 `conversation_id`：

- agentred `daemon_sessions` 主键 → `(conversation_id)`；`daemon_notification_journal`
  主键 → `(conversation_id, seq)`。`peer_fingerprint` 保留为普通列，承担来源标注与授权，
  退出主键。
- server `agent_sessions` / `agent_session_delete_todos` / `agent_session_saves` 的身份键 →
  `(user_id, conversation_id)`；`agent_session_notification_journal` →
  `(user_id, conversation_id, seq)`（该表今天就多带一个 `seq`，新键同样要带）。
  `peer_fingerprint` 同样降级为普通列。
  `agent_session_saves.device_fingerprint`（承载机器）**保留且变得更重要**——它是决策 10
  按对话解析目标机器时读的那一列。
- 桌面端 `chat_sessions` 新增 `conversation_id`，唯一索引。

**跨对端隔离退役。** `runtimeSessionID` 不再接收 `peer` 参数，改为哈希 `conversation_id`
得到进程内 int64 键。Problem 第 3 条描述的那条泄漏路径随之不再存在——不是被更好地防住了，
是没有了。

**三处确认不受影响，不要顺手改。** (1) Wails 边界：`wailsjs/go/models.ts` 里十几处
`sessionId: number` 携带的是桌面端本地 `chat_sessions.id`，桌面端前端从不碰对端/线上 id
（`frontend/src` 里 `peerSessionId` / `conversationId` 零命中）。(2) `agenttool` 的 MCP 授权
令牌 `Ref{AgentID, SessionID int64}`：三个调用方（`hooktool_svc` / `subagent_svc` /
`orgtool_svc`）传的都是本地 id。(3) `chat_repo/replacement_recovery.go` 的 7 处 `<= 0` 校验
同为本地 id。**桌面端因此永久存在一层 `conversation_id` ↔ 本地 int64 主键的翻译**，
这是决策 12 的直接后果，user story 4 的"同一个字段同一个值"指的是**跨库与线格式的对话身份**，
不含桌面端的本地主键。

## 对端身份

**身份来自凭据。** server 的 `jwt.Claims` 新增 `PFP`：Device JWT 填该设备的
`devices.fingerprint`；relay ticket 填账号级 web 对端标识。daemon 在 `HandleAccount` 里从
已验签的凭据取 `pfp` 写入 `AuthState`，`AuthAccountRequest` 不再有 `device_fingerprint` 字段。

前置条件是"一个客户端持中继票或 Device JWT 发起 `auth.account`"，动作是"daemon 验签后从
claim 取对端身份"，可观察结果是"客户端无法自称任何别的对端"，失败行为是"凭据缺少 `pfp`
claim 时握手以 `ErrUnauthorized` 拒绝，与凭据签名不合法同一形态——不回退到请求体，因为
回退等于这条要求不存在"。

**账号级 web 对端标识**由 server 从账号派生，稳定且不进 `devices` 表。前置条件是"同一账号
在任意浏览器上取票"，动作是"server 在票里签入同一个 `pfp`"，可观察结果是"清空站点数据、
换一台设备打开网页，此前从网页发起的对话仍然读得出转录、记得住已读"。

Mode A/B 的 `AuthPairRequest` / `AuthConnectRequest` 的 `device_fingerprint` 原样保留：
那两条路上它由 device token 绑定，本来就是被验证的（Hard invariant 3）。

## 协议版本窗口

`wireversion` 从单值变为一对：`Protocol`（本构建说的版本）与 `MinSupported`（本构建还接受的
最老版本），本轮均为 `0.1.0`，并与 `frontend/packages/agentre-wire/package.json` 的
`version` 保持逐字一致。三个握手的请求与响应各加 `min_supported_protocol_version`，
`Match` 从逐字相等改为**双向下界**："对端的 `Protocol` 不低于本方的 `MinSupported`，且本方的
`Protocol` 不低于对端的 `MinSupported`"。

不取"各自落在对方 `[MinSupported, Protocol]` 闭区间内"这个更直观的写法，是因为它自我作废：
本方 `Protocol` 要落进对端区间就必须不高于**对端的** `Protocol`，反向同理，两条合起来强制
两端 `Protocol` 相等——窗口只在版本不同时才有意义，那个读法因此永远匹配不上，且与下一段
的验收场景直接冲突。上界本来也不该存在：加字段是向后兼容的，旧端读新端只会忽略未知字段，
真正需要判定的只有"对方是否还支持我这个版本"这一个方向。

前置条件是"两端版本不同但都落在对方窗口内"，动作是"握手"，可观察结果是"握手成功且双方都能
调用对方声明的全部方法"；前置条件是"对端版本落在窗口外"，动作是"握手"，可观察结果是"以
`rpcerror.CodeProtocolVersion` 拒绝，错误消息同时给出对端版本与本方窗口"。

**窗口的守恒律**：方法集指纹改变时 `MinSupported` 必须等于 `Protocol`。这条由守卫测试强制，
是窗口机制成立的全部前提（Problem 第 13 条）。

## 按对话寻址

**目标下沉到通道。** `/v1/relay/client` 的 `daemon_fingerprint` 查询参数取消；客户端开一条
账号级中继连接，在每条虚拟通道开通时声明目标：`conversation:<uuid>` 或 `machine:<fingerprint>`。
server 对前者查 `agent_session_saves` 解析出承载机器，对后者直接按 fingerprint 解析，
随后照今天的方式路由。

前置条件是"客户端在已建立的中继连接上为一条已保存的对话开通道"，动作是"声明
`conversation:<uuid>`"，可观察结果是"通道接到承载该对话的机器，客户端全程不知道也不需要知道
那是哪一台"。

**失败按通道隔离。** 这是本节唯一需要重新想清楚的语义：一条连接上的通道现在可能落在不同机器
上，因此单条通道的目标不存在、离线或转发失败**不得影响同一连接上其它通道**。今天这些失败
在 upgrade 前以 HTTP 状态码返回（`relay_ctr/relay.go:129` 之前那一段），改为通道开通应答里
的可区分错误码；客户端据此只把那一条通道标为不可达。整条连接只在鉴权失效时关闭。

**授权判据要跟着下沉。** `relay_svc.isAddressableKind`（`relay_svc/relay.go:180`，只允许
`agentred` / `desktop`）今天在 upgrade 前执行（`:220`），把"浏览器 device kind 不得成为中继
目标"钉在连接建立那一刻。目标下沉到通道之后，这道闸必须**逐通道重查**——`conversation:`
解析出的机器与 `machine:` 直接指定的机器都要过它。漏掉它等于把这道授权闸删掉。

**机器寻址不消失，而且有三类。** 继续用 `machine:` 通道的是：(1) 机器作用域的操作——目录
选择器、引擎设置、技能目录、`session.list`、派发计划；(2) **新建对话**——对话尚不存在，
server 解析不出目标，`runtime.run` 成功并保存之后后续访问才转为按对话；(3) **从机器轴点开
的未保存对话**——它们在服务端没有索引行，而机器是用户刚选定的，本来就在上下文里。

**"客户端不知道机器"这句话的适用面因此是有边界的**：对已保存的对话成立（对话页、跨机器的
统一列表、深链接），对机器轴浏览不成立。规格不承诺后者，Objective 已按此收窄。

**两个宿主各有一个按机器的池，都要改。** 浏览器是 `relayClientPool`（索引从 fingerprint
变为账号，`useMachineReachability` 的每机器一个 resolver 改为共用一条连接开多条通道）；
桌面端是 `remote_device_svc/conn_pool.go:34` 的按目标引用计数池，且 `server_svc/relay.go:79`
自己拼 `/v1/relay/client?daemon_fingerprint=`——决策 10 删掉那个参数会直接打断它。

**常驻与空闲宽限的冲突要裁决。** 两个池子today都是"最后一个使用方走了就收"
（`relayClientPool.ts` 的 `DEFAULT_IDLE_GRACE_MS`、`conn_pool.go:92`），而合并进来的信号
通道要求连接**常驻**。规格取常驻优先：只要账号处于登录态就保持这一条连接，信号通道本身即是
一个永不释放的使用方；空闲宽限继续管普通通道的关闭时机。因此"WebSocket 总数为 1"在**零台
机器在线**时仍然是 1，不是 0。

**保留通道由谁开，两侧不同。** agentred 那条链路上通道本来就由服务端开、`Multiplexer.Accept()`
接住；中继**客户端**链路上今天的通道全部由客户端 `Open()`。因此客户端侧的 multiplexer 要
新增"接受服务端主动开的保留通道"这一能力，这是本节唯一的新机制，不是既有能力的复用。

**猜测启发式删除，但删除面不自足。** `sessionMirror.ts` 中识别镜像行的那一整段
（Problem 第 6 条）删除：URL 与索引行都以 `conversation_id` 为键，没有可猜的余地。
但它的唯一调用方 `SessionDetailView.tsx:431` 依赖它返回的**整行**做两件事——离线时的头部
摘要（`:436`）与取 `origin` 供 `loadMirrorTail`（`:441`）——所以要连带给出替代来源，
不能只删。另外 `/v1/agent-sessions/transcript` 与 `/v1/agent-sessions/read` 今天把
`PeerFingerprint` 声明为 `binding:"required,min=8,max=128"`
（`internal/api/agentsession/session.go:144`、`:120`），改键之后这两个端点的入参契约要一并改。

## 账号信号并入同一条连接

**一个账号一条 socket。** 客户端建立的那条多路复用连接同时承担两件事：普通通道承载 RPC，
一个保留通道承载账号信号（`sync_version` / `mirror_changed` / `device_presence`）。
服务端在连接建立时既订阅账号通道、又挂上帧总线；订阅仍排在 upgrade 之前，
Hard invariant 5 的"不留漏帧窗口"因此原样成立。

前置条件是"客户端建立了中继连接"，动作是"服务端在该账号上广播一条信号"，可观察结果是
"信号从保留通道到达，客户端照旧走自己的 Pull"，失败行为是"订阅建不起来时**按通道级错误作答**，整条连接照常服务 RPC，客户端只把信号
那一路标为不可用并退回 30 秒轮询"。

**这一条与"按对话寻址"节同一条规则，不是例外。** 合并之后那条 socket 同时承载 RPC，若沿用
今天 upgrade 前的 HTTP 拒绝，就会因为一个"尽力而为、可合并、可丢弃"的订阅失败而杀掉全部
RPC——那既违反 Hard invariant 5 的"变的只有它跑在哪条 socket 上"，也与本 spec 对
"connect 时子功能建不起来"给出的另一个答案自相矛盾。保留通道也是通道，它的失败是通道级的。

**三种对端的落点不同，机制相同。** 浏览器与桌面端合进它们的中继**客户端**连接；agentred
合进它的**daemon** 连接。后者不是新概念：daemon 链路上的通道本来就由服务端开、由
`Multiplexer.Accept()` 接住，只是保留号要路由到信号处理器而不是 RPC 注册表。

**保留通道是只出不进的。** 客户端不往它写；写入按协议错误处理。这保住了账号通道今天
"单向、读循环只为感知断开"的性质。

**信号的可靠性等级不随合并改变。** `signalBox` 的合并与有界丢弃发生在写 socket 之前，
原样保留。已知代价，明说：一个大的中继帧会延迟同一条 socket 上的信号投递（队头阻塞）。
可以接受，因为信号不承载权威数据——`accountchan_svc` 包注释写明其设计前提就是"它可以不
可靠"，漏帧、乱序、重复都无害。

**删除面**：`/v1/account/channel` 路由、`accountchan_ctr` 的 websocket 入口与它的
`Drain()`、浏览器 `accountChannel.ts`、桌面端 `server_svc/accountchannel.go`，以及 agentred
侧那条独立连接——它的消费者是 `enginesnapshot.Manager`（自带 dial + 重试循环，
`internal/daemon/enginesnapshot/manager.go:202`，由 `daemon.go:906` 启动），改造面在那里。

**四个 pre-upgrade 业务码要有 wire 对应物。** 是四个而非三个，且分属两个号段：
`internal/pkg/code/code.go:53-55` 的 Relay 三码（`RelayDaemonNotFound` / `RelayDaemonOffline` /
`RelayForwardFailed`，30400 段）与 `:117` 的 `AccountChannelUnavailable`（30700 段）。它们今天
以 HTTP 返回；下沉到通道级之后每一个都要指定对应的 `rpcerror` 码，**并各自同步 `code/en.go`
与 `code/zh_cn.go` 两条文案**，否则客户端分不清失败原因。`accountchan_svc`（per-account Redis Pub/Sub 这条**总线**）
**保留不动**——本轮合并的是传输，不是总线。

## 迁移与回填

三个仓库各自追加迁移，均为**追加**，不修改既有迁移。**结构与回填分两次提交**
（`develop.md` "When Touching Persistent Data" 第 3 步：合在一起时，回填失败分不清是 DDL
还是数据的错）。

- 桌面端：为 `chat_sessions` 加列，按决策 2 对每一行回填 v5 uuid，建唯一索引。回填的输入是
  本机 daemon 指纹与本行 `id`。
- agentred：两张表加列并回填，随后重建主键。三件事 spec 必须点名，否则计划阶段会踩空：
  - **gormigrate 在这两个 SQLite 库上不开事务。** `gormigrate.DefaultOptions.UseTransaction`
    为 `false`，而 `agentre/migrations/migrations.go:17` 与
    `agentre/internal/daemon/migrations/migrations.go:19` 都直接用 `DefaultOptions`——
    `begin()` 只是 `g.tx = g.db`，commit/rollback 是 no-op。多语句重建中途失败会留下半迁移
    状态且不写 migrations 行。因此**迁移体自己包 `tx.Transaction(...)`**，不改全局选项
    （改选项会改变所有既有迁移的行为）。工作区**零** table-rebuild 先例。
  - **换主键必须同批换掉 upsert 的冲突目标。** `session_repo/session.go:137` 的
    `clause.OnConflict{Columns: {peer_fingerprint, peer_session_id}}` 依赖那对列上有
    PK/UNIQUE 约束；主键换成 `(conversation_id)` 而不同批建对应约束，每次 Upsert 都会在
    运行期失败。该 repo 的 sqlmock 测试按 SQL 文本匹配，**抓不到这类 schema 回归**；而该仓库
    既禁止迁移测试、又要求 repo 单测一律 sqlmock，所以这一处**只能由手工验证兜住**——
    必须列进迁移的手工验证清单，不能指望自动化发现。
  - **体量无上界且无裁剪路径**（该表"不再回收任何一行"）。重建要写成**可分批、可重入**
    的形态；"无事务 + 大表 + 不可重试"三者叠加是本轮最大的运维风险。
- server：四张表加列并回填，随后换索引与主键。

**回填的输入必须无歧义。** 决策 2 的输入是**这条对话的发起端指纹**——也就是发起端向执行端
出示、并被 server 存进 `peer_fingerprint` 的那一个值。对桌面端而言它是 keychain
`agentre-device-fingerprint`（R5 决策 8：账号侧不得另生成指纹），**不是**本机 agentred 的
`identity.DaemonFingerprint(uuid)`（`sha256:<hex>`）——两者是不同类的值，取错则桌面端与
server 算出不同的 uuid，决策 2 由构造失败、镜像存量全体成孤儿。

**回填必须幂等且确定**：同一行重跑得到同一个 uuid。迁移在自包的事务内完成，失败整体回滚。

## Out of scope

- **为未保存的对话在 server 上建索引。** 决策 11 说明为何不需要；Hard invariant 2 说明为何
  不做。若日后要支持"机器离线也能列出未保存的对话"，那是一次独立的隐私边界变更，需要重开规格。
- **改变"谁扇出实时通知"。** 决策 13 合并的是**传输**（两条 socket 并成一条），不是**总线**：
  `accountchan_svc` 的 per-account Redis Pub/Sub 与中继的 per-target 路由仍是两条独立的
  服务端通路，各自的可靠性等级不变。让常驻镜像连接替所有浏览器扇出转录实时帧、从而不必为
  读打开中继通道，是下一步，且会耦合交互延迟与镜像租约，需要单独定夺。
- **`agentruntime` 进程内 key 改为字符串**（决策 6）。
- **对话在机器之间迁移 / 换承载机器继续同一条对话。** 本轮把标识准备好，迁移语义本身不做。
- **agentred 的自更新通道与通知日志回收。** 与本轮无关，各自欠账照旧。
- **`peer_fingerprint` 列的最终去留。** 本轮它退出身份键、保留为来源与授权列；是否还有必要
  长期保留由后续观察决定。

## Testing decisions

| Seam | What it verifies | Prior art |
|---|---|---|
| `internal/pkg/wireversion/methodset_test.go` | 方法集指纹改变时 `MinSupported == Protocol` | 该文件现有的 `methodSetDigest` 常量 + `TestMethodSet_GivenTheStrictVersionHandshake_...`，扩一条断言。**注意它是手工流程**：方法集变更时要人工把新指纹填进常量、并同步抬 `package.json` 版本，本轮新增的 `MinSupported` 必须一并纳入该手工清单，否则守卫只挡住指纹、挡不住窗口 |
| `internal/pkg/wireversion/wireversion_test.go` | `Protocol` 与 `package.json` 的 `version` 逐字一致；新增的 `MinSupported` 同样被钉住 | 该文件现有的 `TestProtocol_GivenWirePackageJSON_...`（与方法集守卫是**两个文件、两件事**） |
| daemon RPC 集成测试（Mode C 握手） | 客户端在请求体里给不出对端身份（字段已删）；凭据缺 `pfp` 时握手被拒；`AuthState` 里的对端身份等于凭据里的 `pfp` | `internal/daemon/integration_test.go`、`daemon/auth/auth_test.go` |
| daemon 会话隔离测试 | 两个不同对端持不同 `conversation_id` 时，backend 会话互不并轨；旧的"同号会话"构造不再能构造出来 **没有**以 `runtimeSessionID` 为直接对象的现成测试（全仓库 `*_test.go` 零命中）。真正的先例是 `internal/daemon/integration_test.go:1066` 的 `keyedApprovalRunner`——其注释说明它按真实 backend 的方式索引会话，因此"两个对端的同号会话会不会撞成同一条"在它身上如实反映生产行为；另见 `daemon_test.go:791`、`:805` |
| **`agentre-server` 的迁移测试** | 回填幂等；改键后行数与内容不变 | 该仓库**允许**迁移测试，但**先例比看上去弱**：`agentre-server/migrations/migrations_test.go` 现有的四个测试全是 `TestWithMigrationLock_*`（sqlmock 测 `GET_LOCK` 重试/超时/NULL），**没有任何测试执行 `migrationList()` 的 DDL**。所以这是新写一类测试，不是照抄现成形态 |
| **`agentre` / agentred 的迁移：不写迁移测试** | 同上语义，但走该仓库规定的路线 | `agentre/AGENTS.md` 明令 **"Migrations carry no unit tests of their own — do not add a `migrations/*_test.go`"**。该仓库的既定路线是：`internal/bootstrap/cago_test.go` 只证明链条在**空库**上跑得干净，而回填与改键语义**对着有真实行的库手工验证**——`develop.md` "When Touching Persistent Data" 第 4 步要求同一条查询在迁移前后各跑一次（行数、边界值、NULL 数），并按 `verification.md` 留证。"空库上绿等于没跑过" |
| UUIDv5 回填函数的纯单元测试 | 决策 2 的确定性：同一 `(peer_fingerprint, peer_session_id)` 在三个仓库独立算出同一 uuid | 这是**纯函数**，不是迁移，不受上一条限制。三个仓库各自对同一组向量断言同一批输出，是决策 2 唯一的机械保证 |
| `relay_svc` 单元测试（mockgen） | 通道级路由：同一连接上两条通道落在两台机器；一条通道的目标离线不影响另一条；鉴权失效才关整条连接 | `internal/service/relay_svc/relay_test.go`、`framebus` 现有测试 |
| `relayClientPool` 前端测试 | 一个账号同时观察 N 台机器的对话时，浏览器持有的 WebSocket **总数**为 1——不只是 `pool.size === 1`，账号信号那一条按决策 13 也在这条 socket 上；通道级失败只影响对应对话 | `frontend/src/__tests__/relay-client-pool.test.ts`、`frontend/src/__tests__/account-channel.test.ts` |
| 合并连接的信号投递测试（server 端） | 保留通道的信号在 upgrade 之前订阅、不留漏帧窗口；客户端往保留通道写入被按协议错误处理；订阅失败时以 HTTP 如实作答而非建立半可用连接 | `internal/controller/accountchan_ctr/channel_test.go` 现有的订阅时序与票据测试，迁移到合并后的入口 |
| agentred 保留通道路由测试 | daemon 的 `Multiplexer` 把保留号交给信号处理器而非 RPC 注册表；随机分配的通道号永不落进保留号 | `internal/daemon/relaytransport/multiplexer_test.go`、`multiplexer_isolation_test.go` |
| `savedsession/guard_test.go` | Hard invariant 2：未保存的对话仍然不落任何内容行 | 已存在，但**本轮必须改它而不是只让它绿**：它的子测试 2 断言镜像表的身份键是 `(user_id, peer_fingerprint, peer_session_id)`，改键之后要同步改成 `(user_id, conversation_id)`——保留旧断言会让它"绿着但守的已不是身份键"。另有静默解除风险：子测试 3 按方法名字符串扫 `UpsertSummary` / `WriteFrames`（`:177`）却**没有 `len(calls)==0` 兜底**，改建时顺手改这两个方法名会让守卫扫到 0 处、无声通过——本轮要补上这条兜底 |
| 桌面端"未登录不发网络请求"测试 | Hard invariant 1：未登录 / 无 server 配置下新建并运行对话全程不触发任何网络调用 | **`chat_svc` 里没有这样的先例**——那里只有不含网络断言的建档测试（`chat_ensure_user_session_test.go:18`、`chat_test.go:2369`）。真正的先例在包外：`server_svc/sync_test.go:108`（"未登录时一个网络请求都不发"，httptest + `called` 标志）与 `server_svc/accountchannel_test.go:168`。另注：生产侧 `server_svc.Server() == nil` 的分支（`chat_svc/exec_pick.go:261`）今天零覆盖 |

**风险最高的一处，明说**：agentred 的 `daemon_notification_journal` 是只增不删的永久日志
（`202608080011_daemon_sessions.go` 注释自陈"不再回收任何一行"），本轮要在它上面跑体量最大的
一次主键重建。它没有迁移测试**不是缺口而是该仓库的约定**（见 Testing decisions），因此补救
不是补测试，而是：结构与回填分两次提交、迁移体自包事务、重建写成可分批可重入的形态，
并**对着一个装有真实行的 agentred 库手工验证**，前后各跑一次同样的计数查询。

**无法自动化、由收尾人工核对的部分**：该表在**真实体量**下的主键重建耗时。迁移逻辑本身会有
测试，但"在一个已经跑了几周的库上跑得完"只能靠一次真实库上的手动演练确认，并在收尾报告里
记录实测耗时与库大小。

## Open questions

无。
