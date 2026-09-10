# 宿主转录记全用户产出的内容：附件与插话

> Status: Draft
> Owner: 桌面端 chat 域 / agentred daemon 域
> Last updated: 2026-09-07

**Objective:** 让用户在一轮里产出的每一样东西 —— 提问、附件、轮中插话 —— 都由宿主记进转录
并取号，任何消费方（实时的、补齐的、第二个订阅者）看到的因此是同一份。

**Hard invariant:** `2026-09-05-transcript-storage-alignment` 的五条不变量不得回退，尤其
不变量 1（补齐不重不漏）与**两级帧划分本身**：预览帧不带 seq、不落库、不参与补齐；持久帧
是转录与游标的唯一来源。`2026-09-07-catchup-own-turn-reconciliation` 决策 1 的游标语义
（游标 = 我已经持有的内容）同样不得回退。

## Problem

1. **插话文本从不进 agentred 的转录。** 已实测（2026-09-07，`dev` @ `cb140124`，复现用例与
   完整输出留档在 `.dev-kit/artifacts/2026-09-07-host-transcript-user-input/diagnostics/`）：
   一轮跑到一半插一句 `follow-up-from-user`、后端消费它、收口之后，agentred 库里只有两行 ——
   `user` 正文 `hi` 与 `assistant` 正文 `before-steerafter-steer`，插话文本一个字都没有，
   前后两段被并成了一个块。成因：`internal/pkg/transcript/dispatcher.go:37` 刻意不把 `SteerConsumed`
   注册进事件→块那张表，`internal/daemon/handlers/runtime_transcript.go:146` 的 `observe`
   因此静默丢弃它（`turn.Dispatcher` 对未注册事件的 forward-compat 行为）。

2. **两个宿主对同一件事分叉。** 桌面端做宿主时是**记**的：
   `internal/service/chat_svc/chat.go:2788 persistConsumedSteers` 建下一行 user + 一行新
   assistant，随后依次 `publishPeerMessageFrames`：收口的那条（`:2858`）与插进来的
   用户消息（`:2860`）各取一次号发布。
   `2026-09-05` 决策 2/8 刚把投影与转录存储收敛成一份，这里却是同一件事的两种实现。

3. **消费方在预览帧上落库，与两级帧划分直接矛盾。** 远端那一路的预览出口是
   `internal/pkg/agentruntime/runtimes/remote/runtime.go:858` →
   `internal/service/chat_svc/preview_stream.go:56 applyPreview` →
   `internal/service/chat_svc/turn_run.go:229 applyLive(ev, preview=true)`，而 `:255` 的
   `case agentruntime.SteerConsumed:` **没有任何 `preview` 守卫** —— 一条被定义为「不落库」
   的帧在这里产生了持久写。由此推论：**桌面端做宿主 + 远端消费方**时，同一段插话会被写两遍
   （一遍来自预览帧，一遍来自宿主发来的持久帧 —— 用户消息投影成 `UserMessageEvent`，与
   `2026-09-07` 那一轮 V6 是同一条投影链）。**这一条是推理，未实测**，由下面的测试缝去钉。

4. **附件从不进任何宿主的转录，桌面端做宿主时连模型都收不到。**
   `runtime.run` 带得了附件（`internal/pkg/agentruntime/runtimes/remote/wire/wire.go:361`
   的 `UserBlocks`，proto `RuntimeRunRequest.user_blocks = 12`），浏览器控制台也确实在用它
   发图（`agentre-server/frontend/src/components/session/useSessionSend.ts:235`）。但
   agentred 侧 `internal/daemon/handlers/runtime.go:568` 只把 `p.UserText` 交给
   `beginTranscript`，`internal/daemon/daemon.go:1617` 的 `StartTurn` 因此只
   `acc.AddText(userText)`；桌面端做宿主时 `internal/service/chat_svc/peer_session_controls.go:120`
   更是把 `params.UserBlocks` 整个丢掉 —— 那一侧的图既不落转录，也不进模型。

