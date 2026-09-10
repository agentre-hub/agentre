# 控制台的排队消息队列：把「已排进这一轮」从一句解释变成一份可管理的队列

> Status: Approved
> Owner: 桌面端 / 控制台前端 + wire 协议
> Last updated: 2026-09-08

**Objective:** 让浏览器控制台在一轮进行中插话时，看到的是与桌面端同一份排队消息队列（逐条列出、可撤回、轮末残留可救回），而不是一句解释文案；并把这份队列的展示与归约收敛成工作区里唯一一份实现。

**Hard invariant:**

- `agentre-server` 的 Go 一行不改。插话、撤销、队列状态全部走既有的中继裸传路子，不进服务端的任何响应结构体、不落库。
- 工作区内只有一份排队队列的展示与归约实现。桌面端切到共享包之后，宿主里不得留下等价的第二份。
- 桌面端已有的可见行为不变：队列条的位置（输入框上方卡内）、逐条撤回与清空、丢弃横幅的两颗键与其落点（放回队列）。本轮在桌面端唯一的可见变化是两句写死了后端名的文案改成后端中立措辞。
- 协议改动双向兼容：新对端答给老客户端的字段被忽略，老对端答给新客户端的空字段被识别为「这台机器还不报权威 id」并走降级路径，两侧都不得因此报错或丢消息。
- 跨仓交付顺序不可颠倒：协议与共享实现先在 `agentre` 落地并推送，`agentre-server` 才能 pin 过去；消费方不得先于可解析的共享版本删除任何东西。

## Problem

1. **控制台把一份队列压成了一个布尔。** 插话成功后控制台只置 `SendFeedback = {kind:"queued"}`（`agentre-server/frontend/src/components/session/useSessionSend.ts:27`），渲染成输入框下方一句「这条对话正在进行中，你的消息已排进当前这一轮，这一轮结束前它会读到。」（`SessionComposerBand.tsx:220-231`、`i18n/locales/zh-CN/session.json:50`）。连发三条也只有这一句：用户看不到自己排了几条、排了什么、也撤不回任何一条。而这正是一句纯解释性的状态文案——它替代了本该由界面直接呈现的东西。

2. **桌面端早就有真队列，只是它住在宿主里。** `frontend/src/components/agentre/queued-messages-bar.tsx`（逐条 chip、撤回、清空、丢弃横幅）与 `frontend/src/stores/queued-messages-store.ts`（append / consume / clear / markDropped / restore / dismiss）都在桌面宿主目录下，挂在共享 `ChatComposer` 已有的 `topSlot` 上（`chat-panel.tsx:1113`）。按 `AGENTS.md` 的跨端所有权规则，两端都要渲染的东西归 `@agentre-hub/agentre-ui`。

3. **协议在两类目标上不对称，照搬桌面端会当场坏掉。** 浏览器自己造 `queuedId` 交给 `runtime.steer`（`useSessionSend.ts:283-296`），而 `runtime.steer` 的应答是 `Empty`（`frontend/packages/agentre-wire/src/rpc-methods.ts:92-97`）：
   - 目标是 **agentred** 时，浏览器给的 id 被原样交给 runner（`internal/daemon/handlers/runtime.go:1292`），`runtime.cancelSteer` 也注册着（同文件 `:1307`、`internal/daemon/protobuf_runtime.go:109`）；
   - 目标是**桌面端**时，`EnqueuePeerSession` 转手给 `enqueue`，后者 `queuedID := newQueuedID()` **丢掉**了浏览器给的 id（`internal/service/chat_svc/peer_session_controls.go:219-225`、`internal/service/chat_svc/chat.go:1556`），而 peer 侧的 inbound **根本没注册** `RPC_METHOD_RUNTIME_CANCEL_STEER`（`internal/peer/protobuf_inbound.go:297` 只有 `RPC_METHOD_RUNTIME_STEER`）。

   于是在「会话托管在桌面端」这条路上，chip 既对不上消费事件（`ConsumedSteer.queued_id` 是对端自己造的那个，`proto/agentre/wire/wire.proto:1129`），也撤不掉（撤销会撞 method not found）。

4. **「这个后端能不能撤回」在浏览器侧无处可问。** 桌面端由 `chat_svc` 在入队时按 `runner.(agentruntime.SteerCanceler)` 现场判定并随应答返回（`chat.go:1571-1576`、`types.go:1109`）。浏览器只能从 `runtime.capabilities` 里猜 `cancel_steer`（`capability.CapCancelSteer`，claudecode 为真、codex 与 piagent 为假），而那份能力位描述的是 runtime，并不代表这条链路上撤销这个动作可达——桌面端目标上它恒为「可撤」而实际不可达。