5. **可观察后果，按严重度。**
   （a）**用户自己打的字会永久消失** —— 宿主没有这份记录，消费方在插话被消费的时刻若不在线
   （或写库前进程没了），那句话此后哪里都找不回来。这是不变量 1 的「漏」。
   （b）同一条会话按连通性呈现两种形状：全程在线四行，断线补齐两行且插话消失。
   （c）第二个订阅者（浏览器控制台、`agentre-server` 的 `mirror_svc`）在 agentred 托管的
   会话上从来看不到插话 —— 它们的转录由镜像的帧投影而来，而插话没有帧。
   （a）（b）由问题 1 与问题 3 两处已观测事实推出，跨进程那一串本身未实测（本仓没有
   daemon × chat_svc 的自动化缝，`2026-09-07` spec 的 Testing decisions 修订已记过）。

## Actors and user stories

1. 作为**在远端机器上跑任务的用户**，我希望我在轮中补的那句话是这段对话的一部分 —— 换台设备
   看、或者关掉再开，它还在。
2. 作为**从浏览器控制台发图的用户**，我希望那张图既送到模型、也留在转录里。
3. 作为**同时开着桌面端和浏览器的用户**，我希望两处看到的是同一份内容，不因为谁托管这条会话
   而不同。
4. 作为**读这个仓补齐代码的开发者**，我希望「预览帧」这个级别的定义在宿主与消费方两侧都成立，
   不必逐个调用点去判断某条预览帧会不会落库。

## Design decisions

| # | Decision | Basis and rejected option |
|---|---|---|
| 1 | **插话由宿主落进转录并取号，形状与桌面端做宿主时相同**：收口当前 assistant + 一行 user（带提交方来源）+ 一行新 assistant，三者都取号发帧。 | 桌面端已经是这个形状且已发帧（`chat.go:2858`、`:2860`，实测读到），agentred 补齐即可，不必两边都改；与「宿主是转录的唯一权威」一致。Rejected A：**把插话落成当前 assistant 里的一个新块型** —— 不必在 daemon 侧实现轮次分段，但会让桌面端做宿主时的既有形状变成第二种，与 `2026-09-05` 决策 2/8 刚收敛成一份的投影相抵。Rejected B：**把消费方的持久写降格，谁都不落库** —— 与「预览帧不落库」一致且改动最小，但用户打的字从此不进转录，对本机后端是一次可见的功能倒退。 |
| 2 | **消费方不再从预览帧落库**：预览 `SteerConsumed` 只驱动呈现（清 chip、切呈现累加器），落库交给持久帧。本机后端没有两级帧，那一路照旧落库 —— 它自己就是宿主。 | 这是 `2026-09-05` 两级帧划分的原文，问题 3 是对它的违反。不这么改，决策 1 会让消费方把同一段内容写两遍。Rejected：**保留预览落库、让持久帧那一路认领它** —— 不必改分级，但要养一套内容对账逻辑（`2026-09-07` 决策 1 的 Rejected A 拒的正是这条路），且旧消费方仍会重复。 |
| 3 | **两个宿主都把 `UserBlocks` 落进用户消息的块；桌面端做宿主时还要把它送去执行。** | 问题 4 的两处实测。形态不是新发明的：桌面端**本机**发图时早已把它落成用户消息里的 `blocks.ImageBlock`（`internal/service/chat_svc/chat.go:961 blocksFromSendImages`，并有既存的 `maxSendImages = 4` 上限），本轮只是让远端那两条路与它同形。Rejected：**只落库不执行** —— 转录里看得见图而模型看不见，比现状更难向用户解释。 |
| 4 | **运行应答再补一格可选的「本轮用户消息最低持久帧号」，游标闸门改为「最低号 == 游标 + 1 才推进，推进到最高号」。** | 决策 3 让用户消息可能占多帧，而 `2026-09-07` 的闸门只认「最高号 == 游标 + 1」 —— 不补这一格，带附件那一轮会退回 V6（补齐重复消费方自己的提问），刚交付的修复在附件场景下静默失效。该 spec 的修订也已把这一格点名为「另一轮的协议改动」。Rejected：**把文本与附件合成一个帧位发布** —— 闸门不用改，但帧的粒度与 `2026-09-05` 的块级帧约定不一致。 |
| 5 | **抬协议版本到 `0.3.0`，`Protocol` 与 `MinSupported` 同抬，锁步升级。** | 决策 1 + 2 改的是**既有帧的含义**，新旧混搭两个方向都产生错误组装：旧消费方 + 新宿主 = 同一段插话写两遍；新消费方 + 旧宿主 = 插话彻底不见（比今天更糟，今天消费方本地至少有一份）。这与 `2026-09-07` 决策 3「不抬版本」并不矛盾：那一轮加的是一格**可选的应答字段**，拿不到它的一侧退回旧行为。仓里的守卫也只允许同抬：`internal/pkg/wireversion/methodset_test.go:73` 与 `wireversion_test.go:65` 都无条件要求 `MinSupported == Protocol`，且 `wireversion_test.go:38`／`:65` 要求两者都与 `frontend/packages/agentre-wire/package.json` 的 `version`（今为 `0.2.0`）逐字相等。Rejected A：**不抬、让新消费方去重** —— 旧消费方 + 新宿主仍会重复。Rejected B：**只改 agentred 宿主、不动消费方** —— 把「漏」换成「重」，一个缺陷换另一个。 |
| 6 | **本轮只交付 `agentre`；`agentre-server` 必须在同一次发布里跟随。** | server 出示自己那份版本给 daemon／桌面端校验（`agentre-server/internal/pkg/wireversion/wireversion.go`，今为 `0.2.0`；它自己的注释写明「本仓库只**出示**版本 …… 对端按精确匹配校验」）。桌面端一旦只收 `0.3.0`，仍报 `0.2.0` 的 server 会在握手处被拒 —— 浏览器控制台整体不可用。交付顺序按 AGENTS.md 的跨仓纪律：`agentre` 先落地、验证、推 GitHub，server 再 pin 那个不可变 revision（它今天 pin 的是 `github:agentre-hub/agentre#ae9372f1…&path:/frontend/packages/agentre-wire`）。Rejected：**把 server 侧并进本轮** —— 违反「三个仓独立提交」，且 pin 必须指向已经推送的 revision。 |

## 插话进转录

轮中插话与轮末残留受同一条约定。「宿主」指真正执行这一轮的那一端（agentred，或做宿主的
桌面端）；「消费方」指通过 wire 订阅这条会话的那一端。

- 前置：一条会话正在宿主上跑一轮，用户（本机或某个对端）插了一句话。
- 动作：后端消费掉它 —— 轮中消费，或轮末被 drain 走。
- 可观察结果：宿主的转录里，当前 assistant 就此收口，随后是一行用户消息（正文即插话文本，
  带**提交方**的来源标识）与一行新的 assistant；这三样都取到持久帧号并实时发布。任何消费方
  （实时的、补齐的、第二个订阅者）看到的都是这同一份形状。
- 失败：宿主落库或取号失败时不发布这一段帧 —— 与 `2026-09-05` 的既有约定一致（取到号才发），
  消费方因此不会持有一个宿主认不回来的号。该轮的执行不因此中断。

消费方一侧：预览 `SteerConsumed` 此后**只驱动呈现** —— 清掉插话 chip、切换呈现用的累加器 ——
不再产生任何消息行。本机后端那一路没有两级帧，消费方与宿主同体，它照旧在这条事件上落库。

- 前置：消费方通过 wire 订阅一条会话，宿主上一段插话被消费。
- 动作：预览帧先到，随后是宿主发布的持久帧。
- 可观察结果：这段插话在消费方的转录里**只有一份**，来源是持久帧；呈现上插话 chip 仍在预览
  帧到达时立即消失（不等持久帧）。
- 失败：持久帧因宿主取号失败而没来时，消费方转录里没有这一段 —— 与宿主一致，而不是各持一份。

## 附件进转录