5. **两句文案写死了后端名。** `queuedMessages.codexNoCancel` / `notCancellable` 说的是「codex 不支持撤回」，但 piagent 同样是 `CapCancelSteer=false`（`internal/pkg/agentruntime/runtimes/piagent/runtime_test.go:48`）。搬进共享包后这句会在 piagent 上耗着说错后端名。

## Actors and user stories

1. 作为在浏览器里盯一条正在跑的会话的用户，我希望看见自己刚插进去的每一句话都逐条列着，这样我知道自己排了几条、排了什么。
2. 作为刚发现自己插错话的用户，我希望能把还没被取走的那条撤回来，这样我不必等它被读进上下文再去纠正。
3. 作为一轮跑完发现自己排的话没被取走的用户，我希望它能回到输入框而不是无声消失，这样我写的字不会白写。
4. 作为在两端之间来回切的用户，我希望排队消息在浏览器与桌面端长得一样、行为一样，这样我不必记两套。
5. 作为维护者，我希望队列的展示与状态迁移只有一份实现与一份测试。

## Design decisions

| # | Decision | Basis and rejected option |
|---|---|---|
| 1 | 补齐协议对称性：`runtime.steer` 的应答回传权威 `queued_id` 与 `cancellable`，桌面端 peer 侧补注册 `runtime.cancelSteer` | 用户决定。两类目标行为一致之后，chip 靠 id 精确消费、撤销键靠对端自报的事实而不是能力位推测。Rejected: 纯前端按文本启发式消费 + 撤销能力按目标类型分裂 —— 同一条发送路径长出两种形状；Rejected: 新增「列出未消费 steer」的 RPC 让执行端当队列真相 —— 能看到别的端排的消息，但协议、daemon、peer、前端全要动，明显超出这一轮 |
| 2 | 队列是**浏览器本地的乐观状态**，只画这一屏发出去的那几条 | 决策 1 之后 id 是权威的，但「此刻队列里有什么」在协议上仍无处可读。别的端排进去的消息在被消费之前不可见，这是本轮明确接受的边界（见 Out of scope） |
| 3 | 对端还没升级（应答里 `queued_id` 为空）时：chip 照画、锁住不可撤销、按「同文本最早一条」抵消消费，轮末残留进丢弃横幅 | 用户决定。用户写的字在任何链路组合下都看得见。Rejected: 老对端不画 chip —— 从发送到被消费那段时间屏幕上什么都没有（输入框已清空），正是那句 banner 当初存在的理由；Rejected: 老对端只在轮末清 —— 消息被取走后 chip 与转录里那句话并存，看着像发了两遍 |
| 4 | 轮末未被消费的排队条目照搬桌面端的丢弃横幅（「恢复为草稿 / 丢弃」），不静默清空 | 用户决定。与桌面端同一套读法；静默清空会让用户刚敲完的一段话凭空消失且无补救 |
| 5 | 「恢复为草稿」在控制台落到**输入框草稿**，桌面端维持现有的「放回队列」 | 用户决定，与按钮文案一致；控制台已有 `ChatComposerHandle.restoreDraft` 的用法先例（`agentre-server/frontend/src/components/session/newconv/DraftSession.tsx:340`）。因此共享包只负责把被丢弃的条目交出来，**落点归宿主**。Rejected: 控制台照搬「放回队列」—— 轮已结束，放回去的 chip 不会被任何人取走 |
| 6 | 共享包同时拿走**组件**与**纯归约**，宿主只留键控与接线 | 用户决定。「什么时候消费、什么时候丢弃」是跨端同一套规则，各写一遍必然分叉。单会话的队列状态与迁移归共享包；「按 sessionId 还是按 (did, sid) 存」「dropped 恢复到哪」归宿主 |
| 7 | 两句写死后端名的文案改成后端中立措辞 | 用户决定。piagent 同样不可撤销，现文案会说错后端名 |
| 8 | 队列条挂在 `ChatComposer` 的 `topSlot`，不再用 `feedback` 槽 | 与桌面端同位同样式（`chat-panel.tsx:1113`）。`feedback` 槽随那句 banner 一起空出来 |
| 9 | `SendFeedback` 整型删除 | 删掉 `queued` 之后只剩 `none` 与已经废弃的 `failed`（失败早已由转录里的气泡承载，`SessionComposerBand.tsx:220` 的注释写明了这件事），留一个恒为 `none` 的类型只是噪音 |

## 协议：`runtime.steer` 回传权威句柄

`runtime.steer` 的应答从 `Empty` 换成一个带两格的消息：被排队那条消息在**执行端**的 `queued_id`，以及这条消息在这条链路上**撤不撤得掉**。两格都由应答方按自己的事实填，调用方不再推测。

- **目标是 agentred 时**：`queued_id` 是调用方传来的那个原样回显（它本来就被直接交给 runner），`cancellable` 取「这个 runner 实现了 `SteerCanceler` 没有」——与 `chat_svc` 判定同一个依据。
- **目标是桌面端时**：`queued_id` 是 `chat_svc` 入队时自己造的那个（调用方传来的那个此刻仍被丢弃，本轮不改这件事），`cancellable` 取 `chat_svc` 早就算好并返回给桌面前端的那一格。也就是说桌面端把它已经有的入队应答如实转出去，而不是丢掉。
- **桌面端 peer 侧补齐撤销**：注册 `runtime.cancelSteer`，按会话标识解到本机会话之后复用 `chat_svc` 既有的撤销实现（与入队走同一条解析），应答里返回实际被撤掉的 id 列表；`queued_id` 为空表示清空整条队列，与既有语义一致。撤销与入队一样要求已认证。

**兼容契约**：应答消息的两格都是可选字段语义——老对端回的空消息在新客户端上解出空 `queued_id`，新对端回的两格在老客户端上被忽略。任何一侧都不因此报错。`queued_id` 为空是本规格唯一的「这台机器还不报权威句柄」判据，不按版本号猜。

## 共享包：队列的展示与归约

`@agentre-hub/agentre-ui` 拿走两样：

**排队队列条**（今天的 `queued-messages-bar.tsx` 原样搬迁，文案键剪进包的 `chat.json`、取 `t` 的入口换成包内统一入口）。它的契约不变：

- 队列为空且没有被丢弃的条目时**什么都不渲染**，可见性由组件自己决定。
- 有被丢弃的条目时优先渲染丢弃横幅（标题、「丢弃」、「恢复为草稿」，以及逐条列出被丢弃的原文）。
- 否则渲染队列：头部是「排队中 · N 条」、一句说明（有可撤销项时说「AI 完成后插入」，否则说这个后端不支持撤回）、以及「清空」键（无可撤销项时禁用）；正文逐条列出 chip，单行截断、悬停给全文，可撤销的给撤回键，不可撤销的给锁图标。

**单会话队列的纯归约**：入队、把本地句柄换成权威句柄、按 id 消费、按文本抵消消费（降级路径专用）、撤销后按返回的 id 移除、把整条队列挪进「被丢弃」、丢弃与取出被丢弃的条目。它只认一份「这条会话此刻的队列」，不认宿主的键控方式，也不决定被丢弃的条目该去哪。

桌面端的 zustand store 退成薄壳：仍按 sessionId 持有 `Map` 与全局唯一的 dropped 槽，逐个方法转调共享归约；`restoreDropped` 维持现有落点（放回原会话的队列）。

## 控制台：队列的状态机

队列状态按 `(设备, 会话)` 持有，换目标即整份重置——与详情视图既有的「目标换了就重来」同一条规矩。

**入队。** 一次发送被选路成插话（既有的 `sendRouted` 已经返回了这件事）时，在**提交那一刻**就挂一条 chip：文本是用户刚发的那句，句柄是本地临时的，暂不可撤销。应答回来后把它换成对端给的权威句柄与 `cancellable`；应答里没有权威句柄则保持锁住（决策 3）。发送失败则撤掉这条 chip——那句话此刻由转录里的失败气泡承载，两处同时挂着同一句会读成发了两遍。

竞态：消费事件可能先于应答到达（对端取得快）。换句柄时若这个权威句柄已经被本轮消费过，这条 chip 直接消失，不得复活。

`/compact` 不入队（它不是插话）；一轮在跑时带图发送根本不会走到插话（既有的 `ImagesCannotSteer`），因此也不入队。重连期间排着等连接的那条消息走的是另一条路（既有的 pending 气泡），与本队列互不影响。

**消费。** 收到 `steer_consumed` 帧时，按帧里每条被消费 steer 的 `queued_id` 移除对应 chip；有权威句柄却对不上任何 chip 的（别的端排的）不做任何事。降级路径下（这条 chip 没有权威句柄）按文本抵消：同文本的多条只消掉最早那一条。同一帧可能以预览与持久两种形态各到一次，重复消费必须是空操作。