- 前置：调用方在 `runtime.run` 里带上 `userBlocks`（今天的唯一来源是浏览器控制台发的图）。
- 动作：宿主接受这一轮。
- 可观察结果：宿主落下的用户消息里既有文本块也有这些附件块，它们各自取到持久帧号并发布；
  这一轮的执行同时收到这些附件（两个宿主都是）。
- 失败：附件解不开时按该轮参数无效拒绝整轮，而不是丢掉附件照跑 —— 静默跑一轮「用户以为发了
  图、模型没看见」的对话，比一个明确的错误更糟。

本轮不改附件本身的编解码与体积策略（见 Out of scope）。

## 多帧用户消息与游标

`2026-09-07` 定下的契约是「宿主随运行应答回该轮用户消息的**最高**持久帧号，发起方据它把游标
推进到自己已经持有的位置」，闸门是「该号正好是游标 + 1 才推进」。决策 3 之后用户消息可能占
两帧及以上，那道闸门便再也不成立。

- 前置：发起方在一条会话上派发带附件的一轮。
- 动作：宿主接受该轮，落用户消息、为它的每个块分配帧号，并在应答里带上这一轮用户消息的
  **最低**与**最高**帧号。
- 可观察结果：**最低号正好是发起方当前游标 + 1** 时，游标推进到最高号并当场落库（不进防抖
  批次）；此后补齐不会再把这条用户消息（连同它的附件）交回给它。
- 失败：宿主没给这两个号（拒绝该轮、落库前失败，或对端是不认这一格的旧构建）时不推进；
  最低号与游标之间有洞时同样不推进 —— 无条件跳过去会把中间的帧永久跳掉，那是不变量 1 的
  「漏」，比「重」更严重（理由与 `2026-09-07` 那一轮的同名修订一字不差）。降级面也与那一轮
  相同：这一次派发退回本轮之前的行为，下一条实时帧照旧触发补洞拉取，内容不丢。

## 协议窗口与跨仓交付

握手窗口从 `0.2.0` 这一个点移到 `0.3.0` 这一个点：`Protocol` 与 `MinSupported` 同为
`0.3.0`，并与 `frontend/packages/agentre-wire` 的发布号逐字相等。`0.2.0` 及更早的构建此后
在握手处被明确拒绝，拒绝理由带上双方版本 —— 而不是握上手、直到第一段插话才错位。

- 前置：一台仍报 `0.2.0` 的构建（旧桌面端、旧 agentred、或尚未跟随的 `agentre-server`）
  向新构建发起握手。
- 动作：握手校验版本。
- 可观察结果：当场拒绝，理由里能读出对端报的版本与本方窗口。
- 失败：不存在「部分兼容」的中间态 —— 这正是抬窗口要买到的东西。

**`0.3.0` 不是一次编号世代切换。** `2026-09-05`「兼容性」把「协议版本**跨过 0.2.0 这一档**」
定为「这条对话已存的帧属于旧编号世代、应当一次性作废并重拉」的判据，指的是事件级编号换成
块级编号那一次。本轮不动编号的粒度与分配方式，因此 `0.2.0 → 0.3.0` 这一步**不得**被任何
消费方当成世代切换去作废存量帧。该判据的消费方落地至今仍属 server 轮（`2026-09-05` 决策 6，
本仓没有实现），本轮把这条边界写明，正是为了它落地时不会误伤。

`wireversion_test.go:73` 那个写死的 `previousProtocol = "0.1.0"` 钉的是「上一档被关在门外」
这个具体事实（它的注释明写不从 `Protocol` 推算，就是为了不随之漂移）。窗口移到 `0.3.0` 之后
「上一档」变成 `0.2.0`，那条守卫要跟着钉住新的上一档，否则它守的已不是它自称在守的事。

跨仓交付顺序：`agentre` 先落地、验证、推送到 GitHub（`agentre-server` 的前端 pin 与 Go pin
都从那里解析）；`agentre-server` 随后在自己的轮次里 pin 那个不可变 revision、把它那份
`wireversion` 同抬到 `0.3.0`。两者必须在**同一次发布**里上线：只升其一会让浏览器控制台整体
不可用。

## Out of scope

- **附件的编解码、体积上限与呈现细节**（缩略图、懒加载、超大图的降采样）：本轮只保证它落成块、
  进转录、进执行，与 `2026-09-05` 决策 7 一致，不动块正文编解码。
- **`image` 块的内联存储形态**：`2026-09-05` 的 Out of scope 已把它列为独立问题（本机 153 行
  占 32 MB、单行最大 4.67 MB）。本轮沿用这个既有形态，代价是 agentred 自己的库此后也会按
  同样的形态增长 —— 这是把远端那条路与桌面端本机路对齐的必然结果，不是本轮新引入的形态。
- **存量**：已经发生过、宿主没记下的插话不回溯补写；本轮只保证此后不再丢。
- **`agentre-server` 侧的实现**：按决策 6 另起一轮，但必须与本轮同发布。
- **插话的取消（`runtime.cancelSteer`）路径**：被撤回的插话从来没有被消费，不产生转录内容，
  本轮不改它。

## Testing decisions

| Seam | What it verifies | Prior art |
|---|---|---|
| agentred 宿主的插话落库与发帧 | 轮中消费与轮末 drain 两条路都：收口当前 assistant、落一行带提交方来源的 user、开新 assistant，三者都取号发布 | 无 —— 本轮新增；复现用例已在 `.dev-kit/artifacts/2026-09-07-host-transcript-user-input/diagnostics/` |
| 两个宿主的插话转录同形 | 同一串事件在 agentred 与桌面端宿主上落下的转录逐字节相同 | 部分：`internal/daemon/integration_test.go` 的 `TestIntegration_RemoteTurn_LandsTheSameBlockTranscriptAsTheDesktop` 是同一条对照的既有形式，但它的 `desktopBlocksJSON`（`:3680`）只算**单条**消息的块，表达不了分段成多行 —— 对照方式要本轮扩出来 |
| 消费方不再从预览帧落库 | 预览 `SteerConsumed` 只清 chip / 切呈现累加器，不产生消息行；本机后端那一路照旧落库 | `internal/service/chat_svc/steer_segmentation_test.go` 与 `chat_test.go:5820 TestSend_SteerConsumedSplitsMessages`（后者钉的正是「本机后端照旧分段落库」，本轮不得让它变红） |
| 桌面端宿主 + 远端消费方不重复 | 同一段插话在消费方转录里只有一份 —— 问题 3 那条**推论**由它钉死 | 无 —— 本轮新增 |
| 附件落块与进执行（两个宿主各一处） | 带 `userBlocks` 的一轮：用户消息的块里有附件、每个块各自取号；执行请求里也有它 | `internal/daemon/integration_test.go` 的 `TestIntegration_RemoteTurn_LandsTheSameBlockTranscriptAsTheDesktop`（用户那一行的块此前被断言成单个文本块，本轮要随之扩） |
| 多帧用户消息的游标闸门 | 最低号接在游标之后时推进到最高号并当场落库；有洞或没拿到号时不推进 | `internal/pkg/agentruntime/runtimes/remote/reconnect_test.go` |
| 协议窗口 | `Protocol` 与 `MinSupported` 同为 `0.3.0` 且与 `package.json` 逐字相等；`0.2.0` 在握手处被拒且理由带双方版本 | `internal/pkg/wireversion/wireversion_test.go` 现有三条 + `methodset_test.go:73` |

无法自动化的部分有两处，都归收尾的真实联调：

1. **跨进程那一串** —— 消费方断线 → 插话被消费 → 重连补齐 → 插话在不在。本仓没有
   daemon × chat_svc 的自动化缝（`2026-09-07` spec 的同名修订已记过这一条），问题 5 的
   （a）（b）因此只能在真实联调里观测。
2. **跨仓锁步** —— 仍报 `0.2.0` 的 `agentre-server` 被新桌面端在握手处拒掉，以及跟随之后
   恢复可用。

## Open questions

（无）