**撤销。** 点单条 chip 的撤回键或头部的「清空」，都发一次撤销请求（清空传空句柄），按应答返回的 id 列表移除对应 chip。请求失败时该条 chip **留在原位**并转成锁住状态，把对端那句已本地化的说明放进它的悬停文本——不弹全局提示：失败最常见的原因就是它刚好被取走了，而那时消费事件会紧接着把它清掉。降级路径下的 chip 不可撤销，撤回键本就不出现。

**轮末。** 一轮结束时（既有的终态帧回调）队列里还剩条目，就把它们整体挪进「被丢弃」并清空队列，由丢弃横幅呈现。用户点「丢弃」即彻底清掉；点「恢复为草稿」则把这些条目的文本放回输入框草稿并清掉横幅（多条按排队顺序拼接，之间空行分隔）。

**UI 与可访问性。** 队列条挂在输入框上方、composer 卡片内，与桌面端同位同样式；输入框下方那句解释文案与它的语言资源一并删除。队列条是一个带标签的区域，丢弃横幅是一条 alert；撤回、清空、锁图标各带自己的无障碍标签（这些都随组件一起从桌面端搬来，不新造）。只读态那三档（机器不在 / App 没开 / 设备被撤销）本就不渲染 composer，队列条自然不出现。

## Out of scope

- **看见别的端排进去的消息。** 需要执行端把队列当真相下发（新 RPC + 队列变更事件），本轮不做；本轮的队列只画这一屏发出去的那几条。
- **给 codex / piagent 造撤销能力。** 它们的协议一发即不可撤，本轮只是把这个事实如实呈现。
- **桌面端「恢复为草稿」的落点。** 现有的「放回队列」不动——那是另一轮的事。
- **桌面端 `chat_svc` 丢弃调用方 `queuedId` 这件事。** 权威句柄由应答回传即可解决本轮的问题，改入队侧的取号来源会牵动桌面端自己的入队路径。
- **重连期间的 pending 排队与失败气泡。** 与本队列不同源，行为不变。

## Testing decisions

| Seam | What it verifies | Prior art |
|---|---|---|
| agentred 的控制 RPC 集成测试 | `runtime.steer` 应答回显调用方句柄并按 runner 是否可撤填 `cancellable` | `internal/daemon/integration_test.go:911`（SteerConsumed 带提交方来源那条） |
| 桌面端 peer inbound 的 RPC 测试 | `runtime.steer` 应答带回 `chat_svc` 自己造的句柄与它算出的 `cancellable`；`runtime.cancelSteer` 已注册、要求认证、按会话解到既有撤销实现并返回被撤 id | `internal/peer/protobuf_inbound_test.go` |
| 共享包的队列归约（纯函数） | 入队、换权威句柄、按 id 消费、按文本抵消、撤销移除、轮末挪进丢弃、丢弃/取出；换句柄时撞上「已被消费」不复活；重复消费是空操作 | `frontend/src/stores/__tests__/queued-messages-store.test.ts`（随迁改写） |
| 共享包的队列条组件 | 空队列不渲染、丢弃横幅优先、可撤/不可撤两种 chip、清空键的禁用条件、后端中立文案 | `frontend/src/components/agentre/__tests__/queued-messages-bar.test.tsx`（随迁） |
| 桌面端 store 薄壳 | 转调共享归约后既有语义不变（尤其 `restoreDropped` 仍放回原会话队列） | 同上那份 store 测试 |
| 控制台会话详情（组件级，喂真 Protobuf 帧） | 一轮在跑时发消息 → chip 出现；应答回来 → 可撤销；`steer_consumed` 按 id → chip 消失；撤销 → 发出撤销请求且按应答移除；撤销被拒 → chip 留下并锁住；轮末残留 → 丢弃横幅；「恢复为草稿」→ 文本回到输入框；老对端（空句柄）→ 锁住且按文本抵消；换会话 → 队列清空 | `agentre-server/frontend/src/__tests__/session-detail.test.tsx:1592`（插话与发送失败那一族）、`__tests__/relay-event-vocabulary.test.ts` |
| 工作区守卫 | 仍只有一份 `wire.pb.go`；宿主里不再存在第二份队列实现 | 各仓既有的重复实现守卫 |

自动化盖不到的：真机上「桌面端托管的会话在浏览器里撤回一条排队消息」这条端到端路径，由收尾时在 server 上挂真 agentred / 真桌面端各跑一次实测覆盖。

## Open questions

（无）
